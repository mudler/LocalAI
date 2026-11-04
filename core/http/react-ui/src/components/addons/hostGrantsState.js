// Pure rules of the host access screens: the install step, the upgrade
// approval, the Host access page and the blanket switch. No React and no
// network. The owner decides what is allowed; this module only shapes the
// answers of the host-grants routes for display and builds the request an
// administrator approves. It never grants anything by itself.

export const RISKS = ['read', 'changes_data', 'network', 'runs_jobs']
export const BLANKET_MODES = ['off', 'official', 'any']

// The phrases the administrator types to turn the blanket switch on. The
// owner checks the same text again.
export const BLANKET_CONFIRM = {
  official: 'grant official add-ons what they declare',
  any: 'grant any library add-on what it declares',
}

const STATUSES = new Set(['allowed', 'not_allowed', 'blocked', 'unsupported'])

function statusOf(raw) {
  const s = String(raw || '').toLowerCase().replace(/[\s-]+/g, '_')
  return STATUSES.has(s) ? s : 'not_allowed'
}

// Where a grant comes from, in the three words a person uses. The deployment
// is the policy file or flags; "you" is an explicit choice of an
// administrator; blanket is the switch.
export function sourceOf(raw) {
  const s = String(raw || '').toLowerCase()
  if (s === 'blanket') return 'blanket'
  if (s === 'deployment' || s === 'file' || s === 'flag' || s === 'floor') return 'deployment'
  if (s === 'install' || s === 'upgrade' || s === 'edit' || s === 'ui' || s === 'you') return 'you'
  return ''
}

function riskOf(raw) {
  const r = String(raw || '').toLowerCase().replace(/[\s-]+/g, '_')
  return RISKS.includes(r) ? r : ''
}

const list = (v) => (Array.isArray(v) ? v.filter(x => typeof x === 'string') : [])

// One operation as the screens draw it. `required` is read from the add-on's
// requested list, so a response that forgets the flag cannot make a required
// operation look optional.
export function normalizeOp(raw, requested = {}) {
  const o = raw && typeof raw === 'object' ? raw : {}
  const name = typeof o.name === 'string' ? o.name : ''
  const required = list(requested.required).includes(name) || o.required === true
  return {
    name,
    title: typeof o.title === 'string' && o.title ? o.title : name,
    text: typeof o.text === 'string' ? o.text : '',
    risk: riskOf(o.risk),
    required,
    status: statusOf(o.status),
    source: sourceOf(o.source),
    reason: typeof o.reason === 'string' ? o.reason : '',
    at: typeof o.at === 'string' ? o.at : '',
    by: typeof o.by === 'string' ? o.by : '',
    hint: o.hint === true,
  }
}

function normalizeRequested(raw) {
  const r = raw && typeof raw === 'object' ? raw : {}
  return { required: list(r.required), optional: list(r.optional) }
}

// What the install dialog and the upgrade approval ask the owner for, as it
// answers (POST host-grants/preview).
export function normalizePreview(payload) {
  const p = payload && typeof payload === 'object' ? payload : {}
  const requested = normalizeRequested(p.requested)
  const ops = (Array.isArray(p.ops) ? p.ops : []).map(o => normalizeOp(o, requested))
  const blockedRequired = list(p.blockedRequired)
  return {
    digest: typeof p.digest === 'string' ? p.digest : '',
    version: typeof p.version === 'string' ? p.version : '',
    revision: Number(p.revision) || 0,
    nonce: typeof p.nonce === 'string' ? p.nonce : '',
    official: p.official === true ? true : (p.official === false ? false : null),
    // The owner keeps UI grants locked: the dialog shows what the add-on asks for and grants nothing.
    locked: p.locked === true,
    lockedReason: p.lockedReason === 'no_auth' ? 'no_auth' : 'deployment',
    // Operations that are new in this version. Empty on an install.
    added: list(p.added),
    requested,
    ops,
    blockedRequired,
    canInstall: p.canInstall !== false && blockedRequired.length === 0,
  }
}

// The operations the dialog has nothing to ask about: the add-on declares none.
export function needsStep(preview) {
  return Boolean(preview) && preview.ops.length > 0
}

// An operation the administrator can tick: not held, not blocked, supported.
export function tickable(op) {
  return op.status === 'not_allowed'
}

// The ticks the dialog starts with. Required operations are ticked and cannot
// be unticked. Optional ones are unticked, except one the administrator held
// before an uninstall (the owner sends it as a hint).
export function initialTicks(preview) {
  const out = []
  if (preview.locked) return out
  for (const op of preview.ops) {
    if (!tickable(op)) continue
    if (op.required || op.hint) out.push(op.name)
  }
  return out
}

