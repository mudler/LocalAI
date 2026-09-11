// SPDX-License-Identifier: MIT

package auth

import (
	"net/http"
	"strings"
)

type publicRouteRule struct {
	Method string
	Path   string
	Prefix bool
}

var publicRouteRegistry = []publicRouteRule{
	// Discovery.
	{Method: http.MethodGet, Path: "/api/instructions"},
	{Method: http.MethodGet, Path: "/api/instructions/", Prefix: true},
	{Method: http.MethodGet, Path: "/swagger"},
	{Method: http.MethodGet, Path: "/swagger/", Prefix: true},
	{Method: http.MethodGet, Path: "/.well-known/localai.json"},

	// Health.
	{Method: http.MethodGet, Path: "/healthz"},
	{Method: http.MethodGet, Path: "/readyz"},

	// Authentication bootstrap.
	{Method: http.MethodGet, Path: "/api/auth/status"},
	{Method: http.MethodPost, Path: "/api/auth/token-login"},
	{Method: http.MethodPost, Path: "/api/auth/register"},
	{Method: http.MethodPost, Path: "/api/auth/login"},
	{Method: http.MethodGet, Path: "/api/auth/github/login"},
	{Method: http.MethodGet, Path: "/api/auth/github/callback"},
	{Method: http.MethodGet, Path: "/api/auth/oidc/login"},
	{Method: http.MethodGet, Path: "/api/auth/oidc/callback"},
	{Method: http.MethodOptions, Path: "/api/auth/", Prefix: true},

	// SPA.
	{Method: http.MethodGet, Path: "/"},
	{Method: http.MethodHead, Path: "/"},
	{Method: http.MethodGet, Path: "/app"},
	{Method: http.MethodGet, Path: "/app/", Prefix: true},
	{Method: http.MethodGet, Path: "/browse"},
	{Method: http.MethodGet, Path: "/browse/", Prefix: true},
	{Method: http.MethodGet, Path: "/login"},
	{Method: http.MethodGet, Path: "/invite/", Prefix: true},
	{Method: http.MethodGet, Path: "/explorer"},

	// Assets.
	{Method: http.MethodGet, Path: "/favicon.svg"},
	{Method: http.MethodGet, Path: "/assets/", Prefix: true},
	{Method: http.MethodGet, Path: "/locales/", Prefix: true},
	{Method: http.MethodGet, Path: "/static/", Prefix: true},

	// Branding.
	{Method: http.MethodGet, Path: "/api/branding"},
	{Method: http.MethodGet, Path: "/branding/asset/", Prefix: true},
}

func isPublicRoute(method, path string) bool {
	for _, rule := range publicRouteRegistry {
		if method != rule.Method {
			continue
		}
		if path == rule.Path {
			return true
		}
		if rule.Prefix && strings.HasSuffix(rule.Path, "/") && strings.HasPrefix(path, rule.Path) {
			return true
		}
	}

	return false
}

// usesAlternativeAuthentication identifies requests whose credentials are
// validated by route-group middleware instead of the global auth middleware.
func usesAlternativeAuthentication(path string) bool {
	return strings.HasPrefix(path, "/api/node/")
}
