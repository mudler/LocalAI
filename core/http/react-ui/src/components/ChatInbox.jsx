/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { disablePush, enablePush, pushState } from '../utils/chatPush'
import { relativeDate } from '../utils/fileBrowser'
import {
  badgeText, failed, filterItems, groupByDay, iconOf, reasonText, sourceOf, targetOf, toneOf,
} from '../utils/inbox'
import ActionMenu from './ActionMenu'
import Icon from './Icon'
import { Button, Callout, EmptyState, LoadError, Segmented, SkeletonRows } from './ui'
import './inbox.css'

// How long a notification that just came in keeps its highlight.
const FRESH_MS = 4000

// Browser notifications: a button that asks for permission and subscribes the
// browser, or says why it cannot.
function PushSwitch({ t }) {
  const [state, setState] = useState('')
  const [error, setError] = useState('')
  useEffect(() => {
    let live = true
    pushState().then((s) => { if (live) setState(s) })
    return () => { live = false }
  }, [])
  if (!state || state === 'unsupported') return null
  if (state === 'denied') return <p className="nx-foot__note">{t('inbox.pushDenied')}</p>
  const toggle = async () => {
    setError('')
    try {
      setState(state === 'on' ? await disablePush() : await enablePush())
    } catch (e) {
      setError(e?.status === 409 ? t('inbox.pushUnavailable') : (e?.message || t('errors.send')))
    }
  }
  return (
    <>
      <Button variant="ghost" size="sm" icon={state === 'on' ? 'eye-off' : 'broadcast'} onClick={toggle}>
        {state === 'on' ? t('inbox.pushOff') : t('inbox.pushOn')}
      </Button>
      {error && <p className="nx-foot__note nx-foot__note--error" role="alert">{error}</p>}
    </>
  )
}

// When, as the list says it: under Today how long ago, under Yesterday the
// time, under Earlier the date and time. The exact date is the tooltip.
function whenText(iso, day, locale) {
  const rel = relativeDate(iso, { locale })
  const at = Date.parse(iso || '')
  if (!at || day === 'today') return rel
  const opts = day === 'yesterday' ? { timeStyle: 'short' } : { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }
  try {
    return { label: new Intl.DateTimeFormat(locale, opts).format(at), exact: rel.exact }
  } catch {
    return rel
  }
}

// One notification: the row opens what it is about and marks it read; the
// actions on the right mark it read or delete it.
function InboxItem({ n, day, t, locale, fresh, busy, onOpen, onRead, onDelete }) {
  const id = useId()
  const when = whenText(n.created, day, locale)
  const isFailed = failed(n)
  const target = targetOf(n)
  const source = sourceOf(n)
  const body = isFailed ? reasonText(n.body) : n.body
  const name = n.read ? n.title : t('inbox.itemUnread', { title: n.title })
  const cls = ['nx-item', !n.read && 'is-unread', isFailed && 'is-failed', fresh && 'is-fresh'].filter(Boolean).join(' ')
  return (
    <li className={cls} data-testid="inbox-item" data-id={n.id} data-read={n.read ? 'true' : 'false'}>
      <button
        type="button"
        className="nx-item__main"
        data-testid="inbox-open"
        aria-label={name}
        aria-describedby={`${id}-body ${id}-meta`}
        onClick={() => onOpen(n)}
      >
        <span className={`nx-tile nx-tile--${toneOf(n)}`} aria-hidden="true"><Icon name={iconOf(n)} /></span>
        <span className="nx-item__text">
          <span className="nx-item__title">{n.title}</span>
          {body && (
            <span id={`${id}-body`} className={isFailed ? 'nx-item__reason' : 'nx-item__body'}>
              {isFailed && <Icon name="alert-circle" className="nx-item__reason-icon" />}
              <span className="nx-item__clamp">{body}</span>
            </span>
          )}
          <span id={`${id}-meta`} className="nx-item__meta">
            <time dateTime={n.created} title={when.exact}>{when.label}</time>
            <span aria-hidden="true">·</span>
            <span>{t(`inbox.source.${source}`)}</span>
            {target && <Icon name="arrow-right" className="nx-item__go" />}
          </span>
        </span>
        {!n.read && <span className="nx-item__dot" aria-hidden="true" />}
      </button>
      <span className="nx-item__acts">
        {!n.read && (
          <Button
            variant="ghost" size="sm" icon="check" className="nx-act"
            data-testid="inbox-mark-read" aria-label={t('inbox.markRead')} title={t('inbox.markRead')}
            disabled={busy} onClick={() => onRead(n)}
          />
        )}
        <Button
          variant="ghost" size="sm" icon="trash" className="nx-act nx-act--danger"
          data-testid="inbox-delete" aria-label={t('inbox.delete')} title={t('inbox.delete')}
          disabled={busy} onClick={() => onDelete(n)}
        />
      </span>
    </li>
  )
}

