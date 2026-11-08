// Runs the update of one row and says when it is really done. The browser
// never counts a dispatch as done: the start request only says the server took
// the job. An update is done when
//   1. the operation ended without an error (it left /api/operations, or it
//      sits there with an error and the update failed), and
//   2. a fresh check no longer lists the item.
//
// /api/backends/job/{id} is not used here: it deletes the operation from the
// cache the first time it reports "completed", which would hide the end from
// the Operations list and the Activity history. The list is already polled
// once a second for the whole shell (OperationsContext), so this reads it.
//
// Everything with a clock or a network call is injected, so the node unit
// tests run it with fakes.
import { trackJob, verdictOf, addonStillListed } from './updates.js'
import { nodeConfirmed } from './nodeUpdates.js'

const defaultSleep = (ms, signal) => new Promise((resolve, reject) => {
  const timer = setTimeout(resolve, ms)
  if (signal) {
    signal.addEventListener('abort', () => { clearTimeout(timer); reject(new DOMException('Aborted', 'AbortError')) }, { once: true })
  }
})

// Waits for the operation of `jobID` to end. Resolves { verdict: 'ended' } or
// { verdict: 'failed', error }.
export async function followJob({ jobID, getOperations, now = Date.now, sleep = defaultSleep, signal, pollMs = 500, graceMs = 4000, timeoutMs = 6 * 60 * 60 * 1000 }) {
  let track = { seen: false, since: now() }
  const deadline = now() + timeoutMs
  for (;;) {
    const out = trackJob(track, getOperations(), jobID, now(), graceMs)
    track = out.track
    if (out.verdict !== 'wait') return out
    if (now() > deadline) return { verdict: 'failed', error: 'timeout' }
    await sleep(pollMs, signal)
  }
}

// Asks for fresh checks until the answer decides. `recheck()` resolves the new
// read model. A throttled answer may predate the update, so it is asked again
// after `retryMs`.
export async function confirmGone({ row, recheck, sleep = defaultSleep, signal, retryMs = 5000, maxTries = 4 }) {
  for (let tries = 0; ; tries += 1) {
    let snapshot = null
    try { snapshot = await recheck() } catch { /* an unanswered check cannot confirm */ }
    const source = snapshot && snapshot.sources && snapshot.sources[row.kind]
    const verdict = verdictOf({
      listed: Boolean(snapshot && Array.isArray(snapshot.items) && snapshot.items.some(i => i.id === row.id)),
      throttled: Boolean(snapshot && snapshot.throttled),
      sourceFailed: !snapshot || !source || source.state === 'failed' || source.state === 'never',
      tries,
      maxTries,
    })
    if (verdict !== 'retry') return verdict
    await sleep(retryMs, signal)
  }
}

// Updates one gallery backend. `start()` posts the update and resolves the
// job id. Resolves { ok: true } or { ok: false, code, error }, where code is
// start (the request was refused), failed (the operation failed), unchanged
// (the operation ended and the item is still listed) or unconfirmed (no source
// answered after it).
export async function runGalleryRow({ row, start, getOperations, recheck, sleep, now, signal, retryMs, maxTries }) {
  let jobID
  try {
    jobID = await start(row)
  } catch (e) {
    return { ok: false, code: 'start', error: e && e.message ? String(e.message) : '' }
  }
  const end = await followJob({ jobID, getOperations, sleep, now, signal })
  if (end.verdict === 'failed') return { ok: false, code: 'failed', error: end.error || '' }
  const verdict = await confirmGone({ row, recheck, sleep, signal, retryMs, maxTries })
  if (verdict === 'done') return { ok: true }
  return { ok: false, code: verdict }
}

// Updates one library backend. `install()` follows the host's install job to
// its end and throws when it failed (the add-on client does that), so the end
// of the operation is its own; the check decides the rest.
export async function runLibraryRow({ row, install, recheck, sleep, signal, retryMs, maxTries }) {
  try {
    await install(row)
  } catch (e) {
    return { ok: false, code: 'failed', error: e && e.message ? String(e.message) : '' }
  }
  const verdict = await confirmGone({ row, recheck, sleep, signal, retryMs, maxTries })
  if (verdict === 'done') return { ok: true }
  return { ok: false, code: verdict }
}

// Updates one add-on. `upgrade(record)` resolves { ok, message } when the
// upgrade store saw the owner's operation end (and the installed generation
// move). `reread()` resolves the fresh installed records. The row is done when
// the record no longer carries the update the row showed.
export async function runAddonRow({ row, upgrade, reread }) {
  const out = await upgrade(row.record)
  if (!out || !out.ok) return { ok: false, code: 'failed', error: out && out.message ? out.message : '', errorKey: out && out.message ? out.message : '' }
  let records
  try {
    records = await reread()
  } catch {
    return { ok: false, code: 'unconfirmed' }
  }
  const now = (records || []).find(r => r.id === row.addonId)
  if (addonStillListed(now, row)) return { ok: false, code: 'unchanged' }
  return { ok: true }
}

// Asks for fresh checks until the answer decides for one node. A throttled
// answer that still shows the node behind may predate the update, so it is
// asked again after `retryMs`.
export async function confirmNodeUpdated({ entry, recheck, sleep = defaultSleep, signal, retryMs = 5000, maxTries = 4 }) {
  for (let tries = 0; ; tries += 1) {
    let snapshot = null
    try { snapshot = await recheck() } catch { /* an unanswered check cannot confirm */ }
    const verdict = nodeConfirmed(entry, snapshot)
    if (verdict === 'unchanged' && snapshot.throttled && tries < maxTries) {
      await sleep(retryMs, signal)
      continue
    }
    return verdict
  }
}

// Updates one backend on one node. `start(entry)` posts the node's update and
// resolves the job id (the answer is 202: the node has not changed yet). It
// is done when the operation ended without an error and a fresh check shows
// the node on the new build. Resolves { ok: true } or { ok: false, code,
// error } with the codes of runGalleryRow.
export async function runNodeRow({ entry, start, getOperations, recheck, sleep, now, signal, retryMs, maxTries }) {
  let jobID
  try {
    jobID = await start(entry)
  } catch (e) {
    return { ok: false, code: 'start', error: e && e.message ? String(e.message) : '' }
  }
  const end = await followJob({ jobID, getOperations, sleep, now, signal })
  if (end.verdict === 'failed') return { ok: false, code: 'failed', error: end.error || '' }
  const verdict = await confirmNodeUpdated({ entry, recheck, sleep, signal, retryMs, maxTries })
  if (verdict === 'done') return { ok: true }
  return { ok: false, code: verdict }
}
