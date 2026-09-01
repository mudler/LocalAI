package worker

import (
	"net"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Worker address resolution", func() {
	// advertiseAddr and advertiseHTTPAddr used to be specced here. They are
	// gone with the addresses they resolved: a worker advertises nothing. What
	// they pinned that still matters is below: the port arithmetic they shared
	// with the two functions that survived, and the fact that neither of those
	// resolves to anything but this host.
	Describe("effectiveBasePort", func() {
		DescribeTable("returns the correct port",
			func(addr, serve string, want int) {
				cfg := &Config{Addr: addr, ServeAddr: serve}
				Expect(cfg.effectiveBasePort()).To(Equal(want))
			},
			Entry("Addr takes priority", "worker1.example.com:60000", "0.0.0.0:50051", 60000),
			Entry("falls back to ServeAddr", "", "0.0.0.0:50051", 50051),
			Entry("returns 50051 when neither set", "", "", 50051),
			Entry("Addr with custom port", "10.0.0.5:7000", "", 7000),
			Entry("supports bracketed IPv6", "[2001:db8::1]:7001", "", 7001),
			Entry("invalid port in Addr falls through to ServeAddr", "host:notanumber", "0.0.0.0:9999", 9999),
			Entry("out-of-range port in Addr falls through to ServeAddr", "host:70000", "0.0.0.0:9998", 9998),
		)
	})

	Describe("resolveHTTPAddr", func() {
		DescribeTable("returns the correct address",
			func(httpAddr, addr, serve, want string) {
				cfg := &Config{HTTPAddr: httpAddr, Addr: addr, ServeAddr: serve}
				Expect(cfg.resolveHTTPAddr()).To(Equal(want))
			},
			// An explicit HTTPAddr is bound exactly as written, wildcard
			// included: an operator who asks for a routable bind gets one, and
			// the tunnel still reaches it because the http tag ignores the
			// target and dials whatever this returned.
			Entry("HTTPAddr takes priority", "0.0.0.0:8080", "", "", "0.0.0.0:8080"),
			Entry("derives from Addr port minus 1", "", "worker1:60000", "0.0.0.0:50051", "127.0.0.1:59999"),
			Entry("derives from ServeAddr port minus 1", "", "", "0.0.0.0:50051", "127.0.0.1:50050"),
			Entry("default when nothing set", "", "", "", "127.0.0.1:50050"),
		)

		It("takes only the port from Addr, never its host", func() {
			// The host half of Addr names an interface nothing binds any more.
			// A default bind that carried it forward would put the
			// file-transfer server back on a routable address.
			cfg := &Config{Addr: "0.0.0.0:60000"}
			Expect(cfg.resolveHTTPAddr()).To(Equal("127.0.0.1:59999"))
		})
	})

	Describe("backendListenAddr", func() {
		It("binds a backend process on the host the tunnel dials", func() {
			// Not a literal on either side: this asserts the bind is built from
			// the same constant the grpc stream tag dials, which is what makes
			// "the worker binds where its tunnel dials" true rather than
			// coincidental.
			Expect(backendListenAddr(50052)).To(Equal(net.JoinHostPort(loopbackHost, strconv.Itoa(50052))))
		})

		It("binds no wildcard", func() {
			// Stated separately from the equality above so a change to
			// loopbackHost itself cannot make both pass while publishing every
			// backend process on every interface.
			host, _, err := net.SplitHostPort(backendListenAddr(50052))
			Expect(err).ToNot(HaveOccurred())
			ip := net.ParseIP(host)
			Expect(ip).ToNot(BeNil(), "the backend bind address must be an IP, not a name that could resolve anywhere")
			Expect(ip.IsLoopback()).To(BeTrue(), "backend processes must bind loopback only")
		})
	})

	Describe("registrationBody", func() {
		It("advertises no address at all", func() {
			// The registration body is one of the three places this worker used
			// to state where it could be reached. A key here is not inert: the
			// frontend stores it, the API returns it, and the Nodes page shows
			// it as an endpoint.
			cfg := &Config{NodeName: "w1", Addr: "0.0.0.0:50051", ModelsPath: GinkgoT().TempDir()}
			body := cfg.registrationBody()
			Expect(body).To(HaveKeyWithValue("name", "w1"))
			Expect(body).ToNot(HaveKey("address"))
			Expect(body).ToNot(HaveKey("http_address"))
		})
	})
})
