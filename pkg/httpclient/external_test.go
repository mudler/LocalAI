package httpclient

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeResolver map[string][]string

func (f fakeResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	var out []net.IPAddr
	for _, s := range f[host] {
		out = append(out, net.IPAddr{IP: net.ParseIP(s)})
	}
	if out == nil {
		return nil, errors.New("no such host")
	}
	return out, nil
}

var _ = Describe("external URL policy", func() {
	DescribeTable("IsPublicIP",
		func(addr string, want bool) {
			Expect(IsPublicIP(net.ParseIP(addr))).To(Equal(want))
		},
		Entry("public IPv4", "93.184.216.34", true),
		Entry("documentation range used by tests", "203.0.113.1", true),
		Entry("public IPv6", "2606:4700:4700::1111", true),
		Entry("loopback", "127.0.0.1", false),
		Entry("IPv6 loopback", "::1", false),
		Entry("RFC 1918", "10.1.2.3", false),
		Entry("link-local metadata address", "169.254.169.254", false),
		Entry("shared address space low edge", "100.64.0.0", false),
		Entry("shared address space inside", "100.100.100.200", false),
		Entry("shared address space high edge", "100.127.255.255", false),
		Entry("just below shared address space", "100.63.255.255", true),
		Entry("just above shared address space", "100.128.0.1", true),
		Entry("this network, single address", "0.0.0.0", false),
		Entry("this network, other address", "0.1.2.3", false),
		Entry("benchmarking range", "198.19.0.1", false),
		Entry("multicast", "224.0.0.1", false),
		Entry("reserved class E", "240.0.0.1", false),
		Entry("IPv6 unique local", "fd00::1", false),
		Entry("IPv6 unspecified", "::", false),
		Entry("IPv4-mapped loopback", "::ffff:127.0.0.1", false),
		Entry("IPv4-mapped shared address space", "::ffff:100.64.0.1", false),
		Entry("IPv4-mapped public", "::ffff:93.184.216.34", true),
		Entry("NAT64 wrapping a private address", "64:ff9b::a00:1", false),
		Entry("NAT64 wrapping a public address", "64:ff9b::5db8:d822", true),
	)

	Describe("redirect validation", func() {
		var (
			hits     atomic.Int32
			sink     *httptest.Server
			redirect func(target string) *http.Client
		)

		BeforeEach(func() {
			hits.Store(0)
			sink = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				_, _ = w.Write([]byte("ok"))
			}))
			DeferCleanup(sink.Close)

			// The first hop is a TEST-NET literal so no DNS is needed; the
			// transport serves every request from the in-process server, which
			// answers with a redirect to target on the first call.
			redirect = func(target string) *http.Client {
				var first atomic.Bool
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if first.CompareAndSwap(false, true) {
						http.Redirect(w, r, target, http.StatusFound)
						return
					}
					hits.Add(1)
					_, _ = w.Write([]byte("ok"))
				}))
				DeferCleanup(srv.Close)
				return New(WithFollowRedirects(), WithExternalOnly(), WithTransport(&http.Transport{
					DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
						return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
					},
				}))
			}
		})

		DescribeTable("refuses non-public targets",
			func(target string) {
				_, err := redirect(target).Get("http://203.0.113.1/start")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("redirect URL validation failed"))
				Expect(hits.Load()).To(BeZero())
			},
			Entry("loopback", "http://127.0.0.1:1/x"),
			Entry("link-local metadata", "http://169.254.169.254/latest/meta-data/"),
			Entry("private", "http://10.0.0.5/x"),
			Entry("shared address space", "http://100.64.0.1/x"),
			Entry("localhost name", "http://localhost/x"),
			Entry("non-http scheme", "ftp://203.0.113.9/x"),
		)

		It("follows a redirect to another public target", func() {
			resp, err := redirect("http://203.0.113.2/next").Get("http://203.0.113.1/start")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(resp.Body.Close)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(hits.Load()).To(Equal(int32(1)))
		})

		It("keeps the redirect bound", func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "http://203.0.113.2/loop", http.StatusFound)
			}))
			DeferCleanup(srv.Close)
			c := New(WithFollowRedirects(), WithExternalOnly(), WithTransport(&http.Transport{
				DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
				},
			}))
			_, err := c.Get("http://203.0.113.1/loop")
			Expect(err).To(MatchError(ContainSubstring("stopped after")))
		})
	})

	Describe("validated dial", func() {
		errStop := errors.New("stop before connecting")

		// capture returns a dial function whose underlying dialer records the
		// address it was asked to connect to and then aborts, so no packet is
		// ever sent to the (documentation-range) address.
		capture := func(got *string) func(context.Context, string, string) (net.Conn, error) {
			return validatedDialContext(&net.Dialer{Control: func(_, address string, _ syscall.RawConn) error {
				*got = address
				return errStop
			}})
		}

		BeforeEach(func() {
			orig := resolver
			DeferCleanup(func() { resolver = orig })
		})

		It("refuses a host that resolves only to private addresses", func() {
			resolver = fakeResolver{"internal.example": {"10.0.0.7"}}
			var got string
			_, err := capture(&got)(context.Background(), "tcp", "internal.example:80")
			Expect(err).To(MatchError(ErrNonPublicAddress))
			Expect(got).To(BeEmpty())
		})

		It("refuses a host when any answer is non-public", func() {
			resolver = fakeResolver{"mixed.example": {"198.51.100.7", "100.64.0.9"}}
			var got string
			_, err := capture(&got)(context.Background(), "tcp", "mixed.example:80")
			Expect(err).To(MatchError(ErrNonPublicAddress))
			Expect(got).To(BeEmpty())
		})

		It("refuses a literal internal address", func() {
			var got string
			_, err := capture(&got)(context.Background(), "tcp", "127.0.0.1:80")
			Expect(err).To(MatchError(ErrNonPublicAddress))
		})

		It("dials the validated address, not a fresh lookup", func() {
			resolver = fakeResolver{"public.example": {"198.51.100.7"}}
			var got string
			_, err := capture(&got)(context.Background(), "tcp", "public.example:8080")
			Expect(err).To(HaveOccurred())
			Expect(got).To(Equal("198.51.100.7:8080"))
		})
	})
})
