// Small pure helpers for the Users and keys pages: what a user can reach in a
// few words, the filters over the user list, the state of an invite and of an
// API key. They read only what the auth endpoints return.

export const USER_FILTERS = ['all', 'pending', 'admins', 'disabled']

// A user is waiting when they are neither active nor disabled (the server's
// "pending" status is a sign-up awaiting approval).
export function userState(user) {
  if (user.status === 'active') return 'active'
  if (user.status === 'disabled') return 'disabled'
  return 'pending'
}

export function filterUsers(users, { query = '', filter = 'all' } = {}) {
  const q = query.trim().toLowerCase()
  return users.filter(u => {
    if (filter === 'pending' && userState(u) !== 'pending') return false
    if (filter === 'disabled' && userState(u) !== 'disabled') return false
    if (filter === 'admins' && u.role !== 'admin') return false
    if (!q) return true
    return (u.name || '').toLowerCase().includes(q) || (u.email || '').toLowerCase().includes(q)
  })
}

export function filterCounts(users) {
  return {
    all: users.length,
    pending: users.filter(u => userState(u) === 'pending').length,
    admins: users.filter(u => u.role === 'admin').length,
    disabled: users.filter(u => userState(u) === 'disabled').length,
  }
}

// One line on what a person can use, from the permission map, the model
// allow-list and the quota rules the user list returns. `featureMeta` is
// /api/auth/admin/features: each feature has a `default`. An admin reaches
// everything. A user at the defaults says so.
export function accessSummary(user, featureMeta) {
  if (user.role === 'admin') return 'All access'
  const perms = user.permissions || {}
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
  const parts = []
  const models = user.allowed_models
  if (models?.enabled) parts.push(`${(models.models || []).length} ${(models.models || []).length === 1 ? 'model' : 'models'} only`)
  if (more) parts.push(`${more} more ${more === 1 ? 'feature' : 'features'}`)
  if (fewer) parts.push(`${fewer} fewer ${fewer === 1 ? 'feature' : 'features'}`)
  const limits = (user.quotas || []).length
  if (limits) parts.push(`${limits} ${limits === 1 ? 'limit' : 'limits'}`)
  return parts.length ? parts.join(' · ') : 'Default access'
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
