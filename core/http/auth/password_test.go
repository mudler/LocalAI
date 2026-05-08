package auth

import (
	"strings"
	"testing"
)

// Anything below MinPasswordScore is rejected. zxcvbn scores are subject to
// the embedded dictionary; if the underlying dictionary changes we want the
// test to break loudly so we can re-baseline rather than silently accept
// weaker passwords.
func TestValidatePasswordStrength_Rejects(t *testing.T) {
	cases := []struct {
		name string
		pw   string
	}{
		{"too short", "Tr0ub4dor"},
		{"empty", ""},
		{"common: password", "password1234"},
		{"common: 12345", "12345678901234"},
		{"common: qwerty", "qwertyuiopas"},
		{"keyboard run", "qwertyuiop12"},
		{"app branding only", "localailocalai"},
		{"repeated word", "passwordpassword"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePasswordStrength(tc.pw); err == nil {
				t.Fatalf("expected %q to be rejected", tc.pw)
			}
		})
	}
}

func TestValidatePasswordStrength_Accepts(t *testing.T) {
	cases := []string{
		// Diceware-style passphrase, three uncommon words + a digit.
		"correct horse battery staple unicycle",
		// Random-ish 14-char with punctuation.
		"Th3-Quick~Br0wn-F0x.Jumps",
		// Random mixed.
		"q9V$mZ1pL7nB3w",
	}
	for _, pw := range cases {
		t.Run(pw, func(t *testing.T) {
			if err := ValidatePasswordStrength(pw); err != nil {
				t.Fatalf("expected %q to be accepted, got %v", pw, err)
			}
		})
	}
}

func TestValidatePasswordStrength_LengthBoundaries(t *testing.T) {
	if err := ValidatePasswordStrength(strings.Repeat("a", MinPasswordLength-1)); err != ErrPasswordTooShort {
		t.Fatalf("expected ErrPasswordTooShort, got %v", err)
	}
	if err := ValidatePasswordStrength(strings.Repeat("a", MaxPasswordLength+1)); err != ErrPasswordTooLong {
		t.Fatalf("expected ErrPasswordTooLong, got %v", err)
	}
}

func TestValidatePasswordStrength_NullByte(t *testing.T) {
	pw := "abcdef\x00ghijklmnop"
	if err := ValidatePasswordStrength(pw); err != ErrPasswordNullByte {
		t.Fatalf("expected ErrPasswordNullByte, got %v", err)
	}
}

// AllowWeak skips the entropy check but the hard rules still apply.
func TestValidatePasswordStrength_AllowWeakSkipsEntropyOnly(t *testing.T) {
	weak := "password1234"
	if err := ValidatePasswordStrength(weak); err != ErrPasswordTooWeak {
		t.Fatalf("baseline: expected ErrPasswordTooWeak, got %v", err)
	}
	if err := ValidatePasswordStrength(weak, PasswordPolicy{AllowWeak: true}); err != nil {
		t.Fatalf("with AllowWeak: expected nil, got %v", err)
	}
	// Hard rules still bite even with AllowWeak.
	if err := ValidatePasswordStrength("short", PasswordPolicy{AllowWeak: true}); err != ErrPasswordTooShort {
		t.Fatalf("AllowWeak must not bypass length floor; got %v", err)
	}
	if err := ValidatePasswordStrength("ok\x00password1234", PasswordPolicy{AllowWeak: true}); err != ErrPasswordNullByte {
		t.Fatalf("AllowWeak must not bypass NUL check; got %v", err)
	}
}

func TestPasswordError_StructureAndOverridability(t *testing.T) {
	cases := []struct {
		err         error
		code        string
		overridable bool
	}{
		{ErrPasswordTooShort, "password_too_short", false},
		{ErrPasswordTooLong, "password_too_long", false},
		{ErrPasswordNullByte, "password_null_byte", false},
		{ErrPasswordTooWeak, "password_too_weak", true},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			r := PasswordError(tc.err)
			if r.ErrorCode != tc.code {
				t.Fatalf("ErrorCode: want %q, got %q", tc.code, r.ErrorCode)
			}
			if r.Overridable != tc.overridable {
				t.Fatalf("Overridable: want %v, got %v", tc.overridable, r.Overridable)
			}
			if r.Error == "" {
				t.Fatalf("Error must be populated")
			}
		})
	}
}
