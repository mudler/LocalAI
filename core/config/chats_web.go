package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ParsePorts reads a comma list of TCP ports, as the chats_web_ports setting
// holds it. An empty list means 80 and 443.
func ParsePorts(s string) ([]int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return []int{80, 443}, nil
	}
	var out []int
	seen := map[int]bool{}
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("%q is not a port (1 to 65535)", f)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(out) > 32 {
		return nil, fmt.Errorf("at most 32 ports")
	}
	return out, nil
}

// ValidateSearXNGURL checks the chats_web_searxng_url setting: empty, or an
// http(s) address with a host and no user name or password.
func ValidateSearXNGURL(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("use an http or https address such as https://searx.example.org")
	}
	if u.User != nil {
		return fmt.Errorf("the address must not contain a user name or password")
	}
	return nil
}
