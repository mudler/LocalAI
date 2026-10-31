// The state of an "upgrade all" run, as pure functions so the node unit tests
// drive them without a browser. It knows nothing about add-ons or backends: an
// item is { id, name, from, to, startable, reason } and the caller supplies the
// function that upgrades one of them.
//
// A run is { phase: 'running'|'finished', order, items, status } where
// status[id] is { phase: 'queued'|'upgrading'|'done'|'failed'|'skipped', message }.

// The items a person may pick: only the ones that can start.
export const isStartable = (item) => Boolean(item && item.startable)

export function initialSelection(items) {
  return new Set(items.filter(isStartable).map(i => i.id))
}

export const selectAllItems = initialSelection

export function toggleItem(selected, items, id) {
  const item = items.find(i => i.id === id)
  const next = new Set(selected)
  if (!isStartable(item)) return next
  if (next.has(id)) next.delete(id)
  else next.add(id)
  return next
}

// The ids that would run: selected, startable, in the order of the list.
function runnable(items, selected) {
  return items.filter(i => isStartable(i) && selected.has(i.id)).map(i => i.id)
}

export function canStart(run, items, selected) {
  if (run && run.phase === 'running') return false
  return runnable(items, selected).length > 0
}

// Begins a run, or returns null when one is already running or nothing is
// selected. That null is the guard against a double start. A run that finished
// is replaced: a retry is a new run over the failed items.
export function beginRun(run, items, selected) {
  if (!canStart(run, items, selected)) return null
  const order = runnable(items, selected)
  const status = {}
  for (const id of order) status[id] = { phase: 'queued', message: '' }
  return { phase: 'running', order, items: items.filter(i => order.includes(i.id)), status }
}

const mark = (run, id, patch) => ({ ...run, status: { ...run.status, [id]: { ...run.status[id], ...patch } } })

export function summarize(run) {
  const out = { total: 0, done: 0, failed: 0, skipped: 0, queued: 0, upgrading: 0 }
  if (!run) return out
  for (const id of run.order) {
    out.total += 1
    const p = run.status[id] && run.status[id].phase
    if (p in out) out[p] += 1
  }
  return out
}

export function failedIds(run) {
  return run ? run.order.filter(id => run.status[id].phase === 'failed') : []
}

// Runs the items one after another, never in parallel. `runOne(item, signal)`
// resolves { ok: true } or { ok: false, message, skipped }; a throw is a
// failure of that item only. After an abort the rest are skipped. `onUpdate`
// gets every new state of the run.
export async function runSequential(run, runOne, { signal, onUpdate } = {}) {
  let cur = run
  const push = (next) => { cur = next; if (onUpdate) onUpdate(cur) }
  for (const id of run.order) {
    if (signal && signal.aborted) {
      push(mark(cur, id, { phase: 'skipped' }))
      continue
    }
    push(mark(cur, id, { phase: 'upgrading' }))
    const item = run.items.find(i => i.id === id)
    let phase = 'done'
    let message = ''
    try {
      const out = await runOne(item, signal)
      if (!out || !out.ok) {
        phase = out && out.skipped ? 'skipped' : 'failed'
        message = (out && out.message) || ''
      }
    } catch (e) {
      phase = 'failed'
      message = e && e.message ? String(e.message) : ''
    }
    push(mark(cur, id, { phase, message }))
  }
  push({ ...cur, phase: 'finished' })
  return cur
}
