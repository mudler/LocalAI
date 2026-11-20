import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../components/Icon'
import PageFrame from '../components/PageFrame'
import PageLinkAsk from '../components/PageLinkAsk'
import PageShareDialog from '../components/pages/PageShareDialog'
import TrustSheet, { SourceSheet } from '../components/pages/TrustSheet'
import { useAuth } from '../context/AuthContext'
import { refreshPages, usePages } from '../hooks/usePages'
import { usePageLinks } from '../hooks/usePageLinks'
import { usePinnedPages } from '../hooks/usePinnedPages'
import Switch from '../components/ui/Switch'
import { Button } from '../components/ui'
import { useTheme } from '../contexts/ThemeContext'
import { chatsApi, pagesApi } from '../utils/api'
import { CAP_CHAT } from '../utils/capabilities'
import { readLastModel } from '../utils/lastModel'
import { feedPaths, isPageName, pageChanges, pageIndexPath, trusted } from '../utils/pages'
import { useWorkspaceFeed } from '../hooks/workspaceFeed'
import { usePageAge } from '../hooks/usePages'
import './pages.css'

const MAX_ASKS = 3
let nextAsk = 0

// A message the page wants to send. Nothing reaches a chat until the person
// picks the chat and presses Send; Dismiss answers the page {sent: false}.
export function SendAsk({ ask, chats, onSend, onDismiss, t }) {
  const [target, setTarget] = useState('')
  if (ask.status === 'sent') {
    return (
      <div className="pg-ask pg-ask--done" role="status" data-testid="page-send-done">
        <Icon name="check-circle" className="pg-ask__icon" />
        <span className="pg-ask__label">{t('send.sent', { title: ask.result.title })}</span>
        <span className="pg-ask__actions">
          <Link className="btn btn-secondary btn-sm" to={`/app/chats/${encodeURIComponent(ask.result.chat_id)}`}>{t('send.openChat')}</Link>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onDismiss}>{t('send.dismiss')}</button>
        </span>
      </div>
    )
  }
  return (
    <div className="pg-ask" role="group" aria-label={t('send.label')} data-testid="page-send-ask">
      <span className="pg-ask__label">{t('send.label')}</span>
      <q className="pg-ask__text">{ask.text}</q>
      <span className="pg-ask__actions">
        <label className="pg-ask__to">
          <span>{t('send.to')}</span>
          <select className="input input-sm" value={target} onChange={(e) => setTarget(e.target.value)} data-testid="page-send-target">
            <option value="">{t('send.pageChat')}</option>
            {chats.map((c) => <option key={c.id} value={c.id}>{c.title}</option>)}
          </select>
        </label>
        <button type="button" className="btn btn-primary btn-sm" disabled={ask.status === 'sending'} onClick={() => onSend(target)}>
          {ask.status === 'sending' ? t('send.sending') : t('send.send')}
        </button>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onDismiss}>{t('send.dismiss')}</button>
      </span>
      {ask.error && <span className="pg-ask__error" role="alert">{t('send.failed', { message: ask.error })}</span>}
    </div>
  )
}

