package localai_test

import (
	"net"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// SSRF guards on the CORS proxy. Notably 0.0.0.0/8 and :: route to localhost
// on Linux, and ::ffff:127.0.0.1 (IPv4-mapped IPv6) reaches 127.0.0.1 —
// hand-rolled CIDR blocklists frequently miss these.
var _ = Describe("CORSProxy SSRF guards", func() {
	var app *echo.Echo

	BeforeEach(func() {
		app = echo.New()
		appConfig := config.NewApplicationConfig()
		app.GET("/api/cors-proxy", CORSProxyEndpoint(appConfig))
		app.POST("/api/cors-proxy", CORSProxyEndpoint(appConfig))
	})

	rejectsTarget := func(target string) {
		req := httptest.NewRequest(http.MethodGet, "/api/cors-proxy?url="+target, nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		// Any 4xx is acceptable — we only care that the request was rejected
		// before a connection was attempted to the local network.
		Expect(rec.Code).To(BeNumerically(">=", 400),
			"expected proxy to reject %s, got %d body=%s", target, rec.Code, rec.Body.String())
		Expect(rec.Code).To(BeNumerically("<", 500),
			"expected proxy to reject %s with 4xx, got %d body=%s", target, rec.Code, rec.Body.String())
	}

	It("rejects http://0.0.0.0/ (routes to localhost on Linux)", func() {
		rejectsTarget("http://0.0.0.0/anything")
	})

	It("rejects http://0.0.0.0:PORT/ (catches loopback bind on any port)", func() {
		rejectsTarget("http://0.0.0.0:8080/")
	})

	It("rejects http://[::]/ (IPv6 unspecified)", func() {
		rejectsTarget("http://[::]/")
	})

	It("rejects http://[::ffff:127.0.0.1]/ (IPv4-mapped IPv6 loopback)", func() {
		rejectsTarget("http://[::ffff:127.0.0.1]/")
	})

	It("rejects http://[::ffff:10.0.0.1]/ (IPv4-mapped IPv6 RFC1918)", func() {
		rejectsTarget("http://[::ffff:10.0.0.1]/")
	})

	It("rejects file:// scheme", func() {
		rejectsTarget("file:///etc/passwd")
	})

	It("rejects gopher:// scheme", func() {
		rejectsTarget("gopher://attacker.example.com:1234/")
	})

	It("rejects ftp:// scheme", func() {
		rejectsTarget("ftp://example.com/")
	})

	It("rejects http://localhost/", func() {
		rejectsTarget("http://localhost/")
	})

	It("rejects http://127.0.0.1/", func() {
		rejectsTarget("http://127.0.0.1/")
	})

	It("rejects http://10.0.0.1/", func() {
		rejectsTarget("http://10.0.0.1/")
	})

	It("rejects http://169.254.169.254/ (cloud metadata)", func() {
		rejectsTarget("http://169.254.169.254/latest/meta-data/")
	})

	It("rejects http://metadata.google.internal/", func() {
		rejectsTarget("http://metadata.google.internal/computeMetadata/v1/")
	})

	It("rejects requests with no url parameter", func() {
		req := httptest.NewRequest(http.MethodGet, "/api/cors-proxy", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})

	// Sanity: confirm the test runner machine resolves 0.0.0.0 to itself —
	// otherwise the test could pass for the wrong reason.
	It("baseline: 0.0.0.0 is classified as unspecified by Go stdlib", func() {
		ip := net.ParseIP("0.0.0.0")
		Expect(ip.IsUnspecified()).To(BeTrue())
	})

	It("baseline: :: is classified as unspecified by Go stdlib", func() {
		ip := net.ParseIP("::")
		Expect(ip.IsUnspecified()).To(BeTrue())
	})
})
