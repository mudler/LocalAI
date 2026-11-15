// State machine for polling one background job (for example a model import).
//
// The page used to keep only an interval and a log line: when the status
// endpoint kept failing, nothing ever ended the poll and the form stayed
// locked. This reducer gives the poll a terminal path in every case:
//
//   polling --ok------------> polling   (failure count and clock reset)
//   polling --error---------> polling   (until MAX_FAILURES in a row)
//   polling --error x N-----> gave-up
//   polling --tick, silent too long--> gave-up
//   polling --done----------> done
//   polling --failed--------> failed
//
// done, failed and gave-up are terminal: later events change nothing, so a
// late response cannot reopen a finished poll. A tick while a request is
// still in flight is skipped (`canStart` is false), so requests never
// overlap.

export const MAX_FAILURES = 5
// Longest time without one successful response before the poll gives up. It
// also covers a request that hangs, which never reports an error of its own.
export const SILENCE_CAP_MS = 2 * 60 * 1000

export function newPoll(now) {
  return { phase: 'polling', inFlight: false, failures: 0, lastOkAt: now }
}

export function isTerminal(state) {
  return state.phase !== 'polling'
}

// Events: { type: 'tick' | 'ok' | 'error' | 'done' | 'failed', now }
export function pollStep(state, event) {
  if (isTerminal(state)) return state
  switch (event.type) {
    case 'tick':
      if (event.now - state.lastOkAt > SILENCE_CAP_MS) {
        return { ...state, phase: 'gave-up', inFlight: false }
      }
      return state.inFlight ? state : { ...state, inFlight: true }
    case 'ok':
      return { ...state, inFlight: false, failures: 0, lastOkAt: event.now }
    case 'error': {
      const failures = state.failures + 1
      return {
        ...state,
        inFlight: false,
        failures,
        phase: failures >= MAX_FAILURES ? 'gave-up' : 'polling',
      }
    }
    case 'done':
      return { ...state, phase: 'done', inFlight: false }
    case 'failed':
      return { ...state, phase: 'failed', inFlight: false }
    default:
      return state
  }
}

// True when the step moved from idle to a request in flight, that is, the
// caller should send exactly one request now.
export function canStart(before, after) {
  return !before.inFlight && after.inFlight
}
