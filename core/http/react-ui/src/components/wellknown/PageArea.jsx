/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useLayoutEffect, useMemo, useReducer, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import Icon from '../Icon'
import PageFrame from '../PageFrame'
import PageLinkAsk from '../PageLinkAsk'
import TrustSheet, { SourceSheet } from '../pages/TrustSheet'
import { Button, Callout, StatusChip } from '../ui'
import { usePageLinks } from '../../hooks/usePageLinks'
import { useTilePage } from '../../hooks/useTilePage'
import { useFrameLife } from '../../hooks/useFrameLife'
import { useTheme } from '../../contexts/ThemeContext'
import { filesApi } from '../../utils/api'
import { liveFrames } from '../../utils/liveFrames'
import { tilePagePath, tileTitle } from '../../utils/pageTiles'
import { AREA_IDLE, AREA_PAINT_MS, areaStep, areaTarget, areaView, createAreaSizer, revertTarget } from '../../utils/pageArea'
import '../../pages/pages.css'
import './pageArea.css'

let nextArea = 0

// FailureTile is what the area shows when its page did not load: why, and the
// three things the person can do. Only the area is replaced; the composer and
// the widgets keep working.
function FailureTile({ reason, detail, name, pageRef, wk, onReverted, onRemoved }) {
  const { t } = useTranslation('workspace')
  const [busy, setBusy] = useState('')
  const [note, setNote] = useState('')
  const target = revertTarget(reason, pageRef)
  const file = reason === 'invalid' || !name ? '/LocalAI' : `/pages/${name}`

  const revert = async () => {
    setBusy('revert')
    setNote('')
    try {
      if (target === 'home') {
        await wk.revert()
      } else {
        // The last change of the page's folder that was not an undo.
        const h = await filesApi.history(`/pages/${name}`)
        const last = (h.entries || []).find((e) => e.kind !== 'undo')
        if (!last) {
          setNote(t('wellKnown.home.area.nothingToRevert'))
          return
        }
        await filesApi.undo(last.rev)
      }
      onReverted()
    } catch (e) {
      setNote(e?.message || t('wellKnown.home.area.revertFailed'))
    } finally {
      setBusy('')
    }
  }
  const remove = async () => {
    setBusy('remove')
    setNote('')
    try {
      await onRemoved()
    } catch (e) {
      setNote(e.conflict ? t('wellKnown.conflict') : (e?.message || t('wellKnown.home.area.removeFailed')))
      setBusy('')
    }
  }

  const why = reason === 'script' && detail?.line
    ? t('wellKnown.home.area.why.scriptLine', { line: detail.line })
    : t(`wellKnown.home.area.why.${reason}`, { name: name || '' })
  return (
    <Callout
      tone="warn" role="alert" className="wk-area__failed" testId="page-area-failed" data-reason={reason}
      title={t('wellKnown.home.area.failedTitle')}
      actions={(
        <>
          {target && <Button variant="primary" size="sm" icon="undo" busy={busy === 'revert'} disabled={!!busy} onClick={revert} data-testid="page-area-revert">{t('wellKnown.home.area.revert')}</Button>}
          <Button size="sm" icon="file" to={`/app/files?path=${encodeURIComponent(file)}`} data-testid="page-area-open-file">{t('wellKnown.home.area.openFile')}</Button>
          <Button size="sm" icon="close" busy={busy === 'remove'} disabled={!!busy} onClick={remove} data-testid="page-area-remove">{t('wellKnown.home.area.remove')}</Button>
        </>
      )}
    >
      <span data-testid="page-area-why">{why}</span>
      {note && <span className="wk-area__note" role="status" data-testid="page-area-note"> {note}</span>}
    </Callout>
  )
}

