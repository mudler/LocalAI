import { useCallback, useEffect, useSyncExternalStore } from 'react'
import { filesApi } from '../utils/api'
import { UI_FILE, pinsOf, withPin } from '../utils/pages'
import { useWorkspaceFeed } from './workspaceFeed'

// SHIM: the pinned pages are `pinnedPages` in ui.json of the user's home. The
// service of the well-known files (another change) will own reading, checking
// and writing ui.json; until it is merged this hook does it through the files
// API. A pin reads the file, changes only `pinnedPages` and writes it back from
// the version it read, so a key that another editor added is kept. When the
// file changed in between, the store keeps both versions: the copy is removed
// and the pin is tried again from the new version. A ui.json that is not valid
// JSON is never overwritten.
const MAX_TRIES = 3

let state = { pins: [], loaded: false, error: '' }
const subscribers = new Set()
let inflight = null

function set(next) {
  state = { ...state, ...next }
  subscribers.forEach((fn) => fn())
}

// ui.json and its version; a missing file is {} at version 0.
async function readUi() {
  const list = await filesApi.list('/')
  const entry = (list.entries || []).find((e) => e.path === UI_FILE && e.kind === 'file')
  if (!entry) return { ui: {}, version: 0 }
  const text = await filesApi.readText(UI_FILE, 256 * 1024)
  let ui
  try {
    ui = JSON.parse(text)
  } catch {
    throw new Error('ui.json is not valid JSON; fix the file first')
  }
  if (!ui || typeof ui !== 'object' || Array.isArray(ui)) throw new Error('ui.json is not a JSON object; fix the file first')
  return { ui, version: entry.version }
}

export function refreshPins() {
  if (inflight) return inflight
  inflight = readUi()
    .then(({ ui }) => set({ pins: pinsOf(ui), loaded: true, error: '' }))
    .catch((e) => set({ loaded: true, error: e?.message || 'error' }))
    .finally(() => { inflight = null })
  return inflight
}

export async function setPin(name, pinned) {
  for (let i = 0; i < MAX_TRIES; i++) {
    const { ui, version } = await readUi()
    const next = withPin(ui, name, pinned)
    const body = new Blob([`${JSON.stringify(next, null, 2)}\n`], { type: 'application/json' })
    const res = await filesApi.upload(UI_FILE, body, { base: version || undefined })
    if (!res?.conflict_of) {
      set({ pins: pinsOf(next), loaded: true, error: '' })
      return
    }
    // Someone changed ui.json since the read: drop our copy and start again.
    await filesApi.remove(res.path).catch(() => {})
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
