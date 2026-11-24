// SPDX-License-Identifier: MIT

package auth

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
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

	// A client redeems its one-time pairing code here; it has no credential
	// yet. The handler rate limits and answers every failure the same way.
	{Method: http.MethodPost, Path: "/api/client/pair"},

	// The scripts that join a worker. They answer only for a valid join token
	// in the query, which is the credential, and do not spend it. See
	// localai.JoinScriptEndpoint.
	{Method: http.MethodGet, Path: "/join.sh"},
	{Method: http.MethodGet, Path: "/join.ps1"},
	{Method: http.MethodGet, Path: "/join.yaml"},

	// CORS preflight. An OPTIONS request cannot carry credentials by HTTP
	// spec, so preflights targeting any endpoint must not be gated on auth;
	// the CORS middleware (registered after auth in app.go) answers them.
	// This rule also covers the auth-bootstrap preflights the previous
	// OPTIONS-under-/api/auth/ rule existed for. See #4576.
	{Method: http.MethodOptions, Path: "/", Prefix: true},

	// SPA.
	{Method: http.MethodGet, Path: "/"},
	{Method: http.MethodHead, Path: "/"},
	{Method: http.MethodGet, Path: "/app"},
	{Method: http.MethodGet, Path: "/app/", Prefix: true},
	{Method: http.MethodGet, Path: "/browse"},
	{Method: http.MethodGet, Path: "/browse/", Prefix: true},
	{Method: http.MethodGet, Path: "/login"},
	{Method: http.MethodGet, Path: "/invite/", Prefix: true},

	// Assets.
	{Method: http.MethodGet, Path: "/favicon.svg"},
	{Method: http.MethodGet, Path: "/assets/", Prefix: true},
	{Method: http.MethodGet, Path: "/locales/", Prefix: true},
	{Method: http.MethodGet, Path: "/static/", Prefix: true},

	// Branding.
	{Method: http.MethodGet, Path: "/api/branding"},
	{Method: http.MethodGet, Path: "/branding/asset/", Prefix: true},
}

// eventHookPrefix is the start of the hook path of an event trigger.
const eventHookPrefix = "/api/chats/triggers/"

// isEventHookPath tells whether path is exactly <prefix><id>/hook, with one
// segment for the id. The hook of an event trigger is public because its
// caller is a service with no session; the handler checks the secret of the
// trigger. It is no prefix rule, so no other route under the triggers path
// becomes public.
func isEventHookPath(path string) bool {
	rest, ok := strings.CutPrefix(path, eventHookPrefix)
	if !ok {
		return false
	}
	id, last, ok := strings.Cut(rest, "/")
	return ok && id != "" && last == "hook"
}

// eventHookRoute is the route pattern of that hook as the studio registers it.
const eventHookRoute = eventHookPrefix + ":id/hook"

// isPublicRequest is isPublicRoute for a request that is being routed. The
// decoded URL path is not what Echo routes on when the URL has an escaped form
// (RawPath), so the hook is public only when the router really matched the
// hook route; any other match, or none, leaves it behind the login.
func isPublicRequest(c echo.Context) bool {
	req := c.Request()
	if !isPublicRoute(req.Method, req.URL.Path) {
		return false
	}
	if req.Method == http.MethodPost && isEventHookPath(req.URL.Path) {
		return c.Path() == eventHookRoute
	}
	return true
}

func isPublicRoute(method, path string) bool {
	if method == http.MethodPost && isEventHookPath(path) {
		return true
	}
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

// ClusterConnectPath is the route that a worker dials to open its tunnel. The
// handler checks the own tunnel credential of the node, which is not a user,
// a session or an API key, so the global middleware lets the request through.
//
// It is one exact path and not the prefix /api/cluster/, so that a route that is
// added under that prefix later is covered by the global middleware unless its
// author decides otherwise. The path is written out here because this package
// must not import the tunnel package. The spec of the connect endpoint compares
// it with tunnel.ConnectPath.
const ClusterConnectPath = "/api/cluster/connect"

// ClusterPeerPath is the route that a replica dials to reach a tunnel that
// another replica holds. The handler checks the own credential of the dialling
// replica. The spec of the peer endpoint compares it with tunnel.PeerPath.
const ClusterPeerPath = "/api/cluster/peer"

// usesAlternativeAuthentication identifies requests whose credentials are
// validated by route-group middleware instead of the global auth middleware.
func usesAlternativeAuthentication(path string) bool {
	return strings.HasPrefix(path, "/api/node/") || path == ClusterConnectPath || path == ClusterPeerPath
}
