/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../components/Icon'
import PageFrame from '../components/PageFrame'
import PageLinkAsk from '../components/PageLinkAsk'
import { usePageLinks } from '../hooks/usePageLinks'
import TrustSheet, { SourceSheet } from '../components/pages/TrustSheet'
import { useTheme } from '../contexts/ThemeContext'
import { chatsApi, sharedPagesApi } from '../utils/api'
import { CAP_CHAT } from '../utils/capabilities'
import { readLastModel } from '../utils/lastModel'
import { isPageName } from '../utils/pages'
import { folderLabel, refreshSharedPages } from '../hooks/useSharedPages'
import { SendAsk } from './PageView'
import './pages.css'

const MAX_ASKS = 3
const POLL = 30_000
let nextAsk = 0

// The pill of the strip for each state of the consent (mockups 12, 13, 14).
const PILL = {
  notYet: { tone: 'warn', icon: 'warning' },
  not: { tone: 'warn', icon: 'warning' },
  changed: { tone: 'warn', icon: 'warning' },
  once: { tone: 'ok', icon: 'check-circle' },
  allowed: { tone: 'ok', icon: 'check-circle' },
}

// /app/pages/shared/:folder/:name: a page of a shared folder. Nothing in it
// runs until the viewer allows its version in the trust sheet: the frame is
// built only with a consent, every call of its bridge names the version that
// runs, and a refused call (the consent was revoked, here or elsewhere, or an
// administrator turned shared pages off) takes the frame down.
export default function SharedPageView() {
  const { folder, name } = useParams()
  const { t } = useTranslation('pages')
  const { theme } = useTheme()
  const [src, setSrc] = useState(null) // the answer of GET, as last read
  const [status, setStatus] = useState('loading') // loading, ready, notfound, off, error
  const [error, setError] = useState('')
  const [run, setRun] = useState(null) // {html, page, hash, token}: the version that runs
  const [sheet, setSheet] = useState(null) // 'first' | 'changed' | null
  const [declined, setDeclined] = useState(false)
  const [stopped, setStopped] = useState('') // why a running page was stopped
  const [source, setSource] = useState(false)
  const [busy, setBusy] = useState(false)
  const [sheetError, setSheetError] = useState('')
  const [newer, setNewer] = useState(null) // a newer version while one runs
  const [menu, setMenu] = useState(false)
  const [frameKey, setFrameKey] = useState(0)
  const [navigated, setNavigated] = useState(false)
  const [asks, setAsks] = useState([])
  const [chats, setChats] = useState([])
  const frameRef = useRef(null)
  const links = usePageLinks()
  const asksRef = useRef(asks)
  asksRef.current = asks
  const runRef = useRef(run)
  runRef.current = run

  const load = useCallback(async ({ quiet = false } = {}) => {
    if (!isPageName(name) || !/^\d+$/.test(folder || '')) {
      setStatus('notfound')
      return null
    }
    try {
      const res = await sharedPagesApi.get(folder, name)
      setSrc(res)
      setStatus('ready')
      return res
    } catch (e) {
      if (quiet) return null
      if (e?.body?.error?.type === 'shared_pages_off') setStatus('off')
      else setStatus(e?.status === 404 || e?.status === 400 ? 'notfound' : 'error')
      setError(e?.message || '')
      return null
    }
  }, [folder, name])

  // The first read decides: an allowed version runs at once, anything else
  // opens the sheet and runs nothing.
  useEffect(() => {
    let live = true
    setSrc(null); setRun(null); setSheet(null); setDeclined(false); setStopped(''); setNewer(null); setAsks([])
    load().then((res) => {
      if (!live || !res) return
      if (res.consent?.state === 'allowed') setRun({ html: res.html, page: res.page, hash: res.page.hash, token: '' })
      else setSheet(res.consent?.state === 'changed' ? 'changed' : 'first')
    })
    return () => { live = false }
  }, [load])

  // While a version runs, look for a newer one: it keeps running, and the strip
  // offers to review the change.
  useEffect(() => {
    if (!run) return undefined
    const id = setInterval(async () => {
      if (document.hidden) return
      const res = await load({ quiet: true })
      if (res && runRef.current && res.page.hash !== runRef.current.hash) setNewer(res)
    }, POLL)
    return () => clearInterval(id)
  }, [run, load])

  const allow = async (once) => {
    const target = newer || src
    setBusy(true)
    setSheetError('')
    try {
      const res = await sharedPagesApi.allow(folder, name, target.page.hash, once)
      setRun({ html: target.html, page: target.page, hash: res.hash, token: res.token || '' })
      setSrc(target)
      setNewer(null)
      setSheet(null)
      setDeclined(false)
      setStopped('')
      setNavigated(false)
      setFrameKey((k) => k + 1)
      refreshSharedPages()
    } catch (e) {
      if (e?.status === 409) {
        // The author changed it while the sheet was open: show the new one.
        const fresh = await load({ quiet: true })
        setSheetError(t('trust.changedMeanwhile'))
        if (fresh) setSheet(fresh.consent?.state === 'changed' ? 'changed' : 'first')
      } else {
        setSheetError(e?.message || 'error')
      }
    } finally {
      setBusy(false)
    }
  }

  const cancel = () => {
    setSheet(null)
    setSource(false)
    if (!run) setDeclined(true)
  }

  const revoke = async () => {
    setMenu(false)
    try {
      await sharedPagesApi.revoke(folder, name)
    } catch (e) {
      setError(e?.message || '')
    }
    setRun(null)
    setStopped('revoked')
    setDeclined(true)
    refreshSharedPages()
    load({ quiet: true })
  }

  const onRefused = useCallback((e) => {
    setRun(null)
    setStopped(e?.body?.error?.type === 'shared_pages_off' ? 'off' : 'refused')
    setDeclined(true)
    load({ quiet: true })
  }, [load])

  const frame = run ? { hash: run.hash, token: run.token } : null
  const files = useMemo(() => (frame ? {
    read: (path) => sharedPagesApi.read(folder, name, frame, path),
    readImage: (path) => sharedPagesApi.readImage(folder, name, frame, path),
    write: (path, content, base) => sharedPagesApi.write(folder, name, frame, path, content, base),
    list: async (dir) => (await sharedPagesApi.files(folder, name, frame, dir)).entries || [],
  } : null), [folder, name, frame?.hash, frame?.token]) // eslint-disable-line react-hooks/exhaustive-deps

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
      const res = await sharedPagesApi.send(folder, name, frame, a.text, target, readLastModel(CAP_CHAT))
      a.resolve({ sent: true, chat_id: res.chat_id })
      patchAsk(a.id, { status: 'sent', result: res })
    } catch (e) {
      patchAsk(a.id, { status: 'idle', error: e?.message || 'error' })
      if (e?.status === 403) onRefused(e)
    }
  }
  const dismissAsk = (a) => {
    if (a.status !== 'sent') a.resolve({ sent: false })
    setAsks((list) => list.filter((x) => x.id !== a.id))
  }

  if (status !== 'ready' && status !== 'loading') {
    const key = status === 'off' ? 'shared.off' : status === 'notfound' ? 'shared.gone' : 'view.failed'
    return (
      <div className="page page--medium pg-page" data-testid="page-view">
        <div className="empty-state" data-testid={`shared-${status}`}>
          <h1 className="empty-state-title">{t(key, { message: error })}</h1>
          <div className="empty-state__actions">
            <Link className="btn btn-secondary" to="/app/pages"><Icon name="arrow-left" /> {t('view.back')}</Link>
          </div>
        </div>
      </div>
    )
  }

  const page = (run?.page) || src?.page
  const author = page ? (page.author_name || page.author_id) : ''
  const folderText = page ? folderLabel(page.folder) : ''
  let pill = 'notYet'
  if (run) pill = run.token ? 'once' : 'allowed'
  else if (src?.consent?.state === 'changed') pill = 'changed'
  else if (declined) pill = 'not'
  const pillText = t(`trust.pill.${pill}`)
  const shownSrc = sheet === 'changed' && newer ? newer : src

  return (
    <div className="pg-view" data-testid="page-view" data-shared="true">
      <header className="pg-strip">
        <span className="icon-chip tone-primary pg-strip__chip"><Icon name="users" /></span>
        <div className="pg-strip__id">
          <h1 className="pg-strip__title">{page?.title || name}</h1>
          <code className="pg-strip__path">{folderText}/pages/{name}</code>
        </div>
        {page && (
          <div className="pg-strip__pills">
            <span className={`pg-pill pg-pill--${PILL[pill].tone}`} data-testid="shared-pill" data-state={pill}>
              <Icon name={PILL[pill].icon} /> {pillText}
            </span>
            <span className="pg-pill pg-pill--plain" data-testid="shared-author"><Icon name="user" /> {t('trust.from', { author })}</span>
            {run && <span className="pg-pill" title={t('view.runsAsYouHint')}><Icon name="user" /> {t('view.runsAsYou')}</span>}
          </div>
        )}
        <div className="pg-strip__actions">
          {run && (
            <button type="button" className="btn btn-ghost btn-sm" onClick={() => { setNavigated(false); setFrameKey((k) => k + 1) }} data-testid="page-reload">
              <Icon name="refresh" /> {t('view.reload')}
            </button>
          )}
          {(run || src?.consent?.state === 'allowed' || src?.consent?.state === 'changed') && (
            <span className="pg-menu">
              <button type="button" className="btn btn-ghost btn-sm btn-icon" aria-haspopup="menu" aria-expanded={menu} aria-label={t('shared.menu')} onClick={() => setMenu((m) => !m)} data-testid="shared-menu">
                <Icon name="more-vertical" />
              </button>
              {menu && (
                <span className="pg-menu__list" role="menu">
                  <button type="button" role="menuitem" onClick={revoke} data-testid="shared-revoke"><Icon name="ban" /> {t('viewer.stopAllowing')}</button>
                </span>
              )}
            </span>
          )}
        </div>
      </header>

      {newer && run && (
        <div className="pg-asks">
          <div className="pg-ask" role="status" data-testid="shared-newer">
            <Icon name="info" className="pg-ask__icon" />
            <span className="pg-ask__label">{t('viewer.authorChanged', { author })}</span>
            <span className="pg-ask__actions"><button type="button" className="btn btn-secondary btn-sm" onClick={() => setSheet('changed')}>{t('shared.review')}</button></span>
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

      <section className="pg-stage" aria-label={t('view.frameTitle', { title: page?.title || name })}>
        <div className="pg-stage__bar">
          <Icon name="lock" /> {run ? t('viewer.sandboxShared', { folder: `${folderText}/pages/${name}` }) : t('trust.frame.chrome', { folder: folderText })}
        </div>
        {status === 'loading' || !src ? (
          <div className="pg-stage__state" role="status"><Icon name="spinner" spin /> {t('view.loading')}</div>
        ) : run && !navigated ? (
          <PageFrame
            key={frameKey}
            ref={frameRef}
            page={run.page}
            html={run.html}
            theme={theme}
            title={t('view.frameTitle', { title: run.page.title })}
            files={files}
            codeFiles={run.page.code_files || []}
            onSend={onPageSend}
            onNavigated={() => setNavigated(true)}
            onLink={links.onLink}
            onRefused={onRefused}
          />
        ) : run && navigated ? (
          <div className="pg-stage__state" role="alert" data-testid="page-navigated">
            <span>{t('view.navigated')}</span>
            <button type="button" className="btn btn-secondary btn-sm" onClick={() => { setNavigated(false); setFrameKey((k) => k + 1) }}>{t('view.reload')}</button>
          </div>
        ) : (
          <div className="pg-blocked" data-testid={declined ? 'shared-blocked' : 'shared-waiting'}>
            <span className="pg-blocked__icon" aria-hidden="true"><Icon name="lock" /></span>
            <h2 className="pg-blocked__title">{declined ? t('trust.blocked.title') : t('trust.waiting.title')}</h2>
            <p className="pg-blocked__body">
              {stopped ? t(`trust.stopped.${stopped}`) : declined ? t('trust.blocked.body', { author, folder: folderText }) : t('trust.waiting.body')}
            </p>
            {stopped !== 'off' && (
              <div className="pg-blocked__acts">
                <button type="button" className="btn btn-primary btn-sm" onClick={() => setSheet(src.consent?.state === 'changed' ? 'changed' : 'first')} data-testid="shared-review">{t('trust.blocked.review')}</button>
                <button type="button" className="btn btn-ghost btn-sm" onClick={() => setSource(true)} data-testid="shared-source"><Icon name="code" /> {t('trust.viewSource')}</button>
              </div>
            )}
          </div>
        )}
      </section>

      {sheet && shownSrc && (
        <TrustSheet
          src={shownSrc}
          changed={sheet === 'changed'}
          busy={busy}
          error={sheetError}
          covered={source}
          onCancel={cancel}
          onAllowOnce={() => allow(true)}
          onAllow={() => allow(false)}
          onSource={() => setSource(true)}
        />
      )}
      {source && shownSrc && <SourceSheet src={shownSrc} onClose={() => setSource(false)} />}
    </div>
  )
}
