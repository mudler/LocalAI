import { useCallback, useEffect, useSyncExternalStore } from 'react'
import { chatTriggersApi } from '../utils/api'

// The user's triggers and waiting proposals, read for the widgets of Home: one
// copy shared by every widget, read again on a timer (often while a run goes),
// when the window gets focus, and after an action. A server without triggers,
// or a user without the feature, gives empty lists: the widgets then show where
// their data comes from and nothing about schedules.
let state = { triggers: [], proposals: [], loaded: false, off: false }
const subscribers = new Set()
let inflight = null

function set(next) {
  state = { ...state, ...next }
  subscribers.forEach(fn => fn())
}

export function refreshHomeTriggers() {
  if (inflight) return inflight
  inflight = Promise.all([chatTriggersApi.list(), chatTriggersApi.proposals().catch(() => ({ triggers: [] }))])
    .then(([list, props]) => set({ triggers: list?.triggers || [], proposals: props?.triggers || [], loaded: true, off: false }))
    .catch(e => set({ loaded: true, off: e?.status === 403 || e?.status === 404 || e?.status === 409 }))
    .finally(() => { inflight = null })
  return inflight
}

const subscribe = (fn) => { subscribers.add(fn); return () => subscribers.delete(fn) }
const snapshot = () => state

const busy = () => state.triggers.some(t => t.last_run && (t.last_run.status === 'running' || t.last_run.status === 'queued'))

export function useHomeTriggers(enabled = true) {
  const s = useSyncExternalStore(subscribe, snapshot)
  useEffect(() => {
    if (!enabled) return undefined
    if (!state.loaded) refreshHomeTriggers()
    let timer
    const tick = () => {
      timer = setTimeout(() => { if (!document.hidden) refreshHomeTriggers(); tick() }, busy() ? 5000 : 60000)
    }
    tick()
    const onFocus = () => refreshHomeTriggers()
    window.addEventListener('focus', onFocus)
    return () => { clearTimeout(timer); window.removeEventListener('focus', onFocus) }
  }, [enabled])

  // act runs a trigger action and reads the list again.
  const act = useCallback(async (call) => {
    try { return await call() } finally { await refreshHomeTriggers() }
  }, [])
  return { ...s, act, refresh: refreshHomeTriggers }
}
