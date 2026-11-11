/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import Icon from '../Icon'
import PageFrame from '../PageFrame'
import PageLinkAsk from '../PageLinkAsk'
import TrustSheet, { SourceSheet } from '../pages/TrustSheet'
import { usePageLinks } from '../../hooks/usePageLinks'
import { useTilePage } from '../../hooks/useTilePage'
import { useHidden } from '../../hooks/useHidden'
import { useTheme } from '../../contexts/ThemeContext'
import { liveFrames } from '../../utils/liveFrames'
import { nextFrame, tilePagePath, tileRef, tileSize, tileTitle } from '../../utils/pageTiles'
import '../../pages/pages.css'

let nextTile = 0

// The margin around the screen within which a tile that scrolled away keeps
// its frame (paused); further off, the frame is taken down.
const NEAR = '100% 0px'

// useInView watches el: inView while any of it is on screen, near while it is
// within NEAR of the screen. Without IntersectionObserver a tile counts as in
// view, so it still loads.
function useInView(el) {
  const [view, setView] = useState({ inView: false, near: false })
  useEffect(() => {
    if (!el) return undefined
    if (typeof IntersectionObserver !== 'function') {
      setView({ inView: true, near: true })
      return undefined
    }
    const seen = new IntersectionObserver(([e]) => setView((v) => ({ ...v, inView: e.isIntersecting })))
    const near = new IntersectionObserver(([e]) => setView((v) => ({ ...v, near: e.isIntersecting })), { rootMargin: NEAR })
    seen.observe(el)
    near.observe(el)
    return () => { seen.disconnect(); near.disconnect() }
  }, [el])
  return view
}


