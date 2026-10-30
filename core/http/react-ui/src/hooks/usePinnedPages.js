import { useCallback, useEffect, useSyncExternalStore } from 'react'
import { UI_FILE, pinsOf, withPin } from '../utils/pages'
import { wellKnownApi } from './useWellKnown'
import { useWorkspaceFeed } from './workspaceFeed'

// The pinned pages are `pinnedPages` in ui.json of the user's home. The
// well-known files service reads, checks and writes ui.json: a pin changes only
// `pinnedPages` and is written from the version it read, and the service keeps
// the keys that another editor added. When the file changed in between (a 409
// with the newer document), the pin is tried again on top of it.
const MAX_TRIES = 3

let state = { pins: [], loaded: false, error: '' }
const subscribers = new Set()
let inflight = null

function set(next) {
  state = { ...state, ...next }
  subscribers.forEach((fn) => fn())
}

export function refreshPins() {
  if (inflight) return inflight
  inflight = wellKnownApi.get('ui')
    .then((doc) => set({ pins: pinsOf(doc?.data), loaded: true, error: '' }))
    .catch((e) => set({ loaded: true, error: e?.message || 'error' }))
    .finally(() => { inflight = null })
  return inflight
}

export async function setPin(name, pinned) {
  let doc = await wellKnownApi.get('ui')
  for (let i = 0; i < MAX_TRIES; i++) {
    const next = withPin(doc?.data, name, pinned)
    try {
      const saved = await wellKnownApi.put('ui', { pinnedPages: next.pinnedPages }, { version: doc?.version ?? 0 })
      set({ pins: pinsOf(saved?.data ?? next), loaded: true, error: '' })
      return
    } catch (e) {
      if (!e?.conflict) throw e
      doc = e.current
    }
  }
  throw new Error('ui.json keeps changing; try again')
}

const subscribe = (fn) => {
  subscribers.add(fn)
  return () => subscribers.delete(fn)
}
const snapshot = () => state

export function usePinnedPages(enabled = true) {
  const s = useSyncExternalStore(subscribe, snapshot)
  useEffect(() => {
    if (enabled && !state.loaded && !inflight) refreshPins()
  }, [enabled])
  const onChange = useCallback(() => { if (enabled) refreshPins() }, [enabled])
  useWorkspaceFeed([UI_FILE], onChange)
  return { ...s, setPin, refresh: refreshPins }
}
