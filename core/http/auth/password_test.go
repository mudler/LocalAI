package auth_test

import (
	"strings"

	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Password policy", func() {
	Describe("ValidatePasswordStrength", func() {
		// Anything below MinPasswordScore is rejected. zxcvbn scores are subject
		// to the embedded dictionary; if the underlying dictionary changes we
		// want the test to break loudly so we can re-baseline rather than
		// silently accept weaker passwords.
		DescribeTable("rejects weak inputs",
			func(pw string) {
				Expect(auth.ValidatePasswordStrength(pw)).ToNot(Succeed())
			},
			Entry("too short", "Tr0ub4dor"),
			Entry("empty", ""),
			Entry("common: password", "password1234"),
			Entry("common: 12345", "12345678901234"),
			Entry("common: qwerty", "qwertyuiopas"),
			Entry("keyboard run", "qwertyuiop12"),
			Entry("app branding only", "localailocalai"),
			Entry("repeated word", "passwordpassword"),
		)

		DescribeTable("accepts strong inputs",
			func(pw string) {
				Expect(auth.ValidatePasswordStrength(pw)).To(Succeed())
			},
			Entry("diceware-style passphrase", "correct horse battery staple unicycle"),
			Entry("random-ish 14-char with punctuation", "Th3-Quick~Br0wn-F0x.Jumps"),
			Entry("random mixed", "q9V$mZ1pL7nB3w"),
		)

		Context("length boundaries", func() {
			It("returns ErrPasswordEmpty for empty input", func() {
				Expect(auth.ValidatePasswordStrength("")).To(MatchError(auth.ErrPasswordEmpty))
			})
			It("returns ErrPasswordTooShort just under the floor", func() {
				short := strings.Repeat("a", auth.MinPasswordLength-1)
				Expect(auth.ValidatePasswordStrength(short)).To(MatchError(auth.ErrPasswordTooShort))
			})
			It("returns ErrPasswordTooLong just over the ceiling", func() {
				long := strings.Repeat("a", auth.MaxPasswordLength+1)
				Expect(auth.ValidatePasswordStrength(long)).To(MatchError(auth.ErrPasswordTooLong))
			})
		})

		It("rejects passwords containing NUL bytes", func() {
			Expect(auth.ValidatePasswordStrength("abcdef\x00ghijklmnop")).To(MatchError(auth.ErrPasswordNullByte))
		})

		// AllowWeak skips the policy-level checks (length floor + entropy) but
		// the technical invariants (empty / max-length / NUL byte) always apply.
		Context("with AllowWeak override", func() {
			weak := "password1234"

			It("rejects the weak password by default", func() {
				Expect(auth.ValidatePasswordStrength(weak)).To(MatchError(auth.ErrPasswordTooWeak))
			})
			It("accepts the weak password when AllowWeak is set", func() {
				Expect(auth.ValidatePasswordStrength(weak, auth.PasswordPolicy{AllowWeak: true})).To(Succeed())
			})
			It("bypasses the length floor", func() {
				Expect(auth.ValidatePasswordStrength("short", auth.PasswordPolicy{AllowWeak: true})).To(Succeed())
			})
			It("does not bypass the empty-input check", func() {
				Expect(auth.ValidatePasswordStrength("", auth.PasswordPolicy{AllowWeak: true})).To(MatchError(auth.ErrPasswordEmpty))
			})
			It("does not bypass the NUL-byte check", func() {
				Expect(auth.ValidatePasswordStrength("ok\x00password1234", auth.PasswordPolicy{AllowWeak: true})).To(MatchError(auth.ErrPasswordNullByte))
			})
			It("does not bypass the max-length check", func() {
				long := strings.Repeat("a", auth.MaxPasswordLength+1)
				Expect(auth.ValidatePasswordStrength(long, auth.PasswordPolicy{AllowWeak: true})).To(MatchError(auth.ErrPasswordTooLong))
			})
		})
	})

	Describe("PasswordError", func() {
		DescribeTable("produces a structured response",
			func(err error, code string, overridable bool) {
				r := auth.PasswordError(err)
				Expect(r.ErrorCode).To(Equal(code))
				Expect(r.Overridable).To(Equal(overridable))
				Expect(r.Error).ToNot(BeEmpty())
			},
			Entry("empty", auth.ErrPasswordEmpty, "password_empty", false),
			Entry("too short", auth.ErrPasswordTooShort, "password_too_short", true),
			Entry("too long", auth.ErrPasswordTooLong, "password_too_long", false),
			Entry("null byte", auth.ErrPasswordNullByte, "password_null_byte", false),
			Entry("too weak", auth.ErrPasswordTooWeak, "password_too_weak", true),
		)
	})
})
