package middleware

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The trace buffer feeds an admin-only /api/traces endpoint. Even so, it
// must not retain the request's auth headers — once captured they outlive
// the request, get serialised to JSON for the dashboard, and could leak via
// any heap inspection. This pins the redaction contract so a future refactor
// of TraceMiddleware can't silently regress it.
var _ = Describe("redactSensitiveHeaders", func() {
	It("redacts Authorization", func() {
		h := http.Header{}
		h.Set("Authorization", "Bearer sk-secret-1234567890")
		out := redactSensitiveHeaders(h)
		Expect(out.Get("Authorization")).To(Equal("[redacted]"))
		Expect(out.Get("Authorization")).ToNot(ContainSubstring("sk-secret"))
	})

	It("redacts Proxy-Authorization", func() {
		h := http.Header{}
		h.Set("Proxy-Authorization", "Basic dXNlcjpwYXNz")
		Expect(redactSensitiveHeaders(h).Get("Proxy-Authorization")).To(Equal("[redacted]"))
	})

	It("redacts Cookie and Set-Cookie", func() {
		h := http.Header{}
		h.Set("Cookie", "session=abc123; csrf=xyz")
		h.Set("Set-Cookie", "session=newvalue; HttpOnly")
		out := redactSensitiveHeaders(h)
		Expect(out.Get("Cookie")).To(Equal("[redacted]"))
		Expect(out.Get("Set-Cookie")).To(Equal("[redacted]"))
	})

	It("redacts X-Api-Key (and case variants)", func() {
		h := http.Header{}
		h.Set("X-Api-Key", "key-1")
		h.Set("xi-api-key", "key-2")
		out := redactSensitiveHeaders(h)
		Expect(out.Get("X-Api-Key")).To(Equal("[redacted]"))
		Expect(out.Get("Xi-Api-Key")).To(Equal("[redacted]"))
	})

	It("preserves benign headers", func() {
		h := http.Header{}
		h.Set("Content-Type", "application/json")
		h.Set("User-Agent", "curl/8.0")
		h.Set("Accept", "*/*")
		out := redactSensitiveHeaders(h)
		Expect(out.Get("Content-Type")).To(Equal("application/json"))
		Expect(out.Get("User-Agent")).To(Equal("curl/8.0"))
		Expect(out.Get("Accept")).To(Equal("*/*"))
	})

	It("does not mutate the input header", func() {
		h := http.Header{}
		h.Set("Authorization", "Bearer abc")
		_ = redactSensitiveHeaders(h)
		Expect(h.Get("Authorization")).To(Equal("Bearer abc"),
			"redactSensitiveHeaders must operate on a clone — caller's header must be untouched")
	})
})
