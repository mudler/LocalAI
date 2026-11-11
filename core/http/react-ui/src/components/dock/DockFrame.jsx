/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import PageFrame from '../PageFrame'
import PageLinkAsk from '../PageLinkAsk'
import TrustSheet, { SourceSheet } from '../pages/TrustSheet'
import { Button } from '../ui'
import { useHidden } from '../../hooks/useHidden'
import { usePageLinks } from '../../hooks/usePageLinks'
import { useTilePage } from '../../hooks/useTilePage'
import { useTheme } from '../../contexts/ThemeContext'
import { pagesApi, sharedPagesApi } from '../../utils/api'
import { CAP_CHAT } from '../../utils/capabilities'
import { readLastModel } from '../../utils/lastModel'
import { liveFrames } from '../../utils/liveFrames'

let nextFrame = 0
// The messages a docked page may have waiting for the person at once.
const MAX_ASKS = 3
let nextAsk = 0
// Send of a message the page asks for works only after this delay, so a page
// cannot put the button under a click the person meant for the page.
const ASK_ARM_MS = 500

// DockFrame runs the page of one tab of the dock: the same frame, sandbox,
// bridge and budget as a tile, in the page mode. Its frame counts toward the
// live frames of the app (liveFrames): with all of them taken it shows a card
// that runs on a click, and a frame over its budget stops with Reload. A
// shared page runs only once the viewer allowed it on Home, in the slots and
// in the dock, because the dock starts it again without a click on a reload.
//
// chat.send from the page goes to this chat as a message the person sees,
// sends and can dismiss; nothing is sent by the page alone. The questions lie
// over the top of the frame, so the frame does not move when one comes, and
// their Send works after ASK_ARM_MS.
//
//   tabRef   {name, folder} of the page
//   title    the name of the page for the frame
//   chatId   the chat of the dock: where chat.send goes
//   visible  the dock shows and this tab is in front
//   fresh    the person (or the assistant) just opened the page: it takes a
//            frame even when all are held, as a click on a static card does
//   onState  called with the page as read ({page, state}) for the strip
const DockFrame = forwardRef(function DockFrame({ tabRef, title, chatId, visible, fresh, onState }, ref) {
  const { t } = useTranslation('pages')
  const { t: tw } = useTranslation('workspace')
  const { theme } = useTheme()
  const id = useRef(`dock-${++nextFrame}`).current
  const hidden = useHidden()
  const tile = useTilePage(tabRef, { widget: false })
  const { read, state, run, runnable, files, frameRef, onRefused, sheet } = tile
  const links = usePageLinks()
  const [live, setLive] = useState(false)
  const [stopped, setStopped] = useState('')
  const [frameKey, setFrameKey] = useState(0)
  const [wake, setWake] = useState(0)
  const [asks, setAsks] = useState([])
  const asksRef = useRef(asks)
  asksRef.current = asks
  const freshRef = useRef(fresh)

  const page = run?.page || read.page
  const wantsFrame = runnable && !stopped && !!page

  useEffect(() => { onState?.({ page, state }) }, [page, state]) // eslint-disable-line react-hooks/exhaustive-deps

  const onEvict = useCallback(() => setLive(false), [])
  useEffect(() => liveFrames.subscribe(() => setWake((n) => n + 1)), [])
  useEffect(() => {
    if (!wantsFrame) {
      liveFrames.release(id)
      setLive(false)
      return
    }
    if (liveFrames.holds(id)) return
    // A page just opened takes a frame as a click does; one that comes back
    // with the chat waits for a free one.
    const got = freshRef.current ? liveFrames.take(id, onEvict) : liveFrames.acquire(id, onEvict)
    freshRef.current = false
    setLive(got)
  }, [wantsFrame, id, wake, onEvict])
  useEffect(() => () => liveFrames.release(id), [id])

  const reload = useCallback(() => {
    setStopped('')
    setFrameKey((k) => k + 1)
    tile.load({ quiet: true })
  }, [tile])
  useImperativeHandle(ref, () => ({ reload }), [reload])

  const runNow = () => {
    if (liveFrames.take(id, onEvict)) setLive(true)
  }
  const allow = async (once) => {
    if (await tile.allow(once)) setFrameKey((k) => k + 1)
  }

  // chat.send: shown to the person in the dock, sent to this chat only when
  // they press Send. The page gets {sent, chat_id} or {sent: false}.
  const onSend = useCallback((text) => new Promise((resolve, reject) => {
    if (!chatId) {
      reject(new Error('this chat has not started yet; send a message first'))
      return
    }
    if (asksRef.current.length >= MAX_ASKS) {
      reject(new Error('too many messages are waiting'))
      return
    }
    const item = { id: ++nextAsk, text, resolve, status: 'idle', error: '', armed: false }
    asksRef.current = [...asksRef.current, item]
    setAsks((list) => [...list, item])
    setTimeout(() => setAsks((list) => list.map((a) => (a.id === item.id ? { ...a, armed: true } : a))), ASK_ARM_MS)
  }), [chatId])
  // Leaving the chat or the page answers what still waits.
  useEffect(() => () => { for (const a of asksRef.current) a.resolve({ sent: false }) }, [chatId, tabRef])
  const patchAsk = (aid, patch) => setAsks((list) => list.map((a) => (a.id === aid ? { ...a, ...patch } : a)))
  const sendAsk = async (a) => {
    patchAsk(a.id, { status: 'sending', error: '' })
    try {
      const model = readLastModel(CAP_CHAT)
      const res = tabRef.folder
        ? await sharedPagesApi.send(tabRef.folder, tabRef.name, run || {}, a.text, chatId, model)
        : await pagesApi.send(tabRef.name, a.text, chatId, model)
      a.resolve({ sent: true, chat_id: res.chat_id })
      setAsks((list) => list.filter((x) => x.id !== a.id))
    } catch (e) {
      patchAsk(a.id, { status: 'idle', error: e?.message || 'error' })
    }
  }
  const dismissAsk = (a) => {
    a.resolve({ sent: false })
    setAsks((list) => list.filter((x) => x.id !== a.id))
  }

  let body = null
  if (state === 'loading') {
    body = <div className="pd-state" role="status" data-testid="dock-loading"><Icon name="spinner" spin /> {t('view.loading')}</div>
  } else if (state === 'missing') {
    body = <div className="pd-state" role="note" data-testid="dock-missing"><Icon name="file" /> {t('dock.missing', { name: tabRef.name })}</div>
  } else if (state === 'off') {
    body = <div className="pd-state" role="note" data-testid="dock-off"><Icon name="lock" /> {t('dock.off')}</div>
  } else if (state === 'error') {
    body = (
      <div className="pd-state" role="alert" data-testid="dock-error">
        <span>{t('dock.error')}</span>
        <Button size="sm" onClick={() => tile.load()}>{t('retry')}</Button>
      </div>
    )
  } else if (!runnable) {
    body = (
      <div className="pd-state" data-testid="dock-allow">
        <Icon name="lock" className="pd-state__icon" />
        <span>{t('dock.allowText')}</span>
        <Button size="sm" variant="primary" onClick={tile.openSheet} data-testid="dock-allow-open">{t('dock.allow')}</Button>
      </div>
    )
  } else if (stopped) {
    body = (
      <div className="pd-state" role="alert" data-testid="dock-stopped" data-reason={stopped}>
        <span>{stopped === 'navigated' ? t('view.navigated') : tw(`wellKnown.frames.why.${stopped}`)}</span>
        <Button size="sm" icon="refresh" onClick={reload}>{t('view.reload')}</Button>
      </div>
    )
  } else if (!live) {
    body = (
      <div className="pd-state" data-testid="dock-static">
        <span>{tw('wellKnown.frames.capped', { max: liveFrames.max() })}</span>
        <Button size="sm" icon="play" onClick={runNow} data-testid="dock-run">{t('dock.run')}</Button>
      </div>
    )
  }

  return (
    <div className="pd-body" data-testid="dock-body" data-state={state} data-frame={body ? 'none' : 'live'}>
      {(asks.length > 0 || links.link) && (
        <div className="pd-asks" data-testid="dock-asks">
          {asks.map((a) => (
            <div key={a.id} className="pd-ask" role="group" aria-label={t('dock.sendLabel')} data-testid="dock-send-ask">
              <span className="pd-ask__label">{t('dock.sendLabel')}</span>
              <q className="pd-ask__text">{a.text}</q>
              <span className="pd-ask__acts">
                <Button size="sm" variant="primary" disabled={!a.armed} busy={a.status === 'sending'} onClick={() => sendAsk(a)} data-testid="dock-send">{t('send.send')}</Button>
                <Button size="sm" variant="ghost" onClick={() => dismissAsk(a)}>{t('send.dismiss')}</Button>
              </span>
              {a.error && <span className="pd-ask__error" role="alert">{t('send.failed', { message: a.error })}</span>}
            </div>
          ))}
          {links.link && (
            <div className="pd-ask">
              <PageLinkAsk key={links.link.id} link={links.link} t={t} onOpen={links.open} onCancel={links.cancel} />
            </div>
          )}
        </div>
      )}
      {body || (
        <div className="pd-stage">
          <PageFrame
            key={frameKey}
            ref={frameRef}
            page={page}
            html={run?.html ?? read.html}
            theme={theme}
            title={t('view.frameTitle', { title })}
            files={files}
            codeFiles={tabRef.folder ? (page.code_files || []) : null}
            budget
            visible={visible && !hidden}
            className="pd-frame"
            onSend={onSend}
            onNavigated={() => setStopped('navigated')}
            onStopped={setStopped}
            onLink={links.onLink}
            onRefused={tabRef.folder ? onRefused : undefined}
          />
        </div>
      )}
      {sheet.open && read.src && (
        <TrustSheet
          src={read.src} changed={read.consent === 'changed'} embed busy={sheet.busy} error={sheet.error} covered={sheet.source}
          onCancel={tile.closeSheet} onAllowOnce={() => allow(true)} onAllow={() => allow(false)} onSource={() => tile.showSource(true)}
        />
      )}
      {sheet.source && read.src && <SourceSheet src={read.src} onClose={() => tile.showSource(false)} />}
    </div>
  )
})

export default DockFrame
