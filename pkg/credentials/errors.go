package credentials

import (
	"fmt"
	"net/url"
)

// AuthError reports a 401 or 403 and whether a credentials rule was involved,
// because "add a credential" and "fix the credential" are different fixes.
type AuthError struct {
	// Target identifies what was being fetched, without secrets or signed
	// query strings.
	Target string
	Status int
	// Match is the rule that was sent, or empty when none matched.
	Match string
	Err   error
}

func (e *AuthError) Error() string {
	if e.Match == "" {
		return fmt.Sprintf("authentication required for %s (status %d): no credentials rule matches it", e.Target, e.Status)
	}
	return fmt.Sprintf("credential %q was rejected by %s (status %d)", e.Match, e.Target, e.Status)
}

func (e *AuthError) Unwrap() error {
	return e.Err
}

// NewAuthError looks matchURL up in the default store to tell the two cases
// apart. target is what the message shows.
func NewAuthError(matchURL, target string, status int, cause error) error {
	e := &AuthError{Target: target, Status: status, Err: cause}
	if c, ok := Default().Match(matchURL); ok {
		e.Match = c.Match
	}
	return e
}

// HTTPAuthError builds an AuthError for an HTTP response. The query string is
// dropped because CDNs put signatures there.
func HTTPAuthError(u *url.URL, status int, cause error) error {
	target := u.Scheme + "://" + u.Host + u.Path
	return NewAuthError(u.String(), target, status, cause)
}
