/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { apiUrl } from '../utils/basePath'
import { joinCommands, shortToken, sortNetworks, workerCount } from '../utils/tools'
import { useBranding } from '../contexts/BrandingContext'
import SideSheet from '../components/SideSheet'
import ThemeToggle from '../components/ThemeToggle'
import Icon from '../components/Icon'
import LoadingSpinner from '../components/LoadingSpinner'
import './explorer.css'

const POLL_MS = 5000
const DOCS = 'https://localai.io/features/distribute/'

async function readError(response, fallback) {
  try {
    const data = await response.json()
    if (typeof data?.error === 'string') return data.error
    if (data?.error?.message) return data.error.message
  } catch { /* not JSON */ }
  return fallback
}

function CopyField({ label, text, testId }) {
  const { t } = useTranslation('explorer')
  const [copied, setCopied] = useState(false)
  const timer = useRef(null)
  useEffect(() => () => clearTimeout(timer.current), [])
  const copy = () => {
    navigator.clipboard?.writeText(text).then(() => {
      setCopied(true)
      clearTimeout(timer.current)
      timer.current = setTimeout(() => setCopied(false), 1500)
    }).catch(() => {})
  }
  return (
    <div className="ex-copy">
      <div className="ex-copy__head">
        <span className="dk-label">{label}</span>
        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={copy} data-testid={testId}>
          <Icon name={copied ? 'check' : 'copy'} /> {copied ? t('copied') : t('copy')}
        </button>
      </div>
      <pre className="ex-copy__text dk-mono">{text}</pre>
    </div>
  )
}

function JoinSheet({ network, onClose }) {
  const { t } = useTranslation('explorer')
  const federated = (network.Clusters || []).filter(c => c.Type === 'federated')
  return (
    <SideSheet
      title={t('join.title', { name: network.name })}
      description={t('join.text')}
      onClose={onClose}
      wide
      testId="join-sheet"
      labelId="ex-join-title"
      closeLabel={t('close')}
      footer={<button type="button" className="dk-btn dk-btn--secondary" onClick={onClose}>{t('close')}</button>}
    >
      <div className="ex-join">
        <CopyField label={t('join.token')} text={network.token} testId="copy-token" />
        {federated.length > 0 ? federated.map(cluster => {
          const cmd = joinCommands(network.token, cluster.NetworkID)
          return (
            <div key={`${cluster.NetworkID}-${cluster.Type}`} className="ex-join__cluster">
              <p className="dk-hint">{t('join.federated', { id: cluster.NetworkID || '-' })}</p>
              <CopyField label={t('join.docker')} text={cmd.docker} />
              <CopyField label={t('join.cli')} text={cmd.cli} />
            </div>
          )
        }) : (
          <p className="dk-hint">{t('join.noCommands')}</p>
        )}
        <p className="dk-hint"><a className="dk-link" href={DOCS} target="_blank" rel="noreferrer">{t('join.docs')} <Icon name="external-link" /></a></p>
        <p className="ex-warn"><Icon name="shield" /> {t('warning')}</p>
      </div>
    </SideSheet>
  )
}

