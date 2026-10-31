// SPDX-License-Identifier: MIT
package egress_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/egress"
)

func TestEgress(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "sandbox egress proxy")
}

// fakeHost decides with a function and records the checks and the audit.
type fakeHost struct {
	mu     sync.Mutex
	decide func(egress.CheckArgs) egress.Verdict
	checks []egress.CheckArgs
	ops    []string
	events []egress.Event
}

func (f *fakeHost) Check(_ context.Context, op string, a egress.CheckArgs) (egress.Verdict, error) {
	f.mu.Lock()
	f.checks = append(f.checks, a)
	f.ops = append(f.ops, op)
	d := f.decide
	f.mu.Unlock()
	return d(a), nil
}

func (f *fakeHost) Audit(_ context.Context, _ string, evs []egress.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, evs...)
	return nil
}

func (f *fakeHost) nChecks() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.checks)
}

func (f *fakeHost) audit() []egress.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]egress.Event(nil), f.events...)
}

func allow(egress.CheckArgs) egress.Verdict { return egress.Verdict{Action: "allow", Reason: "preset"} }

// stubResolver maps names to addresses.
type stubResolver map[string][]netip.Addr

func (s stubResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if a, ok := s[host]; ok {
		return a, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

var loopback = netip.MustParseAddr("127.0.0.1")

type fixture struct {
	host  *fakeHost
	proxy *egress.Proxy
	now   *time.Time
	mu    *sync.Mutex
}

func newFixture(tlsPort int, decide func(egress.CheckArgs) egress.Verdict, limits egress.Limits) fixture {
	h := &fakeHost{decide: decide}
	now := time.Now()
	mu := &sync.Mutex{}
	p := egress.New(egress.Options{
		Host:          h,
		Resolver:      stubResolver{"public.test": {loopback}, "other.test": {loopback}, "private.test": {netip.MustParseAddr("10.1.2.3")}, "self.test": {netip.MustParseAddr("8.8.4.4")}},
		DenyAddrs:     func() []netip.Prefix { return nil },
		TreatAsPublic: []netip.Addr{loopback},
		Limits:        limits,
		TLSPort:       tlsPort,
		Now: func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return now
		},
	})
	return fixture{host: h, proxy: p, now: &now, mu: mu}
}

func (f fixture) advance(d time.Duration) {
	f.mu.Lock()
	*f.now = f.now.Add(d)
	f.mu.Unlock()
}

// dial opens a guest connection to the proxy of alice's chat c1.
// The pair is a TCP connection on loopback, buffered like the vsock or Unix
// socket of a real guest (net.Pipe is not, and a 403 written before the
// request is read would block).
func (f fixture) dial() net.Conn {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = ln.Close() }()
	h := f.proxy.Ports("alice", "c1")[egress.Port]
	go func() {
		side, err := ln.Accept()
		if err == nil {
			h(context.Background(), side)
		}
	}()
	guest, err := net.Dial("tcp", ln.Addr().String())
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = guest.Close() })
	return guest
}

func portOf(raw string) int {
	u, _ := url.Parse(raw)
	p, _ := strconv.Atoi(u.Port())
	return p
}

func readResponse(c net.Conn, br *bufio.Reader, req *http.Request) *http.Response {
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	resp, err := http.ReadResponse(br, req)
	Expect(err).NotTo(HaveOccurred())
	return resp
}

