package nodes

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/xlog"
)

// RouteLiveness is what a frontend knows about one route of a node.
type RouteLiveness string

const (
	// RouteLive is a route that works now.
	RouteLive RouteLiveness = "live"
	// RouteReconnecting is a tunnel that left a frontend less than the reconnect
	// grace ago. It still counts as live: a worker that dials again inside the
	// grace must not flap.
	RouteReconnecting RouteLiveness = "reconnecting"
	// RouteDown is a route that does not work now.
	RouteDown RouteLiveness = "down"
)

// The routes a node can hold. RouteHeartbeat is the HTTP heartbeat that every
// worker sends; it proves that the worker process runs, and it carries the
// report of the other routes. NATS and the tunnel carry the work.
const (
	RouteHeartbeat = "heartbeat"
	RouteNATS      = "nats"
	RouteTunnelID  = "tunnel"
)

// RouteState is the liveness of one route of a node, as the node list shows it.
// Transport is "websocket" or "p2p" for the tunnel. Reason says why a route is
// not live, in the words of the worker when it gave some.
type RouteState struct {
	Route     string        `json:"route"`
	Transport string        `json:"transport,omitempty"`
	State     RouteLiveness `json:"state"`
	Reason    string        `json:"reason,omitempty"`

	// settled is a route that is down for longer than the reconnect grace
	// already (a tunnel that presence reports gone). The health monitor waits
	// no further grace for it.
	settled bool
}

// Live says that the route works now or is inside its reconnect grace.
func (s RouteState) Live() bool { return s.State == RouteLive || s.State == RouteReconnecting }

// RouteJudge says, for each route of a node, if it is live. It reads what the
// worker reported in its heartbeat and, for the tunnel, the connection rows that
// every frontend shares. A node is judged by the routes it holds; the cluster
// has no carrier of its own.
//
// It keeps no state, so every frontend gives the same answer for the same rows.
type RouteJudge struct {
	// Presence reads the tunnel rows. Nil means no tunnel row is read, and the
	// report of the worker is the only source for the tunnel.
	Presence NodePresenceReader
	// Grace is the reconnect grace of a tunnel. Zero takes
	// cluster.DefaultReconnectGrace.
	Grace time.Duration
	// StaleThreshold is the age after which a heartbeat is stale.
	StaleThreshold time.Duration
}

// States returns the state of each route of the node: the heartbeat first, then
// NATS and the tunnel when the worker was given them or holds them. A node that
// has not sent its route report yet has the heartbeat only.
func (j RouteJudge) States(ctx context.Context, n *BackendNode, now time.Time) []RouteState {
	fresh := now.Sub(n.LastHeartbeat) <= j.StaleThreshold
	hb := RouteState{Route: RouteHeartbeat, State: RouteLive}
	if !fresh {
		hb.State, hb.Reason = RouteDown, "the heartbeat is stale"
	}
	out := []RouteState{hb}
	if n.Follow == "" {
		return out
	}
	held := splitRoutes(n.Attached)
	given := append(splitRoutes(n.Follow), held...)
	if slices.Contains(given, RouteNATS) {
		out = append(out, natsState(n, held, fresh))
	}
	if slices.Contains(given, RouteTunnelID) {
		out = append(out, j.tunnelState(ctx, n, held, fresh))
	}
	return out
}

// natsState is live when the last report says the worker holds NATS. The report
// travels in the heartbeat, so a stale heartbeat is a report that is out of date,
// and NATS is not counted then.
func natsState(n *BackendNode, held []string, fresh bool) RouteState {
	s := RouteState{Route: RouteNATS, State: RouteLive}
	switch {
	case !fresh:
		s.State, s.Reason = RouteDown, "no recent report from the worker"
	case !slices.Contains(held, RouteNATS):
		s.State = RouteDown
		s.Reason = cmp.Or(routeReason(n.FollowError, RouteNATS), "not attached")
	}
	return s
}

// tunnelState reads the presence of the tunnel first: it is what the frontends
// see, and it does not depend on the heartbeat. A tunnel that a live replica
// holds is live. A tunnel that left inside the grace is reconnecting, which
// counts while the heartbeat is fresh: with a stale heartbeat as well nothing
// shows the worker is alive. A tunnel gone for longer than the grace is down.
// When presence has no answer (no row, or a read that failed) the report of the
// worker decides, if it is fresh.
func (j RouteJudge) tunnelState(ctx context.Context, n *BackendNode, held []string, fresh bool) RouteState {
	s := RouteState{Route: RouteTunnelID, Transport: tunnelTransportOf(n)}
	p := cluster.PresenceUnknown
	if j.Presence != nil {
		var err error
		p, err = j.Presence.Presence(ctx, n.ID, cmp.Or(j.Grace, cluster.DefaultReconnectGrace))
		if err != nil {
			xlog.Warn("Could not read the presence of a node's tunnel; using the report of the worker",
				"node", n.Name, "nodeID", n.ID, "error", err)
			p = cluster.PresenceUnknown
		}
	}
	switch {
	case p == cluster.PresenceConnected:
		s.State = RouteLive
	case p == cluster.PresenceReconnecting && fresh:
		s.State, s.Reason = RouteReconnecting, "the tunnel is dialling again"
	case p == cluster.PresenceReconnecting:
		s.State, s.Reason = RouteDown, "the tunnel is dialling again and the heartbeat is stale"
	case p == cluster.PresenceGone:
		s.State, s.Reason, s.settled = RouteDown, "the tunnel has been gone longer than the reconnect grace", true
	case fresh && slices.Contains(held, RouteTunnelID):
		s.State = RouteLive
	case fresh:
		s.State = RouteDown
		s.Reason = cmp.Or(routeReason(n.FollowError, RouteTunnelID), "not attached")
	default:
		s.State, s.Reason = RouteDown, "no recent report from the worker"
	}
	return s
}

// tunnelTransportOf is how the tunnel of the node travels: what the worker
// reported, or P2P for a node pinned to a peer.
func tunnelTransportOf(n *BackendNode) string {
	if n.TunnelTransport != "" {
		return n.TunnelTransport
	}
	if n.PeerID != "" {
		return TunnelP2P
	}
	return ""
}

// routeReason returns the reason the worker gave for one route in its
// route_error ("nats: connecting; tunnel: refused").
func routeReason(followError, route string) string {
	for part := range strings.SplitSeq(followError, ";") {
		name, reason, ok := strings.Cut(strings.TrimSpace(part), ":")
		if ok && strings.TrimSpace(name) == route {
			return strings.TrimSpace(reason)
		}
	}
	return ""
}

// carryingRoutes returns the routes that carry work (NATS and the tunnel).
func carryingRoutes(states []RouteState) []RouteState {
	return slices.DeleteFunc(slices.Clone(states), func(s RouteState) bool { return s.Route == RouteHeartbeat })
}

// downRoutes names the routes that are not live, for the log.
func downRoutes(states []RouteState) string {
	var out []string
	for _, s := range states {
		if !s.Live() {
			out = append(out, s.Route)
		}
	}
	return strings.Join(out, ",")
}
