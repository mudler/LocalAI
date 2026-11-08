// The rules of the Updates page and of the badges, as pure functions so the
// node unit tests drive them without a browser.
//
// A row is one thing that can be updated: a gallery backend, a library backend
// or an add-on. The three kinds come from three sources (the backend read model
// of GET /api/backends/updates, the installed records of the add-on owner and
// its host-access overview), and this module puts them in one shape:
//
//   { id, kind: 'gallery'|'library'|'addon', name, from, to, state, ... }
//
// state is one of ready, updating, failed, held, blocked, current. A row has
// exactly one state, and a group follows from it (groupOf).
import { versionLabel, shortDigest, opForBackend } from './backendRows.js'
import { updateView, upgradeAvailability } from '../components/addons/updateState.js'

export const GROUPS = ['needs', 'updating', 'ready', 'current']
export const FILTERS = ['all', 'ready', 'needs-you', 'updating', 'current']

const NEEDS_ORDER = { failed: 0, held: 1, blocked: 2 }

export function groupOf(row) {
  switch (row.state) {
    case 'failed':
    case 'held':
    case 'blocked': return 'needs'
    case 'updating': return 'updating'
    case 'ready': return 'ready'
    default: return 'current'
  }
}

// What a backend of the read model shows as "from" and "to": the version, or the
// short digest when the registry names none.
function backendVersions(it) {
  return {
    from: versionLabel(it.installedVersion) || shortDigest(it.installedDigest),
    to: versionLabel(it.availableVersion) || shortDigest(it.availableDigest),
  }
}

// The rows of the backend read model. `operations` is the live list of
// /api/operations.
export function backendRows(snapshot, { operations = [], auto = false } = {}) {
  if (!snapshot || !Array.isArray(snapshot.items)) return []
  return snapshot.items.map((it) => {
    const { from, to } = backendVersions(it)
    const source = snapshot.sources?.[it.kind]
    const nodes = Array.isArray(it.nodes) ? it.nodes : []
    const row = {
      id: it.id,
      kind: it.kind,
      name: it.name,
      from,
      to,
      subtype: it.state,
      availableVersion: it.availableVersion || '',
      installedVersion: it.installedVersion || '',
      reason: it.reason || '',
      sourceId: it.sourceId || '',
      signed: Boolean(it.signed),
      nodes,
      nodeCount: nodes.length,
      driftNodes: it.driftNodes || 0,
      covered: Boolean(auto),
      old: Boolean(source && source.stale),
      startable: true,
      state: 'ready',
    }
    const op = opForBackend(operations, it.name)
    if (op && !op.error && !op.isDeletion) {
      return { ...row, state: 'updating', progress: op.progress > 0 ? Math.min(100, Math.round(op.progress)) : 0, phase: op.isQueued ? 'queued' : (op.message || op.phase || ''), jobID: op.jobID, cancellable: Boolean(op.cancellable) }
    }
    if (op && op.error) return { ...row, state: 'failed', error: String(op.error), jobID: op.jobID, startable: false }
    if (snapshot.installsDisabled) return { ...row, state: 'blocked', blocked: 'installsDisabled', startable: false }
    return row
  })
}

// What this page knows about an update it started that the lists do not show
// (yet): { [id]: { phase: 'running'|'failed', message, progress } }. A running
// update that the operations list does not show yet is Updating, not Ready. A
// failed one (the operation ended and the item is still listed, or no source
// could confirm it) is Failed: the lists alone would call it Ready again.
export function applyLocal(rows, local) {
  if (!local) return rows
  return rows.map((row) => {
    const mine = local[row.id]
    if (!mine || row.state === 'updating') return row
    if (mine.phase === 'running' && (row.state === 'ready' || row.state === 'failed')) {
      return { ...row, state: 'updating', progress: mine.progress || 0, phase: mine.message || '', cancellable: false, startable: false }
    }
    if (mine.phase === 'failed' && row.state === 'ready') {
      return { ...row, state: 'failed', error: mine.message || '', startable: false }
    }
    return row
  })
}

