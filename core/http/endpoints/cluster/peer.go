package cluster

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/xlog"

	clustersvc "github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/tunnel"
)

// PeerHandler serves the link that a replica dials to reach a worker tunnel that
// another replica holds. The dial becomes a websocket, the websocket becomes a
// multiplexed session, and onSession gets the session.
//
// One credential decides, and it is the credential of the dialling replica. The
// id in ?id= is a claim. The handler resolves it to a row of the instances table
// and checks the secret in the header against the hash that the row publishes.
// The registration token of the deployment opens no peer link: it is held by
// every worker, and a worker that could pose as a replica could relay to every
// tunnel that a replica owns.
//
// All checks come before the upgrade, and the credential comes first. A dial with
// no credential is the anonymous case and gets 401 whatever else is missing.
//
// onSession runs on the goroutine of the request, so it returns promptly. The
// session outlives the handler, because the upgrade hijacks the connection, and
// onSession owns closing it.
func PeerHandler(instances *clustersvc.Registry, onSession func(peerID string, sess *tunnel.Session)) echo.HandlerFunc {
	// The default origin check of gorilla refuses a browser of another origin and
	// admits a client with no Origin header, which every peer is.
	upgrader := tunnel.NewUpgrader()

	return func(c echo.Context) error {
		presented := c.Request().Header.Get(clustersvc.PeerIdentityHeader)
		if presented == "" {
			// A missing header is refused and not waved through. Accepting a dial
			// with no credential would leave the hole as open as it was, because an
			// attacker omits the header too.
			return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
		}

		// Not 401. A frontend with no registry cannot resolve any replica, and
		// "unauthorized" would send an operator after a problem with a credential
		// that does not exist. The route is registered in every deployment so that
		// the route coverage test sees it, and a handler that treated a nil registry
		// as "nothing to check" would publish an unauthenticated multiplexer.
		if instances == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "distributed mode not enabled")
		}

		peerID := c.QueryParam("id")
		if peerID == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "missing peer id")
		}

		inst, err := instances.Get(c.Request().Context(), peerID)
		switch {
		case errors.Is(err, clustersvc.ErrInstanceNotFound):
			// 401 and not 404, so that a caller cannot list replica ids by status.
			xlog.Warn("Refusing a peer link: the dial named a replica this deployment has no row for", "peer", peerID)
			return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
		case err != nil:
			// A query that failed is neither a rejection nor an absence. 401 on an
			// unreadable database would tell a healthy replica that its credential
			// is wrong.
			xlog.Error("Looking up a replica for its peer dial failed", "peer", peerID, "error", err)
			return echo.NewHTTPError(http.StatusInternalServerError, "peer lookup failed")
		}

		// Two operator problems with two cures. An empty stored hash is a replica
		// that has not registered a credential, so there is nothing to check and it
		// must register again, which a restart does. A mismatch is a dialler that
		// presents the wrong secret. An empty hash never matches: it is not "no
		// restriction".
		if inst.PeerTokenHash == "" {
			xlog.Warn("Refusing a peer link: the replica it claims to be has no peer credential published, so nothing here can verify the claim",
				"peer", peerID)
			return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
		}
		if !clustersvc.PeerTokenMatches(presented, inst.PeerTokenHash) {
			xlog.Warn("Refusing a peer link: the dial presented the wrong credential for the replica it claims to be", "peer", peerID)
			return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
		}

		ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			// Upgrade has written its own failure to the client.
			xlog.Debug("tunnel peer link upgrade failed", "peer", peerID, "error", err)
			return nil
		}

		sess, err := tunnel.PeerServerSession(ws)
		if err != nil {
			xlog.Error("Tunnel peer link session setup failed", "peer", peerID, "error", err)
			_ = ws.Close()
			return nil
		}

		if onSession == nil {
			// Nothing reads this session, so the peer must not believe it has a
			// live link.
			_ = sess.Close()
			return nil
		}

		xlog.Debug("tunnel peer link established", "peer", peerID, "remote", ws.RemoteAddr().String())
		// net/http recovers a panic of this goroutine but does not close the
		// hijacked connection, so a panicking callback would leave the peer with a
		// link that nobody accepts streams on.
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
