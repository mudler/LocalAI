// The state of an update run as the page shows it, as pure functions so the
// node unit tests drive them without a browser. The server owns the run: it
// orders the items, runs them and keeps their state, so the page only asks for
// a plan (the review), starts the run and reads it back. Nothing here runs an
// update or decides when one is done.
//
// A run of GET /api/updates/runs/:id is
//   { id, source, actor, state: 'running'|'finished'|'cancelled', created_at, finished_at?,
//     items: [{ key, kind, name, title?, from, to, expected_to, phase, progress, message?,
//               queued_reason?, error_key?, error?, retryable, cancellable, verifying?, job_id?, nodes? }] }
// with the items in the order they run. Empty fields are left out; the page
// counts the phases itself.

export const PHASES = ['queued', 'running', 'updated', 'failed', 'rolled_back', 'cancelled', 'skipped', 'unconfirmed']

// The phases after which an item does not change any more.
const ENDED = new Set(['updated', 'failed', 'rolled_back', 'cancelled', 'skipped', 'unconfirmed'])
// The ended phases a person has to look at: the new version is not running.
const ATTENTION = new Set(['failed', 'rolled_back', 'unconfirmed'])

// The status of one node of a backend item (the operation's per-node status:
// queued, downloading, running_on_worker, success, error, rolled_back,
// unconfirmed) as the phase of a run item, so a node reads with the words of
// the run. A status the page does not know is returned as it is.
const NODE_PHASES = {
  queued: 'queued', downloading: 'running', running_on_worker: 'running', running: 'running',
  success: 'updated', error: 'failed', rolled_back: 'rolled_back', unconfirmed: 'unconfirmed',
}
export const nodePhase = (node) => NODE_PHASES[node?.phase] || node?.phase || 'queued'

// Whether an item still holds its place in the run: it runs, or the server
// checks whether its update applied (verifying, also in the unconfirmed phase
// after a restart). An unconfirmed item that no check could answer is
// settled. The same rule as Item.Busy on the server.
export const isBusy = (item) => Boolean(item && (item.phase === 'running' || (item.phase === 'unconfirmed' && item.verifying)))
export const isEnded = (item) => Boolean(item && ENDED.has(item.phase) && !isBusy(item))
export const needsAttention = (item) => Boolean(item && ATTENTION.has(item.phase) && !isBusy(item))
// How many items of a run are busy.
export const busyCount = (run) => (run && Array.isArray(run.items) ? run.items.filter(isBusy).length : 0)
export const isRunning = (run) => Boolean(run && run.state === 'running')

// What the request of a run names for a row of the page: its kind and the part
// of its id after the kind (the row id is the item key, `<kind>:<name>`).
export function requestItemOf(row) {
  const prefix = `${row.kind}:`
  const name = typeof row.id === 'string' && row.id.startsWith(prefix) ? row.id.slice(prefix.length) : row.name
  return { kind: row.kind, name }
}

const itemsOf = (run) => (run && Array.isArray(run.items) ? run.items : [])

// The counts of a run, from its items (a server that sends counts wins).
export function countsOf(run) {
  const out = { total: 0 }
  for (const p of PHASES) out[p] = 0
  if (!run) return out
  const given = run.counts && typeof run.counts === 'object' ? run.counts : null
  if (given && Number.isInteger(given.total)) {
    out.total = given.total
    for (const p of PHASES) out[p] = Number.isInteger(given[p]) ? given[p] : 0
    return out
  }
  for (const item of itemsOf(run)) {
    out.total += 1
    if (item.phase in out) out[item.phase] += 1
  }
  return out
}

// Whether an item counts as needing attention in the result of a run: it
// failed, was put back, could not be confirmed, or was skipped for a reason
// (held, edited keys, an add-on that needs the browser). A cancelled item is
// what a person asked for. The end notification of the run counts the same
// items (needsAttention in core/services/updatenotify/runs.go).
export const countsAsAttention = (item) => Boolean(item && (needsAttention(item) || (item.phase === 'skipped' && item.error_key)))

// How many items of a run need attention.
export const attentionOf = (run) => itemsOf(run).filter(countsAsAttention).length

// How far the run is, from 0 to 100: an ended item counts whole, a busy one
// by its own progress.
export function runProgress(run) {
  const items = itemsOf(run)
  if (items.length === 0) return run && run.state !== 'running' ? 100 : 0
  let sum = 0
  for (const item of items) {
    if (isEnded(item)) sum += 100
    else if (isBusy(item)) sum += Math.max(0, Math.min(100, Number(item.progress) || 0))
  }
  return Math.round(sum / items.length)
}

