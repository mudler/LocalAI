import { useCallback, useEffect, useSyncExternalStore } from 'react'
import { useTranslation } from 'react-i18next'
import { pagesApi } from '../utils/api'
import { ageParts, feedPaths, touchesPages } from '../utils/pages'
import { useWorkspaceFeed } from './workspaceFeed'

// The user's pages, read once and shared by the sidebar and the Pages screen,
// and read again when the change feed says a page changed. `off` holds the
// reason when the server refuses (feature off, files off): the Pages entry then
// stays hidden.
let state = { pages: [], loaded: false, loading: false, error: '', off: '' }
const subscribers = new Set()
let inflight = null

function set(next) {
  state = { ...state, ...next }
  subscribers.forEach((fn) => fn())
}

export function refreshPages() {
  if (inflight) return inflight
  set({ loading: true })
  inflight = pagesApi.list()
    .then((res) => set({ pages: res.pages || [], loaded: true, error: '', off: '' }))
    .catch((e) => {
      const off = e?.status === 403 || e?.status === 409 || e?.status === 404
      set({ loaded: true, error: off ? '' : (e?.message || 'error'), off: off ? (e?.message || 'off') : '' })
    })
    .finally(() => { inflight = null; set({ loading: false }) })
  return inflight
}

// currentPages is the list as the last read left it, outside React.
export const currentPages = () => state.pages

const subscribe = (fn) => {
  subscribers.add(fn)
  return () => subscribers.delete(fn)
}
const snapshot = () => state

export function usePages(enabled = true) {
  const s = useSyncExternalStore(subscribe, snapshot)
  useEffect(() => {
    if (enabled && !state.loaded && !inflight) refreshPages()
  }, [enabled])
  const onChange = useCallback((ev) => {
    if (enabled && touchesPages(feedPaths(ev))) refreshPages()
  }, [enabled])
  useWorkspaceFeed(['/pages/**'], onChange)
  return { ...s, refresh: refreshPages }
}

// A function that says how long ago a time was, in the UI language: "just now"
// under a minute, then "5 minutes ago" and so on.
export function usePageAge() {
  const { t, i18n } = useTranslation('pages')
  return useCallback((ts) => {
    const parts = ageParts(ts)
    if (!parts) return t('justNow')
    try {
      return new Intl.RelativeTimeFormat(i18n.language, { numeric: 'auto' }).format(parts.value, parts.unit)
    } catch {
      return new Intl.RelativeTimeFormat('en', { numeric: 'auto' }).format(parts.value, parts.unit)
    }
  }, [t, i18n.language])
}
