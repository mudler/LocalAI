// The rules of the model rows of the Updates page, as pure functions so the
// node unit tests drive them without a browser.
//
// The read model is GET /api/models/updates: one item per model installed from
// a gallery, with the state the server computed (up-to-date, update-available,
// edited, unknown, gone, held, failed) and the journal of the last or running
// upgrade. This module turns an item into a row of the Updates page:
//
//   { id, kind: 'model', name, from, to, state, needsPerson, ... }
//
// state is one of the page's states (ready, updating, failed, current) or one
// of the model states that are not an update a person can start now: on-hold,
// unknown, gone and cleanup (an upgrade kept the old version). Those four go to
// their own group (groupOf in utils/updates.js).
//
// Models never update automatically: every update, edited update and failed
// upgrade needs a person.
import { shortDigest } from './backendRows.js'

// The phases of an upgrade, in order. The journal and the operation say the
// same word.
export const UPGRADE_PHASES = [
  'queued', 'preflight', 'staging', 'unloading', 'committing', 'loading',
  'confirmed', 'rolling-back', 'rolled-back', 'failed',
]

// An upgrade can be cancelled only before it stops the model.
export const CANCELLABLE_PHASES = ['queued', 'preflight', 'staging']

// The model states that are not an update a person can start now.
export const MODEL_OTHER_STATES = ['on-hold', 'unknown', 'gone', 'cleanup']

const UNKNOWN_REASONS = ['legacy-sidecar', 'unreadable', 'not-checked', 'check-failed']

// The operation of a model upgrade in /api/operations: an upgrade (taskType
// 'upgrade'), not a backend, not a deletion, named after the model. An install
// of the same name is not an upgrade, so it does not make the row "updating".
export function opForModel(operations, name) {
  if (!Array.isArray(operations) || !name) return null
  return operations.find(op => op.taskType === 'upgrade' && !op.isBackend && !op.isDeletion && (op.name === name || op.id === name)) || null
}

// The keys a person edited that the update also changes: each needs a choice.
export function conflictKeys(item) {
  return (item && Array.isArray(item.editedKeys) ? item.editedKeys : []).filter(k => k && k.conflict).map(k => k.key)
}

// The conflict keys that have no choice yet. `extra` are keys the server named
// in a 409 refusal: they need a choice too.
export function missingChoices(item, choices = {}, extra = []) {
  const keys = [...new Set([...conflictKeys(item), ...(extra || [])])]
  return keys.filter(k => choices[k] !== 'mine' && choices[k] !== 'gallery')
}

// The body of POST /api/models/upgrade/:name. Only the choices of keys that
// need one are sent; config_only only when the update downloads nothing.
export function upgradeBody(item, { choices = {}, configOnly = false, extra = [] } = {}) {
  const body = {}
  if (configOnly && item && item.configOnly) body.config_only = true
  const keys = [...new Set([...conflictKeys(item), ...(extra || [])])]
  const picked = {}
  for (const k of keys) if (choices[k] === 'mine' || choices[k] === 'gallery') picked[k] = choices[k]
  if (Object.keys(picked).length > 0) body.choices = picked
  return body
}

// The files of an update, counted by change, how many download, and how many
// would download without a checksum.
export function fileSummary(item) {
  const files = item && Array.isArray(item.changedFiles) ? item.changedFiles : []
  const out = { total: files.length, added: 0, changed: 0, removed: 0, downloads: 0, unchecked: 0 }
  for (const f of files) {
    if (f.change === 'added') out.added += 1
    else if (f.change === 'removed') out.removed += 1
    else out.changed += 1
    // A prompt template or the config is written, not downloaded.
    const download = f.change !== 'removed' && f.role !== 'template' && f.role !== 'config'
    if (download) out.downloads += 1
    if (download && f.checksum === false) out.unchecked += 1
  }
  return out
}

// How a value of a config key is shown: text as it is, anything else as JSON,
// and a missing value as null (the caller says "not set").
export function valueText(v) {
  if (v === undefined || v === null) return null
  if (typeof v === 'string') return v
  try { return JSON.stringify(v) } catch { return String(v) }
}

