package cli

import (
	"net/url"
	"strings"
)

func chatAPIBaseURL(endpoint string) string {
	if !strings.Contains(endpoint, "://") {
		endpoint = "http://" + endpoint
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return strings.TrimRight(endpoint, "/") + "/v1"
	}

	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		u.Path = "/v1"
	} else if path != "/v1" && !strings.HasSuffix(path, "/v1") {
		u.Path = path + "/v1"
	} else {
		u.Path = path
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
