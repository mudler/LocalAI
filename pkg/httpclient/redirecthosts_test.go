package httpclient

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("WithRedirectHosts", func() {
	var (
		hits    atomic.Int32
		header  atomic.Value
		srv     *httptest.Server
		client  func(target string, hosts ...string) *http.Client
		request func(c *http.Client) (*http.Response, error)
	)

	BeforeEach(func() {
		hits.Store(0)
		header.Store(http.Header{})
		var target atomic.Value
		target.Store("https://203.0.113.9/asset")
		// Every address is served by one in-process TLS server: /start
		// answers with a redirect to target, any other path is the asset.
		srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/start" {
				http.Redirect(w, r, target.Load().(string), http.StatusFound)
				return
			}
			hits.Add(1)
			header.Store(r.Header.Clone())
			_, _ = w.Write([]byte("ok"))
		}))
		DeferCleanup(srv.Close)
		client = func(to string, hosts ...string) *http.Client {
			target.Store(to)
			return New(WithRedirectHosts(hosts...), WithTransport(&http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test server certificate
				DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
				},
			}))
		}
		request = func(c *http.Client) (*http.Response, error) {
			// TEST-NET literals need no DNS and pass the external policy.
			req, err := http.NewRequest(http.MethodGet, "https://203.0.113.1/start", nil)
			Expect(err).NotTo(HaveOccurred())
			req.Header.Set("Authorization", "Bearer first-host-secret")
			req.Header.Set("X-Private-Token", "first-host-secret")
			req.Header.Set("User-Agent", "localai-test")
			return c.Do(req)
		}
	})

	It("follows a redirect to an allowed https host and drops the first host's headers", func() {
		resp, err := request(client("https://203.0.113.2/asset?sig=abc", "203.0.113.2"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(resp.Body.Close)
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(hits.Load()).To(Equal(int32(1)))
		got := header.Load().(http.Header)
		Expect(got.Get("Authorization")).To(BeEmpty())
		Expect(got.Get("X-Private-Token")).To(BeEmpty())
		Expect(got.Get("User-Agent")).To(Equal("localai-test"))
	})

	DescribeTable("refuses a hop the allow-list does not cover",
		func(target string, hosts ...string) {
			_, err := request(client(target, hosts...))
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(ErrRedirectBlocked))
			Expect(hits.Load()).To(BeZero())
		},
		Entry("a host not on the list", "https://203.0.113.9/asset?sig=abc", "203.0.113.2"),
		Entry("plain http to an allowed host", "http://203.0.113.2/asset?sig=abc", "203.0.113.2"),
		Entry("another port of an allowed host", "https://203.0.113.2:8443/asset?sig=abc", "203.0.113.2"),
		Entry("an empty list", "https://203.0.113.2/asset?sig=abc"),
	)

	DescribeTable("refuses an allowed host that is not public",
		func(target, host string) {
			_, err := request(client(target, host))
			Expect(err).To(MatchError(ErrRedirectBlocked))
			Expect(err).To(MatchError(ContainSubstring("redirect URL validation failed")))
			Expect(hits.Load()).To(BeZero())
		},
		Entry("loopback", "https://127.0.0.1/asset", "127.0.0.1"),
		Entry("link-local metadata", "https://169.254.169.254/asset", "169.254.169.254"),
		Entry("private", "https://10.0.0.5/asset", "10.0.0.5"),
	)

	It("keeps the redirect bound", func() {
		loop := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://203.0.113.2/loop", http.StatusFound)
		}))
		DeferCleanup(loop.Close)
		c := New(WithRedirectHosts("203.0.113.2"), WithTransport(&http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test server certificate
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, loop.Listener.Addr().String())
			},
		}))
		_, err := c.Get("https://203.0.113.1/loop")
		Expect(err).To(MatchError(ErrRedirectBlocked))
		Expect(err).To(MatchError(ContainSubstring("stopped after")))
	})

	It("still refuses every redirect without the option", func() {
		_, err := request(New(WithTransport(&http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test server certificate
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
			},
		})))
		Expect(err).To(MatchError(ErrRedirectBlocked))
	})

	Describe("dial", func() {
		errStop := errors.New("stop before connecting")
		capture := func(got *string) func(context.Context, string, string) (net.Conn, error) {
			return allowListDialContext(&net.Dialer{Control: func(_, address string, _ syscall.RawConn) error {
				*got = address
				return errStop
			}}, map[string]bool{"assets.example": true})
		}

		BeforeEach(func() {
			orig := resolver
			DeferCleanup(func() { resolver = orig })
		})

		It("refuses an allowed host that resolves to a private address", func() {
			resolver = fakeResolver{"assets.example": {"10.0.0.7"}}
			var got string
			_, err := capture(&got)(context.Background(), "tcp", "assets.example:443")
			Expect(err).To(MatchError(ErrNonPublicAddress))
			Expect(got).To(BeEmpty())
		})

		It("dials the validated address of an allowed host", func() {
			resolver = fakeResolver{"assets.example": {"198.51.100.7"}}
			var got string
			_, err := capture(&got)(context.Background(), "tcp", "assets.example:443")
			Expect(err).To(HaveOccurred())
			Expect(got).To(Equal("198.51.100.7:443"))
		})

		It("leaves the dial of other hosts alone", func() {
			var got string
			_, err := capture(&got)(context.Background(), "tcp", "127.0.0.1:8080")
			Expect(err).To(MatchError(errStop))
			Expect(got).To(Equal("127.0.0.1:8080"))
		})
	})
})
