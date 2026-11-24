// The "View as" state of one tab, and the one place that applies it to
// requests. The rules are in viewAs.js and the list of routes in
// viewAsRoutes.js; this file keeps the state, the session storage and the idle
// timer, and wraps fetch.
//
// Why a wrapper around fetch and not a change in each api.js helper: the pages
// reach the server through fetchJSON, through bare fetch calls (uploads,
// downloads) and through add-on code. One wrapper sees every one of them, so no
// call can forget the mode. The wrapper touches only the routes of
// viewAsRoutes.js.
//
// This is a safety rail in the browser, not a security boundary. The server
// accepts a write with ?user_id= from any administrator; anyone with a console
// can send one. The docs page says so.
import {
  decideRequest, endsTheMode, idleDeadline, initialViewAs, persisted, restored, viewAsStep,
} from './viewAs.js'
import { USER_ID_PARAM } from './viewAsRoutes.js'

export const STORAGE_KEY = 'localai.viewAs'

function defaultStorage() {
  try { return window.sessionStorage } catch { return null }
}

// store.getState() is the state of viewAs.js. Every change goes through
// dispatch, so the storage and the idle timer cannot drift from it.
// A timer that does not keep a node process alive; a browser timer is a number.
function idleTimer(fn, ms) {
  const t = setTimeout(fn, ms)
  if (typeof t === 'object') t.unref?.()
  return t
}

export function createViewAsStore({ storage = defaultStorage(), now = Date.now, setTimer = idleTimer, clearTimer = clearTimeout } = {}) {
  let state = initialViewAs
  let timer = null
  const listeners = new Set()
  const blockedListeners = new Set()

  const save = () => {
    try {
      const raw = persisted(state)
      if (raw) storage?.setItem(STORAGE_KEY, raw)
      else storage?.removeItem(STORAGE_KEY)
    } catch { /* storage blocked: the mode lives for this page only */ }
  }

  const arm = () => {
    if (timer) clearTimer(timer)
    timer = null
    const deadline = idleDeadline(state)
    if (deadline !== null) timer = setTimer(() => store.dispatch({ type: 'tick' }), Math.max(0, deadline - now()))
  }

  const store = {
    getState: () => state,
    subscribe(fn) { listeners.add(fn); return () => listeners.delete(fn) },
    // Called when the wrapper stops a request in read-only mode.
    onBlocked(fn) { blockedListeners.add(fn); return () => blockedListeners.delete(fn) },
    notifyBlocked(info) { blockedListeners.forEach(fn => fn(info)) },
    dispatch(event) {
      const next = viewAsStep(state, { now: now(), ...event })
      if (next === state) return
      state = next
      save()
      arm()
      listeners.forEach(fn => fn())
    },
    enter: (target) => store.dispatch({ type: 'enter', target }),
    allow: () => store.dispatch({ type: 'allow' }),
    revoke: () => store.dispatch({ type: 'revoke' }),
    exit: () => store.dispatch({ type: 'exit' }),
    gone: () => store.dispatch({ type: 'gone' }),
    clear: () => store.dispatch({ type: 'clear' }),
    // Input while acting keeps the session alive. Cheap to call often.
    input: () => { if (state.mode === 'actas') store.dispatch({ type: 'input' }) },
    tick: () => store.dispatch({ type: 'tick' }),
  }

  // A reload brings the target back, read-only.
  try {
    const target = restored(storage?.getItem(STORAGE_KEY))
    if (target) state = viewAsStep(state, { type: 'restore', target })
  } catch { /* storage blocked */ }

  return store
}

export const viewAsStore = createViewAsStore()


function pathOf(url, origin, base) {
  const u = new URL(url, origin)
  if (u.origin !== origin) return null
  const p = base && u.pathname.startsWith(`${base}/`) ? u.pathname.slice(base.length) : u.pathname
  return { url: u, path: p }
}

// Wraps fetch once. `options.message(name)` words the error of a blocked
// write; it defaults to English so a test needs no translations.
export function installViewAsFetch(store, { win = window, base = '', message = (name) => `Read-only: changes to ${name}'s data are off.` } = {}) {
  if (win.__viewAsInstalled) return
  win.__viewAsInstalled = true
  const real = win.fetch.bind(win)
  win.fetch = async function viewAsFetch(input, init) {
    store.tick()
    const state = store.getState()
    if (!state.target) return real(input, init)
    const isRequest = typeof Request !== 'undefined' && input instanceof Request
    const raw = isRequest ? input.url : String(input instanceof URL ? input.href : input)
    let where
    try { where = pathOf(raw, win.location.origin, base) } catch { where = null }
    if (!where) return real(input, init)
    const method = (init && init.method) || (isRequest ? input.method : 'GET')
    const decision = decideRequest(state, method, where.path, where.url.search)
    if (decision.blocked) {
      const text = message(state.target.name)
      store.notifyBlocked({ method, path: where.path, name: state.target.name })
      const err = new Error(text)
      err.status = 0
      err.viewAsBlocked = true
      throw err
    }
    let sent = input
    if (decision.follow) {
      where.url.searchParams.set(USER_ID_PARAM, state.target.id)
      const next = where.url.pathname + where.url.search
      const target = raw.startsWith('http') ? where.url.href : next
      sent = isRequest ? new Request(target, input) : target
    }
    const response = await real(sent, init)
    if (decision.follow && !response.ok && (response.status === 403 || response.status === 404)) {
      let text = ''
      if (response.status === 404) {
        try { text = (await response.clone().json())?.error?.message || '' } catch { /* not JSON */ }
      }
      if (endsTheMode(response.status, text)) store.gone()
    }
    return response
  }
}

