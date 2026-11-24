// Helpers of the Federation page (Swarm, P2P). Pure functions, so the unit
// tests run them without a browser.
import { shortPeer } from './p2p.js'

export { shortPeer }

// The routing policies of a group, in the order the page shows them.
export const POLICIES = ['least_busy', 'round_robin']

// How this server reaches an instance, as a key under `federation.path`, a
// StatusChip tone and whether a relay limits the connection.
export function instancePath(inst) {
  if (!inst || inst.local) return { key: 'local', tone: 'ok', limited: false }
  if (inst.transport !== 'p2p') {
    return { key: 'http', tone: inst.status === 'ok' ? 'ok' : inst.status === 'never' ? 'idle' : 'error', limited: false }
  }
  const p = inst.p2p || {}
  switch (p.state) {
    case 'off': return { key: 'off', tone: 'idle', limited: false }
    case 'error':
    case 'down': return { key: 'down', tone: 'error', limited: false }
    default: break
  }
  switch (p.path) {
    case 'direct': return { key: 'direct', tone: 'ok', limited: false }
    case 'hole_punched': return { key: 'holePunched', tone: 'ok', limited: false }
    case 'relayed': return { key: 'relayed', tone: p.limited ? 'warn' : 'ok', limited: !!p.limited }
    default: return { key: 'waiting', tone: 'idle', limited: false }
  }
}

// The tone of a member's health. A drained member is idle whatever its
// health: it takes no new request.
export function memberTone(m) {
  if (m?.drained) return 'idle'
  switch (m?.state) {
    case 'healthy': return 'ok'
    case 'recovering': return 'warn'
    case 'down':
    case 'missing': return 'error'
    default: return 'idle'
  }
}

// The models a group can use, gathered by the model id they serve at their
// instance, so the same model on several instances shows as one choice with
// an option per instance. Models that only one instance serves come last.
// Each option suggests the group name: the model id.
export function candidateGroups(instances) {
  const byUpstream = new Map()
  for (const inst of instances || []) {
    for (const m of inst.models || []) {
      const upstream = m.upstream_id || m.model
      if (!byUpstream.has(upstream)) byUpstream.set(upstream, [])
      byUpstream.get(upstream).push({
        model: m.model, local: !!inst.local, instanceName: inst.name || '', suggested: upstream,
      })
    }
  }
  const out = [...byUpstream.entries()].map(([upstream, options]) => ({ upstream, options }))
  out.sort((a, b) => (b.options.length > 1) - (a.options.length > 1) || a.upstream.localeCompare(b.upstream))
  return out
}
