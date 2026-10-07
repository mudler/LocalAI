// Package cluster serves the endpoints that the processes of a cluster call
// among themselves.
package cluster

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/tunnel"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// ConnectHandler serves the door that a worker dials. It checks the dial against
// the own credential of the node, upgrades it to a websocket, starts the server
// side of a session on it and gives the session to the tunnel registry.
//
// The worker dials out and never listens, so a worker behind NAT, in another
// cluster or on a laptop needs no inbound port. The worker is the yamux client
// and this side is the server. This side opens the streams. It accepts none: the
// frontend asks and the worker answers, so a stream that the worker opened would
// wait in the accept queue for ever.
//
// A worker holds two sessions. The query parameter lane names the one that a
// dial is for: "inference", which is also the default, or "bulk".
//
// The route is registered on every replica of every deployment, so that the
// route coverage test walks it. A registry that is nil is therefore a real
// answer (503) and not a panic.
//
// It is not in auth.RouteFeatureRegistry. That registry gates a route on the
// features of a user, and here there is no user: the caller is a worker process
// with a machine credential. The global auth middleware runs on this path and
// does not reject it, because usesAlternativeAuthentication names the path as
// one that checks its own credentials. Nothing here reads what the middleware
// set.
func ConnectHandler(registry *nodes.NodeRegistry, tunnels *tunnel.Registry) echo.HandlerFunc {
	// The default origin check of gorilla refuses a browser of another origin
	// and admits a client with no Origin header, which every worker is.
	upgrader := tunnel.NewUpgrader()

	return func(c echo.Context) error {
		// All checks come before the upgrade, and their order matters. The
		// credential is read first, because a dial with no Authorization header
		// is the anonymous case and the route coverage test sends exactly that,
		// with no query. It must get 401 and not a 400 for a missing node id.
		token, ok := bearerToken(c.Request())
		if !ok {
			return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
		}

		// Not 401: a frontend that is not distributed cannot authenticate
		// anyone, and "unauthorized" would send the operator to look for a
		// problem with a token that does not exist.
		if registry == nil || tunnels == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "distributed mode not enabled")
		}

		nodeID := c.QueryParam("id")
		if nodeID == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "missing node id")
		}

		node, err := registry.Get(c.Request().Context(), nodeID)
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			// 401 and not 404, so that a caller cannot list node IDs by status.
			xlog.Debug("worker tunnel dial named an unknown node", "node", nodeID)
			return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
		case err != nil:
			// A query that failed is neither a rejection nor an absence. To tell
			// a worker that its credentials are wrong when the database could not
			// be read sends it to register again instead of retrying, and a
			// worker that registers again has thrown away the credential that
			// its tunnel is keyed by.
			xlog.Error("Looking up a worker for its tunnel dial failed", "node", nodeID, "error", err)
			return echo.NewHTTPError(http.StatusInternalServerError, "node lookup failed")
		}

		// An empty hash and a wrong token are two problems with two fixes. An
		// empty hash means that the node has not registered since the tunnel
		// credential existed, so it has no secret to check and must register.
		// A wrong token is a stale credential, usually from before a rotation.
		if node.TunnelTokenHash == "" {
			// Debug, because every such worker fails in this way on every dial.
			xlog.Debug("refusing a worker tunnel: this node has no tunnel credential, so it has not registered since they were introduced",
				"node", nodeID)
			return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
		}
		if !authorizedWorker(token, node.TunnelTokenHash) {
			xlog.Debug("worker tunnel dial presented the wrong token", "node", nodeID)
			return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
		}

		// The credential is right, but the node is not allowed yet: 403, because
		// the fix is that an admin approves the node and a different credential
		// would not help. A tunnel is a standing pipe into the worker that other
		// replicas relay to, so a node that waits for approval does not get one.
		// Draining and unhealthy nodes keep their tunnels: draining means finish
		// what you have, and a node marked unhealthy for missed heartbeats needs
		// the pipe to recover.
		if node.Status == nodes.StatusPending {
			xlog.Warn("Refusing a worker tunnel: this node is awaiting admin approval", "node", nodeID)
			return echo.NewHTTPError(http.StatusForbidden, "node is pending approval")
		}

		lane, err := tunnel.ParseLane(c.QueryParam("lane"))
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}

		// The bulk lane belongs to the replica that holds the inference lane of
		// the node. A load balancer can send the dial to another replica. Only a
		// status can say that, and the status is possible before the upgrade, so
		// the worker is told to dial again at once.
		if lane == tunnel.LaneBulk && !tunnels.Holds(nodeID) {
			return echo.NewHTTPError(http.StatusConflict, "this replica does not hold the tunnel of the node")
		}

		ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			// Upgrade has written its own failure to the client.
			xlog.Debug("worker tunnel upgrade failed", "node", nodeID, "error", err)
			return nil
		}

		sess, err := tunnel.ServerSession(ws, lane)
		if err != nil {
			xlog.Error("Worker tunnel session setup failed", "node", nodeID, "error", err)
			_ = ws.Close()
			return nil
		}

		// net/http recovers a panic of this goroutine but does not close the
		// connection that was hijacked, and the recovery middleware does not
		// either. A panic below would leave the worker with a session that this
		// replica has no entry for and will never detach. Attach does database
		// work, so this is not far-fetched. The panic goes on, because it is a
		// bug and the recovery middleware is what reports it.
		defer func() {
			if r := recover(); r != nil {
				_ = sess.Close()
				panic(r)
			}
		}()

		// The connection is hijacked, so no status can reach the worker any more.
		// A failure is a closed socket, which is what the reconnect loop of the
		// worker reads.
		attachment, err := tunnels.Attach(c.Request().Context(), nodeID, lane, sess)
		if err != nil {
			xlog.Error("Attaching a worker tunnel failed", "node", nodeID, "lane", lane, "error", err)
			_ = sess.Close()
			return nil
		}

		xlog.Info("Worker tunnel established", "node", nodeID, "lane", lane, "remote", ws.RemoteAddr().String())
		// The session outlives this handler, so something else has to see it end.
		// yamux closes its channel from the receive loop as soon as the
		// connection fails, and its keepalive notices a worker that vanished
		// without a FIN.
		go func() {
			<-sess.CloseChan()
			// The token that Attach returned. Detach compares it by equality: it
			// names this attachment, so a worker that has already dialled again
			// is not removed by the end of its predecessor.
			tunnels.Detach(nodeID, lane, attachment)
			xlog.Debug("worker tunnel closed", "node", nodeID, "lane", lane)
		}()
		return nil
	}
}

// bearerToken returns the token of an Authorization: Bearer header, and whether
// there was one. A missing credential and a wrong credential are different
// answers on purpose: "no credential" decides the 401 before the upgrade, and it
// must be decidable before anything about the node is known.
func bearerToken(r *http.Request) (string, bool) {
	// The scheme is not case-sensitive (RFC 7235). The token is.
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := header[len(prefix):]
	if token == "" {
		return "", false
	}
	return token, true
}

// authorizedWorker compares a token with the hash on the row of the node, in
// constant time.
//
// The token is the own credential of the node and not the registration token of
// the deployment. A tunnel is a standing pipe into a worker. A credential that
// opens every worker would let one leak take over the traffic of any worker
// whose ID can be read, by claiming its tunnel. The secret is minted for each
// node at registration, returned to that worker once and stored as this hash.
// It is not TokenHash, which hashes the token that the worker registered with.
// On most deployments that is the shared registration token.
func authorizedWorker(token, storedHash string) bool {
	if storedHash == "" {
		return false
	}
	sum := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(storedHash)) == 1
}
