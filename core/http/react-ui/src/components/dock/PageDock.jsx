/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import Icon from '../Icon'
import { Button } from '../ui'
import DockFrame from './DockFrame'
import { usePages } from '../../hooks/usePages'
import { folderLabel, useSharedPages } from '../../hooks/useSharedPages'
import { tilePagePath } from '../../utils/pageTiles'
import {
  DOCK_DEFAULT_WIDTH, DOCK_MIN_WIDTH, DOCK_MAX_SHARE, clampDockWidth, closeTab, dockKey, keyStep, refOf,
  selectTab, setDockOpen, setDockWidth,
} from '../../utils/dock'
import './dock.css'

// The page list of the dock: the tabs of the picker and the titles of tabs.
function usePageIndex() {
  const own = usePages(true)
  const shared = useSharedPages(true)
  return useMemo(() => {
    const byKey = new Map()
    for (const p of own.pages || []) byKey.set(p.name, { ...p, folder: 0 })
    for (const p of shared.pages || []) byKey.set(`${p.folder_id}/${p.name}`, { ...p, folder: p.folder_id })
    return { byKey, own: own.pages || [], shared: shared.enabled ? (shared.pages || []) : [] }
  }, [own.pages, shared.pages, shared.enabled])
}

// The list of pages to open in an empty dock, or from the + of the tabs.
function DockPicker({ index, onPick, onCancel, docked, focusToken, t }) {
  const rootRef = useRef(null)
  // A + of the tabs moves the focus to the first page, so the list is seen
  // and the keyboard is in it. An empty dock does not steal the focus.
  useEffect(() => {
    if (focusToken > 0) rootRef.current?.querySelector('.pd-pick__row')?.focus()
  }, [focusToken])
  const row = (key, ref, p) => (
    <li key={key}>
      <button type="button" className="pd-pick__row" onClick={() => onPick(ref)} aria-current={docked.has(key) || undefined} data-testid="dock-pick" data-page={p.name}>
        <Icon name={p.icon || (ref.folder ? 'users' : 'file-code')} className="pd-pick__icon" />
        <span className="pd-pick__title">{p.title || p.name}</span>
        {ref.folder ? <span className="pd-pick__meta">{folderLabel(p.folder)}</span> : null}
      </button>
    </li>
  )
  const none = index.own.length === 0 && index.shared.length === 0
  return (
    <div
      ref={rootRef} className="pd-pick" data-testid="dock-picker"
      onKeyDown={(e) => { if (e.key === 'Escape' && onCancel) { e.preventDefault(); e.stopPropagation(); onCancel() } }}
    >
      <h3 className="pd-pick__head">{t('dock.pickTitle')}</h3>
      {none && <p className="pd-pick__none">{t('dock.pickEmpty')}</p>}
      {index.own.length > 0 && (
        <ul className="pd-pick__list" aria-label={t('yours')}>
          {index.own.map((p) => row(p.name, { name: p.name, folder: 0 }, p))}
        </ul>
      )}
      {index.shared.length > 0 && (
        <>
          <h4 className="pd-pick__group">{t('shared.group')}</h4>
          <ul className="pd-pick__list" aria-label={t('shared.group')}>
            {index.shared.map((p) => row(`${p.folder_id}/${p.name}`, { name: p.name, folder: p.folder_id }, p))}
          </ul>
        </>
      )}
    </div>
  )
}

