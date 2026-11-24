// Shared reading of a gallery job status for manual model imports.
//
// The raw /models/jobs endpoint reports `processed` (set for finished,
// failed and cancelled jobs alike), while the /api/models/job shim
// synthesizes `completed`. Failures arrive as `error` (string or object)
// or as a `message` that starts with "error:". Every import screen must
// read them the same way, so the rules live here.

export const IMPORT_POLL_INTERVAL_MS = 1000
// Give up on a status endpoint that keeps failing: a job the server has
// reaped or never knew answers with an error every time.
export const IMPORT_MAX_CONSECUTIVE_FAILURES = 10
// Upper bound on one wait. The download itself keeps running server side.
export const IMPORT_MAX_WAIT_MS = 2 * 60 * 60 * 1000

// classifyImportJob maps one status payload to
// { state: 'running' | 'done' | 'failed', message }.
// Failure is checked before completion because a failed job also sets
// `processed`.
export function classifyImportJob(data) {
  if (data?.cancelled) return { state: 'failed', message: 'Import cancelled' }
  const hasErrorMessage = typeof data?.message === 'string' && data.message.startsWith('error:')
  if (data?.error || hasErrorMessage) {
    let msg = 'Unknown error'
    if (typeof data.error === 'string') msg = data.error
    else if (data.error?.message) msg = data.error.message
    else if (data.message) msg = String(data.message)
    if (msg.startsWith('error: ')) msg = msg.substring(7)
    return { state: 'failed', message: msg }
  }
  if (data?.completed || data?.processed) {
    return { state: 'done', message: '', name: data.gallery_element_name || '' }
  }
  return { state: 'running', message: '' }
}

function sleep(ms, signal) {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) { reject(new DOMException('Aborted', 'AbortError')); return }
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort)
      resolve()
    }, ms)
    const onAbort = () => {
      clearTimeout(timer)
      reject(new DOMException('Aborted', 'AbortError'))
    }
    signal?.addEventListener('abort', onAbort, { once: true })
  })
}

// waitForImportJob polls getStatus(jobId) until the job ends. It resolves
// with the classified status on success and rejects with an Error when the
// job failed, was cancelled, the status endpoint kept failing, or the wait
// limit passed. An aborted signal rejects with an AbortError, which callers
// should treat as "the screen went away" and not show.
export async function waitForImportJob(jobId, {
  getStatus,
  signal,
  intervalMs = IMPORT_POLL_INTERVAL_MS,
  maxConsecutiveFailures = IMPORT_MAX_CONSECUTIVE_FAILURES,
  maxWaitMs = IMPORT_MAX_WAIT_MS,
  now = Date.now,
} = {}) {
  const startedAt = now()
  let failures = 0
  for (;;) {
    await sleep(intervalMs, signal)
    let data
    try {
      data = await getStatus(jobId)
      failures = 0
    } catch (err) {
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError')
      failures += 1
      if (failures >= maxConsecutiveFailures) {
        throw new Error(`Lost track of the import job: ${err?.message || 'status check failed'}`)
      }
      if (now() - startedAt >= maxWaitMs) {
        throw new Error('Import is still running after waiting too long; check the models list later')
      }
      continue
    }
    const status = classifyImportJob(data)
    if (status.state === 'failed') throw new Error(status.message)
    if (status.state === 'done') return status
    if (now() - startedAt >= maxWaitMs) {
      throw new Error('Import is still running after waiting too long; check the models list later')
    }
  }
}
