// Helpers for the cluster transport panel. They read GET /api/cluster/settings
// (the broadcast bus of the frontend that answers, and the live frontends) and
// the route state of each node in GET /api/nodes.

// The broadcast bus the frontend publishes on now, from the `bus` of the
// settings: NATS while it reaches the NATS server, PostgreSQL otherwise.
export function busLabel(bus) {
  if (bus?.bus === 'nats') return 'NATS connected'
  if (bus?.bus === 'postgres') return 'PostgreSQL'
  return 'unknown'
}

// A note under the bus when NATS is configured and this frontend does not
// reach it, or empty.
export function busNote(bus) {
  if (bus?.nats_configured && !bus?.nats_connected) {
    return 'NATS is configured, but this frontend does not reach it. It broadcasts over PostgreSQL and is not ready until NATS is back.'
  }
  return ''
}

// How a worker attaches to a frontend. `tunnel` is the outbound websocket.
const ROUTE_LABELS = {
  nats: 'NATS',
  tunnel: 'WebSocket',
}

export function routeLabel(route) {
  return ROUTE_LABELS[route] || route || 'unknown'
}

// The routes a node reported it holds, from the `attached` column of the node.
export function attachedRoutes(node) {
  return String(node?.attached || '').split(',').map(r => r.trim()).filter(Boolean)
}

// The routes of a worker to show, from the route_state of its node in
// GET /api/nodes: NATS and the tunnel always, the heartbeat only when it is
// lost (a live heartbeat is what every worker has). `name` is the key of the
// route's word under swarm:route, and `tone` the chip tone of its state.
const STATE_TONES = { live: 'ok', reconnecting: 'warn', down: 'error' }

export function routeChips(routeState) {
  if (!Array.isArray(routeState)) return []
  return routeState
    .filter(r => r && (r.route !== 'heartbeat' || r.state !== 'live'))
    .map(r => ({
      key: r.route,
      name: r.route === 'tunnel' && (r.transport === 'websocket' || r.transport === 'p2p') ? r.transport : r.route,
      state: STATE_TONES[r.state] ? r.state : 'down',
      tone: STATE_TONES[r.state] || 'error',
      reason: r.reason || '',
    }))
}
