import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { apiUrl } from '../utils/basePath'

// LocalAI's own API goes well beyond chat — a sample of capability endpoints
// that have no OpenAI equivalent (see core/http/routes/*.go). The pitch leads
// from this breadth, then presents drop-in compatibility as a bonus on top.
const NATIVE = [
  { name: 'Images', path: '/v1/images/generations' },
  { name: 'Video', path: '/video' },
  { name: 'Realtime voice', path: '/v1/realtime · WebRTC, WS' },
  { name: 'Depth', path: '/v1/depth' },
  { name: 'Object detection', path: '/v1/detection' },
  { name: 'Rerank', path: '/v1/rerank' },
  { name: 'Audio & TTS', path: '/v1/audio/speech' },
  { name: 'Face & voice', path: '/v1/face · /v1/voice' },
]

// Wire-compatible API dialects: any client built for these works unchanged.
const COMPAT = [
  { name: 'OpenAI', path: '/v1' },
  { name: 'Anthropic', path: '/v1/messages' },
  { name: 'Ollama', path: '/api' },
  { name: 'OpenAI Responses', path: '/v1/responses' },
]

export default function HomeConnect() {
  const { t } = useTranslation('home')
  const [copied, setCopied] = useState(false)
  // Endpoint catalog is collapsed by default so Home stays uncluttered; the
  // base URL stays visible and the full list is one click away (discoverable).
  const [showEndpoints, setShowEndpoints] = useState(false)
  // Dismissable: hiding the card unmounts it entirely so the vertical space is
  // recovered, and the choice is remembered across visits.
  const [dismissed, setDismissed] = useState(() => {
    try { return localStorage.getItem('localai_home_connect_dismissed') === '1' } catch { return false }
  })

  // Absolute base for this instance, honouring any sub-path mount.
  const base = new URL(apiUrl('/'), window.location.origin).href.replace(/\/$/, '')

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(base)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch (_) { /* clipboard blocked — the URL is selectable anyway */ }
  }

  const dismiss = () => {
    try { localStorage.setItem('localai_home_connect_dismissed', '1') } catch { /* ignore */ }
    setDismissed(true)
  }

  if (dismissed) return null

  return (
    <section className="home-connect card" aria-labelledby="home-connect-title">
      <div className="home-connect-head">
        <span className="home-connect-icon"><i className="fas fa-plug" aria-hidden="true" /></span>
        <div>
          <h2 id="home-connect-title" className="home-connect-title">{t('connect.title')}</h2>
          <p className="home-connect-sub">{t('connect.subtitle')}</p>
        </div>
        <button type="button" className="home-connect-dismiss" onClick={dismiss} aria-label={t('connect.dismiss')} title={t('connect.dismiss')}>
          <i className="fas fa-times" aria-hidden="true" />
        </button>
      </div>

      <div className="home-connect-url">
        <code>{base}</code>
        <button type="button" className="btn btn-secondary btn-sm" onClick={copy} aria-label={t('connect.copy')}>
          <i className={`fas ${copied ? 'fa-check' : 'fa-copy'}`} aria-hidden="true" />
          <span>{copied ? t('connect.copied') : t('connect.copy')}</span>
        </button>
      </div>

      <button
        type="button"
        className="home-connect-toggle"
        aria-expanded={showEndpoints}
        aria-controls="home-connect-endpoints"
        onClick={() => setShowEndpoints(v => !v)}
      >
        <i className={`fas fa-chevron-${showEndpoints ? 'up' : 'down'}`} aria-hidden="true" />
        <span>{showEndpoints ? t('connect.hide') : t('connect.browse')}</span>
      </button>

      {showEndpoints && (
      <div id="home-connect-endpoints" className="home-connect-endpoints">
      <div className="home-connect-block">
        <div className="home-connect-block-head">
          <span className="home-connect-block-title">{t('connect.nativeTitle')}</span>
          <a className="home-connect-docs" href={apiUrl('/swagger/index.html')} target="_blank" rel="noopener noreferrer">
            {t('connect.apiReference')} <i className="fas fa-arrow-right" aria-hidden="true" />
          </a>
        </div>
        <ul className="home-connect-apis">
          {NATIVE.map(api => (
            <li key={api.name} className="home-connect-api">
              <span className="home-connect-api-name">{api.name}</span>
              <code className="home-connect-api-path">{api.path}</code>
            </li>
          ))}
        </ul>
      </div>

      <div className="home-connect-block">
        <div className="home-connect-block-head">
          <span className="home-connect-block-title">{t('connect.compatTitle')}</span>
        </div>
        <ul className="home-connect-apis">
          {COMPAT.map(api => (
            <li key={api.name} className="home-connect-api">
              <span className="home-connect-api-name">{api.name}</span>
              <code className="home-connect-api-path">{api.path}</code>
            </li>
          ))}
        </ul>
      </div>
      </div>
      )}
    </section>
  )
}
