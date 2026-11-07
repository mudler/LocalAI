// SPDX-License-Identifier: MIT
package contract

import (
	"errors"
	"strconv"
	"strings"
)

// The headers with which an add-on tells the host that the service behind it
// refused a call. Both ride on the 400 answer the add-on already gives for a
// refusal, so a host that does not read them behaves as before.
const (
	// HeaderUpstreamStatus is the HTTP status the service answered, 400 to 599.
	HeaderUpstreamStatus = "X-Addon-Upstream-Status"
	// HeaderUpstreamHint is one plain sentence for the person: what the service
	// refused and the one thing that fixes it. It holds no token and no text
	// the service sent back.
	HeaderUpstreamHint = "X-Addon-Upstream-Hint"
)

// MaxUpstreamHint bounds the hint, in bytes.
const MaxUpstreamHint = 240

// UpstreamError is an add-on's refusal that comes from the service behind it
// (a permission it lacks, a name that does not exist, a value the service
// does not accept). Status is the status of the service and Hint a fixed
// sentence written by the add-on. Nothing was changed by the call.
type UpstreamError struct {
	Status int
	Hint   string
	Cause  error
}

func (e *UpstreamError) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "the service behind the add-on refused the request (" + strconv.Itoa(e.Status) + ")"
}

func (e *UpstreamError) Unwrap() error { return e.Cause }

// UpstreamOf returns the upstream refusal in the chain of err.
func UpstreamOf(err error) (*UpstreamError, bool) {
	var u *UpstreamError
	if errors.As(err, &u) {
		return u, true
	}
	return nil, false
}

// ValidUpstreamStatus reports whether n is a status a service can refuse with.
func ValidUpstreamStatus(n int) bool { return n >= 400 && n <= 599 }

// CleanHint keeps the printable ASCII of a hint, collapses runs of space and
// cuts it at MaxUpstreamHint. Both the add-on that writes the header and the
// host that reads it apply it, so a header never carries a control byte or a
// long echo.
func CleanHint(s string) string {
	var b strings.Builder
	space := false
	for i := 0; i < len(s) && b.Len() < MaxUpstreamHint+1; i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			space = b.Len() > 0
		case c > ' ' && c < 0x7f:
			if space {
				b.WriteByte(' ')
				space = false
			}
			b.WriteByte(c)
		}
	}
	out := b.String()
	if len(out) > MaxUpstreamHint {
		out = out[:MaxUpstreamHint]
	}
	return strings.TrimSpace(out)
}
