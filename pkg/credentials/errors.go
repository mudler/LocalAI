package credentials

import (
	"fmt"
	"net/http"
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
	// Registry marks a registry pull, where docker config credentials are
	// tried after the store, so an unmatched rule is not the whole story.
	Registry bool
	// Provided marks a request that carried a credential the caller chose.
	// The store was not consulted, so it must not be blamed.
	Provided bool
	Err      error

	// causeText replaces Err in the message when Err may not be printed.
	causeText string
}

func (e *AuthError) Error() string {
	var msg string
	switch {
	case e.Provided:
		msg = fmt.Sprintf("the provided credential was rejected by %s (status %d)", e.Target, e.Status)
	case e.Match != "":
		msg = fmt.Sprintf("credential %q was rejected by %s (status %d)", e.Match, e.Target, e.Status)
	case e.Registry:
		msg = fmt.Sprintf("authentication required for %s (status %d): no credentials rule matches it and docker config credentials, if any, were not accepted", e.Target, e.Status)
	default:
		msg = fmt.Sprintf("authentication required for %s (status %d): no credentials rule matches it", e.Target, e.Status)
	}
	// The cause carries what the server said (a registry's DENIED detail, for
	// example), which is often the only hint at what is wrong with the rule.
	switch {
	case e.causeText != "":
		msg += ": " + e.causeText
	case e.Err != nil:
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *AuthError) Unwrap() error {
	return e.Err
}

// NewAuthError looks matchURL up in the default store to tell the two cases
// apart. target is what the message shows.
func NewAuthError(matchURL, target string, status int, cause error) error {
	return newAuthError(matchURL, target, status, cause)
}

func newAuthError(matchURL, target string, status int, cause error) *AuthError {
	e := &AuthError{Target: target, Status: status, Err: cause}
	if c, ok := Default().Match(matchURL); ok {
		e.Match = c.Match
	}
	return e
}

// NewRegistryAuthError is NewAuthError for registry pulls, whose message also
// accounts for docker config credentials having been tried.
func NewRegistryAuthError(matchURL, target string, status int, cause error) error {
	e := newAuthError(matchURL, target, status, cause)
	e.Registry = true
	return e
}

// HTTPAuthError builds an AuthError for an HTTP response. The query string is
// dropped because CDNs put signatures there.
func HTTPAuthError(u *url.URL, status int, cause error) error {
	e := newAuthError(u.String(), httpTarget(u), status, cause)
	e.causeText = httpCauseText(status)
	return e
}

// HTTPProvidedCredentialError builds an AuthError for a request that carried
// a credential chosen by the caller, without consulting the store.
func HTTPProvidedCredentialError(u *url.URL, status int, cause error) error {
	return &AuthError{Target: httpTarget(u), Status: status, Provided: true, Err: cause, causeText: httpCauseText(status)}
}

func httpTarget(u *url.URL) string {
	return u.Scheme + "://" + u.Host + u.Path
}

// httpCauseText stands in for the cause of an HTTP auth error. Callers build
// that cause from the URL they requested, which can hold a signed query
// string, so only the status text is printed. The cause stays reachable
// through Unwrap for errors.Is and errors.As.
func httpCauseText(status int) string {
	return fmt.Sprintf("%d %s", status, http.StatusText(status))
}
