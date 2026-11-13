/* eslint-disable no-unused-vars, react-refresh/only-export-components -- the lazy panel is used only inside JSX, which this config cannot see */
import { createContext, lazy, Suspense, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../context/AuthContext'
import { useActivitySummary } from '../hooks/useActivityFeed'
import { useOperations } from '../hooks/useOperations'
import { useInbox } from '../hooks/useInbox'
import { badgeText, entryVisible, failed as inboxFailed, hasUnreadFailure } from '../utils/inbox'
import { emitSound } from '../utils/soundBus'
import { recentRecords } from '../utils/opsActivity'
import '../components/inbox/inbox-entry.css'

// The panel and its language file load on the first opening, not with the shell.
const InboxPanel = lazy(() => import('../components/inbox/InboxPanel'))

const InboxContext = createContext(null)

// What the ring says when nothing runs, and for anyone who is not an
// administrator: operations are theirs alone to see.
const NO_OPS = { active: false, value: null, failed: false, failedCount: 0, count: 0 }

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
  // Operations are a part of the inbox for an administrator: a ring on the
  // entry while they run, a red one with a dot after a failure. They never
  // touch the unread count, which stays the messages'.
  const summary = useActivitySummary()
  const { active, value: ringValue, failed: opsFailed, failedCount, count } = summary
  const ops = useMemo(
    () => (isAdmin ? { active, value: ringValue, failed: opsFailed, failedCount, count } : NO_OPS),
    [isAdmin, active, ringValue, opsFailed, failedCount, count],
  )
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

  // No inbox here: the chats are off for the server or the person, or the
  // server keeps none. Messages have nowhere to live then, but an administrator
  // still needs the operations, so the entry stays for them while there is
  // something to show: work that runs or failed, work that finished a moment
  // ago, or any work seen since the page opened. It opens on the Activity tab
  // alone and makes no request to the chats.
  const inboxGone = features !== null && (!enabled || inbox.off)
  const { history, fetchHistory } = useOperations()
  useEffect(() => {
    if (inboxGone && isAdmin) fetchHistory()
  }, [inboxGone, isAdmin, fetchHistory])
  const opsSeenRef = useRef(false)
  const opsNow = ops.count > 0 || ops.failed
  if (opsNow) opsSeenRef.current = true
  const recentDone = inboxGone && isAdmin && recentRecords(history).length > 0
  const opsEntry = inboxGone && isAdmin && (opsNow || opsSeenRef.current || recentDone || open)

  const visible = inboxGone
    ? opsEntry
    : entryVisible({ enabled, off: inbox.off, loaded, isAdmin, total: inbox.total, everHad, open })

  // The panel goes with the entry when the inbox turns off under it, unless
  // the operations keep the entry.
  useEffect(() => {
    if (open && (!enabled || inbox.off) && !opsEntry) setOpen(false)
  }, [open, enabled, inbox.off, opsEntry])

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
    // A finished trigger or job has its own sound; a failure the error sound.
    emitSound(list.some(n => inboxFailed(n)) ? 'error' : 'taskDone')
    // An item with a code is worded by the panel, in a language file that is
    // not loaded yet; its stored title is English, so it is announced by count.
    setAnnounce(list.length === 1 && !list[0].code
      ? t('inboxArrived', { title: list[0].title })
      : t('inboxArrivedMany', { count: list.length }))
  }, [inbox.arrived, t])

  // One polite announcement when an operation fails, not one per poll: only a
  // rise in the number of failures speaks.
  const failedBefore = useRef(0)
  useEffect(() => {
    if (ops.failedCount > failedBefore.current) setAnnounce(t('inboxOpsFailedAnnounce', { count: ops.failedCount }))
    failedBefore.current = ops.failedCount
  }, [ops.failedCount, t])

  const unread = inbox.unread
  const value = useMemo(() => ({
    visible,
    open,
    unread,
    badge: badgeText(unread),
    failed: hasUnreadFailure(inbox.items),
    ops,
    opsOnly: inboxGone,
    toggle,
    refresh: inbox.refresh,
  }), [visible, open, unread, ops, inboxGone, inbox.items, toggle, inbox.refresh])

  return (
    <InboxContext.Provider value={value}>
      {children}
      {visible && <div className="sr-only" role="status" aria-live="polite" data-testid="inbox-live">{announce}</div>}
      {open && visible && (
        <Suspense fallback={null}>
          <InboxPanel inbox={inbox} anchorEl={anchorEl} onClose={close} opsOnly={inboxGone} />
        </Suspense>
      )}
    </InboxContext.Provider>
  )
}

// The inbox state for a trigger or a page. Outside the provider (a test of one
// component) the entry is simply not there.
const NONE = { visible: false, open: false, unread: 0, badge: '', failed: false, ops: NO_OPS, opsOnly: false, toggle: () => {}, refresh: () => {} }
export function useInboxEntry() {
  return useContext(InboxContext) || NONE
}
