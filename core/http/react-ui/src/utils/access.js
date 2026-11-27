// Small pure helpers for the Users and API keys pages: what a user can reach in a
// few words, the filters over the user list, the state of an invite and of an
// API key. They read only what the auth endpoints return.

export const USER_FILTERS = ['all', 'pending', 'admins', 'disabled', 'inactive']

// A person is inactive when they were last active more than this long ago, or
// never, and their account is older than that too (a new sign-up has not had
// the time to be active).
export const INACTIVE_DAYS = 30
const DAY_MS = 86_400_000

export function isInactive(user, now = Date.now()) {
  const limit = INACTIVE_DAYS * DAY_MS
  if (user.lastActiveAt) {
    const t = new Date(user.lastActiveAt).getTime()
    return Number.isFinite(t) && now - t > limit
  }
  const created = user.createdAt ? new Date(user.createdAt).getTime() : NaN
  return Number.isFinite(created) && now - created > limit
}

// A user is waiting when they are neither active nor disabled (the server's
// "pending" status is a sign-up awaiting approval).
export function userState(user) {
  if (user.status === 'active') return 'active'
  if (user.status === 'disabled') return 'disabled'
  return 'pending'
}

// Whether an administrator may use View as on this person: an active account
// that is neither the viewer nor an administrator. A person waiting for approval
// or disabled has no data to view, and viewing another administrator is not part
// of View as. `self` is the viewer ({ id, email }).
export function canViewAs(user, self) {
  if (!user || user.role === 'admin' || userState(user) !== 'active') return false
  return !(self && (user.id === self.id || (user.email && user.email === self.email)))
}

// The "Filters" popover narrows the list further: the sign-in method and
// whether the person's access differs from the defaults.
export function filterUsers(users, { query = '', filter = 'all', provider = '', access = '', featureMeta = null, now = Date.now() } = {}) {
  const q = query.trim().toLowerCase()
  return users.filter(u => {
    if (filter === 'pending' && userState(u) !== 'pending') return false
    if (filter === 'disabled' && userState(u) !== 'disabled') return false
    if (filter === 'admins' && u.role !== 'admin') return false
    if (filter === 'inactive' && !isInactive(u, now)) return false
    if (provider && (u.provider || 'local') !== provider) return false
    if (access === 'custom' && (u.role === 'admin' || accessInfo(u, featureMeta).isDefault)) return false
    if (!q) return true
    return (u.name || '').toLowerCase().includes(q) || (u.email || '').toLowerCase().includes(q)
  })
}

export function filterCounts(users, now = Date.now()) {
  return {
    inactive: users.filter(u => isInactive(u, now)).length,
    all: users.length,
    pending: users.filter(u => userState(u) === 'pending').length,
    admins: users.filter(u => u.role === 'admin').length,
    disabled: users.filter(u => userState(u) === 'disabled').length,
  }
}

// What a person can use, as numbers: `all` for an admin, else how many models
// they are limited to (null: no list), how many features they have beyond and
// below the defaults, and how many limits apply. `isDefault` is true when none
// of it differs. `featureMeta` is /api/auth/admin/features: each feature has a
// `default`. The words are the caller's, so a page can translate them.
export function accessInfo(user, featureMeta) {
  if (user.role === 'admin') return { all: true, models: null, more: 0, fewer: 0, limits: 0, isDefault: false }
  // The stored keys, read against the defaults in force, so the summary
  // follows a default changed after the list was loaded.
  const perms = user.explicit_permissions || user.permissions || {}
  const features = [
    ...(featureMeta?.api_features || []),
    ...(featureMeta?.agent_features || []),
    ...(featureMeta?.general_features || []),
  ]
  let more = 0
  let fewer = 0
  for (const f of features) {
    const on = perms[f.key] === undefined ? !!f.default : !!perms[f.key]
    if (on && !f.default) more++
    if (!on && f.default) fewer++
  }
  const models = user.allowed_models?.enabled ? (user.allowed_models.models || []).length : null
  const limits = (user.quotas || []).length
  return { all: false, models, more, fewer, limits, isDefault: models === null && !more && !fewer && !limits }
}

// A user's permission map, as stored: only the features set to an explicit
// value. A feature it does not name follows its default. The PUT of the access
// sheet replaces this map, so the sheet must start from it and never from the
// effective map, or every feature would become explicit. A server older than
// explicit_permissions sends only the effective map, which then counts as
// stored (what the sheet did before).
export function storedPermissions(user) {
  return { ...(user?.explicit_permissions || user?.permissions || {}) }
}

