package middleware

import "strings"

// SafeForwardedPrefix validates an X-Forwarded-Prefix header value before we
// concatenate it into a redirect target or use it for path stripping. An
// untrusted value like "//evil.com" or "http://evil.com" turns the
// reverse-proxy support into an open redirect.
//
// Returns the trimmed, validated value and true on success; "" and false
// when the value is unsafe and should be ignored.
func SafeForwardedPrefix(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	// Must be a path: starts with a single '/' and doesn't begin a
	// protocol-relative URL.
	if !strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//") {
		return "", false
	}
	// Backslashes are interpreted as forward slashes by some clients but
	// not by Echo's router; reject to avoid bypasses.
	if strings.ContainsAny(s, "\\") {
		return "", false
	}
	// No control characters or whitespace inside the path.
	for _, c := range s {
		if c < 0x20 || c == 0x7f {
			return "", false
		}
	}
	return s, true
}
