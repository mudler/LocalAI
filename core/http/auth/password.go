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

// ErrPasswordTooShort is returned when the password is below MinPasswordLength.
var ErrPasswordTooShort = fmt.Errorf("password must be at least %d characters", MinPasswordLength)

// ErrPasswordTooLong is returned when the password exceeds MaxPasswordLength.
var ErrPasswordTooLong = fmt.Errorf("password must be at most %d characters", MaxPasswordLength)

// ErrPasswordNullByte is returned when the password contains a NUL byte —
// some bcrypt callers truncate at the first NUL, which would let an
// attacker register "abc\x00garbage" and authenticate as "abc".
var ErrPasswordNullByte = errors.New("password must not contain null bytes")

// ErrPasswordTooWeak is returned when zxcvbn scores the password below
// MinPasswordScore. Unlike the other errors, this one is overridable via
// PasswordPolicy.AllowWeak — operators sometimes need to set a known-weak
// password (e.g. for a kiosk demo, a CI test rig, or to recover from a
// false positive).
var ErrPasswordTooWeak = errors.New("password is too easy to guess; pick a longer or less common one, or acknowledge the weak password to use it anyway")

// PasswordPolicy controls which checks ValidatePasswordStrength enforces.
// The hard rules (length, NUL-byte) are always applied; AllowWeak skips
// only the zxcvbn entropy check.
type PasswordPolicy struct {
	AllowWeak bool
}

// ValidatePasswordStrength enforces the password policy. Callers should use
// this for every register / change-password / admin-reset flow. Pass an
// optional PasswordPolicy{AllowWeak: true} to skip the zxcvbn check while
// still enforcing length and NUL-byte rules.
func ValidatePasswordStrength(password string, policy ...PasswordPolicy) error {
	if len(password) < MinPasswordLength {
		return ErrPasswordTooShort
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
	if !allowWeak && zxcvbn.PasswordStrength(password, passwordContextHints).Score < MinPasswordScore {
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
	case errors.Is(err, ErrPasswordTooShort):
		r.ErrorCode = "password_too_short"
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
