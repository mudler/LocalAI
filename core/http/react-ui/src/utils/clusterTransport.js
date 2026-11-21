// Helpers for the cluster transport panel. They read the report that
// GET /api/cluster/carrier returns. The API keeps the values `nats` and
// `tunnel`; the panel words them for what they are.

// The broadcast bus the frontends share. `tunnel` is PostgreSQL LISTEN/NOTIFY.
const BUS_LABELS = {
  nats: 'NATS',
  tunnel: 'PostgreSQL',
}

export function busLabel(carrier) {
  return BUS_LABELS[carrier] || carrier || 'unknown'
}

// How a worker attaches to a frontend. `tunnel` is the outbound websocket.
const ROUTE_LABELS = {
  nats: 'NATS',
  tunnel: 'WebSocket',
}

export function routeLabel(route) {
  return ROUTE_LABELS[route] || route || 'unknown'
}

// A replica is ready for the epoch in flight when it reported that epoch and
// gave no reason why not. Outside a change there is nothing to wait for.
export function replicaReady(replica, report) {
  if (!replica) return false
  if (replica.ready_reason) return false
  if (report?.state === 'stable') return true
  return Number(replica.ready_epoch) >= Number(report?.epoch)
}

// The server sends durations in nanoseconds. Returns "m:ss" or "h:mm:ss".
export function formatCountdown(ns) {
  const total = Math.max(0, Math.ceil(Number(ns || 0) / 1e9))
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const two = n => String(n).padStart(2, '0')
  return h > 0 ? `${h}:${two(m)}:${two(s)}` : `${m}:${two(s)}`
}

export function stateLabel(report) {
  if (!report) return ''
  if (report.state === 'stable' && Number(report.drain_remaining_ns) > 0) return 'draining'
  return report.state
}
