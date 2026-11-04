package http

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/pkg/clientip"
	"github.com/mudler/xlog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("HTTP access log", func() {
	var logs *bytes.Buffer

	BeforeEach(func() {
		logs = new(bytes.Buffer)
		xlog.SetLogger(xlog.NewLoggerWithHandler(slog.NewJSONHandler(logs, nil), xlog.LogLevelDebug))
		DeferCleanup(func() { xlog.SetLogger(xlog.NewLogger(xlog.LogLevelInfo, "text")) })
	})

	// serve sends one request through the access log and returns the log line.
	serve := func(logClient bool, mutate func(*http.Request)) string {
		logs.Reset()
		e := echo.New()
		e.Use(accessLog(logClient, nil))
		e.POST("/chat/completions", func(c echo.Context) error {
			return echo.NewHTTPError(http.StatusUnauthorized, "no key")
		})
		req := httptest.NewRequest(http.MethodPost, "/chat/completions", nil)
		req.RemoteAddr = "192.0.2.10:54321"
		if mutate != nil {
			mutate(req)
		}
		e.ServeHTTP(httptest.NewRecorder(), req)
		return logs.String()
	}

	It("logs the peer address and the user agent", func() {
		line := serve(true, func(r *http.Request) { r.Header.Set("User-Agent", "curl/8.5.0") })
		Expect(line).To(ContainSubstring(`"remote":"192.0.2.10"`))
		Expect(line).To(ContainSubstring(`"user_agent":"curl/8.5.0"`))
		Expect(line).To(ContainSubstring(`"status":401`))
		Expect(line).To(ContainSubstring(`"path":"/chat/completions"`))
	})

	It("ignores a forged X-Forwarded-For and X-Real-Ip", func() {
		line := serve(true, func(r *http.Request) {
			r.Header.Set("X-Forwarded-For", "203.0.113.99")
			r.Header.Set("X-Real-Ip", "203.0.113.98")
		})
		Expect(line).To(ContainSubstring(`"remote":"192.0.2.10"`))
		Expect(line).NotTo(ContainSubstring("203.0.113"))
	})

	It("takes the client from X-Forwarded-For only when the peer is a trusted proxy", func() {
		trusted, err := clientip.Parse("192.0.2.0/24")
		Expect(err).NotTo(HaveOccurred())
		logs.Reset()
		e := echo.New()
		e.Use(accessLog(true, trusted))
		e.GET("/x", func(c echo.Context) error { return c.NoContent(http.StatusOK) })
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "192.0.2.10:54321"
		req.Header.Set("X-Forwarded-For", "198.51.100.7, 203.0.113.5, 192.0.2.20")
		e.ServeHTTP(httptest.NewRecorder(), req)
		Expect(logs.String()).To(ContainSubstring(`"remote":"203.0.113.5"`))

		logs.Reset()
		req = httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "198.51.100.99:1"
		req.Header.Set("X-Forwarded-For", "203.0.113.5")
		e.ServeHTTP(httptest.NewRecorder(), req)
		Expect(logs.String()).To(ContainSubstring(`"remote":"198.51.100.99"`))
	})

	It("truncates the user agent to 80 characters", func() {
		line := serve(true, func(r *http.Request) { r.Header.Set("User-Agent", strings.Repeat("a", 500)) })
		Expect(line).To(ContainSubstring(`"user_agent":"` + strings.Repeat("a", 80) + `"`))
	})

	It("replaces control characters in the user agent", func() {
		Expect(sanitizeLogValue("evil\r\nINFO fake\tline\x00", 80)).To(Equal("evil  INFO fake line "))
		Expect(sanitizeLogValue(strings.Repeat("é", 100), 80)).To(HaveLen(160))
	})

	It("adds neither field when the option is off", func() {
		line := serve(false, func(r *http.Request) { r.Header.Set("User-Agent", "curl/8.5.0") })
		Expect(line).NotTo(ContainSubstring("remote"))
		Expect(line).NotTo(ContainSubstring("user_agent"))
		Expect(line).To(ContainSubstring(`"status":401`))
	})

	It("never logs credentials, query strings or cookies", func() {
		line := serve(true, func(r *http.Request) {
			r.URL.RawQuery = "api_key=sk-query-secret"
			r.Header.Set("Authorization", "Bearer sk-header-secret")
			r.Header.Set("Cookie", "session=cookie-secret")
		})
		Expect(line).NotTo(ContainSubstring("sk-header-secret"))
		Expect(line).NotTo(ContainSubstring("Bearer"))
		Expect(line).NotTo(ContainSubstring("sk-query-secret"))
		Expect(line).NotTo(ContainSubstring("cookie-secret"))
		Expect(line).To(ContainSubstring(`"path":"/chat/completions"`))
	})

	It("logs the duration and the request id when one is present", func() {
		line := serve(true, func(r *http.Request) { r.Header.Set("X-Request-Id", "req-42\nforged") })
		Expect(line).To(ContainSubstring(`"duration":`))
		Expect(line).To(ContainSubstring(`"request_id":"req-42 forged"`))
		Expect(serve(true, nil)).NotTo(ContainSubstring("request_id"))
	})

	It("falls back to the raw address when it has no port", func() {
		line := serve(true, func(r *http.Request) { r.RemoteAddr = "@" })
		Expect(line).To(ContainSubstring(`"remote":"@"`))
	})
})

var _ = DescribeTable("client address with no trusted proxies",
	func(remote string, headers map[string]string, want string) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = remote
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		Expect((*clientip.Resolver)(nil).Resolve(req)).To(Equal(want))
	},
	Entry("ipv4 with port", "192.0.2.10:1234", nil, "192.0.2.10"),
	Entry("ipv6 with port", "[2001:db8::1]:1234", nil, "2001:db8::1"),
	Entry("no port", "192.0.2.10", nil, "192.0.2.10"),
	Entry("spoofed X-Forwarded-For", "192.0.2.10:1234", map[string]string{"X-Forwarded-For": "203.0.113.7"}, "192.0.2.10"),
	Entry("spoofed X-Forwarded-For chain", "192.0.2.10:1234", map[string]string{"X-Forwarded-For": "203.0.113.7, 198.51.100.1"}, "192.0.2.10"),
	Entry("spoofed X-Real-Ip", "192.0.2.10:1234", map[string]string{"X-Real-Ip": "203.0.113.7"}, "192.0.2.10"),
	Entry("spoofed Forwarded", "192.0.2.10:1234", map[string]string{"Forwarded": "for=203.0.113.7"}, "192.0.2.10"),
)