// The rows of the add-ons. `installed` are the owner's records (already
// localized), `grants` the host-access overview (held upgrades), `live` the
// upgrades the store of this browser follows.
export function addonRows({ installed = [], grants = null, live = [], auto = false, isAdmin = true } = {}) {
  const rows = []
  const seen = new Set()
  const held = new Map()
  for (const a of (grants && grants.addons) || []) {
    if (a.pendingUpgrade && a.pendingUpgrade.delta && a.pendingUpgrade.delta.length > 0) held.set(a.id, a)
  }
  const liveOf = (id) => live.find(op => op.addonId === id) || null
  for (const rec of installed) {
    const id = `addon:${rec.id}`
    seen.add(rec.id)
    const view = updateView(rec.update)
    const av = upgradeAvailability(rec, { isAdmin })
    const op = liveOf(rec.id)
    const base = {
      id,
      kind: 'addon',
      name: rec.title || rec.id,
      addonId: rec.id,
      from: rec.version ? versionLabel(rec.version) : '',
      to: view.version ? versionLabel(view.version) : '',
      sourceId: rec.sourceId || '',
      covered: Boolean(auto),
      startable: av.startable,
      availability: av,
      record: rec,
      state: 'current',
    }
    const hold = held.get(rec.id)
    if (op && (op.state === 'running' || op.state === 'cancelling')) {
      rows.push({ ...base, state: 'updating', progress: 0, phase: op.stage || op.phase || '', jobID: op.jobID, cancellable: Boolean(op.cancellable), startable: false })
    } else if (op && op.state === 'failed') {
      rows.push({ ...base, state: 'failed', error: op.errorKey || 'unavailable', errorKey: op.errorKey || 'unavailable', jobID: op.jobID, startable: false })
    } else if (hold) {
      rows.push({ ...base, state: 'held', to: versionLabel(hold.pendingUpgrade.version), heldCount: hold.pendingUpgrade.delta.length, grant: hold, startable: false })
    } else if (view.state === 'update-available') {
      rows.push(base.startable ? { ...base, state: 'ready' } : { ...base, state: 'ready', needsCheck: av.reason })
    } else if (view.state === 'incompatible' || view.state === 'integrity-error') {
      rows.push({ ...base, state: 'blocked', blocked: view.state, reasons: view.reasons, startable: false })
    } else if (view.state === 'up-to-date') {
      rows.push({ ...base, state: 'current' })
    }
    // unknown and check-failed are not rows: nothing says the add-on is
    // current, and the source line carries the check.
  }
  // A held upgrade or a running one of an add-on the records do not list.
  for (const [id, hold] of held) {
    if (seen.has(id)) continue
    rows.push({ id: `addon:${id}`, kind: 'addon', name: hold.title || id, addonId: id, from: versionLabel(hold.version), to: versionLabel(hold.pendingUpgrade.version), sourceId: hold.sourceId || '', covered: Boolean(auto), startable: false, state: 'held', heldCount: hold.pendingUpgrade.delta.length, grant: hold })
  }
  return rows
}

// Backends that are installed, not listed as having an update and whose source
// answered: those are current. A source that failed or was never checked says
// nothing about them.
export function currentBackendRows(installed, snapshot) {
  if (!snapshot || !Array.isArray(installed)) return []
  const listed = new Set((snapshot.items || []).map(i => i.name))
  const out = []
  for (const b of installed) {
    const name = b.Name || b.name
    if (!name || b.IsSystem || b.IsMeta || listed.has(name)) continue
    const lib = b.Metadata && b.Metadata.library
    const kind = lib ? 'library' : 'gallery'
    const source = snapshot.sources && snapshot.sources[kind]
    if (!source || source.state !== 'ok') continue
    out.push({
      id: `${kind}:${lib ? `${lib}/` : ''}${name}`,
      kind,
      name,
      from: versionLabel(b.Metadata && b.Metadata.version) || shortDigest(b.Metadata && b.Metadata.digest),
      to: '',
      state: 'current',
      startable: false,
    })
  }
  return out
}