// /app/pages/:name: one page of the workspace in its sandbox, with the strip
// that says what it is and who changed it last.
export default function PageView() {
  const { name } = useParams()
  const { t } = useTranslation('pages')
  const { theme } = useTheme()
  const navigate = useNavigate()
  const [source, setSource] = useState(null) // {page, html}
  const [status, setStatus] = useState('loading') // loading, ready, notfound, error
  const [error, setError] = useState('')
  const [frameKey, setFrameKey] = useState(0)
  const [navigated, setNavigated] = useState(false)
  const [asks, setAsks] = useState([])
  const [chats, setChats] = useState([])
  const frameRef = useRef(null)
  const links = usePageLinks()
  const age = usePageAge()
  const { authEnabled } = useAuth()
  const { pages: ownPages } = usePages(true)
  const listedShared = Number(ownPages.find((p) => p.name === name)?.shared_with) || 0
  // Sharing: the dialog, what it last read, and how many people the page
  // will ask again after a change of its code.
  const [sharing, setSharing] = useState(false)
  const [freshShare, setFreshShare] = useState(null)
  const [askAgain, setAskAgain] = useState(0)
  const seenShare = useRef(null)
  // A page that others may edit runs only after its owner allowed the
  // version, as for a shared page: run is {hash, token} of the version that
  // runs, sheet is the trust sheet ('first' or 'changed'), stopped says why a
  // page that ran was taken down.
  const [run, setRun] = useState(null)
  const [sheet, setSheet] = useState(null)
  const [declined, setDeclined] = useState(false)
  const [stopped, setStopped] = useState('')
  const [sourceOpen, setSourceOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [sheetError, setSheetError] = useState('')
  const [menu, setMenu] = useState(false)
  const runRef = useRef(run)
  runRef.current = run
  const asksRef = useRef(asks)
  asksRef.current = asks
  // The start page is the first pinned page. The switch of the bar writes the
  // order of pinnedPages and nothing else.
  const pinned = usePinnedPages(true)
  const isStart = pinned.start === name
  const alone = isStart && pinned.pins.length === 1
  const [startBusy, setStartBusy] = useState(false)
  const [startError, setStartError] = useState('')
  const setStart = async (on) => {
    setStartBusy(true)
    setStartError('')
    try {
      await pinned.setStart(name, on)
    } catch (e) {
      setStartError(e?.message || 'error')
    } finally {
      setStartBusy(false)
    }
  }

  const load = useCallback(async ({ quiet = false } = {}) => {
    if (!isPageName(name)) {
      setStatus('notfound')
      return
    }
    if (!quiet) setStatus('loading')
    try {
      const res = await pagesApi.get(name)
      // A change of data files leaves the HTML as it was: keep the frame.
      setSource((prev) => (prev && prev.html === res.html ? { ...prev, page: res.page, consent: res.consent, lines: res.lines } : res))
      setStatus('ready')
    } catch (e) {
      if (quiet) return
      setStatus(e?.status === 404 || e?.status === 400 ? 'notfound' : 'error')
      setError(e?.message || '')
    }
  }, [name])

  useEffect(() => {
    setSource(null)
    setSharing(false)
    setFreshShare(null)
    setAskAgain(0)
    seenShare.current = null
    setRun(null); setSheet(null); setDeclined(false); setStopped(''); setSourceOpen(false); setSheetError('')
    setAsks([])
    setNavigated(false)
    load()
  }, [load])

  const onFeed = useCallback((ev) => {
    const ch = pageChanges(feedPaths(ev), name)
    if (!ch.all && !ch.index && !ch.meta && ch.rel.length === 0) return
    load({ quiet: true })
    if (ch.rel.length) frameRef.current?.emit('files.changed', { paths: ch.rel })
    else if (ch.all) frameRef.current?.emit('files.changed', { paths: [], resync: true })
  }, [name, load])
  useWorkspaceFeed([`/pages/${name}/**`], onFeed)

  const reload = () => {
    setNavigated(false)
    setFrameKey((k) => k + 1)
    load({ quiet: true })
  }

  // The code of a shared page changed: the people who allowed the version
  // before are asked again. The strip says how many, and does not block. The
  // page's own answer has the version and the count of people who allowed it.
  useEffect(() => {
    const p = source?.page
    if (!p) return
    const prev = seenShare.current
    if (prev && p.hash && prev.hash && p.hash !== prev.hash && prev.allowed > 0) setAskAgain(prev.allowed)
    seenShare.current = { hash: p.hash || '', allowed: Number.isFinite(p.shared_allowed) ? p.shared_allowed : (prev?.allowed ?? 0) }
  }, [source])

  // A page that others may edit: the version that runs, and the trust sheet.
  const guarded = source?.page?.others_can_edit === true
  useEffect(() => {
    const p = source?.page
    if (!p) return
    if (!p.others_can_edit) {
      // The last Edit was taken away: the page opens as any own page.
      if (runRef.current) setRun(null)
      setSheet(null); setDeclined(false); setStopped('')
      return
    }
    const state = source.consent?.state || p.consent || 'none'
    if (runRef.current) {
      // The code changed under a running page: it stops and asks again.
      if (p.hash && runRef.current.hash && p.hash !== runRef.current.hash) {
        setRun(null); setStopped('stale'); setDeclined(true)
      }
      return
    }
    if (state === 'allowed' && !declined && !stopped) setRun({ hash: p.hash, token: '' })
    else if (!declined && !stopped) setSheet(state === 'changed' ? 'changed' : 'first')
  }, [source]) // eslint-disable-line react-hooks/exhaustive-deps

  const allow = async (once) => {
    const target = source
    setBusy(true)
    setSheetError('')
    try {
      const res = await pagesApi.allow(name, target.page.hash, once)
      setRun({ hash: res.hash || target.page.hash, token: res.token || '' })
      setSheet(null); setDeclined(false); setStopped('')
      setNavigated(false)
      setFrameKey((k) => k + 1)
      refreshPages()
    } catch (e) {
      if (e?.status === 409) {
        // The page changed while the sheet was open: show the new version.
        await load({ quiet: true })
        setSheetError(t('trust.changedMeanwhile'))
      } else {
        setSheetError(e?.message || 'error')
      }
    } finally {
      setBusy(false)
    }
  }
  const cancelSheet = () => { setSheet(null); setSourceOpen(false); if (!run) setDeclined(true) }
  const stopAllowing = async () => {
    setMenu(false)
    try { await pagesApi.stopAllowing(name) } catch (e) { setError(e?.message || '') }
    setRun(null); setStopped('revoked'); setDeclined(true)
    load({ quiet: true })
  }
  const onStale = useCallback(() => {
    setRun(null); setStopped('stale'); setDeclined(true)
    load({ quiet: true })
  }, [load])
  const onRefused = useCallback(() => {
    setRun(null); setStopped('refused'); setDeclined(true)
    load({ quiet: true })
  }, [load])
  const frameId = run ? { hash: run.hash, token: run.token } : null
  const guardedFiles = useMemo(() => {
    if (!frameId) return null
    const f = pagesApi.frame(name, frameId)
    const watch = (call) => call().catch((e) => { if (e?.status === 409) onStale(); throw e })
    return {
      read: (path) => watch(() => f.read(path)),
      readImage: (path) => watch(() => f.readImage(path)),
      write: (path, content, base) => watch(() => f.write(path, content, base)),
      list: (dir) => watch(() => f.list(dir)),
    }
  }, [name, frameId?.hash, frameId?.token, onStale]) // eslint-disable-line react-hooks/exhaustive-deps

  const ask = () => navigate('/app/chats', { state: { draft: t('view.askPrompt', { name }) } })

  // chat.send from the page: shown to the person, never sent by itself.
  const onPageSend = useCallback((text) => new Promise((resolve, reject) => {
    if (asksRef.current.filter((a) => a.status !== 'sent').length >= MAX_ASKS) {
      reject(new Error('too many messages are waiting'))
      return
    }
    const item = { id: ++nextAsk, text, resolve, status: 'idle' }
    asksRef.current = [...asksRef.current, item]
    setAsks((list) => [...list, item])
    chatsApi.list(20, 'chat').then((res) => setChats(res.conversations || [])).catch(() => {})
  }), [])

  const patchAsk = (id, patch) => setAsks((list) => list.map((a) => (a.id === id ? { ...a, ...patch } : a)))
  const sendAsk = async (a, target) => {
    patchAsk(a.id, { status: 'sending', error: '' })
    try {
      const res = guarded && frameId
        ? await pagesApi.frame(name, frameId).send(a.text, target, readLastModel(CAP_CHAT))
        : await pagesApi.send(name, a.text, target, readLastModel(CAP_CHAT))
      a.resolve({ sent: true, chat_id: res.chat_id })
      patchAsk(a.id, { status: 'sent', result: res })
    } catch (e) {
      patchAsk(a.id, { status: 'idle', error: e?.message || 'error' })
      if (guarded && e?.status === 403) onRefused()
    }
  }
  const dismissAsk = (a) => {
    if (a.status !== 'sent') a.resolve({ sent: false })
    setAsks((list) => list.filter((x) => x.id !== a.id))
  }

  if (status === 'notfound' || status === 'error') {
    return (
      <div className="page page--medium pg-page" data-testid="page-view">
        <div className="empty-state" data-testid={status === 'notfound' ? 'page-not-found' : 'page-error'}>
          <h1 className="empty-state-title">{status === 'notfound' ? t('view.notFound', { name }) : t('view.failed', { message: error })}</h1>
          <div className="empty-state__actions">
            <Link className="btn btn-secondary" to="/app/pages"><Icon name="arrow-left" /> {t('view.back')}</Link>
            {status === 'error' && <button type="button" className="btn btn-primary" onClick={() => load()}>{t('retry')}</button>}
          </div>
        </div>
      </div>
    )
  }

  const page = source?.page
  const when = page ? age(page.updated_at) : ''
  const by = page?.updated_by === 'assistant' ? 'assistant' : 'you'
  const shared = listedShared > 0 || !freshShare ? { total: listedShared, onlyEveryone: false } : { total: freshShare.total, onlyEveryone: freshShare.everyone && freshShare.people === 0 }

  return (
    <div className="pg-view" data-testid="page-view">
      <header className="pg-strip">
        <span className="icon-chip tone-primary pg-strip__chip"><Icon name={page?.icon || 'file-code'} /></span>
        <div className="pg-strip__id">
          <h1 className="pg-strip__title">{page?.title || name}</h1>
          <code className="pg-strip__path">{pageIndexPath(name)}</code>
        </div>
        {page && (
          <div className="pg-strip__pills">
            <span className="pg-pill" title={t('view.runsAsYouHint')} data-testid="page-runs-as-you">
              <Icon name="user" /> {t('view.runsAsYou')}
            </span>
            {guarded && (
              <span className="pg-pill pg-pill--warn" data-testid="page-others-edit"><Icon name="pencil" /> {t('card.othersEdit')}</span>
            )}
            {guarded && (
              <span className={`pg-pill pg-pill--${run ? 'ok' : 'warn'}`} data-testid="page-consent-pill" data-state={run ? 'allowed' : 'not'}>
                <Icon name={run ? 'check-circle' : 'warning'} /> {t(run ? (run.token ? 'trust.pill.once' : 'trust.pill.allowed') : (stopped === 'stale' ? 'trust.pill.changed' : 'trust.pill.notYet'))}
              </span>
            )}
            {shared.total > 0 && (
              <span className="pg-pill" data-testid="page-shared-pill">
                <Icon name="users" /> {shared.onlyEveryone ? t('card.sharedEveryone') : t('card.sharedWith', { count: shared.total })}
              </span>
            )}
            {isStart && (
              <span className="pg-pill pg-pill--start" data-testid="page-start-badge">
                <Icon name="pin" /> {t('view.startPage')}
              </span>
            )}
            <span className="pg-pill pg-pill--live" data-testid="page-updated">
              <Icon name="check-circle" /> {t(`view.updated.${by}`, { when })}
            </span>
          </div>
        )}
        <div className="pg-strip__actions">
          {page && pinned.loaded && (
            <span className="pg-start" data-testid="page-start">
              <Switch
                checked={isStart} onChange={setStart} disabled={startBusy || alone}
                labelledBy="pg-start-label" describedBy={alone ? 'pg-start-hint' : undefined} testId="page-start-switch"
              />
              <span id="pg-start-label" className="pg-start__label">{t('view.makeStart')}</span>
              {alone && <span id="pg-start-hint" className="pg-start__hint">{t('view.startAlone')}</span>}
            </span>
          )}
          {page && (
            <Button size="sm" icon="users" onClick={() => setSharing(true)} data-testid="page-share">{t('card.share')}</Button>
          )}
          <button type="button" className="btn btn-ghost btn-sm" onClick={reload} data-testid="page-reload">
            <Icon name="refresh" /> {t('view.reload')}
          </button>
          <button type="button" className="btn btn-secondary btn-sm" onClick={ask} data-testid="page-ask">
            <Icon name="sparkles" /> {t('view.ask')}
          </button>
          {guarded && run && (
            <span className="pg-menu">
              <Button variant="ghost" size="sm" icon="more-vertical" aria-haspopup="menu" aria-expanded={menu} aria-label={t('shared.menu')} onClick={() => setMenu((m) => !m)} data-testid="page-menu" />
              {menu && (
                <span className="pg-menu__list" role="menu">
                  <button type="button" role="menuitem" onClick={stopAllowing} data-testid="page-stop-allowing"><Icon name="ban" /> {t('viewer.stopAllowing')}</button>
                </span>
              )}
            </span>
          )}
        </div>
      </header>

      {startError && <p className="pg-asks pg-ask__error" role="alert" data-testid="page-start-error">{t('view.startFailed', { message: startError })}</p>}

      {askAgain > 0 && (
        <div className="pg-asks">
          <div className="pg-ask" role="status" data-testid="page-ask-again">
            <Icon name="info" className="pg-ask__icon" />
            <span className="pg-ask__label">{t('share.askAgain', { count: askAgain })}</span>
          </div>
        </div>
      )}

      {asks.length > 0 && (
        <div className="pg-asks">
          {asks.map((a) => (
            <SendAsk key={a.id} ask={a} chats={chats} t={t} onSend={(target) => sendAsk(a, target)} onDismiss={() => dismissAsk(a)} />
          ))}
        </div>
      )}

      {links.link && (
        <div className="pg-asks">
          <PageLinkAsk key={links.link.id} link={links.link} t={t} onOpen={links.open} onCancel={links.cancel} />
        </div>
      )}

      {sharing && page && (
        <PageShareDialog
          page={page} sharing={authEnabled}
          onChanged={(s) => { setFreshShare(s); if (s.hash && seenShare.current && s.allowed !== null) seenShare.current = { hash: s.hash, allowed: s.allowed } }}
          onLost={() => setSharing(false)}
          close={() => { setSharing(false); load({ quiet: true }) }}
        />
      )}

      <section className="pg-stage" aria-label={t('view.frameTitle', { title: page?.title || name })}>
        <div className="pg-stage__bar"><Icon name="lock" /> {t('view.sandbox')}</div>
        {status === 'loading' || !source ? (
          <div className="pg-stage__state" role="status"><Icon name="spinner" spin /> {t('view.loading')}</div>
        ) : !trusted(page) ? (
          <div className="pg-stage__state" data-testid="page-untrusted">{t('view.untrusted')}</div>
        ) : guarded && !run ? (
          <div className="pg-blocked" data-testid={declined ? 'page-blocked' : 'page-waiting'} data-reason={stopped}>
            <span className="pg-blocked__icon" aria-hidden="true"><Icon name="lock" /></span>
            <h2 className="pg-blocked__title">{declined ? t('trust.blocked.title') : t('trust.waiting.title')}</h2>
            <p className="pg-blocked__body">
              {stopped === 'stale' ? t('trust.owner.stale') : stopped ? t(`trust.stopped.${stopped}`) : declined ? t('trust.owner.blocked') : t('trust.waiting.body')}
            </p>
            <div className="pg-blocked__acts">
              <Button variant="primary" size="sm" onClick={() => setSheet(source.consent?.state === 'changed' || stopped === 'stale' ? 'changed' : 'first')} data-testid="page-review">{t('trust.blocked.review')}</Button>
              <Button variant="ghost" size="sm" icon="code" onClick={() => setSourceOpen(true)} data-testid="page-source">{t('trust.viewSource')}</Button>
            </div>
          </div>
        ) : navigated ? (
          <div className="pg-stage__state" role="alert" data-testid="page-navigated">
            <span>{t('view.navigated')}</span>
            <button type="button" className="btn btn-secondary btn-sm" onClick={reload}>{t('view.reload')}</button>
          </div>
        ) : (
          <PageFrame
            key={frameKey}
            ref={frameRef}
            page={page}
            html={source.html}
            theme={theme}
            title={t('view.frameTitle', { title: page.title })}
            onSend={onPageSend}
            onNavigated={() => setNavigated(true)}
            onLink={links.onLink}
            files={guarded ? guardedFiles : undefined}
            codeFiles={guarded ? (page.code_files || []) : null}
            onRefused={guarded ? onRefused : undefined}
          />
        )}
      </section>

      {sheet && source && (
        <TrustSheet
          src={source} changed={sheet === 'changed'} busy={busy} error={sheetError} covered={sourceOpen}
          onCancel={cancelSheet} onAllowOnce={() => allow(true)} onAllow={() => allow(false)} onSource={() => setSourceOpen(true)}
        />
      )}
      {sourceOpen && source && <SourceSheet src={source} onClose={() => setSourceOpen(false)} />}
    </div>
  )
}
