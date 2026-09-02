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
	clustersvc "github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/xlog"
)

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

		// SELF-DECLARED, and knowingly so. Unlike the worker route next door,
		// which resolves ?id= to a node row and checks that node's OWN minted
		// credential, this route has only the shared cluster token to check,
		// so the id is a label and not a claim anything verifies.
		//
		// What that costs, exactly, for anything already holding the shared
		// token (every worker holds it, and it is the same token that
		// authenticates registration): it can relay to every worker tunnel this
		// replica owns, reaching every backend gRPC process and every worker's
		// file-transfer server; and by declaring a legitimate replica's id it
		// can make SessionStore.Accept evict that replica's inbound link, at
		// will. Neither is a new capability in KIND - before workers stopped
		// listening, a holder of that token could already dial any worker's
		// advertised ports directly - but the token is now the only thing
		// between an attacker and the whole fleet's tunnels.
		//
		// It is deferred rather than patched, because the cheap patch does not
		// work: checking ?id= against the instances table stops an invented id
		// and stops nothing else, since the attack declares a REAL replica's
		// id, and it would buy a false sense of a closed hole. Closing it takes
		// a credential per replica, minted where a replica joins the instances
		// table and presented here, which is a design with its own migration
		// and its own specs. Tracked as the phase-3 item named at
		// nodes.BackendNode.TunnelTokenHash.
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
