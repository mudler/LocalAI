import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../components/Icon'
import PageFrame from '../components/PageFrame'
import PageLinkAsk from '../components/PageLinkAsk'
import { usePageLinks } from '../hooks/usePageLinks'
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
  const asksRef = useRef(asks)
  asksRef.current = asks

  const load = useCallback(async ({ quiet = false } = {}) => {
    if (!isPageName(name)) {
      setStatus('notfound')
      return
    }
    if (!quiet) setStatus('loading')
    try {
      const res = await pagesApi.get(name)
      // A change of data files leaves the HTML as it was: keep the frame.
      setSource((prev) => (prev && prev.html === res.html ? { ...prev, page: res.page } : res))
      setStatus('ready')
    } catch (e) {
      if (quiet) return
      setStatus(e?.status === 404 || e?.status === 400 ? 'notfound' : 'error')
      setError(e?.message || '')
    }
  }, [name])

  useEffect(() => {
    setSource(null)
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
      const res = await pagesApi.send(name, a.text, target, readLastModel(CAP_CHAT))
      a.resolve({ sent: true, chat_id: res.chat_id })
      patchAsk(a.id, { status: 'sent', result: res })
    } catch (e) {
      patchAsk(a.id, { status: 'idle', error: e?.message || 'error' })
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
            <span className="pg-pill pg-pill--live" data-testid="page-updated">
              <Icon name="check-circle" /> {t(`view.updated.${by}`, { when })}
            </span>
          </div>
        )}
        <div className="pg-strip__actions">
          <button type="button" className="btn btn-ghost btn-sm" onClick={reload} data-testid="page-reload">
            <Icon name="refresh" /> {t('view.reload')}
          </button>
          <button type="button" className="btn btn-secondary btn-sm" onClick={ask} data-testid="page-ask">
            <Icon name="sparkles" /> {t('view.ask')}
          </button>
        </div>
      </header>

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
        <div className="pg-stage__bar"><Icon name="lock" /> {t('view.sandbox')}</div>
        {status === 'loading' || !source ? (
          <div className="pg-stage__state" role="status"><Icon name="spinner" spin /> {t('view.loading')}</div>
        ) : !trusted(page) ? (
          <div className="pg-stage__state" data-testid="page-untrusted">{t('view.untrusted')}</div>
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
          />
        )}
      </section>
    </div>
  )
}
