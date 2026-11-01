package safehttp_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/web/safehttp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// stubResolver answers names from a table and counts the lookups. A name may
// have a list of answers: one per lookup, the last one repeated.
type stubResolver struct {
	mu      sync.Mutex
	answers map[string][][]netip.Addr
	calls   map[string]int
}

func newStub() *stubResolver {
	return &stubResolver{answers: map[string][][]netip.Addr{}, calls: map[string]int{}}
}

func (s *stubResolver) set(host string, answers ...[]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var list [][]netip.Addr
	for _, a := range answers {
		var one []netip.Addr
		for _, x := range a {
			one = append(one, netip.MustParseAddr(x))
		}
		list = append(list, one)
	}
	s.answers[host] = list
}

func (s *stubResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, ok := s.answers[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	n := s.calls[host]
	s.calls[host] = n + 1
	if n >= len(list) {
		n = len(list) - 1
	}
	return list[n], nil
}

func (s *stubResolver) count(host string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[host]
}

var loopback = netip.MustParseAddr("127.0.0.1")

func portOf(srv *httptest.Server) int {
	u, _ := url.Parse(srv.URL)
	p, _ := strconv.Atoi(u.Port())
	return p
}

var _ = Describe("CheckURL", func() {
	c := safehttp.New(safehttp.Policy{}, newStub())

	DescribeTable("refuses",
		func(raw string, want error) {
			_, err := c.CheckURL(raw)
			Expect(err).To(MatchError(want))
		},
		Entry("loopback literal", "http://127.0.0.1/", safehttp.ErrBlockedAddress),
		Entry("other loopback", "http://127.8.9.10/", safehttp.ErrBlockedAddress),
		Entry("cloud metadata", "http://169.254.169.254/latest/meta-data/", safehttp.ErrBlockedAddress),
		Entry("RFC 1918 10/8", "http://10.0.0.1/", safehttp.ErrBlockedAddress),
		Entry("RFC 1918 172.16/12", "http://172.16.5.4/", safehttp.ErrBlockedAddress),
		Entry("RFC 1918 192.168/16", "http://192.168.1.1/", safehttp.ErrBlockedAddress),
		Entry("CGNAT", "http://100.64.1.1/", safehttp.ErrBlockedAddress),
		Entry("unspecified", "http://0.0.0.0/", safehttp.ErrBlockedAddress),
		Entry("multicast", "http://224.0.0.1/", safehttp.ErrBlockedAddress),
		Entry("IPv6 loopback", "http://[::1]/", safehttp.ErrBlockedAddress),
		Entry("IPv6 unspecified", "http://[::]/", safehttp.ErrBlockedAddress),
		Entry("IPv6 ULA", "http://[fd00::1]/", safehttp.ErrBlockedAddress),
		Entry("IPv6 link-local", "http://[fe80::1]/", safehttp.ErrBlockedAddress),
		Entry("IPv6 metadata", "http://[fd00:ec2::254]/", safehttp.ErrBlockedAddress),
		Entry("IPv4-mapped loopback", "http://[::ffff:127.0.0.1]/", safehttp.ErrBlockedAddress),
		Entry("IPv4-mapped private", "http://[::ffff:10.0.0.1]/", safehttp.ErrBlockedAddress),
		Entry("IPv4-compatible loopback", "http://[::127.0.0.1]/", safehttp.ErrBlockedAddress),
		Entry("NAT64 loopback", "http://[64:ff9b::7f00:1]/", safehttp.ErrBlockedAddress),
		Entry("6to4 private", "http://[2002:a00:1::1]/", safehttp.ErrBlockedAddress),
		Entry("decimal integer", "http://2130706433/", safehttp.ErrURL),
		Entry("hex integer", "http://0x7f000001/", safehttp.ErrURL),
		Entry("octal integer", "http://017700000001/", safehttp.ErrURL),
		Entry("hex dotted", "http://0x7f.0.0.1/", safehttp.ErrURL),
		Entry("octal dotted", "http://0177.0.0.1/", safehttp.ErrURL),
		Entry("short form", "http://127.1/", safehttp.ErrURL),
		Entry("zone", "http://[fe80::1%25eth0]/", safehttp.ErrURL),
		Entry("localhost", "http://localhost/", safehttp.ErrBlockedAddress),
		Entry("sub.localhost", "http://a.localhost/", safehttp.ErrBlockedAddress),
		Entry("userinfo", "http://user:pw@example.com/", safehttp.ErrUserinfo),
		Entry("user only", "http://user@example.com/", safehttp.ErrUserinfo),
		Entry("file", "file:///etc/passwd", safehttp.ErrScheme),
		Entry("ftp", "ftp://example.com/x", safehttp.ErrScheme),
		Entry("gopher", "gopher://example.com:70/", safehttp.ErrScheme),
		Entry("data", "data:text/html,<b>x</b>", safehttp.ErrScheme),
		Entry("javascript", "javascript:alert(1)", safehttp.ErrScheme),
		Entry("no scheme", "example.com/x", safehttp.ErrScheme),
		Entry("port 8080", "http://example.com:8080/", safehttp.ErrPort),
		Entry("port 22", "https://example.com:22/", safehttp.ErrPort),
		Entry("no host", "http:///x", safehttp.ErrURL),
		Entry("space", "http://example.com/a b", safehttp.ErrURL),
		Entry("too long", "http://example.com/"+strings.Repeat("a", 3000), safehttp.ErrURL),
	)

	It("accepts a public name and drops the fragment", func() {
		u, err := c.CheckURL("https://example.com/a?b=1#frag")
		Expect(err).NotTo(HaveOccurred())
		Expect(u.String()).To(Equal("https://example.com/a?b=1"))
	})

	It("accepts a public literal on an allowed port", func() {
		_, err := c.CheckURL("http://93.184.215.14:80/")
		Expect(err).NotTo(HaveOccurred())
	})

	It("accepts another port when the policy lists it", func() {
		c2 := safehttp.New(safehttp.Policy{Ports: []int{80, 443, 8080}}, newStub())
		_, err := c2.CheckURL("http://example.com:8080/")
		Expect(err).NotTo(HaveOccurred())
	})

	It("lets private addresses through only when allowed, never metadata", func() {
		c2 := safehttp.New(safehttp.Policy{AllowPrivate: true}, newStub())
		_, err := c2.CheckURL("http://10.0.0.1/")
		Expect(err).NotTo(HaveOccurred())
		_, err = c2.CheckURL("http://localhost/")
		Expect(err).NotTo(HaveOccurred())
		_, err = c2.CheckURL("http://169.254.169.254/")
		Expect(err).To(MatchError(safehttp.ErrBlockedAddress))
		_, err = c2.CheckURL("http://0.0.0.0/")
		Expect(err).To(MatchError(safehttp.ErrBlockedAddress))
	})
})

var _ = Describe("Allowed", func() {
	It("passes public addresses", func() {
		for _, a := range []string{"93.184.215.14", "1.1.1.1", "2606:4700:4700::1111", "::ffff:8.8.8.8"} {
			Expect(safehttp.Allowed(netip.MustParseAddr(a), false)).To(BeTrue(), a)
		}
	})
})

var _ = Describe("Get", func() {
	var (
		srv      *httptest.Server
		mux      *http.ServeMux
		resolver *stubResolver
		client   *safehttp.Client
		policy   safehttp.Policy
	)

	BeforeEach(func() {
		mux = http.NewServeMux()
		srv = httptest.NewServer(mux)
		DeferCleanup(srv.Close)
		resolver = newStub()
		resolver.set("public.test", []string{"127.0.0.1"})
		resolver.set("internal.test", []string{"10.1.2.3"})
		policy = safehttp.Policy{Ports: []int{portOf(srv)}}
	})

	JustBeforeEach(func() {
		client = safehttp.New(policy, resolver)
		safehttp.TreatAsPublic(client, loopback)
	})

	at := func(path string) string { return fmt.Sprintf("http://public.test:%d%s", portOf(srv), path) }

	It("fetches a page and sends no cookie, no credentials and only its own headers", func() {
		var got http.Header
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Clone()
			http.SetCookie(w, &http.Cookie{Name: "s", Value: "1"})
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<p>hi</p>"))
		})
		resp, err := client.Get(context.Background(), at("/"), []string{"text/html"})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(resp.Body)).To(Equal("<p>hi</p>"))
		Expect(resp.ContentType).To(Equal("text/html"))
		Expect(got.Get("User-Agent")).To(ContainSubstring("LocalAI"))
		Expect(got.Get("Authorization")).To(BeEmpty())
		Expect(got.Get("Cookie")).To(BeEmpty())

		// A second request carries no cookie from the first.
		_, err = client.Get(context.Background(), at("/"), []string{"text/html"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Get("Cookie")).To(BeEmpty())
	})

	It("refuses a name that resolves to a private address", func() {
		_, err := client.Get(context.Background(), fmt.Sprintf("http://internal.test:%d/", portOf(srv)), nil)
		Expect(err).To(MatchError(safehttp.ErrBlockedAddress))
	})

	It("refuses a name with one public and one private address", func() {
		resolver.set("mixed.test", []string{"127.0.0.1", "10.0.0.7"})
		_, err := client.Get(context.Background(), fmt.Sprintf("http://mixed.test:%d/", portOf(srv)), nil)
		Expect(err).To(MatchError(safehttp.ErrBlockedAddress))
	})

	It("refuses a redirect to a private address", func() {
		mux.HandleFunc("/go", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, fmt.Sprintf("http://internal.test:%d/", portOf(srv)), http.StatusFound)
		})
		_, err := client.Get(context.Background(), at("/go"), nil)
		Expect(err).To(MatchError(safehttp.ErrBlockedAddress))
	})

	It("refuses a redirect to a private literal and to another scheme", func() {
		mux.HandleFunc("/lit", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, fmt.Sprintf("http://10.0.0.1:%d/", portOf(srv)), http.StatusFound)
		})
		mux.HandleFunc("/file", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		})
		_, err := client.Get(context.Background(), at("/lit"), nil)
		Expect(err).To(MatchError(safehttp.ErrBlockedAddress))
		_, err = client.Get(context.Background(), at("/file"), nil)
		Expect(err).To(MatchError(safehttp.ErrScheme))
	})

	It("follows five redirects and refuses the sixth", func() {
		mux.HandleFunc("/r/", func(w http.ResponseWriter, r *http.Request) {
			n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/r/"))
			if n == 0 {
				_, _ = w.Write([]byte("done"))
				return
			}
			http.Redirect(w, r, fmt.Sprintf("/r/%d", n-1), http.StatusFound)
		})
		resp, err := client.Get(context.Background(), at("/r/5"), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(resp.Body)).To(Equal("done"))
		_, err = client.Get(context.Background(), at("/r/6"), nil)
		Expect(err).To(MatchError(safehttp.ErrTooManyRedirects))
	})

	It("connects to the address it checked, whatever a later lookup says (DNS rebinding)", func() {
		// The first answer is the server; every later answer is private. A
		// client that resolved twice (check, then connect) would fail or
		// reach the private address; this one resolves once per connection.
		resolver.set("rebind.test", []string{"127.0.0.1"}, []string{"10.9.9.9"})
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
		resp, err := client.Get(context.Background(), fmt.Sprintf("http://rebind.test:%d/", portOf(srv)), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(resp.Body)).To(Equal("ok"))
		Expect(resolver.count("rebind.test")).To(Equal(1))

		// A new connection resolves again and the private answer is refused.
		c2 := safehttp.New(policy, resolver)
		safehttp.TreatAsPublic(c2, loopback)
		_, err = c2.Get(context.Background(), fmt.Sprintf("http://rebind.test:%d/", portOf(srv)), nil)
		Expect(err).To(MatchError(safehttp.ErrBlockedAddress))
	})

	rebuild := func(change func(*safehttp.Policy)) {
		p := policy
		change(&p)
		client = safehttp.New(p, resolver)
		safehttp.TreatAsPublic(client, loopback)
	}

	It("cuts the body at the size limit", func() {
		rebuild(func(p *safehttp.Policy) { p.MaxBodyBytes = 1000 })
		mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("x", 5000)))
		})
		resp, err := client.Get(context.Background(), at("/big"), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(resp.Body)).To(Equal(1000))
		Expect(resp.Truncated).To(BeTrue())
	})

	It("stops at the timeout", func() {
		rebuild(func(p *safehttp.Policy) { p.Timeout = 200 * time.Millisecond })
		release := make(chan struct{})
		DeferCleanup(func() { close(release) })
		mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		})
		start := time.Now()
		_, err := client.Get(context.Background(), at("/slow"), nil)
		Expect(err).To(MatchError(safehttp.ErrTimeout))
		Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second))
	})

	It("refuses a content type the caller cannot read", func() {
		mux.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write([]byte("PK"))
		})
		_, err := client.Get(context.Background(), at("/bin"), []string{"text/*", "application/*+json"})
		Expect(err).To(MatchError(safehttp.ErrContentType))
		Expect(err.Error()).To(ContainSubstring("application/zip"))
	})

	It("says when the server answers an error status", func() {
		_, err := client.Get(context.Background(), at("/missing"), nil)
		Expect(errors.Is(err, safehttp.ErrStatus)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("404"))
	})

	It("reaches the loopback server without the test hook only when private addresses are allowed", func() {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
		plain := safehttp.New(policy, resolver)
		_, err := plain.Get(context.Background(), srv.URL+"/", nil)
		Expect(err).To(MatchError(safehttp.ErrBlockedAddress))

		p := policy
		p.AllowPrivate = true
		open := safehttp.New(p, resolver)
		resp, err := open.Get(context.Background(), srv.URL+"/", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(resp.Body)).To(Equal("ok"))
	})
})

var _ = Describe("Accepts", func() {
	It("matches exact, wildcard and suffix types", func() {
		acc := []string{"text/html", "text/*", "application/*+json"}
		Expect(safehttp.Accepts(acc, "text/plain")).To(BeTrue())
		Expect(safehttp.Accepts(acc, "application/ld+json")).To(BeTrue())
		Expect(safehttp.Accepts(acc, "application/json")).To(BeFalse())
		Expect(safehttp.Accepts(acc, "image/png")).To(BeFalse())
	})
})