// The items a Retry failed would run again: only those the server marks
// retryable.
export function retryableKeys(run) {
  return itemsOf(run).filter(i => needsAttention(i) && i.retryable).map(i => i.key)
}

export function runKeys(run) {
  return new Set(itemsOf(run).map(i => i.key))
}

// ---- Requests ----------------------------------------------------------------

// The confirmation an administrator's browser sends with a start and a retry.
// The server runs add-on items only with it (they are skipped as
// not_human_surface without it); other kinds ignore it. Only the page sends
// it: an API key or a tool never does.
export const CONFIRM_HEADERS = Object.freeze({ 'X-LocalAI-Confirm': 'upgrade-run' })

// The fetch options of a start or a retry: JSON with the confirmation.
export function confirmedPost(body) {
  return { method: 'POST', headers: { 'Content-Type': 'application/json', ...CONFIRM_HEADERS }, body: JSON.stringify(body) }
}

// Whether an answer says the server keeps no update runs.
export function runsUnavailable(err) {
  return Boolean(err && (err.status === 503 || (err.body && err.body.code === 'unavailable')))
}

// ---- Review ------------------------------------------------------------------

// The groups of the review, in the order the page shows them.
export const REVIEW_GROUPS = [
  { id: 'backends', kinds: ['gallery', 'library'] },
  { id: 'addons', kinds: ['addon'] },
  { id: 'models', kinds: ['model'] },
]

export function reviewGroups(items) {
  return REVIEW_GROUPS
    .map(g => ({ id: g.id, items: (items || []).filter(i => g.kinds.includes(i.kind)) }))
    .filter(g => g.items.length > 0)
}

// The items that start selected: what can start and what the server selects by
// default (a model is not: an update reloads it).
export function defaultSelection(items) {
  return (items || []).filter(i => i.startable && i.default_selected).map(i => i.key)
}

export const startableKeys = (items) => (items || []).filter(i => i.startable).map(i => i.key)

// The body of POST /api/updates/runs: the selected items that can start, in
// the order of the plan, each pinned to what the plan says to send back
// (expected_to): the version the review showed, or for an add-on the digest
// of its candidate, whose version is only for display.
export function startItems(review) {
  if (!review || !Array.isArray(review.items)) return []
  const byKey = new Map(review.items.map(i => [i.key, i]))
  const selected = new Set(review.selected || [])
  const order = Array.isArray(review.order) && review.order.length > 0 ? review.order : review.items.map(i => i.key)
  return order
    .map(k => byKey.get(k))
    .filter(i => i && i.startable && selected.has(i.key))
    .map(i => ({ kind: i.kind, name: i.name, expected_to: i.expected_to || i.to }))
}

// The words of one risk the server named for an item (the risk of a preview
// item: { code, params }, the numbers of params as decimal strings). Every code
// the server sends has words:
//   stops_models  { count, nodes }  models loaded with the backend stop
//   cluster       { nodes }         the backend is replaced on that many nodes
//   drift         { count }         that many nodes run another build now
//   config_only                     only the model's settings change
//   migrates_data                   the add-on is backed up and may migrate
// A risk with nothing to say (no model stops) and a code this build does not
// know say nothing. `t` is the translator of the updates namespace.
export function riskText(risk, t) {
  const p = (risk && risk.params) || {}
  const num = (v) => {
    const n = Number(v)
    return Number.isFinite(n) && n > 0 ? Math.round(n) : 0
  }
  switch (risk && risk.code) {
    case 'stops_models': {
      const count = num(p.count)
      if (!count) return ''
      return num(p.nodes) > 1
        ? t('review.risk.stopsModelsNodes', { count, nodes: num(p.nodes) })
        : t('review.risk.stopsModels', { count })
    }
    case 'cluster': return num(p.nodes) > 1 ? t('review.risk.cluster', { count: num(p.nodes) }) : ''
    case 'drift': return num(p.count) ? t('review.risk.drift', { count: num(p.count) }) : ''
    case 'config_only': return t('review.risk.configOnly')
    case 'migrates_data': return t('review.risk.migratesData')
    case 'edited_keys': return t('review.risk.editedKeys', { count: num(p.count), action: t('model.action.upgrade') })
    case 'held': return t('review.risk.held')
    default: return ''
  }
}

// Whether the end of a run is to be told now: the page watched it run, it
// does not run any more, and its end was not told yet (firedFor is the id of
// the last run whose end was told).
export function endedNow(wasRunning, run, firedFor) {
  return Boolean(wasRunning && run && !isRunning(run) && run.id !== firedFor)
}

