// The session as a small state machine, so the provider and its tests share
// one set of rules.
//
//   loading      the first status read has not answered
//   unreachable  the status read failed (network, non-JSON, 5xx). It says
//                nothing about the server's auth mode, so it is never read as
//                "auth is off"
//   anonymous    the status answered and nobody is signed in (or auth is off)
//   signed-in    the status answered with a user
//   expired      a signed-in person got a 401 from the API
//
// Events: status-ok {data}, status-failed, retry, unauthorized {url}, logout.
// A step that changes nothing returns the same object, so callers can compare
// by identity (a second 401 of a parallel burst is such a step).
export const RETRY_BASE_MS = 2000
export const RETRY_CAP_MS = 30000

export const initialSession = Object.freeze({
  status: 'loading',
  sessionEpoch: 0,
  authEnabled: false,
  staticApiKeyRequired: false,
  user: null,
  permissions: {},
  failures: 0,
  retrying: false,
  ended: false,
})

// Wait before the next automatic retry, after `failures` failed reads.
export function retryDelay(failures) {
  const n = Math.max(1, failures)
  return Math.min(RETRY_CAP_MS, RETRY_BASE_MS * 2 ** (n - 1))
}

// Calls whose 401 is part of their job: signing in or up, reading the status,
// signing out, and the public branding read.
const EXEMPT = /\/api\/(auth\/(status|login|register|token-login|logout)|branding)(\/|\?|$)/

export function isSessionExempt(url) {
  if (!url) return false
  let path = String(url)
  try { path = new URL(path, 'http://localhost').pathname } catch { /* keep as is */ }
  return EXEMPT.test(path)
}

export function sessionStep(state, event) {
  switch (event.type) {
    case 'status-ok': {
      const data = event.data || {}
      const user = data.user || null
      return {
        ...state,
        status: user ? 'signed-in' : 'anonymous',
        sessionEpoch: state.sessionEpoch + 1,
        authEnabled: data.authEnabled || false,
        staticApiKeyRequired: data.staticApiKeyRequired || false,
        user,
        permissions: user?.permissions || {},
        failures: 0,
        retrying: false,
        ended: false,
      }
    }
    case 'status-failed':
      // A refresh that fails must not sign out someone who is signed in, and a
      // late failure after logout is noise.
      if (state.status === 'signed-in' || state.ended) return state
      return {
        ...state,
        status: 'unreachable',
        sessionEpoch: state.sessionEpoch + 1,
        user: null,
        permissions: {},
        failures: state.failures + 1,
        retrying: false,
      }
    case 'retry':
      if (state.status !== 'unreachable' || state.retrying) return state
      return { ...state, retrying: true }
    case 'unauthorized':
      if (state.status !== 'signed-in' || !state.authEnabled || isSessionExempt(event.url)) return state
      return { ...state, status: 'expired', sessionEpoch: state.sessionEpoch + 1, user: null, permissions: {} }
    case 'logout':
      return { ...state, status: 'anonymous', sessionEpoch: state.sessionEpoch + 1, user: null, permissions: {}, failures: 0, retrying: false, ended: true }
    default:
      return state
  }
}
