// The sandbox profile of a chat, as the chat page shows it. Pure functions over
// GET /api/chats/{id}/sandbox-profile, so the node unit tests cover them. The
// profiles come from the add-on that runs the commands; the page names none.

// How often the view is read again while an image is being pulled.
export const POLL_MS = 2000

export const profilesOf = (view) => (Array.isArray(view?.profiles) ? view.profiles : [])
export const profileById = (view, id) => profilesOf(view).find(p => p.id === id) || null

// The profile the chat's commands run in now: the one the server names, else
// the default, else the first.
export function activeProfile(view) {
  return profileById(view, view?.profile) || profileById(view, view?.default) || profilesOf(view)[0] || null
}

// What the Tools menu shows: the row, or nothing. The row needs a loaded view
// of a provider that enforces isolation (applies) and at least one profile.
export function profileMode(view) {
  if (!view || !view.loaded || !view.applies || profilesOf(view).length === 0) return 'hidden'
  return 'row'
}

// The state of an image: phase idle, ready, pulling, failed or absent.
export const phaseOf = (p) => p?.state?.phase || 'idle'

export function pulling(view) {
  return profilesOf(view).filter(p => phaseOf(p) === 'pulling')
}

// Whether the page must read the view again soon.
export const needsPolling = (view) => pulling(view).length > 0

export function percentOf(p) {
  const n = Number(p?.state?.percent)
  return Number.isFinite(n) ? Math.max(0, Math.min(100, Math.round(n))) : 0
}

// A profile the person cannot pick: the administrator limits it, or this
// release has no image for it.
export function disabledReason(p) {
  if (!p) return ''
  if (p.allowed === false) return 'notAllowed'
  if (phaseOf(p) === 'absent') return 'absent'
  return ''
}

// The profile that replaced the one the chat asked for, when the image of the
// requested one could not be used.
export const fellBack = (view) => Boolean(view?.fallback)

// The error key for a failed PUT, from the server's error type.
export function errorKey(err) {
  const type = err?.body?.error?.type || ''
  if (type === 'sandbox_profile_not_allowed') return 'notAllowedError'
  if (type === 'sandbox_profile_unknown') return 'unknownError'
  return ''
}

// The labels of who chose the profile: the chat itself, its saved agent or the
// administrator's default.
export const SOURCES = ['chat', 'agent', 'default']
export const sourceOf = (view) => (SOURCES.includes(view?.source) ? view.source : 'default')

// A draft chat has no id and no sandbox yet. The view is built from the list
// of profiles any user may pick from, with the default active.
export function draftView(list) {
  const profiles = (list?.profiles || []).map(p => ({ ...p, state: p.state || { phase: 'idle', percent: 0, error: '' } }))
  return {
    applies: profiles.length > 0, addon: list?.addon || '', profile: list?.default || '', selected: '', source: 'default',
    default: list?.default || '', fallback: false, fallback_reason: '', newer_image: false, profiles, draft: true,
  }
}

// ---- The administrator's settings (owner policy key sandbox_profiles) ----

export const emptyPolicy = () => ({ default: '', access: {}, memory_mib: 0, disk_mib: 0, pin_previous: [] })

export function normalizePolicy(raw) {
  const p = { ...emptyPolicy(), ...(raw || {}) }
  p.access = p.access && typeof p.access === 'object' ? p.access : {}
  p.pin_previous = Array.isArray(p.pin_previous) ? p.pin_previous : []
  return p
}

// Whether a profile has no restriction: no roles and no users.
export function everyone(policy, id) {
  const a = policy?.access?.[id]
  return !a || ((a.roles || []).length === 0 && (a.users || []).length === 0)
}

// Splits "alice, bob" into a clean, unique list.
export function parseUsers(text) {
  return [...new Set(String(text || '').split(/[\s,]+/).map(x => x.trim()).filter(Boolean))]
}

// The access of one profile with a role or the users changed. An empty result
// is removed from the map: absent means everyone.
export function withAccess(policy, id, change) {
  const cur = policy.access[id] || { roles: [], users: [] }
  const next = { roles: change.roles ?? cur.roles ?? [], users: change.users ?? cur.users ?? [] }
  const access = { ...policy.access }
  if (next.roles.length === 0 && next.users.length === 0) delete access[id]
  else access[id] = { roles: [...next.roles], users: [...next.users] }
  return { ...policy, access }
}

export function toggleRole(policy, id, role) {
  const roles = policy.access[id]?.roles || []
  return withAccess(policy, id, { roles: roles.includes(role) ? roles.filter(r => r !== role) : [...roles, role] })
}

export function setPinned(policy, id, on) {
  const rest = policy.pin_previous.filter(x => x !== id)
  return { ...policy, pin_previous: on ? [...rest, id] : rest }
}

// A quota field: '' or 0 mean no limit; else a whole number of MiB.
export function parseQuota(text) {
  const s = String(text ?? '').trim()
  if (s === '') return 0
  const n = Number(s)
  return Number.isSafeInteger(n) && n >= 0 ? n : NaN
}

export function samePolicy(a, b) { return JSON.stringify(normalizePolicy(a)) === JSON.stringify(normalizePolicy(b)) }

// The value to send: only entries that mean something, in a stable shape.
export function policyToSend(policy) {
  const p = normalizePolicy(policy)
  const access = {}
  for (const [id, a] of Object.entries(p.access)) {
    const roles = [...(a.roles || [])], users = [...(a.users || [])]
    if (roles.length || users.length) access[id] = { roles, users }
  }
  return { default: p.default || '', access, memory_mib: p.memory_mib || 0, disk_mib: p.disk_mib || 0, pin_previous: [...p.pin_previous] }
}