// AreaPage runs the page of the area: reads it, builds its frame in the
// sandbox of every page, watches the signals the shell sees and shows the
// strip above it. The page gets mode area.
function AreaPage({ pageRef: ref, wk, onRemoved, listed }) {
  const { t } = useTranslation('workspace')
  const { t: tp } = useTranslation('pages')
  const { theme } = useTheme()
  const navigate = useNavigate()
  const id = useRef(`area-${++nextArea}`).current
  const [el, setEl] = useState(null)
  const tile = useTilePage(ref, { widget: false })
  const { read, state, run, runnable, files, frameRef, load, onRefused, sheet } = tile
  const [life, dispatch] = useReducer(areaStep, AREA_IDLE)
  const [frameKey, setFrameKey] = useState(0)
  const links = usePageLinks()
  // The height the page reports (localai.page.resize): the area follows it,
  // within the limits of pageArea.css. Set on the body as a custom property,
  // never by a reload of the frame. A page that never reports keeps the
  // default height and scrolls inside.
  const [sizer] = useState(() => {
    const box = { el: null }
    const s = createAreaSizer({
      apply: (h) => {
        box.el?.style.setProperty('--wk-area-h', `${h}px`)
        box.el?.setAttribute('data-height', String(h))
      },
    })
    s.box = box
    return s
  })
  useEffect(() => { sizer.box.el = el }, [el, sizer])
  useEffect(() => () => sizer.stop(), [sizer])
  const onResize = useCallback((h) => sizer.offer(h), [sizer])

  const { frame, capped, runNow } = useFrameLife(el, id, { runnable, stopped: life.phase === 'failed' })
  const html = run?.html ?? read.html

  // The signals: the frame was built (start), its first beat came (ready),
  // nothing came in time (timeout), the budget stopped it (stopped). The start
  // and its timer are set in the commit that builds the frame (layout effects),
  // before the frame's document parses: a page that blocks the thread would
  // otherwise hold back the timer that is meant to catch it.
  useLayoutEffect(() => {
    if (frame.alive && life.phase === 'idle') dispatch({ type: 'start' })
    // A frame taken down (every live frame of the app is taken) is not failing.
    if (!frame.alive && (life.phase === 'starting' || life.phase === 'running')) dispatch({ type: 'reset' })
  }, [frame.alive, life.phase])
  useLayoutEffect(() => {
    if (life.phase !== 'starting') return undefined
    const timer = setTimeout(() => dispatch({ type: 'timeout' }), AREA_PAINT_MS)
    return () => clearTimeout(timer)
  }, [life.phase, frameKey])
  // A new version of the page is a new start: a broken edit shows at once.
  const lastHtml = useRef(html)
  useEffect(() => {
    if (lastHtml.current !== html && life.phase === 'running') dispatch({ type: 'reload' })
    lastHtml.current = html
  }, [html, life.phase])

  const page = run?.page || read.page || listed
  const title = tileTitle({ page: ref.name }, page)
  const icon = page?.icon || (ref.folder ? 'users' : 'file-code')
  const view = areaView({ target: { kind: 'page', ref }, enabled: true, read: state, life })

  // The page is read again before the frame starts: a frame built from the
  // version that failed would fail again.
  const reload = async () => {
    await load({ quiet: true })
    dispatch({ type: 'reload' })
    setFrameKey((k) => k + 1)
  }
  const allow = async (once) => {
    if (await tile.allow(once)) setFrameKey((k) => k + 1)
  }
  const onSend = useCallback(() => Promise.reject(new Error('a page on Home cannot send messages; open the page')), [])
  const ask = () => navigate('/app/chats', { state: { draft: t('wellKnown.home.area.askPrompt', { name: ref.name }) } })
  const shared = !!ref.folder
  const by = page?.updated_by === 'assistant' ? 'assistant' : 'you'

  let body
  if (view.view === 'failed') {
    return (
      <FailureTile
        reason={view.reason} detail={life.detail} name={ref.name} pageRef={ref} wk={wk}
        onReverted={reload} onRemoved={onRemoved}
      />
    )
  } else if (view.view === 'loading') {
    body = <div className="skeleton skeleton--block wk-area__fill" role="status" aria-label={t('wellKnown.home.area.loading')} data-testid="page-area-loading" />
  } else if (view.view === 'allow') {
    body = (
      <div className="wk-area__calm" data-testid="page-area-allow">
        <Icon name="lock" className="wk-area__calm-icon" />
        <p className="wk-area__calm-title">{t(read.consent === 'changed' ? 'wellKnown.home.page.changed' : 'wellKnown.home.page.notAllowed')}</p>
        <p className="wk-area__calm-text">{t('wellKnown.home.page.allowHint', { author: page?.author_name || page?.author_id || '' })}</p>
        <Button variant="primary" size="sm" icon="shield" onClick={tile.openSheet} data-testid="page-area-allow-open">{t('wellKnown.home.page.allow')}</Button>
      </div>
    )
  } else if (capped && !frame.alive) {
    body = (
      <div className="wk-area__calm" data-testid="page-area-static">
        <Icon name="pause" className="wk-area__calm-icon" />
        <p className="wk-area__calm-text">{t('wellKnown.frames.capped', { max: liveFrames.max() })}</p>
        <Button size="sm" icon="play" onClick={runNow} data-testid="page-area-run">{t('wellKnown.frames.run')}</Button>
      </div>
    )
  } else if (!frame.alive) {
    body = <div className="wk-area__fill wk-area__idle" aria-hidden="true" data-testid="page-area-idle" />
  } else {
    body = (
      <PageFrame
        key={frameKey}
        ref={frameRef}
        page={page}
        html={html}
        theme={theme}
        title={t('wellKnown.home.area.frameTitle', { title })}
        files={files}
        codeFiles={shared ? (page.code_files || []) : null}
        mode="area"
        budget
        visible={frame.visible}
        className="wk-area__frame"
        onSend={onSend}
        onNavigated={() => dispatch({ type: 'navigated' })}
        onStopped={(reason) => dispatch({ type: 'stopped', reason })}
        onReady={() => dispatch({ type: 'ready' })}
        onScriptError={(e) => dispatch({ type: 'error', detail: { message: e?.message, line: e?.line } })}
        onResize={onResize}
        onLink={links.onLink}
        onRefused={shared ? onRefused : undefined}
      />
    )
  }

  return (
    <section
      className="wk-area" aria-label={t('wellKnown.home.area.label')} data-testid="page-area"
      data-state={view.view} data-frame={frame.alive ? (frame.visible ? 'live' : 'paused') : capped ? 'static' : 'none'}
    >
      <div className="wk-area__bar" data-testid="page-area-bar">
        <span className="wk-area__name"><Icon name={icon} className="wk-area__icon" /><span className="wk-area__title" data-testid="page-area-title">{title}</span></span>
        {page && (
          <span className="wk-area__pills">
            <StatusChip tone="accent" icon={shared ? 'users' : by === 'assistant' ? 'sparkles' : 'user'} testId="page-area-origin">
              {shared ? t('wellKnown.home.area.byAuthor', { author: page.author_name || page.author_id || '' }) : t(`wellKnown.home.area.by.${by}`)}
            </StatusChip>
            <StatusChip tone="idle" icon="user" title={t('wellKnown.home.area.runsAsYouHint')} testId="page-area-runs-as-you">{t('wellKnown.home.area.runsAsYou')}</StatusChip>
          </span>
        )}
        <span className="wk-area__tools">
          <Button variant="ghost" size="sm" icon="refresh" onClick={reload} data-testid="page-area-reload">{t('wellKnown.home.area.reload')}</Button>
          {!shared && <Button variant="ghost" size="sm" icon="pencil" onClick={ask} title={t('wellKnown.home.area.askTitle')} data-testid="page-area-edit">{t('wellKnown.home.area.edit')}</Button>}
          <Button variant="ghost" size="sm" icon="arrow-right" to={tilePagePath(ref)} aria-label={t('wellKnown.home.page.open')} title={t('wellKnown.home.page.open')} data-testid="page-area-open" />
        </span>
      </div>
      <div className="wk-area__body" ref={setEl}>
        {body}
        {links.link && (
          <div className="wk-area__ask">
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

// PageArea is the area of Home below the composer, from the pageArea of
// home.json. The shell draws the strip and the failure tile; the page fills
// the rest and is clipped to it. Nothing else on Home depends on it.
//
//   value    the pageArea value of home.json
//   enabled  false when an administrator turned the page area off; null while
//            that is not known
//   wk       useWellKnown('home'), for Revert and Remove from Home
//   listed   the pages list, for the title before the page is read
export default function PageArea({ value, enabled, wk, pages = [] }) {
  const { t } = useTranslation('workspace')
  const target = useMemo(() => areaTarget(value), [JSON.stringify(value)]) // eslint-disable-line react-hooks/exhaustive-deps
  const top = areaView({ target, enabled, read: 'ready', life: AREA_IDLE })
  const remove = useCallback(async () => {
    await wk.save({ pageArea: null }, { version: wk.doc?.version ?? 0 })
  }, [wk])

  if (top.view === 'none') return null
  if (top.view === 'off') {
    return (
      <Callout tone="info" className="wk-area__off" testId="page-area-off" title={t('wellKnown.home.area.off')}>
        {t('wellKnown.home.area.offHint')}
      </Callout>
    )
  }
  if (top.view === 'loading') return <div className="wk-area wk-area--wait" data-testid="page-area-wait" aria-busy="true"><div className="skeleton skeleton--block wk-area__fill" /></div>
  if (top.view === 'failed') {
    return <FailureTile reason={top.reason} name="" pageRef={null} wk={wk} onReverted={() => wk.reload()} onRemoved={remove} />
  }
  return (
    <AreaPage
      key={`${target.ref.folder}/${target.ref.name}`}
      pageRef={target.ref} wk={wk} onRemoved={remove}
      listed={target.ref.folder ? undefined : pages.find((p) => p.name === target.ref.name)}
    />
  )
}