// The names to send as approved: the ticked ones, and every required one that
// can still be granted, so a required operation can never go missing from the
// request because of a stale tick list.
export function approvedOps(preview, ticked) {
  if (preview.locked) return []
  const set = new Set(ticked)
  for (const op of preview.ops) if (tickable(op) && op.required) set.add(op.name)
  return preview.ops.filter(op => tickable(op) && set.has(op.name)).map(op => op.name)
}

// The body of the install or upgrade that carries the approval.
export function approvalBody(preview, ticked, { operationId, action = 'install', sourceId, addonId, version, blanketAck, downgrade } = {}) {
  const approve = { digest: preview.digest, ops: approvedOps(preview, ticked), revision: preview.revision, nonce: preview.nonce }
  if (blanketAck) approve.blanketAck = true
  // Only an install from a file can be a downgrade, and only after a tick.
  return { operationId, action, sourceId, addonId, version, ...(downgrade ? { downgrade: true } : {}), approve }
}

// The operations grouped by family (the part before the first dot), in the
// order the owner sent them.
export function groupOps(ops) {
  const groups = []
  for (const op of ops) {
    const family = op.name.split('.')[0] || 'other'
    let g = groups.find(x => x.family === family)
    if (!g) { g = { family, ops: [] }; groups.push(g) }
    g.ops.push(op)
  }
  return groups
}

// What the Install button does for a preview: it is disabled when a required
// operation is blocked or the host does not support it.
export function installBlock(preview) {
  if (!preview) return null
  const blocked = preview.ops.filter(o => o.required && o.status === 'blocked').map(o => o.name)
  const unsupported = preview.ops.filter(o => o.required && o.status === 'unsupported').map(o => o.name)
  for (const n of preview.blockedRequired) if (!blocked.includes(n)) blocked.push(n)
  if (blocked.length > 0) return { kind: 'blocked', ops: blocked }
  if (unsupported.length > 0) return { kind: 'unsupported', ops: unsupported }
  return null
}

// The key of the words for a refusal of a host-grants route.
export function hostErrorKey(error) {
  if (!error) return 'unavailable'
  const type = error.type || ''
  if (['not_human_surface', 'locked_by_deployment', 'blocked_by_policy', 'revision_conflict', 'approval_stale', 'approval_required'].includes(type)) return type
  if (error.kind === 'unknown_outcome') return 'unknownOutcome'
  if (error.kind === 'denied') return 'denied'
  if (error.kind === 'auth') return 'auth'
  if (error.kind === 'network') return 'network'
  return 'unavailable'
}

// ---- The Host access page ---------------------------------------------------

function normalizeAddon(raw) {
  const a = raw && typeof raw === 'object' ? raw : {}
  const requested = normalizeRequested(a.requested)
  const ops = (Array.isArray(a.ops) ? a.ops : []).map(o => normalizeOp(o, requested))
  const pending = a.pendingUpgrade && typeof a.pendingUpgrade === 'object' ? a.pendingUpgrade : null
  return {
    id: typeof a.id === 'string' ? a.id : '',
    title: typeof a.title === 'string' && a.title ? a.title : (a.id || ''),
    version: typeof a.version === 'string' ? a.version : '',
    sourceId: typeof a.sourceId === 'string' ? a.sourceId : '',
    state: typeof a.state === 'string' ? a.state : '',
    requested,
    ops,
    noLongerRequested: (Array.isArray(a.noLongerRequested) ? a.noLongerRequested : []).map(o => normalizeOp(typeof o === 'string' ? { name: o, status: 'allowed', source: 'edit' } : o, requested)),
    newOptional: list(a.newOptional),
    identityMismatch: a.identityMismatch === true,
    stopped: typeof a.stopped === 'string' ? a.stopped : '',
    pendingUpgrade: pending ? { version: typeof pending.version === 'string' ? pending.version : '', delta: list(pending.delta) } : null,
  }
}

function normalizeRules(raw) {
  const out = {}
  if (raw && typeof raw === 'object') for (const [k, v] of Object.entries(raw)) out[k] = list(v)
  return out
}