export function sortRows(rows) {
  return [...rows].sort((a, b) => {
    const ga = GROUPS.indexOf(groupOf(a))
    const gb = GROUPS.indexOf(groupOf(b))
    if (ga !== gb) return ga - gb
    if (ga === 0 && NEEDS_ORDER[a.state] !== NEEDS_ORDER[b.state]) return NEEDS_ORDER[a.state] - NEEDS_ORDER[b.state]
    return a.name.localeCompare(b.name)
  })
}

export function groupRows(rows) {
  const out = { needs: [], updating: [], ready: [], current: [] }
  for (const row of sortRows(rows)) out[groupOf(row)].push(row)
  return out
}

export function filterRows(rows, filter) {
  switch (filter) {
    case 'ready': return rows.filter(r => groupOf(r) === 'ready')
    case 'needs-you': return rows.filter(r => groupOf(r) === 'needs')
    case 'updating': return rows.filter(r => groupOf(r) === 'updating')
    case 'current': return rows.filter(r => groupOf(r) === 'current')
    default: return rows
  }
}

export function filterCounts(rows) {
  const g = groupRows(rows)
  return { all: rows.length, ready: g.ready.length, 'needs-you': g.needs.length, updating: g.updating.length, current: g.current.length }
}

export function normalizeFilter(value) {
  return FILTERS.includes(value) ? value : 'all'
}

// The gallery backends that have a newer build, by name, in the shape the
// Backends page, the Operate overview and the Nodes page read: the versions and
// the nodes that differ from the others. One place, so a page never keeps a
// poll of the same fact.
export function galleryUpdateMap(rows) {
  return Object.fromEntries((rows || [])
    .filter(r => r.kind === 'gallery' && (r.state === 'ready' || r.state === 'blocked'))
    .map(r => [r.name, {
      backend_name: r.name,
      installed_version: r.installedVersion || r.from,
      available_version: r.availableVersion || r.to,
      node_drift: (r.nodes || []).filter(n => n.drift).map(n => ({ node_id: n.nodeId, node_name: n.nodeName, version: n.version })),
    }]))
}

// The number on the tab and on the Operate item: what needs a person. A ready
// update that an automatic setting will apply is not in it. A failed or held
// update is, whatever the setting says. An update that this server refuses by
// policy (installs off) is not: nobody can act on it.
export function badgeOf(rows) {
  let count = 0
  let failed = false
  for (const row of rows) {
    if (row.state === 'failed') { count += 1; failed = true } else if (row.state === 'held') count += 1
    else if (row.state === 'blocked') { if (row.blocked !== 'installsDisabled') count += 1 } else if (row.state === 'ready' && !row.covered) count += 1
  }
  return { count, failed }
}

// ---- Sources ----------------------------------------------------------------

// The state of the add-on source, from what the shell could read. `status` is
// 'off' (the server has no add-ons for this person), 'loading', 'error' or
// 'ready'.
export function addonSource({ status, installed = [], error = '' } = {}) {
  if (status === 'off') return { state: 'disabled', reason: 'off' }
  if (status === 'loading') return { state: 'never', loading: true }
  if (status === 'error') return { state: 'failed', error: error || 'unavailable', stale: installed.length > 0 }
  if (installed.length === 0) return { state: 'ok', empty: true }
  let at = ''
  let failed = ''
  let checked = 0
  for (const rec of installed) {
    const u = rec.update
    if (!u || typeof u !== 'object') continue
    const view = updateView(u)
    if (view.checkedAt) {
      checked += 1
      if (!at || Date.parse(view.checkedAt) > Date.parse(at)) at = view.checkedAt
    }
    if (view.state === 'check-failed' && !failed) failed = view.error || 'internal'
  }
  if (failed) return { state: 'failed', error: failed, checkedAt: at, stale: checked > 0 }
  if (checked === 0) return { state: 'never' }
  return { state: 'ok', checkedAt: at }
}

// The sources in the order of the source line: gallery, library, add-ons.
export function sourceViews(snapshot, addons) {
  const out = []
  if (snapshot && snapshot.sources) {
    for (const id of ['gallery', 'library']) {
      const s = snapshot.sources[id]
      if (s) out.push({ id, ...s })
    }
  }
  if (addons && !(addons.state === 'ok' && addons.empty)) out.push({ id: 'addons', ...addons })
  return out
}

