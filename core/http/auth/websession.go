package auth

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"
)

// IsWebSession reports whether the request comes from the web UI: a session
// cookie, a legacy key the UI keeps in its token cookie, or any request when
// the server has no authentication at all (authOn false). A key sent in a
// header (Authorization, x-api-key, xi-api-key) is not, even a session token
// in a bearer header, and neither is an in-process request of the chat loop.
// Features that only the web UI may switch on, such as the incognito marker,
// use it so that an API client cannot.
func IsWebSession(c echo.Context, authOn bool) bool {
	if _, ok := internalUser(c.Request().Context()); ok {
		return false
	}
	if !authOn {
		return true
	}
	if _, ok := c.Get("_auth_session").(*Session); ok {
		return true
	}
	if GetSource(c) == UsageSourceLegacy {
		header, _ := c.Get(contextKeyHeaderAuthenticated).(bool)
		return !header
	}
	return false
}

// ToolContextHeaders mark a request made by a tool, an agent run or an MCP
// session. A confirmation that carries any of them is refused, whatever else
// it carries.
var ToolContextHeaders = []string{"Mcp-Session-Id", "X-LocalAI-Tool-Call", "X-LocalAI-Agent", "X-LocalAI-Execution-Context", "X-Mcp-Session-Id"}

// HasToolContext reports whether h carries any of ToolContextHeaders.
func HasToolContext(h http.Header) bool {
	for _, name := range ToolContextHeaders {
		if h.Get(name) != "" {
			return true
		}
	}
	return false
}

// SameOriginFetch reports whether r is a same-origin fetch of this page's
// own code. With Sec-Fetch-Site it must be same-origin, with destination
// empty and mode cors or same-origin. Browsers omit Sec-Fetch-* over plain
// HTTP on an address that is not localhost, so without Sec-Fetch-Site the
// request must name this host in Origin or, failing that, Referer
// (SameHostBrowserRequest); a destination or a mode that is still present
// must then be the one of a fetch.
func SameOriginFetch(r *http.Request) bool {
	h := r.Header
	if site := h.Get("Sec-Fetch-Site"); site != "" {
		if site != "same-origin" || h.Get("Sec-Fetch-Dest") != "empty" {
			return false
		}
		mode := h.Get("Sec-Fetch-Mode")
		return mode == "cors" || mode == "same-origin"
	}
	if !SameHostBrowserRequest(r) {
		return false
	}
	if dest := h.Get("Sec-Fetch-Dest"); dest != "" && dest != "empty" {
		return false
	}
	mode := h.Get("Sec-Fetch-Mode")
	return mode == "" || mode == "cors" || mode == "same-origin"
}

// SameHostBrowserRequest reports whether Origin, or failing that Referer,
// names the same host and port as the request. A malformed or "null" value
// never matches. When Origin is present, Referer is not consulted.
func SameHostBrowserRequest(r *http.Request) bool {
	source := r.Header.Get("Origin")
	if source == "" {
		source = r.Header.Get("Referer")
	}
	if source == "" {
		return false
	}
	u, err := url.Parse(source)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}
