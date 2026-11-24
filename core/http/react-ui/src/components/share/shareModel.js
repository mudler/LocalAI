// SPDX-License-Identifier: MIT
// The rules of the share dialog that do not need React: how an error is told,
// which search results are new, whether the references step is complete, the
// default role labels and the line a list row shows about sharing. The server
// decides every permission; this only chooses words and what is enabled.
//
// No React and no network: the node unit tests drive it directly.

export const EVERYONE = '@everyone'
export const REFERENCE_OPTIONS = Object.freeze(['view', 'use', 'none'])
// The picker of a kind searches every account, so a query needs three
// characters; the server refuses fewer.
export const SEARCH_MIN = 3
export const SEARCH_MAX = 64

// describeShareError returns { kind, key, retry, lost } for what a client
// threw: the add-on client's AddonsError (kind), the SDK's AddonSdkError
// (code withdrawn) or an abort. `lost` says the person can no longer manage
// sharing of this resource here, so the caller should close and refresh.
export function describeShareError(error) {
  const e = error || {}
  if (e.code === 'withdrawn' || e.name === 'AbortError') return { kind: 'withdrawn', key: 'share.error.withdrawn', retry: false, lost: true }
  switch (e.kind) {
    case 'auth': return { kind: 'denied', key: 'share.error.auth', retry: false, lost: true }
    case 'denied': return { kind: 'denied', key: 'share.error.denied', retry: false, lost: true }
    case 'sharing_disabled': return { kind: 'sharing', key: 'share.disabled.text', retry: false, lost: false }
    case 'unknown_outcome': return { kind: 'unknown', key: 'share.error.unknown', retry: true, lost: false }
    case 'invalid': return { kind: 'invalid', key: 'share.error.invalid', retry: false, lost: false }
    case 'network': return { kind: 'network', key: 'share.error.network', retry: true, lost: false }
    case 'busy':
    case 'unavailable': return { kind: 'unavailable', key: 'share.error.unavailable', retry: true, lost: false }
    default: return { kind: 'error', key: 'share.error.generic', retry: false, lost: false }
  }
}

// The grants as the dialog draws them: the grant to everyone apart from the
// grants of named people, in the order the server sent them.
export function splitGrants(payload) {
  const list = Array.isArray(payload?.grants) ? payload.grants.filter(g => g && typeof g.userId === 'string') : []
  return {
    everyone: list.find(g => g.userId === EVERYONE) || null,
    people: list.filter(g => g.userId !== EVERYONE),
  }
}

// The search results that have no grant yet. An answer carries only an
// identifier and a display name; nothing else is read.
export function freshUsers(payload, grants) {
  const have = new Set((grants || []).map(g => g.userId))
  const users = Array.isArray(payload?.users) ? payload.users : []
  return users
    .filter(u => u && typeof u.id === 'string' && u.id !== EVERYONE && !have.has(u.id))
    .map(u => ({ id: u.id, displayName: typeof u.displayName === 'string' ? u.displayName : '' }))
}

export function searchText(query) {
  const q = typeof query === 'string' ? query.trim() : ''
  return q.length >= SEARCH_MIN && q.length <= SEARCH_MAX ? q : ''
}

// The roles the dialog offers, cleaned: an id and a label each, no owner and
// no duplicate.
export function cleanRoles(roles) {
  const seen = new Set()
  const out = []
  for (const r of Array.isArray(roles) ? roles : []) {
    const id = typeof r === 'string' ? r : r?.id
    if (typeof id !== 'string' || id === '' || id === 'owner' || seen.has(id)) continue
    seen.add(id)
    out.push({ id, label: typeof r?.label === 'string' && r.label ? r.label : id, help: typeof r?.help === 'string' ? r.help : '' })
  }
  return out
}

const KNOWN_ROLES = new Set(['use', 'manage', 'viewer', 'editor'])

// shareRoles turns the role ids a kind grants (the `roles` of a share
// listing) into the dialog's roles, with the host's words for the roles it
// knows. A kind with its own roles passes its own labels instead.
export function shareRoles(ids, t) {
  return cleanRoles((Array.isArray(ids) ? ids : []).map(id => (KNOWN_ROLES.has(id)
    ? { id, label: t(`share.roles.${id}`), help: t(`share.roleHelp.${id}`) }
    : { id, label: id })))
}

// The references step. Each row must have one of its own options before the
// dialog shares anything. A row marked readOnly cannot be shared from here:
// it is shown with its note (the person needs access of their own) and asks
// nothing.
export function cleanReferences(references) {
  return (Array.isArray(references) ? references : [])
    .filter(r => r && typeof r.id === 'string' && r.id !== '')
    .map(r => {
      const readOnly = r.readOnly === true
      return {
        id: r.id,
        label: typeof r.label === 'string' && r.label ? r.label : r.id,
        kind: typeof r.kind === 'string' ? r.kind : '',
        readOnly,
        note: readOnly && typeof r.note === 'string' ? r.note : '',
        options: readOnly ? [] : (Array.isArray(r.options) ? r.options : REFERENCE_OPTIONS).filter(o => REFERENCE_OPTIONS.includes(o)),
      }
    })
    .filter(r => r.readOnly || r.options.length > 0)
}

export function referencesReady(references, choices) {
  const c = choices || {}
  return cleanReferences(references).every(r => r.readOnly || r.options.includes(c[r.id]))
}

// The choices handed to onShare: only rows that exist, only valid options.
export function referenceChoices(references, choices) {
  const out = {}
  for (const r of cleanReferences(references)) {
    if (!r.readOnly && r.options.includes(choices?.[r.id])) out[r.id] = choices[r.id]
  }
  return out
}

// shareLine says what a list row shows about sharing, from an entry of a
// share listing (GET /api/shares/{kind}: owned and shared) or an add-on
// listing: { key, vars } for the host's common namespace, or null.
//   owned   { role: 'owner', sharedWith }
//   shared  { role, owner, everyone }
export function shareLine(entry) {
  if (!entry || typeof entry !== 'object') return null
  if (entry.role === 'owner') {
    const n = Number(entry.sharedWith) || 0
    return n > 0 ? { key: 'share.line.sharedWith', vars: { count: n } } : { key: 'share.line.private', vars: {} }
  }
  const name = typeof entry.owner === 'string' ? entry.owner : ''
  if (!name) return null
  return { key: entry.everyone === true ? 'share.line.sharedEveryone' : 'share.line.sharedBy', vars: { name } }
}

// The tone of a role badge. A role is not a status, so only the owner stands
// out; every granted role is drawn the same.
export function roleTone(role) {
  return role === 'owner' ? 'accent' : 'idle'
}
