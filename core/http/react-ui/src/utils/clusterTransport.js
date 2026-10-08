// Helpers for the cluster transport panel. They read the report that
// GET /api/cluster/carrier and a dry run return.

export const CARRIERS = ['nats', 'tunnel']

export const CARRIER_LABELS = {
  nats: 'NATS',
  tunnel: 'Database tunnel',
}

export function carrierLabel(carrier) {
  return CARRIER_LABELS[carrier] || carrier || 'unknown'
}

export function otherCarrier(carrier) {
  return carrier === 'tunnel' ? 'nats' : 'tunnel'
}

// A worker that reports no capabilities predates carrier switching. A worker
// that can follow both carriers has an address of its own (dual-capable). A
// worker that follows only the tunnel makes outbound connections only.
export function workerProfile(worker) {
  const follow = Array.isArray(worker?.follow) ? worker.follow : []
  if (follow.length === 0) return 'legacy'
  if (follow.includes('nats') && follow.includes('tunnel')) return 'dual'
  if (follow.includes('tunnel')) return 'tunnel-only'
  return 'nats-only'
}

export const PROFILE_LABELS = {
  legacy: 'Legacy (NATS only)',
  dual: 'Dual-capable',
  'tunnel-only': 'Tunnel-only',
  'nats-only': 'NATS only',
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

// A dry run may be forced when every blocker says so.
export function canForce(report) {
  const blockers = report?.blockers || []
  return blockers.length > 0 && blockers.every(b => b.forceable)
}
