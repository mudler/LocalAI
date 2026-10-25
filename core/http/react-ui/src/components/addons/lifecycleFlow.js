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

// Reads the status of `operationId` until it is completed or failed. Returns
// { outcome: 'completed'|'failed'|'unknown', phase }. `unknown` means the
// bounded reads ended without a final phase; the identifier is still the one to
// ask about later.
export async function settleOperation(api, { operationId, addonId, signal, waits = DEFAULT_WAITS, sleep = defaultSleep }) {
  let last = ''
  for (let i = 0; i <= waits.length; i += 1) {
    try {
      const status = await api.operationStatus(operationId, addonId, signal)
      last = status.phase
      if (isFinalPhase(status.phase)) return { outcome: status.phase, phase: status.phase }
    } catch (e) {
      if (e && e.name === 'AbortError') throw e
      // A denial is final; anything else is a lost read and is tried again.
      if (e instanceof AddonsError && (e.kind === 'denied' || e.kind === 'auth')) throw e
    }
    if (i < waits.length) await sleep(waits[i], signal)
  }
  return { outcome: 'unknown', phase: last }
}

// Run one action. `submit()` performs the single write with the action's
// operation identity. Resolves { outcome, phase }; throws an AddonsError for a
// definite refusal (denied, invalid, auth) or an unavailable owner. After an
// unknown outcome the same operation is read, never submitted again.
export async function runLifecycle(api, { operationId, addonId, submit, signal, waits, sleep }) {
  try {
    const status = await submit()
    if (status && status.phase && !isFinalPhase(status.phase)) {
      return settleOperation(api, { operationId, addonId, signal, waits, sleep })
    }
    return { outcome: status && status.phase === 'failed' ? 'failed' : 'completed', phase: status?.phase || 'completed' }
  } catch (e) {
    if (e instanceof AddonsError && e.kind === 'unknown_outcome') {
      return settleOperation(api, { operationId: e.operationId || operationId, addonId, signal, waits, sleep })
    }
    throw e
  }
}