// The sheet keeps the administrator's changes apart from the stored map:
// `edits` maps a feature to true or false (an explicit value) or to null (back
// to the default). An edit that ends where the stored map already was is
// dropped, so the save sends only real changes.
const has = (map, key) => Object.prototype.hasOwnProperty.call(map, key)

// What the switch of a feature shows, and whether the user has an explicit
// value for it. `def` is the default in force, so a following feature moves
// when the default changes.
export function featureState(stored, edits, key, def) {
  if (has(edits, key)) {
    return edits[key] === null ? { on: !!def, explicit: false } : { on: !!edits[key], explicit: true }
  }
  if (has(stored, key)) return { on: !!stored[key], explicit: true }
  return { on: !!def, explicit: false }
}

// The edits after the administrator sets a feature to value.
export function setFeature(stored, edits, key, value, def) {
  const next = { ...edits }
  const unchanged = has(stored, key) ? stored[key] === value : value === !!def
  if (unchanged) delete next[key]
  else next[key] = value
  return next
}

// The edits after "Use default": the feature follows its default again.
export function followDefault(stored, edits, key) {
  const next = { ...edits }
  if (has(stored, key)) next[key] = null
  else delete next[key]
  return next
}

// The body of PUT /api/auth/admin/users/{id}/permissions: the stored keys with
// the edits applied.
export function permissionPayload(stored, edits) {
  const out = { ...stored }
  for (const [key, value] of Object.entries(edits)) {
    if (value === null) delete out[key]
    else out[key] = value
  }
  return out
}

// One line on what a person can use, in English: "2 models only · 1 limit".
export function accessSummary(user, featureMeta) {
  const info = accessInfo(user, featureMeta)
  if (info.all) return 'All access'
  const parts = []
  if (info.models !== null) parts.push(`${info.models} ${info.models === 1 ? 'model' : 'models'} only`)
  if (info.more) parts.push(`${info.more} more ${info.more === 1 ? 'feature' : 'features'}`)
  if (info.fewer) parts.push(`${info.fewer} fewer ${info.fewer === 1 ? 'feature' : 'features'}`)
  if (info.limits) parts.push(`${info.limits} ${info.limits === 1 ? 'limit' : 'limits'}`)
  return parts.length ? parts.join(' · ') : 'Default access'
}

// The words for a person's access, in the language of the page.
export function accessText(t, info) {
  if (info.all) return t('users.access.all')
  const parts = []
  if (info.models !== null) parts.push(t('users.access.models', { count: info.models }))
  if (info.more) parts.push(t('users.access.more', { count: info.more }))
  if (info.fewer) parts.push(t('users.access.fewer', { count: info.fewer }))
  if (info.limits) parts.push(t('users.access.limits', { count: info.limits }))
  return parts.length ? parts.join(' \u00b7 ') : t('users.access.default')
}

export function initialOf(user) {
  const text = (user.name || user.email || '?').trim()
  return text ? text[0].toUpperCase() : '?'
}

// 'open', 'used' or 'expired', as the invite list shows it.
export function inviteState(invite, now = Date.now()) {
  if (invite.usedBy) return 'used'
  if (invite.expiresAt && new Date(invite.expiresAt).getTime() <= now) return 'expired'
  return 'open'
}

export function timeUntil(iso, now = Date.now()) {
  const ms = new Date(iso).getTime() - now
  if (!Number.isFinite(ms)) return ''
  const abs = Math.abs(ms)
  const unit = abs >= 86400000 ? [Math.round(abs / 86400000), 'day'] : abs >= 3600000 ? [Math.round(abs / 3600000), 'hour'] : [Math.max(1, Math.round(abs / 60000)), 'minute']
  const text = `${unit[0]} ${unit[1]}${unit[0] === 1 ? '' : 's'}`
  return ms >= 0 ? `in ${text}` : `${text} ago`
}

// The state of one of the caller's API keys.
export function keyState(key, now = Date.now()) {
  if (key.expiresAt && new Date(key.expiresAt).getTime() <= now) return 'expired'
  if (key.disabled) return 'paused'
  if (key.pausedUntil && new Date(key.pausedUntil).getTime() > now) return 'paused-until'
  return 'active'
}

// The invite lifetimes the create form offers, in hours (POST sends hours).
export const INVITE_LIFETIMES = [
  { hours: 24, label: '1 day' },
  { hours: 168, label: '7 days' },
  { hours: 720, label: '30 days' },
]

// Lifetimes an API key can be created with. The server reads "30d", "90d" and
// "1y"; "" leaves the server's own default (which may be no expiry).
export const KEY_EXPIRIES = [
  { value: '', label: 'Server default' },
  { value: '30d', label: '30 days' },
  { value: '90d', label: '90 days' },
  { value: '1y', label: '1 year' },
]