func body(resp *http.Response) string {
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

var _ = Describe("the proxy", func() {
	var tlsSrv, plainSrv *httptest.Server

	BeforeEach(func() {
		tlsSrv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "secure") }))
		DeferCleanup(tlsSrv.Close)
		plainSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/redirect":
				http.Redirect(w, r, "http://other.test:"+strconv.Itoa(portOf(plainSrv.URL))+"/", http.StatusFound)
			case "/headers":
				_, _ = fmt.Fprintf(w, "auth=%q pc=%q", r.Header.Get("Proxy-Authorization"), r.Header.Get("Proxy-Connection"))
			case "/big":
				_, _ = io.WriteString(w, strings.Repeat("x", 4096))
			default:
				_, _ = io.WriteString(w, "plain")
			}
		}))
		DeferCleanup(plainSrv.Close)
	})

	connect := func(f fixture, target string) (net.Conn, *bufio.Reader, *http.Response) {
		c := f.dial()
		_, err := fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
		Expect(err).NotTo(HaveOccurred())
		br := bufio.NewReader(c)
		return c, br, readResponse(c, br, &http.Request{Method: http.MethodConnect})
	}

	It("blocks every connection while no call runs, asks nothing, and reports it at the next call", func() {
		f := newFixture(0, allow, egress.Limits{})
		c, _, resp := connect(f, "public.test:443")
		_ = c
		Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
		Expect(body(resp)).To(ContainSubstring("no command of this chat is running"))
		Expect(f.host.nChecks()).To(BeZero())
		end := f.proxy.Begin(context.Background(), "alice", "c1", "op1")
		end()
		Expect(f.host.audit()).To(ContainElement(HaveField("Reason", "idle")))
	})

	It("tunnels an allowed CONNECT after a ClientHello that names the host, and audits the bytes at the end of the call", func() {
		port := portOf(tlsSrv.URL)
		f := newFixture(port, allow, egress.Limits{})
		end := f.proxy.Begin(context.Background(), "alice", "c1", "op1")
		c, br, resp := connect(f, fmt.Sprintf("public.test:%d", port))
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		tc := tls.Client(&bufConn{Conn: c, r: br}, &tls.Config{ServerName: "public.test", InsecureSkipVerify: true}) //nolint:gosec // a test server
		Expect(tc.Handshake()).To(Succeed())
		_, _ = io.WriteString(tc, "GET / HTTP/1.1\r\nHost: public.test\r\nConnection: close\r\n\r\n")
		r2, err := http.ReadResponse(bufio.NewReader(tc), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(body(r2)).To(Equal("secure"))
		_ = tc.Close()
		end()
		Expect(f.host.checks).To(ConsistOf(egress.CheckArgs{Host: "public.test", Port: port, Kind: "connect", TLS: true}))
		Expect(f.host.ops).To(ConsistOf("op1"))
		Expect(f.host.audit()).To(ContainElement(SatisfyAll(HaveField("Verdict", "done"), HaveField("Host", "public.test"), HaveField("BytesDown", BeNumerically(">", 0)))))
	})

	It("closes a TLS tunnel whose server name is another host, and one that does not start with TLS", func() {
		port := portOf(tlsSrv.URL)
		f := newFixture(port, allow, egress.Limits{})
		end := f.proxy.Begin(context.Background(), "alice", "c1", "op1")
		c, br, resp := connect(f, fmt.Sprintf("public.test:%d", port))
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		tc := tls.Client(&bufConn{Conn: c, r: br}, &tls.Config{ServerName: "other.test", InsecureSkipVerify: true}) //nolint:gosec // a test server
		_ = tc.SetDeadline(time.Now().Add(5 * time.Second))
		Expect(tc.Handshake()).NotTo(Succeed())

		c2, br2, resp2 := connect(f, fmt.Sprintf("public.test:%d", port))
		Expect(resp2.StatusCode).To(Equal(http.StatusOK))
		_, _ = io.WriteString(c2, "GET / HTTP/1.1\r\nHost: public.test\r\n\r\n")
		_ = c2.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, err := br2.ReadByte()
		Expect(err).To(HaveOccurred(), "plain bytes in a TLS tunnel are cut")
		end()
		sni := 0
		for _, e := range f.host.audit() {
			if e.Reason == "sni" {
				sni++
			}
		}
		Expect(sni).To(Equal(2))
	})

	It("answers a block with 403 and the line the model reads, and does not cache it", func() {
		var mu sync.Mutex
		verdict := egress.Verdict{Action: "block", Reason: "off"}
		f := newFixture(0, func(egress.CheckArgs) egress.Verdict { mu.Lock(); defer mu.Unlock(); return verdict }, egress.Limits{})
		end := f.proxy.Begin(context.Background(), "alice", "c1", "op1")
		defer end()
		_, _, resp := connect(f, "evil.example:443")
		Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
		Expect(resp.Header.Get("Content-Type")).To(HavePrefix("text/plain"))
		Expect(body(resp)).To(Equal("Blocked by LocalAI: evil.example:443 is not allowed for this chat (off). Ask the user to allow it.\n"))
		_, _, _ = connect(f, "evil.example:443")
		Expect(f.host.nChecks()).To(Equal(2), "a block is not cached")
	})

	It("tells the model a block by the server's setting is not a missing approval", func() {
		f := newFixture(0, func(egress.CheckArgs) egress.Verdict { return egress.Verdict{Action: "block", Reason: "ceiling"} }, egress.Limits{})
		end := f.proxy.Begin(context.Background(), "alice", "c1", "op1")
		defer end()
		_, _, resp := connect(f, "evil.example:443")
		Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
		text := body(resp)
		Expect(text).To(SatisfyAll(ContainSubstring("server's setting"), ContainSubstring("not by a missing approval"), ContainSubstring("will not help")))
		Expect(text).NotTo(ContainSubstring("Ask the user to allow it"))
	})

	It("refuses a host the guard refuses without asking or resolving", func() {
		f := newFixture(0, allow, egress.Limits{})
		end := f.proxy.Begin(context.Background(), "alice", "c1", "op1")
		for _, target := range []string{"169.254.169.254:443", "0x7f.1:443", "localhost:443"} {
			_, _, resp := connect(f, target)
			Expect(resp.StatusCode).To(Equal(http.StatusForbidden), target)
		}
		_, _, resp := connect(f, "public.test:80")
		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest), "CONNECT to port 80")
		end()
		Expect(f.host.nChecks()).To(BeZero())
		Expect(f.host.audit()).To(ContainElement(HaveField("Reason", "metadata")))
	})

	It("forwards an absolute-form HTTP request, drops the proxy headers and follows no redirect", func() {
		f := newFixture(0, allow, egress.Limits{})
		end := f.proxy.Begin(context.Background(), "alice", "c1", "op1")
		port := portOf(plainSrv.URL)
		c := f.dial()
		br := bufio.NewReader(c)
		_, _ = fmt.Fprintf(c, "GET http://public.test:%d/headers HTTP/1.1\r\nHost: public.test:%d\r\nProxy-Authorization: Basic eDp5\r\nProxy-Connection: keep-alive\r\n\r\n", port, port)
		resp := readResponse(c, br, nil)
		Expect(body(resp)).To(Equal(`auth="" pc=""`))
		// The same kept-alive connection: a redirect comes back to the guest.
		_, _ = fmt.Fprintf(c, "GET http://public.test:%d/redirect HTTP/1.1\r\nHost: public.test:%d\r\n\r\n", port, port)
		resp = readResponse(c, br, nil)
		Expect(resp.StatusCode).To(Equal(http.StatusFound))
		_ = body(resp)
		// The guest follows it with a new request, which is checked again.
		_, _ = fmt.Fprintf(c, "GET http://other.test:%d/ HTTP/1.1\r\nHost: other.test:%d\r\nConnection: close\r\n\r\n", port, port)
		resp = readResponse(c, br, nil)
		Expect(body(resp)).To(Equal("plain"))
		end()
		hosts := []string{}
		for _, a := range f.host.checks {
			hosts = append(hosts, a.Host)
		}
		Expect(hosts).To(Equal([]string{"public.test", "other.test"}), "public.test was cached for the call; other.test was checked")
	})

	It("refuses the origin form, https in absolute form, an Upgrade and a Host that is not the URL's", func() {
		f := newFixture(0, allow, egress.Limits{})
		end := f.proxy.Begin(context.Background(), "alice", "c1", "op1")
		defer end()
		port := portOf(plainSrv.URL)
		for _, raw := range []string{
			"GET / HTTP/1.1\r\nHost: public.test\r\n\r\n",
			"GET https://public.test/ HTTP/1.1\r\nHost: public.test\r\n\r\n",
			fmt.Sprintf("GET http://public.test:%d/ HTTP/1.1\r\nHost: public.test:%d\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n", port, port),
			fmt.Sprintf("GET http://public.test:%d/ HTTP/1.1\r\nHost: other.test\r\n\r\n", port),
		} {
			c := f.dial()
			_, _ = io.WriteString(c, raw)
			resp := readResponse(c, bufio.NewReader(c), nil)
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest), raw)
		}
		Expect(f.host.nChecks()).To(BeZero())
	})

	It("keeps an allow for 30 seconds of the call only", func() {
		f := newFixture(0, allow, egress.Limits{})
		port := portOf(plainSrv.URL)
		get := func() {
			c := f.dial()
			_, _ = fmt.Fprintf(c, "GET http://public.test:%d/ HTTP/1.1\r\nHost: public.test:%d\r\nConnection: close\r\n\r\n", port, port)
			resp := readResponse(c, bufio.NewReader(c), nil)
			Expect(body(resp)).To(Equal("plain"))
		}
		end := f.proxy.Begin(context.Background(), "alice", "c1", "op1")
		get()
		get()
		Expect(f.host.nChecks()).To(Equal(1))
		f.advance(31 * time.Second)
		get()
		Expect(f.host.nChecks()).To(Equal(2))
		end()
		end2 := f.proxy.Begin(context.Background(), "alice", "c1", "op2")
		get()
		end2()
		Expect(f.host.nChecks()).To(Equal(3), "a new call asks again")
	})

	It("closes the tunnels of a call when it ends", func() {
		port := portOf(tlsSrv.URL)
		f := newFixture(port, allow, egress.Limits{})
		end := f.proxy.Begin(context.Background(), "alice", "c1", "op1")
		c, br, resp := connect(f, fmt.Sprintf("public.test:%d", port))
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		tc := tls.Client(&bufConn{Conn: c, r: br}, &tls.Config{ServerName: "public.test", InsecureSkipVerify: true}) //nolint:gosec // a test server
		Expect(tc.Handshake()).To(Succeed())
		end()
		_ = tc.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, err := tc.Read(make([]byte, 1))
		Expect(err).To(HaveOccurred())
		Expect(err).NotTo(MatchError(ContainSubstring("timeout")), "closed, not timed out")
	})

	It("limits the connections of a sandbox and the bytes of a call", func() {
		f := newFixture(0, allow, egress.Limits{Rate: 1, Burst: 2, DownBytes: 1000})
		end := f.proxy.Begin(context.Background(), "alice", "c1", "op1")
		defer end()
		port := portOf(plainSrv.URL)
		codes := []int{}
		for range 3 {
			c := f.dial()
			_, _ = fmt.Fprintf(c, "GET http://public.test:%d/ HTTP/1.1\r\nHost: public.test:%d\r\nConnection: close\r\n\r\n", port, port)
			resp := readResponse(c, bufio.NewReader(c), nil)
			_ = body(resp)
			codes = append(codes, resp.StatusCode)
		}
		Expect(codes).To(Equal([]int{200, 200, 503}), "burst 2, then the rate")
		f.advance(5 * time.Second)
		c := f.dial()
		_, _ = fmt.Fprintf(c, "GET http://public.test:%d/big HTTP/1.1\r\nHost: public.test:%d\r\nConnection: close\r\n\r\n", port, port)
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		got, _ := io.ReadAll(c)
		Expect(len(got)).To(BeNumerically("<", 4096), "the download is cut at the call's cap")
	})

	It("asks again for a name with private answers, and dials it only with an administrator's allow", func() {
		f := newFixture(0, func(a egress.CheckArgs) egress.Verdict {
			if a.Private {
				return egress.Verdict{Action: "block", Reason: "private"}
			}
			return egress.Verdict{Action: "allow", Reason: "preset"}
		}, egress.Limits{})
		end := f.proxy.Begin(context.Background(), "alice", "c1", "op1")
		_, _, resp := connect(f, "private.test:443")
		Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
		Expect(body(resp)).To(ContainSubstring("(private)"))
		end()
		Expect(f.host.checks).To(HaveLen(2))
		Expect(f.host.checks[1].Private).To(BeTrue())
	})

	It("refuses the server's own public address that the host sends with an allow, also from the cache", func() {
		// 8.8.4.4 stands for the public address of the server (the host of
		// its external base URL): not an interface of the add-on's machine,
		// so only the host's list can name it. The guard refuses it before
		// any dial.
		f := newFixture(0, func(egress.CheckArgs) egress.Verdict {
			return egress.Verdict{Action: "allow", Reason: "preset", Self: []string{"8.8.4.4/32", "bogus"}}
		}, egress.Limits{})
		end := f.proxy.Begin(context.Background(), "alice", "c1", "op1")
		for range 2 {
			_, _, resp := connect(f, "self.test:443")
			Expect(resp.StatusCode).To(Equal(http.StatusBadGateway))
			Expect(body(resp)).To(ContainSubstring("(self)"))
		}
		end()
		Expect(f.host.nChecks()).To(Equal(1), "the second connection used the cached allow and was still refused")
		self := 0
		for _, e := range f.host.audit() {
			if e.Reason == "self" {
				self++
			}
		}
		Expect(self).To(Equal(2))
	})
})

// bufConn reads through the bufio.Reader that read the CONNECT answer.
type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufConn) Read(p []byte) (int, error) { return b.r.Read(p) }
