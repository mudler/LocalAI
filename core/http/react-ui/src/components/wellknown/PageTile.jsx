/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import Icon from '../Icon'
import PageFrame from '../PageFrame'
import PageLinkAsk from '../PageLinkAsk'
import TrustSheet, { SourceSheet } from '../pages/TrustSheet'
import { usePageLinks } from '../../hooks/usePageLinks'
import { useWorkspaceFeed } from '../../hooks/workspaceFeed'
import { refreshSharedPages } from '../../hooks/useSharedPages'
import { useTheme } from '../../contexts/ThemeContext'
import { pagesApi, sharedPagesApi } from '../../utils/api'
import { feedPaths, pageChanges } from '../../utils/pages'
import { createFrameSlots, nextFrame, tilePagePath, tileRef, tileSize, tileState, tileTitle } from '../../utils/pageTiles'
import '../../pages/pages.css'

// The frames of page tiles alive on Home, for the whole tab.
const slots = createFrameSlots()
let nextTile = 0

// The margin around the screen within which a tile that scrolled away keeps
// its frame (paused); further off, the frame is taken down.
const NEAR = '100% 0px'

const errorOf = (e) => ({ status: e?.status || 0, type: e?.body?.error?.type || '', message: e?.message || '' })

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

function useHidden() {
  const [hidden, setHidden] = useState(() => typeof document !== 'undefined' && document.hidden)
  useEffect(() => {
    const on = () => setHidden(document.hidden)
    document.addEventListener('visibilitychange', on)
    return () => document.removeEventListener('visibilitychange', on)
  }, [])
  return hidden
}

