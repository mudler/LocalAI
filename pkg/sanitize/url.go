package sanitize

import "net/url"

// URL masks the userinfo (username:password) portion of a URL string.
// Returns "***" if parsing fails.
func URL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "***"
	}
	if u.User != nil {
		u.User = url.UserPassword("***", "***")
	}
	return u.String()
}
