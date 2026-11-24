// The rules of "View as": an administrator looks at, and with a second step
// acts on, another user's data. Pure functions over one small state, so a node
// test drives every transition. The store (viewAsStore.js) keeps the state,
// the session storage and the timers; this file has no browser in it.
//
// The mode is a client-side safety rail, not a security boundary: the server
// accepts a write with ?user_id= from any administrator whatever the UI shows.
import { USER_ID_PARAM, followsApi } from './viewAsRoutes.js'

export const READONLY = 'readonly'
export const ACTAS = 'actas'

// Act-as returns to read-only after this long without input.
export const IDLE_MS = 15 * 60 * 1000

// target is { id, name } or null. mode is READONLY or ACTAS. lastInput is the
// time of the last input while acting. ended says why the mode stopped on its
// own ({ reason: 'gone', name }); reverted says why acting stopped on its own
// ('idle'). Both are read once by the shell, then cleared.
export const initialViewAs = Object.freeze({ target: null, mode: READONLY, lastInput: 0, ended: null, reverted: null })

const clean = (target) => (target && typeof target.id === 'string' && target.id
  ? { id: target.id, name: String(target.name || target.id) }
  : null)

// Events:
//   enter    { target, now }   start viewing a user, read-only
//   restore  { target }        a reload: the target comes back, read-only
//   allow    { now }           act as the target (the person confirmed)
//   revoke   {}                back to read-only
//   input    { now }           the person did something while acting
//   tick     { now }           time passed
//   exit     {}                end the mode
//   gone     {}                the server says the viewed user is not there
//   clear    {}                the shell has shown ended and reverted
export function viewAsStep(state, event) {
  switch (event.type) {
    case 'enter': {
      const target = clean(event.target)
      return target ? { ...initialViewAs, target } : state
    }
    case 'restore': {
      const target = clean(event.target)
      return target ? { ...initialViewAs, target } : state
    }
    case 'allow':
      return state.target ? { ...state, mode: ACTAS, lastInput: event.now, reverted: null } : state
    case 'revoke':
      if (!state.target || (state.mode === READONLY && !state.reverted)) return state
      return { ...state, mode: READONLY, lastInput: 0, reverted: null }
    case 'input':
      return state.mode === ACTAS ? { ...state, lastInput: event.now } : state
    case 'tick':
      return state.mode === ACTAS && event.now - state.lastInput >= IDLE_MS
        ? { ...state, mode: READONLY, lastInput: 0, reverted: 'idle' }
        : state
    case 'exit':
      return initialViewAs
    case 'gone':
      return state.target ? { ...initialViewAs, ended: { reason: 'gone', name: state.target.name } } : state
    case 'clear':
      return state.ended || state.reverted ? { ...state, ended: null, reverted: null } : state
    default:
      return state
  }
}

// When an acting session would expire, or null when nothing can expire.
export function idleDeadline(state) {
  return state.mode === ACTAS ? state.lastInput + IDLE_MS : null
}

export const isActive = (state) => Boolean(state.target)
export const isActing = (state) => Boolean(state.target) && state.mode === ACTAS

// The target kept for a reload. The mode is never kept: a reload is read-only.
export function persisted(state) {
  return state.target ? JSON.stringify(state.target) : ''
}

export function restored(raw) {
  if (!raw) return null
  try { return clean(JSON.parse(raw)) } catch { return null }
}

const SAFE = new Set(['GET', 'HEAD', 'OPTIONS'])

// What to do with one request. `path` has no base path and no query.
//   { follow: false }                  not a following route: untouched
//   { follow: true, blocked: true }    read-only and not a read: stop it here
//   { follow: true, blocked: false }   add user_id
// A request that already names a user_id keeps it: a page that picks a user
// itself (the Collections page does) knows better than the mode.
export function decideRequest(state, method, path, search = '') {
  if (!state.target || !followsApi(path)) return { follow: false, blocked: false }
  const own = new URLSearchParams(search).has(USER_ID_PARAM)
  const blocked = state.mode === READONLY && !SAFE.has(String(method || 'GET').toUpperCase())
  return { follow: !own, blocked }
}

// The server's answer when ?user_id= names nobody (projectOwner in
// core/http/endpoints/studio/projects.go). A 404 on a following route is also
// the answer for a project that does not exist, so a 404 ends the mode only
// when it says this.
export const USER_NOT_FOUND = 'user not found'

// Whether an answer to a following route ends the mode: the viewed user can no
// longer be acted for. 403 is the answer for a caller who is no longer an
// administrator; 404 with the text above is the answer for a deleted user.
export function endsTheMode(status, message = '') {
  return status === 403 || (status === 404 && message === USER_NOT_FOUND)
}