// PageTile is a widget of type page: a page of the workspace in its compact
// mode, in the same sandbox and bridge as the full page. The tile reserves the
// height of its size, builds its frame only once it scrolls into view, pauses
// it out of sight and takes it down far away. The frame counts toward the
// frames of the whole app (liveFrames): with all of them taken, the tile is a
// static card that runs on a click. A frame over its budget is stopped, with
// Reload. A page that is gone, has no widget block, or is a shared page the
// viewer has not allowed on Home shows a calm line with what to do; never a
// broken frame.
//
//   w        the widget of home.json
//   listed   the page as the pages list knows it (its size before the read)
//   onFix    opens Customise Home
export default function PageTile({ w, listed, onFix }) {
  const { t } = useTranslation('workspace')
  const { t: tp } = useTranslation('pages')
  const { theme } = useTheme()
  const ref = useMemo(() => tileRef(w), [w.page, w.folder]) // eslint-disable-line react-hooks/exhaustive-deps
  const id = useRef(`tile-${++nextTile}`).current
  const [el, setEl] = useState(null)
  const view = useInView(el)
  const hidden = useHidden()
  const tile = useTilePage(ref)
  const { read, state, run, runnable, files, frameRef, load, onRefused, sheet } = tile
  const [frame, setFrame] = useState({ alive: false, visible: false })
  const [frameKey, setFrameKey] = useState(0)
  const [failed, setFailed] = useState(false) // the frame left its page
  const [stopped, setStopped] = useState('') // the frame went over its budget
  const [capped, setCapped] = useState(false) // every live frame of the app is taken
  const [wake, setWake] = useState(0)
  const links = usePageLinks()

  // The frame: built in view, paused out of sight, taken down far away.
  const frameNow = useRef(frame)
  const onEvict = useCallback(() => {
    frameNow.current = { alive: false, visible: false }
    setFrame(frameNow.current)
    setCapped(true)
  }, [])
  useEffect(() => liveFrames.subscribe(() => setWake((n) => n + 1)), [])
  useEffect(() => {
    const prev = frameNow.current
    let next = { alive: false, visible: false }
    let full = false
    if (runnable && !stopped) {
      const want = !prev.alive && view.inView && !hidden
      const slot = liveFrames.holds(id) || (want && liveFrames.acquire(id, onEvict))
      full = want && !slot
      next = nextFrame(prev, { ...view, hidden, slot })
    }
    if (!next.alive) liveFrames.release(id)
    setCapped(full)
    if (next.alive !== prev.alive || next.visible !== prev.visible) {
      frameNow.current = next
      setFrame(next)
    }
  }, [runnable, stopped, id, view, hidden, wake, onEvict])
  useEffect(() => () => liveFrames.release(id), [id])

  const page = run?.page || read.page || listed
  const size = tileSize(read.page || listed)
  const title = tileTitle(w, page)
  const icon = page?.icon || (ref?.folder ? 'users' : 'file-code')
  const pagePath = tilePagePath(ref)

  // A page on Home cannot send to a chat: the question needs the full page.
  const onSend = useCallback(() => Promise.reject(new Error('a page on Home cannot send messages; open the page')), [])

  const refresh = () => {
    setFailed(false)
    setStopped('')
    setFrameKey((k) => k + 1)
    load({ quiet: true })
  }
  const retry = () => { setFailed(false); load() }
  const runNow = () => {
    if (liveFrames.take(id, onEvict)) {
      setCapped(false)
      setWake((n) => n + 1)
    }
  }
  const allow = async (once) => {
    if (await tile.allow(once)) setFrameKey((k) => k + 1)
  }

  let body
  if (state === 'loading') {
    body = <div className="skeleton skeleton--block wk-ptile__fill" role="status" aria-label={tp('view.loading')} data-testid="page-tile-loading" />
  } else if (state === 'missing' || state === 'noWidget' || state === 'off') {
    const key = state === 'missing' ? 'gone' : state === 'noWidget' ? 'noWidget' : 'off'
    body = (
      <div className="wk-ptile__calm" data-testid={`page-tile-${key}`}>
        <Icon name={state === 'noWidget' ? 'square-dashed' : 'file'} className="wk-ptile__calm-icon" />
        <p className="wk-ptile__calm-title">{t(`wellKnown.home.page.${key}`)}</p>
        <p className="wk-ptile__calm-text">{t(`wellKnown.home.page.${key}Hint`, { name: ref?.name || w.page || '' })}</p>
        {state !== 'off' && (
          <button type="button" className="wk-linkbtn" onClick={onFix} data-testid="page-tile-fix"><Icon name="pencil" /> {t('wellKnown.home.page.fix')}</button>
        )}
      </div>
    )
  } else if (state === 'error') {
    body = (
      <div className="wk-ptile__calm" role="alert" data-testid="page-tile-error">
        <Icon name="alert-circle" className="wk-ptile__calm-icon" />
        <p className="wk-ptile__calm-title">{t('wellKnown.home.page.failed')}</p>
        <p className="wk-ptile__calm-text">{read.error?.message}</p>
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={retry} data-testid="page-tile-retry"><Icon name="refresh" /> {t('wellKnown.home.page.retry')}</button>
      </div>
    )
  } else if (!runnable) {
    body = (
      <div className="wk-ptile__calm" data-testid="page-tile-allow">
        <Icon name="lock" className="wk-ptile__calm-icon" />
        <p className="wk-ptile__calm-title">{t(read.consent === 'changed' ? 'wellKnown.home.page.changed' : 'wellKnown.home.page.notAllowed')}</p>
        <p className="wk-ptile__calm-text">{t('wellKnown.home.page.allowHint', { author: page?.author_name || page?.author_id || '' })}</p>
        <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" onClick={tile.openSheet} data-testid="page-tile-allow-open"><Icon name="shield" /> {t('wellKnown.home.page.allow')}</button>
      </div>
    )
  } else if (failed) {
    body = (
      <div className="wk-ptile__calm" role="alert" data-testid="page-tile-navigated">
        <p className="wk-ptile__calm-text">{tp('view.navigated')}</p>
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={refresh}><Icon name="refresh" /> {tp('view.reload')}</button>
      </div>
    )
  } else if (stopped) {
    body = (
      <div className="wk-ptile__calm" role="alert" data-testid="page-tile-stopped" data-reason={stopped}>
        <Icon name="pause" className="wk-ptile__calm-icon" />
        <p className="wk-ptile__calm-title">{t('wellKnown.frames.stopped')}</p>
        <p className="wk-ptile__calm-text">{t(`wellKnown.frames.why.${stopped}`)}</p>
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={refresh} data-testid="page-tile-reload"><Icon name="refresh" /> {tp('view.reload')}</button>
      </div>
    )
  } else if (capped && !frame.alive) {
    body = (
      <div className="wk-ptile__calm" data-testid="page-tile-static">
        <Icon name="pause" className="wk-ptile__calm-icon" />
        <p className="wk-ptile__calm-text">{t('wellKnown.frames.capped', { max: liveFrames.max() })}</p>
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={runNow} data-testid="page-tile-run"><Icon name="play" /> {t('wellKnown.frames.run')}</button>
      </div>
    )
  } else if (!frame.alive) {
    body = <div className="wk-ptile__fill wk-ptile__idle" aria-hidden="true" data-testid="page-tile-idle" />
  } else {
    const p = run?.page || read.page
    body = (
      <PageFrame
        key={frameKey}
        ref={frameRef}
        page={p}
        html={run?.html ?? read.html}
        theme={theme}
        title={t('wellKnown.home.page.frameTitle', { title })}
        files={files}
        codeFiles={ref.folder ? (p.code_files || []) : null}
        mode="widget"
        size={size}
        budget
        visible={frame.visible}
        className="wk-ptile__frame"
        onSend={onSend}
        onNavigated={() => setFailed(true)}
        onStopped={setStopped}
        onLink={links.onLink}
        onRefused={ref.folder ? onRefused : undefined}
      />
    )
  }

  return (
    <section
      ref={setEl}
      className={`wk-widget wk-ptile wk-ptile--${size}`}
      data-testid={`home-widget-${w.id}`}
      data-state={state}
      data-size={size}
      data-frame={frame.alive ? (frame.visible ? 'live' : 'paused') : stopped ? 'stopped' : capped ? 'static' : 'none'}
      aria-label={title}
    >
      <h2 className="wk-ptile__bar">
        <span className="wk-ptile__name"><Icon name={icon} className="wk-ptile__icon" /><span className="wk-ptile__title">{title}</span></span>
        <span className="wk-ptile__tools">
          {state === 'ready' && (
            <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm dk-btn--icon" aria-label={t('wellKnown.home.page.refresh')} title={t('wellKnown.home.page.refresh')} onClick={refresh} data-testid="page-tile-refresh"><Icon name="refresh" /></button>
          )}
          {ref && state !== 'missing' && (
            <Link className="wk-ptile__open" to={pagePath} data-testid="page-tile-open">{t('wellKnown.home.page.open')}<Icon name="arrow-right" /></Link>
          )}
        </span>
      </h2>
      <div className="wk-ptile__body">
        {body}
        {links.link && (
          <div className="wk-ptile__ask">
            <PageLinkAsk key={links.link.id} link={links.link} t={tp} onOpen={links.open} onCancel={links.cancel} />
          </div>
        )}
      </div>
      {sheet.open && read.src && (
        <TrustSheet
          src={read.src}
          changed={read.consent === 'changed'}
          embed
          busy={sheet.busy}
          error={sheet.error}
          covered={sheet.source}
          onCancel={tile.closeSheet}
          onAllowOnce={() => allow(true)}
          onAllow={() => allow(false)}
          onSource={() => tile.showSource(true)}
        />
      )}
      {sheet.source && read.src && <SourceSheet src={read.src} onClose={() => tile.showSource(false)} />}
    </section>
  )
}