// One row of the Updates page from one item. `operations` is the live list of
// /api/operations: an upgrade the list shows is running, whatever the journal
// read a moment ago says.
export function modelRow(it, operations = []) {
  const up = it.upgrade || null
  const conflicts = conflictKeys(it)
  const row = {
    id: it.id || `model:${it.name}`,
    kind: 'model',
    name: it.name,
    from: shortDigest(it.installedDigest),
    to: shortDigest(it.availableDigest),
    gallery: it.gallery || '',
    entryName: it.entryName || '',
    modelState: it.state,
    reason: UNKNOWN_REASONS.includes(it.reason) ? it.reason : (it.reason ? 'unreadable' : ''),
    error: it.error || '',
    held: Boolean(it.held) || it.state === 'held',
    edited: it.state === 'edited' || conflicts.length > 0,
    conflicts: conflicts.length,
    editedKeys: Array.isArray(it.editedKeys) ? it.editedKeys.length : 0,
    configOnly: Boolean(it.configOnly),
    files: fileSummary(it),
    removableOld: Boolean(up && up.removableOld),
    retained: up && Array.isArray(up.retainedFiles) ? up.retainedFiles.length : 0,
    // Gone and unknown say nothing reliable about a newer version.
    hasUpdate: Boolean(it.availableDigest && it.installedDigest && it.availableDigest !== it.installedDigest)
      && ['update-available', 'edited', 'failed', 'held'].includes(it.state),
    // Models never take part in "Update ready": each upgrade is confirmed
    // on its own, because it stops the model.
    startable: false,
    item: it,
    state: 'current',
    needsPerson: false,
  }
  const op = opForModel(operations, it.name)
  if ((op && !op.error) || (up && up.running)) {
    const phase = op && op.isQueued ? 'queued' : ((op && op.phase) || (up && up.phase) || '')
    return {
      ...row,
      state: 'updating',
      phase,
      queued: phase === 'queued',
      progress: op && !op.isQueued && op.progress > 0 ? Math.min(100, Math.round(op.progress)) : 0,
      jobID: op ? op.jobID : (up && up.opId) || '',
      cancellable: Boolean(op && op.cancellable && CANCELLABLE_PHASES.includes(phase)),
    }
  }
  switch (it.state) {
    case 'failed':
      return {
        ...row,
        state: 'failed',
        error: (up && up.error) || (op && op.error ? String(op.error) : '') || it.error || '',
        rolledBack: Boolean(up && up.phase === 'rolled-back'),
        needsPerson: true,
      }
    case 'update-available':
    case 'edited':
      if (op && op.error) return { ...row, state: 'failed', error: String(op.error), jobID: op.jobID, rolledBack: false, needsPerson: true }
      return { ...row, state: 'ready', needsPerson: true }
    case 'held':
      return { ...row, state: 'on-hold' }
    case 'gone':
      return { ...row, state: 'gone' }
    case 'unknown':
      return { ...row, state: 'unknown' }
    default:
      return row.removableOld ? { ...row, state: 'cleanup' } : row
  }
}

export function modelRows(snapshot, { operations = [] } = {}) {
  if (!snapshot || !Array.isArray(snapshot.items)) return []
  return snapshot.items.filter(it => it && it.name).map(it => modelRow(it, operations))
}

// The model rows by name, for the Models page: what its indicator and its
// hold switch read.
export function modelRowsByName(rows) {
  const out = {}
  for (const r of rows || []) if (r.kind === 'model') out[r.name] = r
  return out
}

// The state of the model gallery source, from the read model. A server with
// no model gallery says disabled; a source that never answered is never.
export function modelSource(snapshot, status = 'ready') {
  if (status === 'loading') return { state: 'never', loading: true }
  if (status === 'error' && !snapshot) return { state: 'failed', error: 'unavailable' }
  const s = snapshot && snapshot.sources && snapshot.sources.gallery
  if (!s) return null
  return { ...s }
}

// The indicator of an installed model on the Models page: an update that
// needs a person, an upgrade that runs, a failed one, or the hold.
export function modelIndicator(row) {
  if (!row) return null
  switch (row.state) {
    case 'ready': return { key: row.edited ? 'edited' : 'update', icon: 'arrow-up' }
    case 'updating': return { key: 'updating', icon: 'spinner' }
    case 'failed': return { key: 'failed', icon: 'alert-circle' }
    case 'on-hold': return { key: 'held', icon: 'lock' }
    default: return null
  }
}

// Whether the hold of a model can change now: it needs a recorded origin
// (a legacy or unreadable install has none) and no upgrade running.
export function canHold(row) {
  if (!row || row.state === 'updating') return false
  if (row.state === 'unknown' && (row.reason === 'legacy-sidecar' || row.reason === 'unreadable')) return false
  return true
}

// Whether a model can be adopted: it was installed by a release that did not
// record where it came from (unknown, reason legacy-sidecar).
export function canAdopt(row) {
  return Boolean(row && row.kind === 'model' && row.state === 'unknown' && row.reason === 'legacy-sidecar')
}

// The candidates of a refused adoption, from err.body.candidates
// ("gallery@entry" each), as { id, gallery, entry }. A string with no "@" is
// kept as an entry with no gallery name. Duplicates and blanks are dropped.
export function adoptCandidates(body) {
  const list = body && Array.isArray(body.candidates) ? body.candidates : []
  const seen = new Set()
  const out = []
  for (const raw of list) {
    if (typeof raw !== 'string' || !raw.trim() || seen.has(raw)) continue
    seen.add(raw)
    const at = raw.indexOf('@')
    out.push(at < 0 ? { id: raw, gallery: '', entry: raw } : { id: raw, gallery: raw.slice(0, at), entry: raw.slice(at + 1) })
  }
  return out
}

// The body of POST /api/models/:name/adopt. Without a pick the server finds
// the entry by itself; a pick names the gallery and the entry.
export function adoptBody(pick) {
  if (!pick) return {}
  const body = {}
  if (pick.gallery) body.gallery = pick.gallery
  if (pick.entry) body.entry = pick.entry
  return body
}
