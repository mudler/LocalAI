import { useEffect, useSyncExternalStore } from 'react'
import { sharedPagesApi } from '../utils/api'

// The pages of the user's shared folders, read once and shared by the sidebar
// and the Pages screen. enabled is false when an administrator turned shared
// pages off; the pages are still listed so the screen can say so.
let state = { pages: [], enabled: true, loaded: false, error: '' }
const subscribers = new Set()
let inflight = null

function set(next) {
  state = { ...state, ...next }
  subscribers.forEach(fn => fn())
}

export function refreshSharedPages() {
  if (inflight) return inflight
  inflight = sharedPagesApi.list()
    .then(res => set({ pages: res.pages || [], enabled: res.enabled !== false, loaded: true, error: '' }))
    .catch(e => set({ pages: [], loaded: true, error: e?.status === 403 || e?.status === 404 || e?.status === 409 ? '' : (e?.message || 'error') }))
    .finally(() => { inflight = null })
  return inflight
}

const subscribe = (fn) => { subscribers.add(fn); return () => subscribers.delete(fn) }
const snapshot = () => state

export function useSharedPages(enabled = true) {
  const s = useSyncExternalStore(subscribe, snapshot)
  useEffect(() => {
    if (enabled && !state.loaded && !inflight) refreshSharedPages()
  }, [enabled])
  return { ...s, refresh: refreshSharedPages }
}

// sharedPagePath is the route of a shared page in the UI.
export const sharedPagePath = (p) => `/app/pages/shared/${encodeURIComponent(p.folder_id)}/${encodeURIComponent(p.name)}`

// folderLabel shows a shared folder without the leading slash.
export const folderLabel = (folder) => String(folder || '').replace(/^\/+/, '')