// The answer of GET host-grants.
export function normalizeOverview(payload) {
  const p = payload && typeof payload === 'object' ? payload : {}
  const b = p.blanket && typeof p.blanket === 'object' ? p.blanket : {}
  const mode = BLANKET_MODES.includes(b.mode) ? b.mode : 'off'
  const deny = p.deny && typeof p.deny === 'object' ? p.deny : {}
  return {
    revision: Number(p.revision) || 0,
    locked: p.locked === true,
    lockedReason: p.lockedReason === 'no_auth' ? 'no_auth' : 'deployment',
    blanket: {
      mode,
      includeOptional: b.includeOptional === true,
      setBy: b.setBy === 'deployment' ? 'deployment' : 'ui',
      revision: Number(b.revision) || 0,
    },
    deny: { file: normalizeRules(deny.file), ui: normalizeRules(deny.ui) },
    addons: (Array.isArray(p.addons) ? p.addons : []).map(normalizeAddon).filter(a => a.id),
  }
}

// Whether the administrator can change an operation by hand: the deployment's
// own grants are read-only, and a blocked or unsupported one cannot be granted.
export function opEditable(op, locked = false) {
  if (locked) return false
  if (op.status === 'blocked' || op.status === 'unsupported') return false
  return op.source !== 'deployment'
}

// The operations of the page as the administrator sees them: the ones the
// add-on asks for, then the ones it holds that it no longer asks for.
export function pageOps(addon) {
  const asked = new Set(addon.ops.map(o => o.name))
  return [...addon.ops, ...addon.noLongerRequested.filter(o => !asked.has(o.name))]
}

// The names that stay granted after an edit of the page: every held operation
// that the administrator does not turn off, plus the ones turned on.
export function editedOps(addon, changes) {
  const out = []
  for (const op of pageOps(addon)) {
    if (op.status === 'blocked' || op.status === 'unsupported') continue
    if (op.source === 'deployment') continue
    const held = op.status === 'allowed'
    const next = Object.hasOwn(changes, op.name) ? changes[op.name] : held
    if (next) out.push(op.name)
  }
  return out
}

// Which add-ons hold something the blanket switch granted.
export function blanketHolders(overview) {
  const out = []
  for (const a of overview.addons) {
    const ops = pageOps(a).filter(o => o.source === 'blanket' && o.status === 'allowed').map(o => o.name)
    if (ops.length > 0) out.push({ id: a.id, title: a.title, ops })
  }
  return out
}

// The add-ons that wait for an approval, for the banner.
export function pendingApprovals(overview) {
  return overview.addons.filter(a => a.pendingUpgrade && a.pendingUpgrade.delta.length > 0)
}

// ---- The blanket switch -----------------------------------------------------

export function blanketBody(overview, { mode, includeOptional, applyToInstalled = false, revokeOnOff = false, confirm = '' }) {
  const body = { revision: overview.revision, mode, includeOptional: Boolean(includeOptional) }
  if (mode !== 'off') {
    body.confirm = confirm
    body.applyToInstalled = Boolean(applyToInstalled)
  } else {
    body.revokeOnOff = Boolean(revokeOnOff)
  }
  return body
}

// The typed text must match exactly, ignoring only the spaces around it.
export function confirmMatches(mode, typed) {
  const want = BLANKET_CONFIRM[mode]
  return Boolean(want) && String(typed || '').trim() === want
}

// ---- Deny rules --------------------------------------------------------------

// A deny rule as one row: the add-on it covers ("*" is all add-ons) and the
// prefix, the exact name or "*" for everything.
export function denyRows(rules, origin) {
  const rows = []
  for (const [target, entries] of Object.entries(rules || {})) {
    for (const entry of entries) rows.push({ origin, target, entry, key: `${origin}:${target}:${entry}` })
  }
  return rows
}

// The UI deny map after a rule is added or removed.
export function withDenyRule(ui, { target, entry }, add) {
  const next = {}
  for (const [k, v] of Object.entries(ui || {})) next[k] = [...v]
  const cur = next[target] || []
  if (add) {
    if (entry === '*') next[target] = ['*']
    else if (!cur.includes(entry) && !cur.includes('*')) next[target] = [...cur, entry]
  } else {
    next[target] = cur.filter(e => e !== entry)
  }
  if (!next[target] || next[target].length === 0) delete next[target]
  return next
}

const ENTRY = /^[a-z]+(\.[a-z]+)*\.?$/

// What the form can know before it asks the owner. The owner decides.
export function validateDenyDraft({ target, entry }) {
  if (target !== '*' && !/^[a-z0-9][a-z0-9-]*$/.test(target || '')) return 'target'
  if (entry === '*') return ''
  return ENTRY.test(entry || '') ? '' : 'entry'
}

export function opTitle(ops, name) {
  const op = ops.find(o => o.name === name)
  return op && op.title ? op.title : name
}
