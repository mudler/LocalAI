// SPDX-License-Identifier: MIT

package cluster

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/libp2p/go-yamux/v5"
	clustersvc "github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// ConnectHandler serves the door a worker knocks on: it authenticates the dial
// against the node's OWN stored token, upgrades it to a WebSocket, wraps that as
// a yamux server session and attaches it to the tunnel registry.
//
// The worker dials out and never listens, which is the whole point of the
// tunnel: a worker behind NAT, in another cluster, or on a laptop needs no
// inbound port. It is therefore the yamux CLIENT and this side the SERVER, so
// this side owns the even stream IDs and is the side that opens streams.
// Nothing here accepts streams: in this design the frontend asks and the worker
// answers, so a worker that opened a stream into this session would park on the
// accept backlog rather than be served.
//
// The route is registered in every deployment, including single-binary ones, so
// that the route-coverage test under build tag `auth` walks it. A nil registry
// or a nil tunnel registry therefore has to be a real answer rather than a
// panic; see the 503 below.
//
// It is deliberately absent from auth.RouteFeatureRegistry. That registry gates
// a route on the FEATURES OF AN AUTHENTICATED USER, resolved from auth.GetUser,
// and there is no user here: the dialer is a worker process holding a machine
// credential.
//
// The global auth middleware does RUN on this path; what it does not do is
// reject. It attempts session, bearer and legacy-key authentication first, so it
// may even have set auth_user from a worker token that happens to match an API
// key, and then core/http/auth/middleware.go:90 lets the request through
// because usesAlternativeAuthentication reports the path as one whose
// credentials its own route checks. Nothing here reads what it set.
func ConnectHandler(registry *nodes.NodeRegistry, tunnels *clustersvc.TunnelRegistry) echo.HandlerFunc {
	// gorilla's default CheckOrigin restricts a browser to same-origin and lets
	// a header-less client (which every worker is) through, so the zero value
	// is what this link wants. The same choice PeerHandler makes.
	upgrader := websocket.Upgrader{}

	return func(c echo.Context) error {
		// Everything below happens BEFORE the upgrade, and the order inside it
		// is load-bearing. The credential is read first because a dial with no
		// Authorization header at all is the anonymous case, and the
		// route-coverage test issues exactly that, with no query string: it
		// must see 401 rather than a 400 about a missing node id.
		token, ok := bearerToken(c.Request())
		if !ok {
			return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
		}

		// Not 401. A frontend with no cluster cannot authenticate anybody, and
		// answering "unauthorized" would send the operator hunting a token
		// problem that does not exist. It is checked after the header so that
		// an anonymous dial still gets the 401 the coverage test requires.
		//
		// Only the registry half is covered by a spec. The two are read from one
		// application.Distributed() in core/http/app.go and initDistributed
		// returns an error rather than a partial struct, so a non-nil registry
		// beside a nil tunnel registry is unreachable and no spec constructs it;
		// the second half is defence against a future wiring that splits them,
		// where the cost would be a nil dereference in Attach after the
		// connection is already hijacked.
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
			// A node this frontend has never seen. Reported as 401 rather than
			// 404 so a caller cannot enumerate node IDs by status code.
			xlog.Debug("worker tunnel dial named an unknown node", "node", nodeID)
			return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
		case err != nil:
			// A query that FAILED is neither a rejection nor an absence. This
			// is the phase's standing rule in its HTTP form: telling a worker
			// its credentials are wrong when the database merely could not be
			// read sends it re-registering instead of retrying, and a worker
			// that re-registers has thrown away the identity its tunnel and its
			// loaded models are keyed by.
			xlog.Error("Looking up a worker for its tunnel dial failed", "node", nodeID, "error", err)
			return echo.NewHTTPError(http.StatusInternalServerError, "node lookup failed")
		}

		// Split from the mismatch below because they are different operator
		// problems with different fixes. An empty stored hash means the node
		// registered against a frontend with no LOCALAI_REGISTRATION_TOKEN, so
		// no worker on that deployment can ever tunnel until one is set and the
		// workers re-register; a mismatch means one worker holds the wrong
		// secret. One log line for both leaves an operator reading "wrong token"
		// while every worker fails identically.
		if node.TokenHash == "" {
			// Debug, not Warn. Every worker in such a deployment fails this way
			// on every reconnect, so warning per dial buries the log; the fact
			// is stated once, at boot, where core/http/app.go warns that no
			// registration token is configured. What this line adds is which
			// node, for an operator who has already read that warning.
			xlog.Debug("refusing a worker tunnel: this node has no stored token, so it registered with no registration token configured",
				"node", nodeID, "knob", "LOCALAI_REGISTRATION_TOKEN")
			return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
		}
		if !authorizedWorker(token, node.TokenHash) {
			xlog.Debug("worker tunnel dial presented the wrong token", "node", nodeID)
			return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
		}

		// Authenticated but not authorised, so 403 rather than 401: the fix is an
		// admin approving the node, not a different credential, and answering
		// 401 would send an operator looking at tokens.
		//
		// Only StatusPending is refused. The rest of /api/node/ self-service
		// gates on nothing at all, but the two places that hand a node something
		// DURABLE both refuse a pending one: the agent worker's API key
		// (core/http/endpoints/localai/nodes.go:224) and its NATS credential
		// (nodes.go:293). A tunnel is that kind of grant, not a heartbeat: it is
		// a standing pipe into the worker recorded in node_connections and
		// relayed to by every other replica. Draining and unhealthy nodes keep
		// their tunnels on purpose; draining means finish what you have, and a
		// node marked unhealthy for missed heartbeats needs the pipe to recover
		// through.
		if node.Status == nodes.StatusPending {
			xlog.Warn("Refusing a worker tunnel: this node is awaiting admin approval", "node", nodeID)
			return echo.NewHTTPError(http.StatusForbidden, "node is pending approval")
		}

		ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			// Upgrade has already written its own failure to the client.
			xlog.Debug("worker tunnel upgrade failed", "node", nodeID, "error", err)
			return nil
		}

		sess, err := yamux.Server(clustersvc.WebsocketConn(ws), nil, nil)
		if err != nil {
			xlog.Error("Worker tunnel session setup failed", "node", nodeID, "error", err)
			_ = ws.Close()
			return nil
		}

		// The same guard PeerHandler carries, for the same reason and one more.
		// net/http recovers a panic from this goroutine but does not close the
		// hijacked connection, and middleware.Recover does not either, so a
		// panic below would leave the worker holding a live session this replica
		// has no entry for and will never detach. The extra reason here is that
		// Attach does database work: a panic inside it, with the session left
		// open, is a tunnel nothing can reach and nothing will clean up.
		//
		// It re-panics rather than swallowing. Whatever it caught is a bug, and
		// the recovery middleware above is what should report it.
		defer func() {
			if r := recover(); r != nil {
				_ = sess.Close()
				panic(r)
			}
		}()

		// From here the connection is hijacked, so no status can reach the
		// worker any more: a failure is a closed socket, which is what its
		// reconnect loop reads.
		epoch, err := tunnels.Attach(c.Request().Context(), nodeID, sess)
		if err != nil {
			xlog.Error("Attaching a worker tunnel failed", "node", nodeID, "error", err)
			_ = sess.Close()
			return nil
		}

		xlog.Info("Worker tunnel established", "node", nodeID, "remote", ws.RemoteAddr().String())
		// The session outlives this handler, so something other than the
		// request goroutine has to notice it die. yamux closes shutdownCh from
		// its receive loop the moment the underlying conn fails
		// (go-yamux/v5@v5.1.0/session.go:691-695 calling close at
		// session.go:297-311), and its default config keepalives every 30s
		// (mux.go:73-74), so a worker that vanishes without a FIN is noticed
		// too rather than held forever.
		go func() {
			<-sess.CloseChan()
			// The token Attach returned, never a fresh or zero one. Detach
			// matches it by EQUALITY: it identifies THIS attachment, so a
			// worker that has already re-dialled onto this replica is not
			// evicted by its predecessor's teardown.
			tunnels.Detach(nodeID, epoch)
			xlog.Debug("worker tunnel closed", "node", nodeID)
		}()
		return nil
	}
}