// PageTile is a widget of type page: a page of the workspace in its compact
// mode, in the same sandbox and bridge as the full page. The tile reserves the
// height of its size, builds its frame only once it scrolls into view, pauses
// it out of sight and takes it down far away. A page that is gone, has no
// widget block, or is a shared page the viewer has not allowed shows a calm
// line with what to do; never a broken frame.
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
  const [read, setRead] = useState({ loading: true })
  const [run, setRun] = useState(null) // shared: {html, page, hash, token}
  const [frame, setFrame] = useState({ alive: false, visible: false })
  const [frameKey, setFrameKey] = useState(0)
  const [failed, setFailed] = useState(false) // the frame left its page
  const [sheet, setSheet] = useState(false)
  const [source, setSource] = useState(false)
  const [busy, setBusy] = useState(false)
  const [sheetError, setSheetError] = useState('')
  const [wake, setWake] = useState(0)
  const frameRef = useRef(null)
  const links = usePageLinks()

  const load = useCallback(async ({ quiet = false } = {}) => {
    if (!ref) {
      setRead({ loading: false, error: { status: 400 } })
      return
    }
    if (!quiet) setRead((r) => ({ ...r, loading: true }))
    try {
      if (ref.folder) {
        const res = await sharedPagesApi.get(ref.folder, ref.name)
        setRead({ loading: false, page: res.page, html: res.html, consent: res.consent?.state, src: res })
      } else {
        const res = await pagesApi.get(ref.name)
        // A change of data files leaves the HTML as it was: keep the frame.
        setRead((prev) => ({ loading: false, page: res.page, html: prev.html === res.html ? prev.html : res.html }))
      }
    } catch (e) {
      if (!quiet) setRead({ loading: false, error: errorOf(e) })
    }
  }, [ref])

  useEffect(() => { setRun(null); load() }, [load])

  const state = tileState(read)
  // A shared page runs the version the viewer allowed, as read then.
  useEffect(() => {
    if (state === 'ready' && ref?.folder && !run) setRun({ html: read.html, page: read.page, hash: read.page.hash, token: '' })
  }, [state, ref, run, read])

  // The own page's files: a change of index.html or page.json reads the page
  // again, a change of another file goes to the page as files.changed.
  const onFeed = useCallback((ev) => {
    if (!ref || ref.folder) return
    const ch = pageChanges(feedPaths(ev), ref.name)
    if (!ch.all && !ch.index && !ch.meta && ch.rel.length === 0) return
    if (ch.all || ch.index || ch.meta) load({ quiet: true })
    if (ch.rel.length) frameRef.current?.emit('files.changed', { paths: ch.rel })
    else if (ch.all) frameRef.current?.emit('files.changed', { paths: [], resync: true })
  }, [ref, load])
  useWorkspaceFeed(ref && !ref.folder ? [`/pages/${ref.name}/**`] : [], onFeed)

  // The frame: built in view, paused out of sight, taken down far away.
  const runnable = state === 'ready' && (!ref?.folder || run)
  const frameNow = useRef(frame)
  useEffect(() => slots.subscribe(() => setWake((n) => n + 1)), [])
  useEffect(() => {
    const prev = frameNow.current
    let next = { alive: false, visible: false }
    if (runnable) {
      const want = !prev.alive && view.inView && !hidden
      const slot = slots.holds(id) || (want && slots.acquire(id))
      next = nextFrame(prev, { ...view, hidden, slot })
    }
    if (!next.alive) slots.release(id)
    if (next.alive !== prev.alive || next.visible !== prev.visible) {
      frameNow.current = next
      setFrame(next)
    }
  }, [runnable, id, view, hidden, wake])
  useEffect(() => () => slots.release(id), [id])

  const page = run?.page || read.page || listed
  const size = tileSize(read.page || listed)
  const title = tileTitle(w, page)
  const icon = page?.icon || (ref?.folder ? 'users' : 'file-code')
  const pagePath = tilePagePath(ref)

  const files = useMemo(() => (ref?.folder && run ? {
    read: (path) => sharedPagesApi.read(ref.folder, ref.name, run, path),
    readImage: (path) => sharedPagesApi.readImage(ref.folder, ref.name, run, path),
    write: (path, content, base) => sharedPagesApi.write(ref.folder, ref.name, run, path, content, base),
    list: async (dir) => (await sharedPagesApi.files(ref.folder, ref.name, run, dir)).entries || [],
  } : null), [ref, run])

  // A page on Home cannot send to a chat: the question needs the full page.
  const onSend = useCallback(() => Promise.reject(new Error('a page on Home cannot send messages; open the page')), [])

  const refresh = () => {
    setFailed(false)
    setFrameKey((k) => k + 1)
    load({ quiet: true })
  }
  const retry = () => { setFailed(false); load() }

  const onRefused = useCallback(() => {
    setRun(null)
    load({ quiet: true })
  }, [load])

  const allow = async (once) => {
    const src = read.src
    if (!src) return
    setBusy(true)
    setSheetError('')
    try {
      const res = await sharedPagesApi.allow(ref.folder, ref.name, src.page.hash, once)
      setRun({ html: src.html, page: src.page, hash: res.hash, token: res.token || '' })
      setRead((r) => ({ ...r, consent: 'allowed' }))
      setSheet(false)
      setFrameKey((k) => k + 1)
      refreshSharedPages()
    } catch (e) {
      if (e?.status === 409) {
        await load({ quiet: true })
        setSheetError(tp('trust.changedMeanwhile'))
      } else {
        setSheetError(e?.message || 'error')
      }
    } finally {
      setBusy(false)
    }
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
  } else if (state === 'needsAllow' || (ref?.folder && !run)) {
    body = (
      <div className="wk-ptile__calm" data-testid="page-tile-allow">
        <Icon name="lock" className="wk-ptile__calm-icon" />
        <p className="wk-ptile__calm-title">{t(read.consent === 'changed' ? 'wellKnown.home.page.changed' : 'wellKnown.home.page.notAllowed')}</p>
        <p className="wk-ptile__calm-text">{t('wellKnown.home.page.allowHint', { author: page?.author_name || page?.author_id || '' })}</p>
        <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" onClick={() => { setSheetError(''); setSheet(true) }} data-testid="page-tile-allow-open"><Icon name="shield" /> {t('wellKnown.home.page.allow')}</button>
      </div>
    )
  } else if (failed) {
    body = (
      <div className="wk-ptile__calm" role="alert" data-testid="page-tile-navigated">
        <p className="wk-ptile__calm-text">{tp('view.navigated')}</p>
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={refresh}><Icon name="refresh" /> {tp('view.reload')}</button>
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
        visible={frame.visible}
        className="wk-ptile__frame"
        onSend={onSend}
        onNavigated={() => setFailed(true)}
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
      data-frame={frame.alive ? (frame.visible ? 'live' : 'paused') : 'none'}
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
      {sheet && read.src && (
        <TrustSheet
          src={read.src}
          changed={read.consent === 'changed'}
          busy={busy}
          error={sheetError}
          covered={source}
          onCancel={() => { setSheet(false); setSource(false) }}
          onAllowOnce={() => allow(true)}
          onAllow={() => allow(false)}
          onSource={() => setSource(true)}
        />
      )}
      {source && read.src && <SourceSheet src={read.src} onClose={() => setSource(false)} />}
    </section>
  )
}