// PageDock is the panel beside a chat that shows pages of the workspace in
// tabs (mock m01). The chat keeps it (useChatDock); this draws it. Beside the
// conversation it is a column at the right of the thread and the composer,
// which narrow for it; its left edge resizes it (a separator: drag, arrows,
// Home, End, Enter or a double click to reset). On a phone it is a bottom
// sheet over the thread, with a handle that makes it full screen or closes it.
//
// Under the tabs a strip says where the page comes from, that it runs as the
// viewer, and offers "Ask the assistant to change this", which puts a request
// that names the page in the message box. The footer says that the page is
// live and has no network.
//
//   dock      the dock of the chat (utils/dock.js)
//   update    update(fn, {lazy}) changes it (useChatDock)
//   chatId    the chat; chat.send of a page goes there
//   onOpen    opens a page the person picked (usePageDock's open: it takes a
//             frame at once)
//   fresh     the key of the tab just opened by a click
//   phone     the phone layout
//   onAsk     puts a request in the message box
export default function PageDock({ dock, update, chatId, onOpen, fresh, phone, onAsk }) {
  const { t } = useTranslation('pages')
  const index = usePageIndex()
  const rootRef = useRef(null)
  const frameRef = useRef(null)
  const [viewport, setViewport] = useState(() => window.innerWidth)
  const [picking, setPicking] = useState(false)
  const [pickFocus, setPickFocus] = useState(0)
  const [full, setFull] = useState(false)
  const [seen, setSeen] = useState({}) // key -> {page, state} of the tab in front
  const [dragging, setDragging] = useState(false)
  const width = clampDockWidth(dock.width, viewport)

  useEffect(() => {
    const on = () => setViewport(window.innerWidth)
    window.addEventListener('resize', on)
    return () => window.removeEventListener('resize', on)
  }, [])
  // The width is a value of the run, set on the panel; no inline style.
  useEffect(() => {
    rootRef.current?.style.setProperty('--pd-w', `${width}px`)
  }, [width, dock.open, phone])
  // The sheet of a phone stops under the chat header, which it never covers:
  // its top is where the header ends, whatever the shell draws above it.
  useEffect(() => {
    const el = rootRef.current
    if (!phone || !dock.open || !el) return undefined
    const place = () => {
      const head = el.parentElement?.querySelector('[data-testid="chat-header"]')
      el.style.setProperty('--pd-top', `${Math.ceil(head ? head.getBoundingClientRect().bottom : 0)}px`)
    }
    place()
    window.addEventListener('resize', place)
    return () => window.removeEventListener('resize', place)
  }, [phone, dock.open])

  const tab = dock.tabs[dock.active] || null
  const key = tab ? dockKey(tab) : ''
  // A stable ref per tab, so the frame reads its page once.
  const refs = useRef(new Map())
  const tabRef = useMemo(() => {
    if (!tab) return null
    if (!refs.current.has(key)) refs.current.set(key, refOf(tab))
    return refs.current.get(key)
  }, [key]) // eslint-disable-line react-hooks/exhaustive-deps
  const info = index.byKey.get(key)
  const front = seen[key]?.page || info
  const title = front?.title || tab?.page || ''
  // The picker shows when asked for with the +, and always while the dock has
  // no tab. The + only asks for it while the dock has a tab to go back to.
  const showPicker = picking || dock.tabs.length === 0

  // A page opened or brought to the front from anywhere else (the assistant,
  // the Pages list, a slot, another tab) or a tab closed ends the request:
  // the picker must not stay over the page that just opened.
  const tabsKey = `${dock.tabs.map(dockKey).join('|')}#${dock.active}`
  useEffect(() => { setPicking(false) }, [tabsKey])
  // A hidden dock forgets the request.
  useEffect(() => { if (!dock.open) setPicking(false) }, [dock.open])

  const close = () => update((d) => setDockOpen(d, false))
  const pick = (ref) => {
    setPicking(false)
    onOpen(ref)
  }
  // The + always opens the picker for a new tab and puts the focus in it; it
  // never toggles, so a second press cannot look like nothing happened. The
  // tab in front, Escape or a page of the list end it.
  const add = () => {
    setPicking(true)
    setPickFocus((n) => n + 1)
  }
  const onState = useCallback((s) => setSeen((m) => ({ ...m, [key]: s })), [key])

  // The edge: drag to resize. The width saves once the drag rests.
  const startDrag = (e) => {
    if (phone || e.button !== 0) return
    e.preventDefault()
    const el = rootRef.current
    const right = el.getBoundingClientRect().right
    setDragging(true)
    const move = (ev) => {
      const w = clampDockWidth(right - ev.clientX, window.innerWidth)
      el.style.setProperty('--pd-w', `${w}px`)
      update((d) => setDockWidth(d, w), { lazy: true })
    }
    const up = () => {
      setDragging(false)
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
      window.removeEventListener('pointercancel', up)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
    window.addEventListener('pointercancel', up)
  }
  const onEdgeKey = (e) => {
    const w = keyStep(e.key, width, viewport, e.shiftKey)
    if (w === null) return
    e.preventDefault()
    update((d) => setDockWidth(d, w), { lazy: true })
  }
  const reset = () => update((d) => setDockWidth(d, DOCK_DEFAULT_WIDTH))

  // The handle of the sheet: up for full screen, down to close; a click
  // switches between the two heights.
  const sheetDrag = (e) => {
    if (!phone) return
    const y0 = e.clientY
    let moved = false
    const move = (ev) => { if (Math.abs(ev.clientY - y0) > 8) moved = true }
    const up = (ev) => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
      const dy = ev.clientY - y0
      if (!moved) setFull((f) => !f)
      else if (dy < -40) setFull(true)
      else if (dy > 40) { if (full) setFull(false); else close() }
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
  }

  if (!dock.open) return null

  const by = front?.source === 'shared' || tab?.folder ? 'shared' : (front?.author === 'assistant' ? 'assistant' : 'you')
  const origin = by === 'shared'
    ? t('dock.shared', { folder: folderLabel(front?.folder) || t('shared.group') })
    : t(`dock.by.${by}`)
  const docked = new Set(dock.tabs.map(dockKey))
  const maxW = Math.max(DOCK_MIN_WIDTH, Math.floor(viewport * DOCK_MAX_SHARE))

  return (
    <aside
      ref={rootRef}
      className={`pd-dock${phone ? ' pd-dock--sheet' : ''}`}
      data-full={(phone && full) || undefined}
      data-dragging={dragging || undefined}
      aria-label={t('dock.region')}
      data-testid="page-dock"
      data-width={width}
    >
      {!phone && (
        <div
          className="pd-edge"
          role="separator"
          aria-orientation="vertical"
          aria-label={t('dock.resize')}
          aria-valuemin={DOCK_MIN_WIDTH}
          aria-valuemax={maxW}
          aria-valuenow={width}
          aria-describedby="pd-edge-hint"
          tabIndex={0}
          onPointerDown={startDrag}
          onDoubleClick={reset}
          onKeyDown={onEdgeKey}
          data-testid="dock-edge"
        >
          <span className="pd-edge__grip" aria-hidden="true" />
          <span id="pd-edge-hint" className="dk-sr-only">{t('dock.resizeHint')}</span>
        </div>
      )}
      {phone && (
        <button type="button" className="pd-handle" onPointerDown={sheetDrag} onClick={(e) => e.preventDefault()} aria-label={t(full ? 'dock.halfScreen' : 'dock.fullScreen')} title={t('dock.sheetHint')} data-testid="dock-handle">
          <span className="pd-handle__bar" aria-hidden="true" />
        </button>
      )}

      <div className="pd-bar">
        <div className="pd-tabs" role="tablist" aria-label={t('dock.tabs')}>
          {dock.tabs.map((tb, i) => {
            const k = dockKey(tb)
            const p = (k === key ? seen[k]?.page : null) || index.byKey.get(k)
            const name = p?.title || tb.page
            const on = i === dock.active && !showPicker
            return (
              <span key={k} className="pd-tab" data-active={on || undefined} data-testid="dock-tab" data-page={tb.page}>
                <button
                  type="button" role="tab" aria-selected={on} className="pd-tab__btn"
                  onClick={() => { setPicking(false); update((d) => selectTab(d, i)) }}
                  title={name}
                >
                  <Icon name={p?.icon || (tb.folder ? 'users' : 'file-code')} className="pd-tab__icon" />
                  <span className="pd-tab__label">{name}</span>
                </button>
                <button type="button" className="pd-tab__x" aria-label={t('dock.closeTab', { title: name })} onClick={() => update((d) => closeTab(d, i))} data-testid="dock-tab-close">
                  <Icon name="close" />
                </button>
              </span>
            )
          })}
          <Button
            variant="ghost" size="sm" icon="plus" className="pd-add" aria-label={t('dock.add')} title={t('dock.add')}
            aria-pressed={showPicker} onClick={add} data-testid="dock-add"
          />
        </div>
        <span className="pd-bar__acts">
          {tab && !showPicker && (
            <>
              <Button variant="ghost" size="sm" icon="refresh" aria-label={t('view.reload')} title={t('view.reload')} onClick={() => frameRef.current?.reload()} data-testid="dock-reload" />
              <Button variant="ghost" size="sm" icon="external-link" aria-label={t('dock.openFull', { title })} title={t('dock.openFull', { title })} to={tilePagePath(tabRef)} data-testid="dock-open-full" />
            </>
          )}
          <Button variant="ghost" size="sm" icon={phone ? 'close' : 'chevron-right'} aria-label={t('dock.hide')} title={t('dock.hideKeys')} onClick={close} data-testid="dock-hide" />
        </span>
      </div>

      {tab && !showPicker && (
        <div className="pd-strip" data-testid="dock-strip">
          <span className="pd-pill pd-pill--accent" data-testid="dock-origin" data-by={by}><Icon name={by === 'shared' ? 'users' : by === 'assistant' ? 'sparkles' : 'user'} /> {origin}</span>
          <span className="pd-pill" title={t('view.runsAsYouHint')} data-testid="dock-runs-as-you"><Icon name="user" /> {t('view.runsAsYou')}</span>
          {!tab.folder && (
            <button type="button" className="pd-strip__ask" onClick={() => onAsk(t('dock.askPrompt', { name: tab.page, title }))} data-testid="dock-ask">
              <Icon name="arrow-up" className="pd-strip__askicon" /> {t('view.ask')}
            </button>
          )}
        </div>
      )}

      {showPicker ? (
        <div className="pd-body pd-body--pick">
          <DockPicker index={index} onPick={pick} onCancel={dock.tabs.length > 0 ? () => setPicking(false) : undefined} docked={docked} focusToken={pickFocus} t={t} />
        </div>
      ) : (
        <DockFrame
          key={key}
          ref={frameRef}
          tabRef={tabRef}
          title={title}
          chatId={chatId}
          visible={dock.open}
          fresh={fresh === key}
          onState={onState}
        />
      )}

      <footer className="pd-foot">
        <span className="pd-foot__live" data-testid="dock-live"><span className="pd-foot__dot" aria-hidden="true" /> {phone ? t('dock.liveShort') : t('dock.live')}</span>
        <span className="pd-foot__sandbox"><Icon name="lock" /> {t('dock.sandboxed')}</span>
      </footer>
    </aside>
  )
}
