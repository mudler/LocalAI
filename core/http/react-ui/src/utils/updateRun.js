// Runs the update of a backend on one node, for the node dialog, and says when
// it is really done. Every other update is an update run that the server owns
// (utils/upgradeRun.js). The browser never counts a dispatch as done: the start
// request only says the server took the job. A node is done when
//   1. the operation ended without an error (it left /api/operations, or it
//      sits there with an error and the update failed), and
//   2. a fresh check shows the node on the new build.
//
// /api/backends/job/{id} is not used here: it deletes the operation from the
// cache the first time it reports "completed", which would hide the end from
// the Operations list and the Activity history. The list is already polled
// once a second for the whole shell (OperationsContext), so this reads it.
//
// Everything with a clock or a network call is injected, so the node unit
// tests run it with fakes.
import { trackJob } from './updates.js'
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
// error } where code is start (the request was refused), failed (the operation
// failed), unchanged (the node still shows the old build) or unconfirmed (no
// source answered after it).
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
