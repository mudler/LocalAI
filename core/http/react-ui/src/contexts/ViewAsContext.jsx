import { createContext, useCallback, useContext, useEffect, useMemo, useSyncExternalStore } from 'react'
import { useAuth } from '../context/AuthContext'
import { viewAsStore } from '../utils/viewAsStore'
import { ACTAS, isActing, isActive } from '../utils/viewAs'

// The "View as" state of this tab, for the app shell and the pages. The rules
// are in utils/viewAs.js; the store applies them to requests. Only an
// administrator of a server with sign-in can use it.
const ViewAsContext = createContext(null)

// Input while acting keeps the session alive; one write a second is plenty.
const INPUT_EVENTS = ['pointerdown', 'keydown']
const INPUT_THROTTLE_MS = 1000

export function ViewAsProvider({ children, store = viewAsStore }) {
  const { status, authEnabled, user } = useAuth()
  const state = useSyncExternalStore(store.subscribe, store.getState)
  const canViewAs = Boolean(authEnabled && user?.role === 'admin')
  // Once the session is known, anyone who cannot use the mode leaves it: a
  // signed-out tab, a person who lost the role, a server with sign-in off.
  const settled = status !== 'loading' && status !== 'unreachable'
  useEffect(() => { if (settled && !canViewAs) store.exit() }, [settled, canViewAs, store])

  const acting = state.mode === ACTAS && Boolean(state.target)
  useEffect(() => {
    if (!acting) return undefined
    let last = 0
    const onInput = () => {
      const now = Date.now()
      if (now - last < INPUT_THROTTLE_MS) return
      last = now
      store.input()
    }
    INPUT_EVENTS.forEach(e => window.addEventListener(e, onInput, true))
    return () => INPUT_EVENTS.forEach(e => window.removeEventListener(e, onInput, true))
  }, [acting, store])

  const enter = useCallback((target) => store.enter(target), [store])
  const value = useMemo(() => ({
    canViewAs,
    target: canViewAs ? state.target : null,
    mode: state.mode,
    active: canViewAs && isActive(state),
    acting: canViewAs && isActing(state),
    ended: state.ended,
    reverted: state.reverted,
    enter, allow: store.allow, revoke: store.revoke, exit: store.exit, clear: store.clear,
  }), [state, canViewAs, enter, store])
  return <ViewAsContext.Provider value={value}>{children}</ViewAsContext.Provider>
}

const OFF = Object.freeze({
  canViewAs: false, target: null, mode: 'readonly', active: false, acting: false, ended: null, reverted: null,
  enter() {}, allow() {}, revoke() {}, exit() {}, clear() {},
})

// Outside the provider (a test render of one component) the mode is off.
export function useViewAs() {
  return useContext(ViewAsContext) || OFF
}
