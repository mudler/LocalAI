package middleware

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// SafeForwardedPrefix gates every X-Forwarded-Prefix consumer (the path
// stripper and the SPA-shell redirect helpers). The threat is operator-
// trusted reverse-proxy headers being forgeable via a misconfigured chain;
// any value the attacker can inject must not escape the local origin.
var _ = Describe("SafeForwardedPrefix", func() {
	DescribeTable("accepts well-formed path prefixes",
		func(in string) {
			out, ok := SafeForwardedPrefix(in)
			Expect(ok).To(BeTrue(), "expected %q to validate", in)
			Expect(out).To(Equal(in))
		},
		Entry("simple", "/api"),
		Entry("nested", "/api/v1"),
		Entry("trailing slash", "/api/"),
		Entry("with hyphens and dots", "/api-v1.beta"),
	)

	DescribeTable("rejects values that would escape the origin",
		func(in string) {
			_, ok := SafeForwardedPrefix(in)
			Expect(ok).To(BeFalse(), "expected %q to be rejected", in)
		},
		Entry("empty", ""),
		Entry("whitespace only", "   "),
		Entry("protocol-relative", "//evil.com"),
		Entry("protocol-relative with path", "//evil.com/x"),
		Entry("absolute http URL", "http://evil.com"),
		Entry("absolute https URL", "https://evil.com/x"),
		Entry("javascript scheme", "javascript:alert(1)"),
		Entry("data scheme", "data:text/html,foo"),
		Entry("missing leading slash", "api"),
		Entry("backslash injection", "/foo\\evil.com"),
		Entry("CR injection", "/foo\rLocation: //evil.com"),
		Entry("LF injection", "/foo\nSet-Cookie: x=y"),
		Entry("NUL byte", "/foo\x00bar"),
	)

	It("trims surrounding whitespace before validating", func() {
		out, ok := SafeForwardedPrefix("  /api  ")
		Expect(ok).To(BeTrue())
		Expect(out).To(Equal("/api"))
	})
})
