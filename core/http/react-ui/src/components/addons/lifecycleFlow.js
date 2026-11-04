// The lifecycle of one user action: submit once with one operation identity,
// and when the answer is lost, read that same operation until it settles.
// Never submits a second operation for the same action.
import { AddonsError, isFinalPhase } from '../../utils/addonsApi.js'

const DEFAULT_WAITS = [500, 1000, 2000, 4000, 8000]

const defaultSleep = (ms, signal) => new Promise((resolve, reject) => {
  const timer = setTimeout(resolve, ms)
  if (signal) {
    signal.addEventListener('abort', () => {
      clearTimeout(timer)
      reject(new DOMException('Aborted', 'AbortError'))
    }, { once: true })
  }
})

// While the add-on service does not answer (it starts, or restarts after an
// upgrade), the reads back off: the wait doubles from UNAVAILABLE_BASE_MS up to
// UNAVAILABLE_MAX_MS, so a page that follows an operation does not ask every
// two seconds for minutes.
export const UNAVAILABLE_BASE_MS = 2000
export const UNAVAILABLE_MAX_MS = 30000

// unavailableWait is the wait after the n-th read in a row that found the
// service unavailable (n from 1), never shorter than the planned wait.
export function unavailableWait(n, planned = 0) {
  const backoff = Math.min(UNAVAILABLE_BASE_MS * 2 ** Math.max(0, n - 1), UNAVAILABLE_MAX_MS)
  return Math.max(planned, backoff)
}

const isUnavailable = (e) => e instanceof AddonsError && (e.kind === 'unavailable' || e.kind === 'network')

// Reads the status of `operationId` until it is completed or failed. Returns
// { outcome: 'completed'|'failed'|'unknown', phase }. `unknown` means the
// bounded reads ended without a final phase; the identifier is still the one to
// ask about later. onWait, when given, hears { unavailable, waitMs } before
// each wait: unavailable is true while the service does not answer.
export async function settleOperation(api, { operationId, addonId, signal, waits = DEFAULT_WAITS, sleep = defaultSleep, onWait }) {
  let last = ''
  let down = 0
  for (let i = 0; i <= waits.length; i += 1) {
    try {
      const status = await api.operationStatus(operationId, addonId, signal)
      last = status.phase
      down = 0
      if (isFinalPhase(status.phase)) return { outcome: status.phase, phase: status.phase }
    } catch (e) {
      if (e && e.name === 'AbortError') throw e
      // A denial is final; anything else is a lost read and is tried again.
      if (e instanceof AddonsError && (e.kind === 'denied' || e.kind === 'auth')) throw e
      down = isUnavailable(e) ? down + 1 : 0
    }
    if (i < waits.length) {
      const waitMs = down > 0 ? unavailableWait(down, waits[i]) : waits[i]
      onWait?.({ unavailable: down > 0, waitMs })
      await sleep(waitMs, signal)
    }
  }
  return { outcome: 'unknown', phase: last }
}

// Run one action. `submit()` performs the single write with the action's
// operation identity. Resolves { outcome, phase }; throws an AddonsError for a
// definite refusal (denied, invalid, auth) or an unavailable owner. After an
// unknown outcome the same operation is read, never submitted again.
export async function runLifecycle(api, { operationId, addonId, submit, signal, waits, sleep, onWait }) {
  try {
    const status = await submit()
    if (status && status.phase && !isFinalPhase(status.phase)) {
      return settleOperation(api, { operationId, addonId, signal, waits, sleep, onWait })
    }
    return { outcome: status && status.phase === 'failed' ? 'failed' : 'completed', phase: status?.phase || 'completed' }
  } catch (e) {
    if (e instanceof AddonsError && e.kind === 'unknown_outcome') {
      return settleOperation(api, { operationId: e.operationId || operationId, addonId, signal, waits, sleep, onWait })
    }
    throw e
  }
}
