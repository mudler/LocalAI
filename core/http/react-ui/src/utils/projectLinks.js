// Pure rules of the Connections and Automations tabs of a project. No React and
// no network, so a node test drives them directly.
import { kindItem, kindSetupAddress } from './agentWizard.js'
import { kindPageHref } from './chatConnectors.js'

// A trigger: 'off' when switched off, 'paused' when it is on and its project
// is archived (it resumes on restore), else 'active'.
export function triggerState(trigger, archived) {
  if (!trigger?.enabled) return 'off'
  return archived ? 'paused' : 'active'
}

// A messaging connection: 'off' when the person switched it off, 'paused' when
// it is on and its project is archived (a message gets a short note and no
// chat until the restore), else 'active'. An archived project never turns a
// connection that was off into 'paused'.
export function bindingState(binding, archived) {
  if (!binding?.enabled) return 'off'
  return archived ? 'paused' : 'active'
}

// A loop: 'active', 'paused-archived' (active, held by the archived project),
// 'paused' (the person paused it) or 'ended'. Anything unknown reads as paused.
export function loopState(loop, archived) {
  if (loop?.state === 'ended') return 'ended'
  if (loop?.state === 'active') return archived ? 'paused-archived' : 'active'
  return 'paused'
}

// The kinds the Connect menu offers: the ones whose bot answers as an agent.
// Kinds that only keep credentials are left out. Nothing loaded gives none.
export function connectKinds(kinds) {
  return (Array.isArray(kinds) ? kinds : []).map(kindItem).filter((i) => i.type === 'kind')
}

// The name of a kind, else its id.
export function kindLabel(kinds, kind) {
  const k = (Array.isArray(kinds) ? kinds : []).find((x) => x.kind === kind)
  return (k && (k.localized_title || k.title)) || kind
}

// What choosing a kind does: { href } with exactly one project agent,
// { choose: true } with several (ask which answers), { none: true } with none.
export function connectTarget(item, agents) {
  const list = Array.isArray(agents) ? agents : []
  if (list.length === 0) return { none: true }
  if (list.length > 1) return { choose: true }
  return { href: kindSetupAddress(item, list[0].id) }
}

// The page of one connection: its kind's page opened on the connection, else
// the gallery entry of the add-on (or the kind id when nothing is known).
export function connectionHref(kinds, connection) {
  const k = (Array.isArray(kinds) ? kinds : []).find((x) => x.kind === connection.kind)
  if (!k) return `/app/addons?detail=${encodeURIComponent(connection.kind)}`
  const href = kindPageHref(k)
  return `${href}${href.includes('?') ? '&' : '?'}binding=${encodeURIComponent(connection.id)}`
}

// A run time in the person's language, or ''.
export function nextRunLabel(iso, lang) {
  const at = Date.parse(iso)
  if (!iso || Number.isNaN(at)) return ''
  try {
    return new Date(at).toLocaleString(lang || undefined, { weekday: 'short', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
  } catch {
    return new Date(at).toLocaleString()
  }
}
