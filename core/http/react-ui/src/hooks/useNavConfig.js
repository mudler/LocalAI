import { useCallback, useEffect, useMemo, useState } from 'react'
import { chatsApi } from '../utils/api'
import { readItems } from '../utils/nav'
import { useWellKnown } from './useWellKnown'
import { usePages } from './usePages'
import { useSharedPages } from './useSharedPages'

// The sidebar order of the user: nav.json, read through the same shared copy
// as the other settings files, so a change by the assistant or another tab
// shows at once. The server answers the last good version when the file has
// errors, so a broken file never empties the sidebar; with no file the list is
// empty and the sidebar is the default one.
export function useNavConfig(enabled = true) {
  const wk = useWellKnown('nav', { enabled })
  const items = useMemo(() => readItems(wk.doc?.data), [wk.doc])
  return { items, doc: wk.doc, error: wk.error, loading: wk.loading, wk }
}

// What a chat link was last known to be: id -> {status: 'ok'|'missing', name}.
// Kept for the tab, so the sidebar and the editor ask once.
const chatKnown = new Map()
const LIST_EVENT = 'localai:chats-list'

// useNavLinks answers for the links of a list: status(item) is 'ok',
// 'missing' (the page or the chat is gone) or 'unknown' (not read yet), and
// title(item) is the name of what it opens. A page link needs the pages
// feature; without it the page counts as unknown and the sidebar skips it.
export function useNavLinks(items, { pagesOn, enabled = true }) {
  const { pages, loaded } = usePages(enabled && pagesOn)
  const { pages: shared } = useSharedPages(enabled && pagesOn)
  const [, bump] = useState(0)
  const chatIds = useMemo(() => items.filter(i => i.type === 'chat').map(i => i.chat), [items])
  const idsKey = chatIds.join('\n')

  const check = useCallback(() => {
    let live = true
    chatIds.forEach((id) => {
      chatsApi.get(id)
        .then((c) => { chatKnown.set(id, { status: 'ok', name: c?.title || c?.name || '' }) })
        .catch((e) => {
          // Only a 404 or 410 says the chat is gone; any other failure keeps
          // what was known, or shows the link.
          if (e?.status === 404 || e?.status === 410) chatKnown.set(id, { status: 'missing', name: '' })
          else if (!chatKnown.has(id)) chatKnown.set(id, { status: 'ok', name: '' })
        })
        .finally(() => { if (live) bump(n => n + 1) })
    })
    return () => { live = false }
  }, [chatIds])

  useEffect(() => {
    if (!enabled || chatIds.length === 0) return undefined
    const stop = check()
    // A deleted chat is announced by the pages that list the chats.
    const on = () => check()
    window.addEventListener(LIST_EVENT, on)
    return () => { stop(); window.removeEventListener(LIST_EVENT, on) }
    // idsKey stands for chatIds.
  }, [enabled, idsKey]) // eslint-disable-line react-hooks/exhaustive-deps

  const page = (name) => pages.find(p => p.name === name)
  const status = (it) => {
    if (it.type === 'page') {
      if (!pagesOn || !loaded) return 'unknown'
      return page(it.page) ? 'ok' : (shared.some(p => p.name === it.page) ? 'ok' : 'missing')
    }
    if (it.type === 'chat') return chatKnown.get(it.chat)?.status || 'unknown'
    return 'ok'
  }
  const title = (it) => {
    if (it.label) return it.label
    if (it.type === 'page') return page(it.page)?.title || it.page
    if (it.type === 'chat') return chatKnown.get(it.chat)?.name || ''
    return it.url
  }
  const icon = (it) => it.icon || (it.type === 'page' ? page(it.page)?.icon || 'file-code' : it.type === 'chat' ? 'chat' : 'globe')
  return { status, title, icon }
}