function ListSheet({ onClose, onListed }) {
  const { t } = useTranslation('explorer')
  const [form, setForm] = useState({ name: '', description: '', token: '' })
  const [understood, setUnderstood] = useState(false)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const set = (key) => (e) => setForm(prev => ({ ...prev, [key]: e.target.value }))
  const ready = form.name.trim() && form.description.trim() && form.token.trim() && understood

  const submit = async (e) => {
    e.preventDefault()
    if (!ready) return
    setPending(true)
    setError('')
    try {
      const response = await fetch(apiUrl('/network/add'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: form.name.trim(), description: form.description.trim(), token: form.token.trim() }),
      })
      if (!response.ok) {
        setError(await readError(response, t('list.failed')))
        setPending(false)
        return
      }
      onListed()
    } catch {
      setError(t('list.failed'))
      setPending(false)
    }
  }

  return (
    <SideSheet
      title={t('list.title')}
      description={t('list.text')}
      onClose={onClose}
      testId="list-sheet"
      labelId="ex-list-title"
      closeLabel={t('close')}
      footer={(
        <>
          <button type="button" className="dk-btn dk-btn--ghost" onClick={onClose}>{t('list.cancel')}</button>
          <button type="submit" form="ex-list-form" className="dk-btn dk-btn--primary" disabled={!ready || pending} aria-busy={pending || undefined} data-testid="list-submit">
            {pending ? <><LoadingSpinner size="sm" /> {t('list.adding')}</> : <><Icon name="plus" /> {t('list.add')}</>}
          </button>
        </>
      )}
    >
      <form id="ex-list-form" className="ex-form" onSubmit={submit}>
        <div className="dk-field">
          <label className="dk-label" htmlFor="ex-name">{t('list.name')}</label>
          <input id="ex-name" className="dk-input" value={form.name} onChange={set('name')} autoComplete="off" />
        </div>
        <div className="dk-field">
          <label className="dk-label" htmlFor="ex-desc">{t('list.description')}</label>
          <textarea id="ex-desc" className="dk-textarea" rows={2} value={form.description} onChange={set('description')} />
        </div>
        <div className="dk-field">
          <label className="dk-label" htmlFor="ex-token">{t('list.token')}</label>
          <textarea id="ex-token" className="dk-textarea dk-input--mono" rows={3} value={form.token} onChange={set('token')} autoComplete="off" spellCheck="false" />
          <p className="dk-hint">{t('list.tokenHint')}</p>
        </div>
        <p className="ex-warn" role="note"><Icon name="warning" /> {t('list.public')}</p>
        <label className="dk-choice">
          <input className="dk-check" type="checkbox" checked={understood} onChange={e => setUnderstood(e.target.checked)} data-testid="list-understood" />
          {t('list.understood')}
        </label>
        {error && <p className="dk-field-error" role="alert" data-testid="list-error">{error}</p>}
      </form>
    </SideSheet>
  )
}

