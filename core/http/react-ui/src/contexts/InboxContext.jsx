/* eslint-disable no-unused-vars, react-refresh/only-export-components -- the lazy panel is used only inside JSX, which this config cannot see */
import { createContext, lazy, Suspense, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../context/AuthContext'
import { useInbox } from '../hooks/useInbox'
import { badgeText, entryVisible, hasUnreadFailure } from '../utils/inbox'
import '../components/inbox/inbox-entry.css'

// The panel and its language file load on the first opening, not with the shell.
const InboxPanel = lazy(() => import('../components/inbox/InboxPanel'))

const InboxContext = createContext(null)

// The notification inbox of the whole app: one list, one stream, one panel.
// App mounts it under the sign-in gate, so it never runs for a signed-out
// visitor. It runs the inbox hook only for a person who may have an inbox: the
// chats feature on for the server and for the person. Anyone else makes no
// request at all, so a 403 cannot loop. The entry (sidebar) and the bell (phone
// header) read the badge and open the panel from here; the panel is a sibling
// of the layout, outside the transformed sidebar and the inert main area.
//
// features is the answer of /api/features, or null until it arrived.
export function InboxProvider({ features, children }) {
  const { t } = useTranslation('nav')
  const { isAdmin, hasFeature } = useAuth()
  const enabled = features !== null && hasFeature('chats') && features.chats !== false
  const inbox = useInbox({ enabled })
  const [open, setOpen] = useState(false)
  // The trigger that opened the panel, for the panel's place and for focus on close.
  const [anchorEl, setAnchorEl] = useState(null)
  const [everHad, setEverHad] = useState(false)
  const [announce, setAnnounce] = useState('')
  const everRef = useRef(false)

  const loaded = inbox.status !== 'loading'
  useEffect(() => {
    if (!everRef.current && inbox.total > 0) { everRef.current = true; setEverHad(true) }
  }, [inbox.total])

  const visible = entryVisible({ enabled, off: inbox.off, loaded, isAdmin, total: inbox.total, everHad, open })

  // The panel goes with the entry when the inbox turns off under it.
  useEffect(() => {
    if (open && (!enabled || inbox.off)) setOpen(false)
  }, [open, enabled, inbox.off])

  const close = useCallback((refocus) => {
    setOpen(false)
    if (!refocus) return
    // The trigger may be gone (the entry hid after "Clear all") or hidden (the
    // drawer of a phone closed again): focus goes to the page heading then.
    const usable = (el) => el?.isConnected && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== 'hidden'
    requestAnimationFrame(() => {
      if (usable(anchorEl)) { anchorEl.focus(); return }
      const heading = document.querySelector('main h1')
      if (heading) { heading.setAttribute('tabindex', '-1'); heading.focus() }
    })
  }, [anchorEl])

  const toggle = useCallback((el) => {
    if (open) { close(true); return }
    setAnchorEl(el || null)
    setOpen(true)
  }, [open, close])

  // One polite announcement of an arrival, whichever page is open. The panel
  // only highlights the new row.
  useEffect(() => {
    const list = inbox.arrived || []
    if (list.length === 0) return
    // An item with a code is worded by the panel, in a language file that is
    // not loaded yet; its stored title is English, so it is announced by count.
    setAnnounce(list.length === 1 && !list[0].code
      ? t('inboxArrived', { title: list[0].title })
      : t('inboxArrivedMany', { count: list.length }))
  }, [inbox.arrived, t])

  const unread = inbox.unread
  const value = useMemo(() => ({
    visible,
    open,
    unread,
    badge: badgeText(unread),
    failed: hasUnreadFailure(inbox.items),
    toggle,
    refresh: inbox.refresh,
  }), [visible, open, unread, inbox.items, toggle, inbox.refresh])

  return (
    <InboxContext.Provider value={value}>
      {children}
      {visible && <div className="sr-only" role="status" aria-live="polite" data-testid="inbox-live">{announce}</div>}
      {open && visible && (
        <Suspense fallback={null}>
          <InboxPanel inbox={inbox} anchorEl={anchorEl} onClose={close} />
        </Suspense>
      )}
    </InboxContext.Provider>
  )
}

// The inbox state for a trigger or a page. Outside the provider (a test of one
// component) the entry is simply not there.
const NONE = { visible: false, open: false, unread: 0, badge: '', failed: false, toggle: () => {}, refresh: () => {} }
export function useInboxEntry() {
  return useContext(InboxContext) || NONE
}
