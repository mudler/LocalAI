/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import WaveformPlayer from '../audio/WaveformPlayer'
import Icon from '../Icon'
import { durationText, nsToMs } from '../../utils/traffic'

const AUDIO_DATA_KEYS = new Set([
  'audio_wav_base64', 'audio_duration_s', 'audio_snippet_s',
  'audio_sample_rate', 'audio_samples', 'audio_rms_dbfs',
  'audio_peak_dbfs', 'audio_dc_offset',
])

function formatValue(value) {
  if (value === null || value === undefined) return 'null'
  if (typeof value === 'boolean') return value ? 'true' : 'false'
  if (typeof value === 'object') return JSON.stringify(value)
  return String(value)
}

function formatLarge(value) {
  if (typeof value === 'string') {
    try { return JSON.stringify(JSON.parse(value), null, 2) } catch { return value }
  }
  if (typeof value === 'object') return JSON.stringify(value, null, 2)
  return String(value)
}

function isLarge(value) {
  if (typeof value === 'string') return value.length > 120
  if (typeof value === 'object' && value !== null) return JSON.stringify(value).length > 120
  return false
}

function isPlainObject(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

// A base64 WAV payload as a blob: object URL for the waveform player. A data:
// URL would play in <audio> but the peaks renderer fetch()es the src, and the
// CSP's connect-src only allows blob:. Decoding to a Blob also tolerates a
// payload that is not base64, such as the "<truncated: N bytes>" marker older
// servers stamped into oversized fields, by giving null instead of a broken
// player.
function useWavObjectURL(b64) {
  const [url, setUrl] = useState(null)
  useEffect(() => {
    if (!b64) {
      setUrl(null)
      return undefined
    }
    let objectUrl = null
    try {
      const bin = atob(b64)
      const bytes = new Uint8Array(bin.length)
      for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
      objectUrl = URL.createObjectURL(new Blob([bytes], { type: 'audio/wav' }))
      setUrl(objectUrl)
    } catch {
      setUrl(null)
    }
    return () => { if (objectUrl) URL.revokeObjectURL(objectUrl) }
  }, [b64])
  return url
}

function AudioSnippet({ data }) {
  const { t } = useTranslation('traffic')
  const audioUrl = useWavObjectURL(data?.audio_wav_base64)
  if (!data?.audio_wav_base64) return null
  const metrics = [
    [t('backend.audio.duration'), `${data.audio_duration_s} s`],
    [t('backend.audio.rate'), `${data.audio_sample_rate} Hz`],
    [t('backend.audio.rms'), `${data.audio_rms_dbfs} dBFS`],
    [t('backend.audio.peak'), `${data.audio_peak_dbfs} dBFS`],
    [t('backend.audio.samples'), data.audio_samples],
    [t('backend.audio.snippet'), `${data.audio_snippet_s} s`],
    [t('backend.audio.dc'), data.audio_dc_offset],
  ]
  return (
    <section className="tf-block">
      <h4 className="tf-h3"><Icon name="headphones" /> {t('backend.audio.title')}</h4>
      {audioUrl
        ? <WaveformPlayer src={audioUrl} height={64} />
        : (
          <p className="tf-note-line" data-testid="audio-snippet-unavailable">
            <Icon name="warning" /> {t('backend.audio.truncated')}
          </p>
        )}
      <dl className="dk-kv tf-kv tf-kv--wide">
        {metrics.map(([label, value]) => (
          <div key={label} className="tf-kv__pair"><dt>{label}</dt><dd className="dk-mono">{value}</dd></div>
        ))}
      </dl>
    </section>
  )
}

// The data fields of a backend trace, nested objects and long values opening
// in place.
function DataFields({ data, nested }) {
  const { t } = useTranslation('traffic')
  const [expanded, setExpanded] = useState({})
  const fields = Object.entries(data).filter(([key]) => !AUDIO_DATA_KEYS.has(key))
  if (fields.length === 0) return null
  return (
    <div className="tf-fields">
      {!nested && <h4 className="tf-h3">{t('backend.fields')}</h4>}
      <ul className="tf-fields__list">
        {fields.map(([key, value]) => {
          const object = isPlainObject(value)
          const large = !object && isLarge(value)
          const open = Boolean(expanded[key])
          const canOpen = object || large
          return (
            <li key={key} className="tf-field">
              {canOpen ? (
                <button type="button" className="tf-field__head" aria-expanded={open} onClick={() => setExpanded(prev => ({ ...prev, [key]: !prev[key] }))}>
                  <Icon name={open ? 'chevron-down' : 'chevron-right'} />
                  <span className="tf-field__key">{key}</span>
                  {object && !open && <span className="tf-sub">{t('backend.fieldCount', { count: Object.keys(value).length })}</span>}
                  {large && !open && <span className="tf-sub tf-field__cut">{formatValue(value).slice(0, 120)}...</span>}
                </button>
              ) : (
                <div className="tf-field__head">
                  <span className="tf-field__chev" />
                  <span className="tf-field__key">{key}</span>
                  <span className="dk-mono tf-sub">{formatValue(value)}</span>
                </div>
              )}
              {open && object && <div className="tf-field__nested"><DataFields data={value} nested /></div>}
              {open && large && <pre className="tf-code">{formatLarge(value)}</pre>}
            </li>
          )
        })}
      </ul>
    </div>
  )
}

// The row of a backend operation, opened in place.
export default function BackendTraceDetail({ trace }) {
  const { t } = useTranslation('traffic')
  const running = trace.status === 'running'
  const ms = nsToMs(trace.duration)
  return (
    <div className="tf-detail" data-testid="backend-trace-detail">
      <dl className="dk-kv tf-kv tf-kv--wide">
        <div className="tf-kv__pair"><dt>{t('backend.type')}</dt><dd><span className="dk-chip dk-chip--sm">{trace.type || '-'}</span></dd></div>
        <div className="tf-kv__pair"><dt>{t('backend.model')}</dt><dd className="dk-mono">{trace.model_name || '-'}</dd></div>
        <div className="tf-kv__pair"><dt>{t('backend.backend')}</dt><dd className="dk-mono">{trace.backend || '-'}</dd></div>
        <div className="tf-kv__pair"><dt>{t('backend.duration')}</dt><dd className="dk-mono">{ms == null ? '-' : durationText(ms)}{running ? ` (${t('backend.running')})` : ''}</dd></div>
      </dl>
      {trace.error && (
        <p className="tf-error" role="alert"><Icon name="alert-circle" /><span className="dk-mono">{trace.error}</span></p>
      )}
      {trace.model_name && (
        // /app/backend-logs/:modelId is the unified entry point: standalone it
        // streams local logs, in distributed mode it resolves the model to the
        // worker(s) that host it.
        <a
          className="dk-btn dk-btn--secondary dk-btn--sm tf-link"
          href={`/app/backend-logs/${encodeURIComponent(trace.model_name)}${trace.timestamp ? `?from=${encodeURIComponent(trace.timestamp)}` : ''}`}
        >
          <Icon name="terminal" /> {t('backend.logs')}
        </a>
      )}
      {trace.data && <AudioSnippet data={trace.data} />}
      {trace.body && (
        <section className="tf-block">
          <h4 className="tf-h3">{t('backend.body')}</h4>
          <pre className="tf-code">{formatLarge(trace.body)}</pre>
        </section>
      )}
      {trace.data && Object.keys(trace.data).length > 0 && <DataFields data={trace.data} />}
    </div>
  )
}
