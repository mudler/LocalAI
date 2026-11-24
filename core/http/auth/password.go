package auth

import (
	"errors"
	"fmt"
	"strings"

	"github.com/timbutler/zxcvbn"
	"golang.org/x/crypto/bcrypt"
)

// MinPasswordLength is the floor for any new password. LocalAI does not
// (yet) support a second factor, so the bar sits above NIST's 8-char
// recommendation for MFA-protected accounts.
const MinPasswordLength = 12

// MaxPasswordLength matches bcrypt's 72-byte truncation. Accepting longer
// inputs creates a confusing UX where two "different" passwords hash to
// the same value because bcrypt silently dropped the suffix.
const MaxPasswordLength = 72

// MinPasswordScore is the minimum zxcvbn score (0-4) we accept. 3 means
// "safely unguessable: moderate protection from offline slow-hash scenario"
// per Dropbox's scoring; 4 is the highest.
const MinPasswordScore = 3

// passwordContextHints are tokens fed to zxcvbn so it penalises passwords
// built from the application's own name or branding.
var passwordContextHints = []string{"localai", "local-ai", "admin"}

// ErrPasswordEmpty is returned for a zero-length password. Always rejected;
// not overridable — bcrypt comparison on an empty string is its own hazard
// and there's no realistic legitimate use.
var ErrPasswordEmpty = errors.New("password must not be empty")

// ErrPasswordTooShort is returned when the password is below
// MinPasswordLength. Overridable — short is a policy choice, not a
// technical constraint.
var ErrPasswordTooShort = fmt.Errorf("password is shorter than %d characters; pick a longer one or acknowledge the weak password to use it anyway", MinPasswordLength)

// ErrPasswordTooLong is returned when the password exceeds MaxPasswordLength.
// Not overridable — bcrypt silently truncates at 72 bytes.
var ErrPasswordTooLong = fmt.Errorf("password must be at most %d characters", MaxPasswordLength)

// ErrPasswordNullByte is returned when the password contains a NUL byte —
// some bcrypt callers truncate at the first NUL, which would let an
// attacker register "abc\x00garbage" and authenticate as "abc". Not
// overridable.
var ErrPasswordNullByte = errors.New("password must not contain null bytes")

// ErrPasswordTooWeak is returned when zxcvbn scores the password below
// MinPasswordScore. Overridable — an operator may legitimately want a
// known-weak password (kiosk demo, CI rig, false positive on zxcvbn).
var ErrPasswordTooWeak = errors.New("password is too easy to guess; pick a longer or less common one, or acknowledge the weak password to use it anyway")

// PasswordPolicy controls which checks ValidatePasswordStrength enforces.
// AllowWeak skips the policy-level checks (length floor, zxcvbn score) but
// the technical invariants (non-empty, max length, no NUL bytes) always
// apply.
type PasswordPolicy struct {
	AllowWeak bool
}

// ValidatePasswordStrength enforces the password policy. Callers should use
// this for every register / change-password / admin-reset flow. Pass an
// optional PasswordPolicy{AllowWeak: true} to skip the policy-level checks;
// the technical invariants still apply.
func ValidatePasswordStrength(password string, policy ...PasswordPolicy) error {
	// Hard rules — always enforced. These aren't policy, they're invariants
	// the bcrypt layer below us depends on.
	if len(password) == 0 {
		return ErrPasswordEmpty
	}
	if len(password) > MaxPasswordLength {
		return ErrPasswordTooLong
	}
	if strings.ContainsRune(password, 0) {
		return ErrPasswordNullByte
	}

	allowWeak := false
	if len(policy) > 0 {
		allowWeak = policy[0].AllowWeak
	}
	if allowWeak {
		return nil
	}

	// Policy-level checks — bypassable via AllowWeak.
	if len(password) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	if zxcvbn.PasswordStrength(password, passwordContextHints).Score < MinPasswordScore {
		return ErrPasswordTooWeak
	}
	return nil
}

// HashPassword returns a bcrypt hash of the given password.
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// CheckPassword compares a bcrypt hash with a plaintext password.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// PasswordErrorResponse describes a password-policy rejection in a
// machine-readable form so the UI can choose whether to offer an "use
// this anyway" override (only when Overridable is true).
type PasswordErrorResponse struct {
	Error       string `json:"error"`
	ErrorCode   string `json:"error_code"`
	Overridable bool   `json:"overridable"`
}

// PasswordError returns a structured response for a ValidatePasswordStrength
// error. err must be one of the package-level password errors.
func PasswordError(err error) PasswordErrorResponse {
	r := PasswordErrorResponse{Error: err.Error()}
	switch {
	case errors.Is(err, ErrPasswordEmpty):
		r.ErrorCode = "password_empty"
	case errors.Is(err, ErrPasswordTooShort):
		r.ErrorCode = "password_too_short"
		r.Overridable = true
	case errors.Is(err, ErrPasswordTooLong):
		r.ErrorCode = "password_too_long"
	case errors.Is(err, ErrPasswordNullByte):
		r.ErrorCode = "password_null_byte"
	case errors.Is(err, ErrPasswordTooWeak):
		r.ErrorCode = "password_too_weak"
		r.Overridable = true
	}
	return r
}
