package pii

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func mustCompile(ids ...string) []Pattern {
	all := DefaultPatterns()
	if len(ids) == 0 {
		out, err := Compile(all)
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "compile")
		return out
	}
	pickP := pick(all, ids)
	out, err := Compile(pickP)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "compile")
	return out
}

func pick(all []Pattern, ids []string) []Pattern {
	keep := map[string]bool{}
	for _, id := range ids {
		keep[id] = true
	}
	var out []Pattern
	for _, p := range all {
		if keep[p.ID] {
			out = append(out, p)
		}
	}
	return out
}

var _ = Describe("Redactor", func() {
	It("masks email", func() {
		r := NewRedactor(mustCompile("email"))
		res := r.Redact("Contact me at alice@example.com any time.")
		Expect(res.Blocked).To(BeFalse(), "email is mask-action by default, should not block")
		Expect(res.Redacted).To(ContainSubstring("[REDACTED:email]"))
		Expect(res.Redacted).NotTo(ContainSubstring("alice@example.com"))
		Expect(res.Spans).To(HaveLen(1))
		Expect(res.Spans[0].HashPrefix).NotTo(BeEmpty(), "hash prefix must be set so audits can dedupe leaks")
	})

	It("masks SSN", func() {
		r := NewRedactor(mustCompile("ssn"))
		res := r.Redact("call me about SSN 123-45-6789 please")
		Expect(res.Redacted).To(ContainSubstring("[REDACTED:ssn]"))
	})

	It("uses Luhn for credit card", func() {
		r := NewRedactor(mustCompile("credit_card"))

		// 4111 1111 1111 1111 — canonical Luhn-valid Visa test number.
		good := r.Redact("card: 4111 1111 1111 1111")
		Expect(good.Spans).To(HaveLen(1))
		Expect(good.Redacted).To(ContainSubstring("[REDACTED:credit_card]"))

		// 4111 1111 1111 1112 — same shape, fails Luhn. Must NOT match.
		bad := r.Redact("card: 4111 1111 1111 1112")
		Expect(bad.Spans).To(BeEmpty(), "Luhn-invalid 16-digit run must not be redacted")
		Expect(bad.Redacted).To(ContainSubstring("1112"), "Luhn-invalid input should pass through untouched")
	})

	It("validates IPv4 octets", func() {
		r := NewRedactor(mustCompile("ipv4"))

		good := r.Redact("server at 192.168.1.10 is up")
		Expect(good.Spans).To(HaveLen(1))

		// 999.999.999.999 — regex matches but octet > 255 must reject.
		bad := r.Redact("not an ip: 999.999.999.999")
		Expect(bad.Spans).To(BeEmpty(), "ipv4 with octet>255 must not match")
	})

	It("api_key defaults to block", func() {
		r := NewRedactor(mustCompile("api_key_prefix"))
		res := r.Redact("here's a token sk-abcdefghijklmnopqrstuvwxyz0123456789 to use")
		Expect(res.Blocked).To(BeTrue(), "api_key default action is block; Result.Blocked must be true")
		// The redacted output keeps the matched value when blocking — the
		// caller is expected to refuse the request, not to forward a partial.
		Expect(res.Redacted).To(ContainSubstring("sk-abcdefghijklmn"), "blocked actions leave the matched span intact for caller inspection")
	})

	It("preserves non-matching text", func() {
		r := NewRedactor(mustCompile()) // all default patterns
		in := "no PII here at all, just words and numbers like 42 and 1.5"
		res := r.Redact(in)
		Expect(res.Redacted).To(Equal(in), "non-PII input should pass through unchanged")
		Expect(res.Spans).To(BeEmpty())
	})

	It("handles empty input", func() {
		r := NewRedactor(mustCompile())
		res := r.Redact("")
		Expect(res.Redacted).To(BeEmpty())
		Expect(res.Blocked).To(BeFalse())
		Expect(res.LocalOnly).To(BeFalse())
		Expect(res.Spans).To(BeEmpty())
	})

	It("nil patterns is a no-op", func() {
		// Disabled-PII deployment: pii.NewRedactor(nil) is a no-op.
		r := NewRedactor(nil)
		res := r.Redact("alice@example.com sent it")
		Expect(res.Redacted).To(Equal("alice@example.com sent it"))
	})

	It("hash prefix is stable", func() {
		r := NewRedactor(mustCompile("email"))
		a := r.Redact("a@b.com")
		b := r.Redact("hi a@b.com again")
		Expect(a.Spans).To(HaveLen(1))
		Expect(b.Spans).To(HaveLen(1))
		Expect(a.Spans[0].HashPrefix).To(Equal(b.Spans[0].HashPrefix), "same matched value must produce same hash prefix")
	})
})

var _ = Describe("Compile", func() {
	It("rejects unknown pattern id", func() {
		_, err := Compile([]Pattern{{ID: "nonexistent", Action: ActionMask}})
		Expect(err).To(HaveOccurred(), "Compile must error on unknown pattern id")
	})
})

var _ = Describe("MaxPatternLength", func() {
	It("returns the longest pattern's max length", func() {
		patterns := mustCompile("email", "ssn")
		got := MaxPatternLength(patterns)
		// email is the longer of the two (254). The streaming filter
		// will use this to size its tail buffer.
		Expect(got).To(Equal(254))
	})
})

var _ = Describe("RedactWithOverrides", func() {
	It("upgrades action", func() {
		// email is mask by default; the per-model override turns it into a
		// hard block for one request without mutating the redactor.
		r := NewRedactor(mustCompile("email"))
		res := r.RedactWithOverrides("contact alice@example.com",
			map[string]Action{"email": ActionBlock})
		Expect(res.Blocked).To(BeTrue(), "override should have set Blocked")
		// Block leaves the value intact (the caller short-circuits the
		// request) — the redactor never echoes the matched text.
		Expect(res.Redacted).To(ContainSubstring("alice@example.com"), "block leaves text intact for the caller to discard")
		// Stored action is unchanged so a subsequent default Redact still
		// masks rather than blocks.
		res2 := r.Redact("contact alice@example.com")
		Expect(res2.Blocked).To(BeFalse(), "override must not mutate stored action")
	})

	It("ignores unknown IDs", func() {
		// An override for a pattern this redactor doesn't know about is a
		// no-op rather than an error — per-model configs may reference
		// patterns from a wider catalogue than the active redactor holds.
		r := NewRedactor(mustCompile("email"))
		res := r.RedactWithOverrides("contact alice@example.com",
			map[string]Action{"ssn": ActionBlock})
		Expect(res.Blocked).To(BeFalse(), "ssn override against email-only redactor must be no-op")
	})
})

var _ = Describe("SetAction", func() {
	It("swaps in place", func() {
		r := NewRedactor(mustCompile("email"))
		Expect(r.SetAction("email", ActionRouteLocal)).To(Succeed())
		res := r.Redact("contact alice@example.com")
		Expect(res.LocalOnly).To(BeTrue(), "expected LocalOnly after SetAction(route_local)")
		Expect(res.Blocked).To(BeFalse(), "SetAction(route_local) should not block")
	})

	It("rejects unknown id", func() {
		r := NewRedactor(mustCompile("email"))
		Expect(r.SetAction("nonexistent", ActionMask)).NotTo(Succeed(), "expected error for unknown pattern id")
	})

	It("rejects unknown action", func() {
		r := NewRedactor(mustCompile("email"))
		Expect(r.SetAction("email", Action("frobnicate"))).NotTo(Succeed(), "expected error for unknown action")
	})
})

