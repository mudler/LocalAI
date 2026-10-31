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

// The names of the search providers of the built-in web_search, as the
// chats_web_search_providers setting lists them. The keyless ones read the
// result pages of public engines; searxng is the instance of
// chats_web_searxng_url.
const (
	SearchProviderSearXNG    = "searxng"
	SearchProviderDuckDuckGo = "duckduckgo"
	SearchProviderBrave      = "brave_html"
	SearchProviderBing       = "bing_html"
)

// SearchProviderNames lists every name chats_web_search_providers accepts.
var SearchProviderNames = []string{SearchProviderSearXNG, SearchProviderDuckDuckGo, SearchProviderBrave, SearchProviderBing}

// DefaultKeylessProviders is the order of the keyless providers when the
// setting is empty and no SearXNG instance is set.
var DefaultKeylessProviders = []string{SearchProviderDuckDuckGo, SearchProviderBrave, SearchProviderBing}

// ParseSearchProviders reads the chats_web_search_providers setting: a comma
// list of SearchProviderNames, each at most once. Empty is the default: the
// SearXNG instance alone when one is set (an administrator who chose it may
// not want the queries to reach the public engines), else the keyless
// providers in DefaultKeylessProviders order.
func ParseSearchProviders(s string, searxngSet bool) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		if searxngSet {
			return []string{SearchProviderSearXNG}, nil
		}
		return append([]string(nil), DefaultKeylessProviders...), nil
	}
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.Split(s, ",") {
		f = strings.ToLower(strings.TrimSpace(f))
		if f == "" {
			continue
		}
		known := false
		for _, n := range SearchProviderNames {
			if n == f {
				known = true
			}
		}
		if !known {
			return nil, fmt.Errorf("%q is not a search provider (use %s)", f, strings.Join(SearchProviderNames, ", "))
		}
		if seen[f] {
			return nil, fmt.Errorf("%q is listed twice", f)
		}
		seen[f] = true
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("list at least one search provider, or switch web search off")
	}
	return out, nil
}
