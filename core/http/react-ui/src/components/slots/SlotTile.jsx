/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useNavigate } from 'react-router-dom'
import Icon from '../Icon'
import PageFrame from '../PageFrame'
import PageLinkAsk from '../PageLinkAsk'
import TrustSheet, { SourceSheet } from '../pages/TrustSheet'
import { useHidden } from '../../hooks/useHidden'
import { usePageLinks } from '../../hooks/usePageLinks'
import { useTilePage } from '../../hooks/useTilePage'
import { useTheme } from '../../contexts/ThemeContext'
import { liveFrames } from '../../utils/liveFrames'
import { compactOf, tilePagePath, tileRef, tileTitle } from '../../utils/pageTiles'
import { cardSize, itemLabel, openSlotPage, tileMode } from '../../utils/slots'

let nextTile = 0
// The narrowest thread that makes room for an open card, in px.
const MIN_THREAD = 360

// SlotTile is a page in a slot of ui.json. A page whose widget block has
// compact runs in the strip in its compact mode; a click opens it as a card
// anchored under (or, at the foot of the window, above) the tile, at the
// page's widget size. The card is the same frame in a bigger box: the page is
// told its new size through the bridge and is never reloaded. A page without
// compact is a plain button that opens the page.
//
// The frame counts toward the live frames of the app (liveFrames). With all
// of them taken the tile is a static chip that runs on a click; a frame over
// its budget stops, with Reload. A shared page runs only once the viewer
// allowed it on Home and in the slots.
//
//   item   the item of the slot: {type: 'page', page, folder?, label?, icon?}
//   slot   the name of the slot, for the place of the card
export default function SlotTile({ item, slot }) {
  const { t } = useTranslation('workspace')
  const { t: tp } = useTranslation('pages')
  const { theme } = useTheme()
  const navigate = useNavigate()
  const ref = useMemo(() => tileRef(item), [item.page, item.folder]) // eslint-disable-line react-hooks/exhaustive-deps
  const id = useRef(`slot-${++nextTile}`).current
  const hidden = useHidden()
  const tile = useTilePage(ref)
  const { read, state, run, runnable, files, frameRef, onRefused, sheet } = tile
  const links = usePageLinks()
  const [live, setLive] = useState(false)
  const [capped, setCapped] = useState(false)
  const [stopped, setStopped] = useState('')
  const [frameKey, setFrameKey] = useState(0)
  const [open, setOpen] = useState(false)
  const [wake, setWake] = useState(0)
  const rootRef = useRef(null)
  const hitRef = useRef(null)

  const page = run?.page || read.page
  const compact = compactOf(page)
  const title = itemLabel(item, tileTitle({}, page))
  const icon = item.icon || page?.icon || (ref?.folder ? 'users' : 'file-code')
  const wantsFrame = !!compact && runnable && !stopped

  const onEvict = useCallback(() => {
    setLive(false)
    setCapped(true)
    setOpen(false)
  }, [])
  useEffect(() => liveFrames.subscribe(() => setWake((n) => n + 1)), [])
  useEffect(() => {
    if (!wantsFrame) {
      liveFrames.release(id)
      setLive(false)
      setCapped(false)
      return
    }
    if (liveFrames.holds(id)) return
    if (liveFrames.acquire(id, onEvict)) {
      setLive(true)
      setCapped(false)
    } else {
      setLive(false)
      setCapped(true)
    }
  }, [wantsFrame, id, wake, onEvict])
  useEffect(() => () => liveFrames.release(id), [id])

  // The width of the strip comes from the page's compact entry: a value of
  // the page at run time, set on the tile whenever it is drawn live.
  const width = compact?.width || 0
  const setRoot = useCallback((el) => {
    rootRef.current = el
    if (el && width) el.style.setProperty('--ws-tile-w', `${width}px`)
  }, [width])

  // On a phone the card spans the window: it is placed against the tile, which
  // the CSS cannot reach from a fixed box.
  useLayoutEffect(() => {
    const el = rootRef.current
    if (!open || !el) return
    const r = el.getBoundingClientRect()
    el.style.setProperty('--ws-fixed-top', `${Math.round(r.bottom + 6)}px`)
    el.style.setProperty('--ws-fixed-bottom', `${Math.round(window.innerHeight - r.top + 6)}px`)
  }, [open])

  // The card of the chat header covers no message: while it is open, the
  // thread under the header keeps its column left of the card. The column
  // narrows for that; in a window too narrow for a readable column the card
  // lies over the thread, as on a phone, where it spans the window.
  useEffect(() => {
    if (!open || slot !== 'chatHeader') return undefined
    const card = rootRef.current?.querySelector('.ws-card')
    const body = rootRef.current?.closest('.cx-conv')?.querySelector('.cx-body')
    if (!card || !body) return undefined
    const b = body.getBoundingClientRect()
    const room = Math.ceil(b.right - card.getBoundingClientRect().left + 16)
    if (room <= 0 || b.width - room - 48 < MIN_THREAD) return undefined
    body.style.setProperty('--ws-room', `${room}px`)
    body.setAttribute('data-ws-room', '')
    return () => {
      body.removeAttribute('data-ws-room')
      body.style.removeProperty('--ws-room')
    }
  }, [open, slot])

  // The card closes on Escape and on a click outside the tile.
  useEffect(() => {
    if (!open) return undefined
    const onKey = (e) => {
      if (e.key !== 'Escape' || sheet.open) return
      e.preventDefault()
      setOpen(false)
      hitRef.current?.focus()
    }
    const onDown = (e) => { if (!rootRef.current?.contains(e.target)) setOpen(false) }
    document.addEventListener('keydown', onKey)
    document.addEventListener('mousedown', onDown)
    return () => {
      document.removeEventListener('keydown', onKey)
      document.removeEventListener('mousedown', onDown)
    }
  }, [open, sheet.open])

  const openPage = () => openSlotPage(ref, (r) => navigate(tilePagePath(r)))
  const runNow = () => {
    if (liveFrames.take(id, onEvict)) {
      setCapped(false)
      setLive(true)
    }
  }
  const reload = () => {
    setStopped('')
    setFrameKey((k) => k + 1)
  }
  const allow = async (once) => {
    if (await tile.allow(once)) setFrameKey((k) => k + 1)
  }
  const onSend = useCallback(() => Promise.reject(new Error('a page in a slot cannot send messages; open the page')), [])

  const chip = (props, children) => (
    <button type="button" className="ws-chip" {...props}>{children}</button>
  )
  const name = <span className="ws-chip__label">{title}</span>

  let body
  if (state === 'loading') {
    body = <span className="ws-chip ws-chip--wait skeleton" role="status" aria-label={tp('view.loading')} data-testid="slot-tile-loading" />
  } else if (state === 'missing' || state === 'off' || state === 'error') {
    body = chip({ className: 'ws-chip ws-chip--dim', onClick: () => navigate('/app/account?tab=slots'), title: t(`wellKnown.slots.tile.${state}`, { name: ref?.name || item.page }), 'data-testid': 'slot-tile-missing' },
      <><Icon name="file" className="ws-chip__icon" />{name}</>)
  } else if (state === 'noWidget' || (page && !compact && state === 'ready')) {
    body = chip({ onClick: openPage, title: t('wellKnown.slots.tile.open', { title }), 'data-testid': 'slot-tile-button' },
      <><Icon name={icon} className="ws-chip__icon" />{name}<Icon name="arrow-right" className="ws-chip__go" /></>)
  } else if (!runnable) {
    body = chip({ onClick: tile.openSheet, title: t('wellKnown.slots.tile.allow', { title }), 'data-testid': 'slot-tile-allow' },
      <><Icon name="lock" className="ws-chip__icon" />{name}</>)
  } else if (stopped) {
    body = chip({ className: 'ws-chip ws-chip--warn', onClick: reload, title: t(`wellKnown.frames.why.${stopped}`), 'data-testid': 'slot-tile-stopped', 'data-reason': stopped },
      <><Icon name="refresh" className="ws-chip__icon" />{name}<span className="ws-chip__meta">{tp('view.reload')}</span></>)
  } else if (!live) {
    body = chip({ onClick: runNow, title: t('wellKnown.frames.capped', { max: liveFrames.max() }), 'data-testid': 'slot-tile-static' },
      <><Icon name="play" className="ws-chip__icon" />{name}</>)
  }

  if (body) {
    return (
      <div className="ws-tile ws-tile--chip" data-testid="slot-tile" data-page={item.page} data-state={state} data-frame={stopped ? 'stopped' : capped ? 'static' : 'none'}>
        {body}
        {sheet.open && read.src && (
          <TrustSheet
            src={read.src} changed={read.consent === 'changed'} embed busy={sheet.busy} error={sheet.error} covered={sheet.source}
            onCancel={tile.closeSheet} onAllowOnce={() => allow(true)} onAllow={() => allow(false)} onSource={() => tile.showSource(true)}
          />
        )}
        {sheet.source && read.src && <SourceSheet src={read.src} onClose={() => tile.showSource(false)} />}
      </div>
    )
  }

  const p = run?.page || read.page
  const size = cardSize(p)
  const m = tileMode(open, p)
  return (
    <div
      ref={setRoot}
      className={`ws-tile ws-tile--live ws-tile--card-${size}`}
      data-open={open || undefined}
      data-testid="slot-tile"
      data-page={item.page}
      data-state={state}
      data-frame="live"
      data-size={m.size}
    >
      <button
        ref={hitRef} type="button" className="ws-tile__hit" aria-expanded={open} aria-haspopup="dialog"
        aria-label={t(open ? 'wellKnown.slots.tile.close' : 'wellKnown.slots.tile.expand', { title })}
        title={title} onClick={() => setOpen((v) => !v)} data-testid="slot-tile-hit"
      />
      {open && <span className="ws-tile__face" aria-hidden="true"><Icon name={icon} /><span>{title}</span></span>}
      {open && (
        <div className="ws-card" role="dialog" aria-label={title} data-testid="slot-card">
          <span className="ws-card__head">
            <Icon name={icon} className="ws-card__icon" />
            <span className="ws-card__title">{title}</span>
            <span className="ws-card__badge">{t(`wellKnown.slots.badge.${slot}`)}</span>
            <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm dk-btn--icon" aria-label={t('wellKnown.slots.tile.close', { title })} onClick={() => { setOpen(false); hitRef.current?.focus() }} data-testid="slot-card-close"><Icon name="close" /></button>
          </span>
          <span className="ws-card__body" />
          <span className="ws-card__foot">
            <span className="ws-card__note"><Icon name="shield" /> {t('wellKnown.slots.tile.sandboxed')}</span>
            <Link to={tilePagePath(ref)} className="ws-card__open" onClick={(e) => { e.preventDefault(); setOpen(false); openPage() }} data-testid="slot-card-open">{t('wellKnown.home.page.open')}<Icon name="arrow-right" /></Link>
          </span>
        </div>
      )}
      <div className="ws-tile__box">
        <PageFrame
          key={frameKey}
          ref={frameRef}
          page={p}
          html={run?.html ?? read.html}
          theme={theme}
          title={t('wellKnown.home.page.frameTitle', { title })}
          files={files}
          codeFiles={ref.folder ? (p.code_files || []) : null}
          mode={m.mode}
          size={m.size}
          budget
          visible={!hidden}
          className="ws-tile__frame"
          onSend={onSend}
          onNavigated={() => setStopped('navigated')}
          onStopped={setStopped}
          onLink={links.onLink}
          onRefused={ref.folder ? onRefused : undefined}
        />
      </div>
      {links.link && (
        <div className="ws-card ws-card--ask">
          <PageLinkAsk key={links.link.id} link={links.link} t={tp} onOpen={links.open} onCancel={links.cancel} />
        </div>
      )}
    </div>
  )
}