// The one sentence of the strip. A claim of "no updates" needs every source
// that can be checked to have answered; otherwise the strip says what is
// missing.
export function headlineOf({ rows, sources, loading = false, loadError = false }) {
  if (loading) return { kind: 'loading' }
  if (loadError) return { kind: 'loadError' }
  const live = sources.filter(s => s.state !== 'disabled')
  const total = rows.filter(r => groupOf(r) !== 'current').length
  if (total > 0) return { kind: 'updates', count: total }
  if (live.length === 0) return { kind: 'unavailable' }
  const failed = live.filter(s => s.state === 'failed')
  const never = live.filter(s => s.state === 'never')
  if (failed.length === live.length) return { kind: 'couldNot' }
  if (never.length === live.length) return { kind: 'never' }
  if (failed.length > 0) return { kind: 'noneButFailed', sources: failed.map(s => s.id) }
  if (never.length > 0) return { kind: 'noneButNever', sources: never.map(s => s.id) }
  return { kind: 'current' }
}

// The counts of the line under the sentence: "2 ready, 1 updating, 2 need you".
export function breakdownOf(rows) {
  const g = groupRows(rows)
  return { ready: g.ready.length, updating: g.updating.length, needs: g.needs.length }
}

// ---- Bulk and completion ----------------------------------------------------

// The candidates of "Update all ready" in the shape of useBulkUpgrade. Only the
// ready ones that can start take part; held and blocked rows never run.
export function bulkItemsOf(rows) {
  return sortRows(rows).filter(r => r.state === 'ready').map(r => ({
    id: r.id,
    name: r.name,
    from: r.from,
    to: r.to,
    startable: Boolean(r.startable),
    reason: r.needsCheck || '',
    row: r,
  }))
}

// How an operation of /api/operations moves a followed job to its end. `track`
// is { seen, since }, kept by the caller between calls. The list never says
// "done": a finished operation leaves it, so leaving after it was seen, or
// never showing in `graceMs`, is "ended". A listed operation with an error is
// failed. Whether the update worked is not decided here (see verdictOf).
export function trackJob(track, operations, jobID, now, graceMs = 4000) {
  const op = (operations || []).find(o => o.jobID === jobID)
  if (op && op.error) return { track: { ...track, seen: true }, verdict: 'failed', error: String(op.error) }
  if (op) return { track: { ...track, seen: true }, verdict: 'wait' }
  if (track.seen) return { track, verdict: 'ended' }
  if (now - track.since >= graceMs) return { track, verdict: 'ended' }
  return { track, verdict: 'wait' }
}

// The end of an update, from the check that ran after the operation ended. Done
// means the operation ended without error and the fresh check no longer lists
// the item. A throttled answer is seconds old and may predate the update, so
// it is asked again while tries remain. A source that failed cannot confirm.
export function verdictOf({ listed, throttled = false, sourceFailed = false, tries = 0, maxTries = 4 }) {
  if (sourceFailed) return 'unconfirmed'
  if (!listed) return 'done'
  if (throttled && tries < maxTries) return 'retry'
  return 'unchanged'
}

// Whether an add-on record still carries the update the row showed.
export function addonStillListed(record, row) {
  if (!record) return false
  const view = updateView(record.update)
  if (view.state !== 'update-available') return false
  return versionLabel(view.version) === row.to || !row.to
}

// How long ago a time was, as a whole number of the largest fitting unit, for
// Intl.RelativeTimeFormat. Null for a time that is missing or does not parse.
export function ageParts(iso, now = Date.now()) {
  const at = Date.parse(iso || '')
  if (!iso || Number.isNaN(at)) return null
  const seconds = Math.max(0, Math.round((now - at) / 1000))
  if (seconds < 60) return { value: seconds, unit: 'second', justNow: seconds < 20 }
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return { value: minutes, unit: 'minute' }
  const hours = Math.round(minutes / 60)
  if (hours < 48) return { value: hours, unit: 'hour' }
  return { value: Math.round(hours / 24), unit: 'day' }
}