// bearerToken returns the token from an Authorization: Bearer header, and
// whether one was present at all.
//
// The presence of a credential and its correctness are separate answers on
// purpose: "no credential" is what decides the pre-upgrade 401, and it has to be
// decidable before anything about the node is known.
func bearerToken(r *http.Request) (string, bool) {
	// RFC 7235 makes the scheme case-insensitive; the token after it is not.
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

// authorizedWorker compares a presented token against the hash stored on the
// node's own row, in constant time.
//
// Against the NODE's stored hash, not the configured registration token, which
// is a deliberate strengthening over how /api/node/ authenticates. A tunnel is a
// durable, multiplexed pipe into a worker; a credential that authorizes every
// worker at once would mean one leak lets an attacker impersonate any worker
// whose ID it can read, and take over that worker's traffic by claiming its
// tunnel.
//
// What that buys TODAY is the mechanism, not yet the isolation, and the
// difference should not be overstated: a worker registers by presenting the
// deployment's registration token, so the hash on its row is that token's hash
// and a leaked registration token still opens a tunnel for a node whose ID the
// attacker knows. What this does rule out is the shortcut of comparing against
// the configured token itself, which is what would have to be unpicked later;
// the day a worker is issued its own secret at registration, this check starts
// isolating workers from each other with no change here, PROVIDED the secret
// lands in TokenHash. The one per-node secret LocalAI mints today, the agent
// worker's api_token, does not: provisionAgentWorkerKey writes an auth.User and
// an auth.APIKey referenced by node.AuthUserID / node.APIKeyID and never touches
// this column. If the next task follows that precedent instead, this comparison
// is what has to change.
//
// The empty-hash guard is defensive rather than deciding: a stored hash is
// hex-encoded SHA-256, so 64 bytes or nothing, and ConstantTimeCompare already
// returns 0 on a length mismatch (crypto/internal/fips140/subtle/constant_time.go:17-20
// returns 0 outright when the lengths differ). It is kept because a reader should not have to
// derive "an unregistered node authorizes nobody" from a length rule, and
// because the caller logs that case separately.
func authorizedWorker(token, storedHash string) bool {
	if storedHash == "" {
		return false
	}
	sum := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(storedHash)) == 1
}