// ---- The state of the hook -----------------------------------------------------

export const initialRunState = {
  // The run the page shows: the active one, or the last one until it is dismissed.
  run: null,
  // False until the first answer, so the page does not claim "no run" early.
  loaded: false,
  // True when the server keeps no update runs (503): nothing can start.
  unavailable: false,
  // The last action that failed: the server's message and its code ('' for none).
  error: '',
  errorCode: '',
  // Which action waits for the server: start, cancel, retry, dismiss or ''.
  busy: '',
  // Ids of runs the person dismissed: a poll that was already on its way does
  // not bring one back.
  dismissed: [],
  // The number of the request whose answer is shown; an older answer is
  // dropped.
  seq: -1,
  // The open review: { status: 'loading'|'ready'|'error', single, items, order,
  // selected, error, code }, or null.
  review: null,
}

export function upgradeRunReducer(state, action) {
  switch (action.type) {
    case 'run': {
      const run = action.run || null
      // Each request numbers its answer (seq) when it is asked. An answer
      // asked before the one shown is older: a poll that left before a cancel
      // and came back after it would show the run running again.
      const seq = Number.isInteger(action.seq) ? action.seq : null
      if (seq !== null && seq < state.seq) return state
      const next = seq !== null ? seq : state.seq
      if (run && state.dismissed.includes(run.id)) return { ...state, loaded: true, seq: next }
      // A poll answers for the run it asked about; one that comes back after
      // the page moved to another run is dropped.
      if (action.from === 'poll' && (!state.run || !run || state.run.id !== run.id)) return state
      return { ...state, run, loaded: true, unavailable: false, seq: next }
    }
    // A server that cannot list runs has none to show; the page says nothing
    // about it (the next look comes with the idle poll).
    case 'loadFailed':
      return { ...state, loaded: true, unavailable: Boolean(action.unavailable) }
    case 'error':
      return { ...state, busy: '', error: action.error || '', errorCode: action.code || '' }
    case 'busy':
      return action.busy ? { ...state, busy: action.busy, error: '', errorCode: '' } : { ...state, busy: '' }
    case 'reviewOpen':
      return { ...state, review: { status: 'loading', single: Boolean(action.single), items: [], order: [], selected: [], error: '', code: '', activeRun: '' } }
    case 'reviewReady': {
      if (!state.review) return state
      const items = Array.isArray(action.preview && action.preview.items) ? action.preview.items : []
      const order = Array.isArray(action.preview && action.preview.order) ? action.preview.order : items.map(i => i.key)
      // A review of one row is the question about that row: it starts selected
      // when it can start, whatever the default of a longer list.
      const selected = state.review.single ? startableKeys(items) : defaultSelection(items)
      // A run is active elsewhere: a start would be refused, the review says so.
      const activeRun = action.preview && action.preview.active_run_id ? String(action.preview.active_run_id) : ''
      return { ...state, review: { ...state.review, status: 'ready', items, order, selected, activeRun } }
    }
    case 'reviewFailed':
      if (!state.review) return state
      return {
        ...state,
        unavailable: state.unavailable || action.code === 'unavailable',
        review: { ...state.review, status: 'error', error: action.error || '', code: action.code || '' },
      }
    case 'toggle': {
      if (!state.review) return state
      const item = state.review.items.find(i => i.key === action.key)
      if (!item || !item.startable) return state
      const has = state.review.selected.includes(action.key)
      const selected = has ? state.review.selected.filter(k => k !== action.key) : [...state.review.selected, action.key]
      return { ...state, review: { ...state.review, selected, error: '', code: '' } }
    }
    case 'selectAll':
      if (!state.review) return state
      return { ...state, review: { ...state.review, selected: startableKeys(state.review.items), error: '', code: '' } }
    case 'selectNone':
      if (!state.review) return state
      return { ...state, review: { ...state.review, selected: [], error: '', code: '' } }
    case 'reviewClose':
      return { ...state, review: null }
    case 'startFailed':
      return { ...state, busy: '', unavailable: state.unavailable || action.code === 'unavailable', review: state.review ? { ...state.review, error: action.error || '', code: action.code || '' } : null }
    case 'started':
      return { ...state, busy: '', error: '', errorCode: '', review: null, run: action.run || state.run, loaded: true,
        seq: Number.isInteger(action.seq) ? Math.max(action.seq, state.seq) : state.seq }
    case 'dismissed': {
      const id = state.run ? state.run.id : action.id
      return { ...state, busy: '', error: '', errorCode: '', run: null, dismissed: id ? [...state.dismissed, id] : state.dismissed }
    }
    default:
      return state
  }
}
