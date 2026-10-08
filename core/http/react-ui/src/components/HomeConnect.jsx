import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { apiUrl } from '../utils/basePath'
import Icon from './Icon'

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
  // Collapsed by default: the page is about the next message, and the endpoint
  // is one click away. The base URL stays in the row so it can be read without
  // opening anything.
  const [open, setOpen] = useState(false)
  // Dismissable: hiding the section unmounts it entirely so the space is
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
    } catch (_) { /* clipboard blocked: the URL is selectable anyway */ }
  }

  const dismiss = () => {
    try { localStorage.setItem('localai_home_connect_dismissed', '1') } catch { /* ignore */ }
    setDismissed(true)
  }

  if (dismissed) return null

  return (
    <section className="home-connect" data-open={open ? 'true' : undefined} aria-labelledby="home-connect-title">
      <button
        type="button"
        className="home-connect__head"
        aria-expanded={open}
        aria-controls="home-connect-body"
        onClick={() => setOpen(v => !v)}
      >
        <b id="home-connect-title">{t('connect.title')}</b>
        <code className="home-connect__base">{base}</code>
        <span className="home-connect__compat">{COMPAT.map(a => a.name).join(', ')}</span>
        <Icon name="chevron-down" className="home-connect__caret" />
      </button>

      {open && (
        <div className="home-connect__body" id="home-connect-body">
          <p className="home-connect__sub">{t('connect.subtitle')}</p>
          <div className="home-connect-url">
            <code>{base}</code>
            <button type="button" className="home-secondary home-secondary--sm" onClick={copy} aria-label={t('connect.copy')}>
              <Icon name={copied ? 'check' : 'copy'} />
              <span>{copied ? t('connect.copied') : t('connect.copy')}</span>
            </button>
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

          <div className="home-connect-block">
            <div className="home-connect-block-head">
              <span className="home-connect-block-title">{t('connect.nativeTitle')}</span>
              <a className="home-connect-docs" href={apiUrl('/swagger/index.html')} target="_blank" rel="noopener noreferrer">
                {t('connect.apiReference')} <Icon name="arrow-right" />
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

          <div className="home-connect__foot">
            <button type="button" className="home-ghost" onClick={dismiss}>{t('connect.hideSection')}</button>
          </div>
        </div>
      )}
    </section>
  )
}