// The inbox button of the chat header and its panel: a popover on a wide
// screen, a sheet over the bottom of the screen on a phone.
export default function ChatInbox({ inbox }) {
  const { t, i18n } = useTranslation('chats')
  const navigate = useNavigate()
  const titleId = useId()
  const [open, setOpen] = useState(false)
  const [error, setError] = useState('')
  // confirm: '' (none), 'all' or 'read': which clear the person is asked to confirm.
  const [confirm, setConfirm] = useState('')
  const [done, setDone] = useState('')
  const [busy, setBusy] = useState(false)
  const [fresh, setFresh] = useState(() => new Set())
  const [announce, setAnnounce] = useState('')
  const ref = useRef(null)
  const btnRef = useRef(null)
  const panelRef = useRef(null)

  const close = useCallback((refocus) => {
    setOpen(false)
    if (refocus) btnRef.current?.focus()
  }, [])

  // The panel floats over the page: a click outside or Esc closes it. A click
  // in the panel's own menu (drawn at the end of the page) is not outside.
  useEffect(() => {
    if (!open) return undefined
    const onDown = (e) => {
      if (ref.current?.contains(e.target)) return
      if (e.target?.closest?.('.popover')) return
      setOpen(false)
    }
    const onKey = (e) => {
      if (e.key !== 'Escape') return
      e.stopPropagation()
      // Esc first backs out of a question, then closes the panel.
      if (confirm) setConfirm('')
      else close(true)
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open, confirm, close])

  // A question or a result belongs to one opening of the panel. Opening reads
  // the list again, so a failure since the last read shows, and moves focus
  // into the panel.
  useEffect(() => {
    if (!open) { setConfirm(''); setDone(''); setError(''); return }
    inbox.refresh()
    panelRef.current?.focus({ preventScroll: true })
    // eslint-disable-next-line react-hooks/exhaustive-deps -- once per opening
  }, [open])

  // New arrivals: a highlight for a few seconds and one polite announcement.
  useEffect(() => {
    const list = inbox.arrived || []
    if (list.length === 0) return undefined
    setFresh(new Set(list.map(n => n.id)))
    setAnnounce(list.length === 1 ? t('inbox.arrived', { title: list[0].title }) : t('inbox.arrivedMany', { count: list.length }))
    const timer = setTimeout(() => setFresh(new Set()), FRESH_MS)
    return () => clearTimeout(timer)
  }, [inbox.arrived, t])

  const shown = useMemo(() => filterItems(inbox.items, inbox.filter), [inbox.items, inbox.filter])
  const groups = useMemo(() => groupByDay(shown), [shown])

  if (inbox.off) return null

  // A failed mark or delete shows in the panel; the list is as the server has it.
  const attempt = async (call) => {
    setError('')
    setDone('')
    setBusy(true)
    try { await call() } catch (e) { setError(e?.message || t('errors.send')) } finally { setBusy(false) }
  }

  const openItem = async (n) => {
    if (!n.read) await inbox.markRead([n.id]).catch(() => {})
    const target = targetOf(n)
    if (target) {
      setOpen(false)
      navigate(target)
    }
  }

  const readCount = Math.max(0, inbox.total - inbox.unread)
  const clear = (readOnly) => attempt(async () => {
    const n = await inbox.clear(readOnly)
    setConfirm('')
    setDone(t('inbox.cleared', { count: n }))
  })
  const askClear = (which) => { setDone(''); setConfirm(which) }

  // Arrows move between the items, Home and End jump, Delete removes the
  // focused one and keeps focus in the list.
  const onPanelKey = (e) => {
    if (e.defaultPrevented || e.target?.closest?.('[role="menu"]')) return
    const rows = Array.from(panelRef.current?.querySelectorAll('.nx-item__main') || [])
    if (rows.length === 0) return
    const at = rows.indexOf(document.activeElement)
    const go = (i) => { e.preventDefault(); rows[Math.max(0, Math.min(rows.length - 1, i))].focus() }
    if (e.key === 'ArrowDown') go(at < 0 ? 0 : at + 1)
    else if (e.key === 'ArrowUp') go(at < 0 ? rows.length - 1 : at - 1)
    else if (e.key === 'Home' && at >= 0) go(0)
    else if (e.key === 'End' && at >= 0) go(rows.length - 1)
    else if ((e.key === 'Delete' || e.key === 'Backspace') && at >= 0) {
      e.preventDefault()
      const n = shown[at]
      if (!n) return
      attempt(async () => {
        await inbox.remove([n.id])
        requestAnimationFrame(() => {
          const next = Array.from(panelRef.current?.querySelectorAll('.nx-item__main') || [])
          ;(next[Math.min(at, next.length - 1)] || panelRef.current)?.focus()
        })
      })
    }
  }

  const badge = badgeText(inbox.unread)
  const buttonName = inbox.unread > 0 ? t('inbox.buttonUnread', { count: inbox.unread }) : t('inbox.title')
  const filters = [
    { id: 'all', label: t('inbox.filterAll') },
    { id: 'unread', label: t('inbox.filterUnread') },
    { id: 'failed', label: t('inbox.filterFailed') },
  ]
  const menu = [
    { key: 'read', icon: 'broom', label: t('inbox.clearRead'), disabled: readCount === 0, onClick: () => askClear('read') },
    { key: 'all', icon: 'trash', label: t('inbox.clearAll'), danger: true, disabled: inbox.total === 0, onClick: () => askClear('all') },
  ]
  const firstLoad = inbox.status === 'loading' && inbox.items.length === 0
  const failedLoad = inbox.status === 'error'
  const ready = !firstLoad && !(failedLoad && inbox.items.length === 0)

  let empty = null
  if (ready && shown.length === 0) {
    if (inbox.filter === 'unread') {
      empty = <EmptyState variant="no-results" icon="check-circle" level={3} title={t('inbox.caughtUpTitle')} body={t('inbox.caughtUpBody')} testId="inbox-caught-up" />
    } else if (inbox.filter === 'failed') {
      empty = <EmptyState variant="no-results" icon="check-circle" level={3} title={t('inbox.noFailedTitle')} body={t('inbox.noFailedBody')} testId="inbox-no-failed" />
    } else {
      empty = <EmptyState variant="no-results" icon="inbox" level={3} title={t('inbox.emptyTitle')} body={t('inbox.emptyBody')} testId="inbox-empty" />
    }
  }

  return (
    <div className="nx" data-testid="chat-inbox" ref={ref}>
      <button
        ref={btnRef}
        type="button"
        className="cx-icobtn cx-icobtn--lg nx-btn"
        aria-expanded={open}
        aria-haspopup="dialog"
        aria-label={buttonName}
        title={t('inbox.title')}
        onClick={() => setOpen(!open)}
      >
        <Icon name="inbox" />
        {badge && <span key={badge} className="nx-badge" data-testid="inbox-count" aria-hidden="true">{badge}</span>}
      </button>
      <div className="sr-only" role="status" aria-live="polite" data-testid="inbox-live">{announce}</div>
      {open && <div className="nx-scrim" aria-hidden="true" onClick={() => close(true)} />}
      {open && (
        <div
          className="nx-panel"
          role="dialog"
          aria-labelledby={titleId}
          tabIndex={-1}
          ref={panelRef}
          onKeyDown={onPanelKey}
          data-testid="inbox-panel"
        >
          <header className="nx-head">
            <div className="nx-head__row">
              <h2 id={titleId} className="nx-head__title">{t('inbox.title')}</h2>
              {inbox.unread > 0 && <span className="nx-head__count">{t('inbox.unread', { count: inbox.unread })}</span>}
              <span className="nx-head__acts">
                <span data-testid="inbox-menu">
                  <ActionMenu items={menu} ariaLabel={t('inbox.more')} compact />
                </span>
                <Button variant="ghost" size="sm" icon="close" className="nx-head__close" aria-label={t('inbox.close')} onClick={() => close(true)} />
              </span>
            </div>
            {(inbox.total > 0 || inbox.filter !== 'all') && <div className="nx-head__tools">
              <Segmented
                className="nx-filter"
                label={t('inbox.filterLabel')}
                value={inbox.filter}
                onChange={(f) => { setConfirm(''); inbox.setFilter(f) }}
                options={filters}
                itemTestId="inbox-filter-"
              />
              <Button variant="ghost" size="sm" icon="check" className="nx-mark-all" disabled={inbox.unread === 0 || busy} onClick={() => attempt(() => inbox.markAllRead())} data-testid="inbox-mark-all">
                {t('inbox.markAll')}
              </Button>
            </div>}
          </header>
          {confirm && inbox.total > 0 && (
            <Callout
              tone="warn"
              className="nx-confirm"
              role="alertdialog"
              aria-labelledby={`${titleId}-confirm`}
              testId="inbox-confirm"
              actions={(
                <>
                  <Button variant="danger" size="sm" data-testid="inbox-confirm-yes" autoFocus busy={busy} onClick={() => clear(confirm === 'read')}>
                    {confirm === 'read' ? t('inbox.clearRead') : t('inbox.clearAll')}
                  </Button>
                  {confirm === 'all' && readCount > 0 && readCount < inbox.total && (
                    <Button size="sm" data-testid="inbox-confirm-read" onClick={() => setConfirm('read')}>{t('inbox.onlyRead', { count: readCount })}</Button>
                  )}
                  <Button variant="ghost" size="sm" onClick={() => setConfirm('')}>{t('inbox.cancel')}</Button>
                </>
              )}
            >
              <span id={`${titleId}-confirm`}>{confirm === 'read' ? t('inbox.confirmRead', { count: readCount }) : t('inbox.confirmAll', { count: inbox.total })}</span>
            </Callout>
          )}
          <div className="nx-body">
            {done && <p className="nx-note nx-note--ok" role="status" data-testid="inbox-done"><Icon name="check-circle" />{done}</p>}
            {error && <p className="nx-note nx-note--error" role="alert"><Icon name="alert-circle" />{error}</p>}
            {failedLoad && (
              <LoadError className="nx-load-error" title={t('inbox.loadError')} error={inbox.error} onRetry={() => inbox.refresh()} testId="inbox-load-error" />
            )}
            {firstLoad && <SkeletonRows count={4} />}
            {empty}
            {ready && inbox.filter === 'all' && inbox.unread === 0 && shown.length > 0 && (
              <p className="nx-note" data-testid="inbox-caught-up-note"><Icon name="check-circle" />{t('inbox.caughtUp')}</p>
            )}
            {groups.map(g => (
              <section key={g.day} className="nx-group" aria-labelledby={`${titleId}-${g.day}`}>
                <h3 id={`${titleId}-${g.day}`} className="nx-day">{t(`inbox.day.${g.day}`)}</h3>
                <ul className="nx-list">
                  {g.items.map(n => (
                    <InboxItem
                      key={n.id} n={n} day={g.day} t={t} locale={i18n.language} fresh={fresh.has(n.id)} busy={busy}
                      onOpen={openItem}
                      onRead={(x) => attempt(() => inbox.markRead([x.id]))}
                      onDelete={(x) => attempt(() => inbox.remove([x.id]))}
                    />
                  ))}
                </ul>
              </section>
            ))}
            {inbox.filter !== 'failed' && inbox.items.length > 0 && inbox.items.length < inbox.listTotal && (
              <div className="nx-more">
                <span>{t('inbox.showing', { shown: inbox.items.length, total: inbox.listTotal })}</span>
                <Button variant="ghost" size="sm" icon="chevron-down" data-testid="inbox-more" disabled={busy} onClick={() => attempt(() => inbox.showMore())}>{t('inbox.showMore')}</Button>
              </div>
            )}
          </div>
          <footer className="nx-foot">
            <PushSwitch t={t} />
          </footer>
        </div>
      )}
    </div>
  )
}
