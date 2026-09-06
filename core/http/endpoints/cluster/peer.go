// SPDX-License-Identifier: MIT

// Package cluster serves the replica-to-replica link that a LocalAI frontend
// uses to reach a worker tunnel it does not own. A peer dials
// GET /api/cluster/peer, the connection becomes one multiplexed yamux session,
// and the relay opens a stream on it per request.
package cluster

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/libp2p/go-yamux/v5"
	clustersvc "github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/xlog"
)

// PeerHandler upgrades an authenticated peer dial to a WebSocket, wraps it as
// a yamux server session and hands it to onSession.
//
// TWO credentials are checked, and both must pass. token is the deployment's
// shared cluster token and says the dialler belongs here at all; instances
// resolves the replica id in ?id= to its row and checks the dialler's OWN peer
// credential against the hash that row publishes. Neither replaces the other:
// the shared check is unchanged, and identity is added in front of the
// multiplexer it guards.
//
// onSession runs on the request goroutine, so it must return promptly; the
// session outlives the handler because the upgrade hijacks the connection, and
// closing it is the caller's job.
func PeerHandler(token string, instances *clustersvc.Registry, onSession func(peerID string, sess *yamux.Session)) echo.HandlerFunc {
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

		// Not 401. A frontend with no cluster registry cannot resolve any
		// replica's identity, and answering "unauthorized" would send an operator
		// hunting a token problem that does not exist. Checked after the shared
		// token so an anonymous dial still gets the 401 first, which is the same
		// ordering ConnectHandler takes next door and for the same reason.
		//
		// This route is registered only in distributed mode, where the registry is
		// never nil, so the branch is defence against a future wiring that
		// registers it more widely. Failing closed is the point: a handler that
		// treated a nil registry as "nothing to check" would publish exactly the
		// unauthenticated multiplexer this argument was added to prevent.
		if instances == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "distributed mode not enabled")
		}

		// Read before the credential so a dial that names nobody is still a 400.
		// The id is no longer taken on trust: everything below turns it from a
		// label into a claim, by resolving it to a row and requiring the dialler to
		// present that row's secret.
		peerID := c.QueryParam("id")
		if peerID == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "missing peer id")
		}

		// The identity half, read before any database work so a dial carrying no
		// credential costs no query.
		//
		// A missing header is refused, not waved through, and the choice is the
		// whole migration story of this route. Accepting a credential-less dial
		// "for compatibility" would leave the hole exactly as open as it was,
		// because an attacker simply omits the header too; there is no version of
		// a downgrade here that is safe, only versions that are quiet. So the
		// failure is loud instead: this is the one line that tells an operator
		// mid-rollout why an old replica cannot reach a new one, and it names the
		// upgrade rather than the network.
		presented := c.Request().Header.Get(clustersvc.PeerIdentityHeader)
		if presented == "" {
			xlog.Warn("Refusing a peer link: the dialling replica presented no peer credential. It is running a release from before per-replica peer identity, or it never registered a credential of its own. Upgrade it; this replica will not accept an unproven peer id",
				"peer", peerID, "header", clustersvc.PeerIdentityHeader)
			return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
		}

		inst, err := instances.Get(c.Request().Context(), peerID)
		switch {
		case errors.Is(err, clustersvc.ErrInstanceNotFound):
			// A replica id this deployment has no row for. Reported as 401 rather
			// than 404 so a caller cannot enumerate replica ids by status code,
			// which is the same choice the worker tunnel makes for node ids.
			xlog.Warn("Refusing a peer link: the dial named a replica this deployment has no row for",
				"peer", peerID)
			return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
		case err != nil:
			// A query that FAILED is neither a rejection nor an absence, and this
			// is that rule in its HTTP form. Answering 401 on an unreadable
			// database would tell a healthy replica its credentials are wrong, and
			// nothing above the transport may conclude anything about a WORKER
			// from it either: a peer that cannot be linked to is not a worker that
			// has gone away.
			xlog.Error("Looking up a replica for its peer dial failed", "peer", peerID, "error", err)
			return echo.NewHTTPError(http.StatusInternalServerError, "peer lookup failed")
		}

		// Split from the mismatch below because they are different operator
		// problems with different fixes, exactly as the worker tunnel splits them.
		// An empty stored hash means that replica last registered against a
		// LocalAI that predates peer credentials, so there is no secret to check
		// and it must register again, which a restart does. A mismatch means the
		// dialler is presenting the wrong one.
		//
		// Empty is refused. It is not "no restriction": an empty credential that
		// matched would mean every replica registered by an older frontend could
		// be impersonated by anyone holding the shared token, which is the whole
		// exposure being closed.
		if inst.PeerTokenHash == "" {
			xlog.Warn("Refusing a peer link: the replica it claims to be has no peer credential published, so nothing here can verify the claim. That replica has not registered since per-replica peer identity was introduced",
				"peer", peerID)
			return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
		}
		if !clustersvc.PeerTokenMatches(presented, inst.PeerTokenHash) {
			// This is the impostor case, and it is a WARN rather than a debug
			// line: holding the shared token and declaring somebody else's id is
			// precisely the attack this check exists for, and it should not be
			// invisible at default log level.
			xlog.Warn("Refusing a peer link: the dial presented the wrong credential for the replica it claims to be",
				"peer", peerID)
			return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
		}

		ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			// Upgrade has already written its own failure to the client.
			xlog.Debug("cluster peer link upgrade failed", "peer", peerID, "error", err)
			return nil
		}

		// Server side of the mux: the dialing peer is the client, so it owns
		// the odd stream IDs and this side the even ones.
		//
		// The SAME configuration the dialler uses, and that is load bearing
		// rather than symmetry for its own sake. A yamux receive window is
		// advertised by the receiving side, so a nil here left this end on the
		// 256 KiB default while the dialler ran at 4 MiB, and the direction
		// governed by this end is the one that carries a relayed model artifact
		// INTO the replica that owns the worker's tunnel. That direction was
		// measured at roughly half the throughput of the same transfer without
		// a relay in it.
		//
		// It also puts the same ceiling on unread data at this end that
		// PeerLinkConfig already documents for the dialling end, so a replica
		// is now sized against that figure per link in BOTH directions. That
		// is the cost of the window being useful at all: a window is a bound
		// on data received and not yet read, so a receiver that will not
		// buffer cannot advertise one.
		sess, err := yamux.Server(clustersvc.WebsocketConn(ws), clustersvc.PeerLinkConfig(), nil)
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