// The Explorer: the public swarms a LocalAI explorer server knows about. It is a
// separate server mode (`local-ai explorer`) with two endpoints: GET /networks
// lists each listed token with its name, description and clusters (a cluster has
// a type, a network id and its workers), and POST /network/add lists a new one.
// The page shows exactly that. It has no map or graph because the server holds
// no positions and no links between workers.
export default function Explorer() {
  const { t } = useTranslation('explorer')
  const branding = useBranding()
  const [state, setState] = useState('loading') // loading | ready | off | error
  const [networks, setNetworks] = useState([])
  const [sheet, setSheet] = useState(null) // { kind: 'list' } | { kind: 'join', network }
  const [notice, setNotice] = useState('')

  const load = useCallback(async () => {
    try {
      const response = await fetch(apiUrl('/networks'))
      const type = response.headers.get('content-type') || ''
      if (!response.ok || !type.includes('application/json')) {
        // A LocalAI that is not in explorer mode has no /networks.
        setState(prev => (prev === 'ready' ? prev : response.status === 404 || !type.includes('application/json') ? 'off' : 'error'))
        return
      }
      const data = await response.json()
      setNetworks(sortNetworks(Array.isArray(data) ? data : []))
      setState('ready')
    } catch {
      setState(prev => (prev === 'ready' ? prev : 'error'))
    }
  }, [])

  useEffect(() => {
    load()
    const timer = setInterval(load, POLL_MS)
    return () => clearInterval(timer)
  }, [load])

  const listed = () => {
    setSheet(null)
    setNotice(t('listed'))
    load()
  }

  return (
    <div className="ex" data-testid="explorer-page" data-state={state}>
      <header className="ex-top">
        <Link className="ex-brand" to="/app" aria-label={t('openApp')}>
          <img className="ex-brand__logo" src={apiUrl(branding.logoUrl)} alt="" />
          <span className="ex-brand__name">{t('brand', { name: branding.instanceName || 'LocalAI' })}</span>
        </Link>
        <div className="ex-top__acts">
          <Link className="dk-btn dk-btn--secondary dk-btn--sm" to="/app"><span className="dk-hide-phone">{t('openApp')}</span> <Icon name="external-link" /><span className="dk-only-phone dk-sr-only">{t('openApp')}</span></Link>
          <ThemeToggle />
        </div>
      </header>

      <main className="ex-main">
        <section className="ex-head">
          <div>
            <h1 className="ex-title">{t('title')}</h1>
            <p className="ex-lede">{t('lede')}</p>
          </div>
          {state === 'ready' && (
            <button type="button" className="dk-btn dk-btn--secondary" onClick={() => setSheet({ kind: 'list' })} data-testid="list-open">
              <Icon name="plus" /> {t('list.open')}
            </button>
          )}
        </section>

        {notice && <p className="ex-notice" role="status" data-testid="explorer-notice"><Icon name="check-circle" /> {notice}</p>}

        {state === 'loading' && (
          <div className="ex-list" aria-busy="true" aria-label={t('loading')}>
            {[0, 1, 2].map(i => <div key={i} className="ex-skeleton dk-skeleton dk-skeleton--block" />)}
          </div>
        )}

        {state === 'off' && (
          <section className="dk-empty ex-empty" data-testid="explorer-off">
            <div className="dk-empty-icon"><Icon name="network" /></div>
            <h2 className="dk-empty-title">{t('off.title')}</h2>
            <p className="dk-empty-text">{t('off.text')}</p>
            <pre className="ex-copy__text dk-mono">local-ai explorer</pre>
            <a className="dk-link" href={DOCS} target="_blank" rel="noreferrer">{t('join.docs')} <Icon name="external-link" /></a>
          </section>
        )}

        {state === 'error' && (
          <section className="dk-empty ex-empty" role="alert" data-testid="explorer-error">
            <div className="dk-empty-icon"><Icon name="alert-circle" /></div>
            <h2 className="dk-empty-title">{t('error.title')}</h2>
            <p className="dk-empty-text">{t('error.text')}</p>
            <button type="button" className="dk-btn dk-btn--secondary" onClick={() => { setState('loading'); load() }}><Icon name="refresh" /> {t('error.retry')}</button>
          </section>
        )}

        {state === 'ready' && networks.length === 0 && (
          <section className="dk-empty ex-empty" data-testid="explorer-empty">
            <div className="dk-empty-icon"><Icon name="network" /></div>
            <h2 className="dk-empty-title">{t('empty.title')}</h2>
            <p className="dk-empty-text">{t('empty.text')}</p>
            <button type="button" className="dk-btn dk-btn--primary" onClick={() => setSheet({ kind: 'list' })}><Icon name="plus" /> {t('list.open')}</button>
          </section>
        )}

        {state === 'ready' && networks.length > 0 && (
          <>
            <h2 className="ex-count">
              {t('count', { count: networks.length })}
              <span className="dk-hint">{t('sorted')}</span>
            </h2>
            <ul className="ex-list" aria-label={t('title')} data-testid="explorer-list">
              {networks.map(network => {
                const types = [...new Set((network.Clusters || []).map(c => c.Type).filter(Boolean))]
                const workers = workerCount(network)
                return (
                  <li key={network.token} className="ex-net" data-network={network.name}>
                    <div className="ex-net__main">
                      <h3 className="ex-net__name">
                        {network.name}
                        {types.map(type => <span key={type} className="dk-badge">{type}</span>)}
                      </h3>
                      {network.description && <p className="ex-net__text">{network.description}</p>}
                      <p className="ex-net__token"><Icon name="key" /> <span className="dk-mono" title={t('tokenTitle')}>{shortToken(network.token)}</span></p>
                    </div>
                    <div className="ex-net__end">
                      <span className="dk-status"><span className="dk-dot dk-dot--ok" /> <b className="dk-mono">{workers}</b> {t('workers', { count: workers })}</span>
                      <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => setSheet({ kind: 'join', network })} data-testid="join-open">{t('join.open')}</button>
                    </div>
                  </li>
                )
              })}
            </ul>
            <p className="ex-warn"><Icon name="shield" /> {t('warning')}</p>
          </>
        )}
      </main>

      {sheet?.kind === 'list' && <ListSheet onClose={() => setSheet(null)} onListed={listed} />}
      {sheet?.kind === 'join' && <JoinSheet network={sheet.network} onClose={() => setSheet(null)} />}
    </div>
  )
}
