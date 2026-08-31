// SPDX-License-Identifier: MIT

// Package cluster serves the replica-to-replica link that a LocalAI frontend
// uses to reach a worker tunnel it does not own. A peer dials
// GET /api/cluster/peer, the connection becomes one multiplexed yamux session,
// and the relay opens a stream on it per request.
package cluster

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/libp2p/go-yamux/v5"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/xlog"
)

// AlternativeAuthPrefix is the path prefix whose credentials are checked by
// this package rather than by the global session middleware.
//
// It is the auth package's own constant rather than a second copy: auth decides
// which paths bypass the session middleware, so owning the prefix there and
// deriving the route from it here means a change to either one moves both. The
// dependency runs endpoints -> auth, the direction the rest of core/http flows;
// pointing it the other way would deadlock the build as soon as this package
// needs anything from auth, which registering in RouteFeatureRegistry will.
const AlternativeAuthPrefix = auth.ClusterPathPrefix

// PeerPath is the route a peer replica dials.
const PeerPath = AlternativeAuthPrefix + "peer"

// RegisterClusterRoutes registers the peer link. onPeer receives every
// authenticated session; see PeerHandler for what it is expected to do with it.
func RegisterClusterRoutes(e *echo.Echo, token string, onPeer func(string, *yamux.Session)) {
	e.GET(PeerPath, PeerHandler(token, onPeer))
}

// PeerHandler upgrades an authenticated peer dial to a WebSocket, wraps it as
// a yamux server session and hands it to onSession.
//
// onSession runs on the request goroutine, so it must return promptly; the
// session outlives the handler because the upgrade hijacks the connection, and
// closing it is the caller's job.
func PeerHandler(token string, onSession func(peerID string, sess *yamux.Session)) echo.HandlerFunc {
	// gorilla's default CheckOrigin already restricts a browser to same-origin
	// and lets a header-less client (which every peer is) through, so the
	// zero value is what this link wants.
	upgrader := websocket.Upgrader{}

	return func(c echo.Context) error {
		// Reject before upgrading. Upgrading and then closing would give the
		// dialer a WebSocket error in place of an HTTP status, and both the
		// route-coverage test and a peer's own retry logic read the status.
		if !authorizedPeer(c.Request(), token) {
			return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
		}

		peerID := c.QueryParam("id")
		if peerID == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "missing peer id")
		}

		ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			// Upgrade has already written its own failure to the client.
			xlog.Debug("cluster peer link upgrade failed", "peer", peerID, "error", err)
			return nil
		}

		// Server side of the mux: the dialing peer is the client, so it owns
		// the odd stream IDs and this side the even ones.
		sess, err := yamux.Server(WebsocketConn(ws), nil, nil)
		if err != nil {
			xlog.Error("cluster peer link session setup failed", "peer", peerID, "error", err)
			_ = ws.Close()
			return nil
		}

		if onSession == nil {
			// Nothing will ever read from this session, so do not leave the
			// peer believing it has a live link.
			_ = sess.Close()
			return nil
		}

		xlog.Debug("cluster peer link established", "peer", peerID, "remote", ws.RemoteAddr().String())
		// net/http recovers a panic from this goroutine but does not close a
		// hijacked connection afterwards, so a panicking callback would leave
		// the peer holding a link nobody accepts streams on: its opens would
		// fill the 256-deep backlog and then hang without an error.
		defer func() {
			if r := recover(); r != nil {
				_ = sess.Close()
				panic(r)
			}
		}()
		onSession(peerID, sess)
		return nil
	}
}

// authorizedPeer compares the request's bearer token with the cluster token in
// constant time, matching the check the worker file-transfer server makes.
//
// Unlike that one, an empty configured token authorizes nobody: this route is
// registered in every deployment, so failing open would publish an
// unauthenticated mux to any caller that can reach the port.
func authorizedPeer(r *http.Request, expected string) bool {
	if expected == "" {
		return false
	}
	// RFC 7235 makes the scheme case-insensitive; the token after it is not.
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header[len(prefix):]), []byte(expected)) == 1
}
