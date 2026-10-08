/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useRef, useState } from 'react'
import { useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { metricsApi } from '../utils/api'
import { apiUrl } from '../utils/basePath'
import { copyToClipboard } from '../utils/clipboard'
import { metricsReport, parseMetricFamilies, scrapeConfig } from '../utils/traffic'
import Icon from '../components/Icon'
import './traffic.css'

function CopyBlock({ text, label, testId, addToast }) {
  const { t } = useTranslation('traffic')
  const [copied, setCopied] = useState(false)
  const timer = useRef(null)
  useEffect(() => () => clearTimeout(timer.current), [])
  const copy = async () => {
    const ok = await copyToClipboard(text)
    if (ok) {
      setCopied(true)
      clearTimeout(timer.current)
      timer.current = setTimeout(() => setCopied(false), 2000)
      addToast?.(t('prometheus.copied'), 'success', 2000)
    } else {
      addToast?.(t('prometheus.copyFailed'), 'error')
    }
  }
  return (
    <div className="tf-cmd" data-testid={testId}>
      <pre className="tf-cmd__text" tabIndex={0} aria-label={label}><code>{text}</code></pre>
      <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm tf-cmd__copy" onClick={copy} aria-label={`${t('prometheus.copy')}: ${label}`}>
        <Icon name={copied ? 'check' : 'copy'} /> {copied ? t('prometheus.done') : t('prometheus.copy')}
      </button>
    </div>
  )
}

// What /metrics really exposes, checked against this server. The endpoint is
// admin only and can be switched off, so the page reads it once and says which
// of the metrics LocalAI can export it saw. The always-on one is api_call, a
// histogram of request duration in seconds by HTTP method and route. The rest
// exist only while the feature behind them runs, so "not seen" is not an error.
export default function Prometheus() {
  const { addToast } = useOutletContext() || {}
  const { t } = useTranslation('traffic')
  const [scrape, setScrape] = useState({ state: 'loading', families: null })
  const endpoint = `${window.location.origin}${apiUrl('/metrics')}`

  useEffect(() => {
    let cancelled = false
    metricsApi.scrape().then(({ status, text }) => {
      if (cancelled) return
      if (status === 200) setScrape({ state: 'ok', families: parseMetricFamilies(text) })
      else if (status === 404) setScrape({ state: 'off', families: null })
      else if (status === 401 || status === 403) setScrape({ state: 'denied', families: null })
      else setScrape({ state: 'error', families: null })
    })
    return () => { cancelled = true }
  }, [])

  const report = useMemo(() => metricsReport(scrape.families), [scrape.families])
  const config = useMemo(() => scrapeConfig(window.location.host), [])
  const query = 'histogram_quantile(0.95, sum by (le) (rate(api_call_bucket[5m])))'

  return (
    <div className="page page--wide tf-page tf-page--narrow" data-testid="prometheus-page">
      <header className="tf-head tf-head--col">
        <h1 className="tf-title">{t('prometheus.title')}</h1>
        <p className="tf-lede">{t('prometheus.lede')}</p>
      </header>

      <section className="tf-section" data-testid="prometheus-endpoint">
        <h2 className="tf-h2">{t('prometheus.endpoint')}</h2>
        <CopyBlock text={endpoint} label={t('prometheus.endpoint')} testId="endpoint-url" addToast={addToast} />
        <p className="tf-note-line">{t('prometheus.auth')}</p>
        <p className={`tf-scrape tf-scrape--${scrape.state}`} data-testid="scrape-state" role="status">
          <Icon name={scrape.state === 'ok' ? 'check-circle' : scrape.state === 'loading' ? 'spinner' : 'warning'} spin={scrape.state === 'loading'} />
          {t(`prometheus.scrape.${scrape.state}`, { count: scrape.families?.size || 0 })}
        </p>
      </section>

      <section className="tf-section" data-testid="prometheus-metrics">
        <h2 className="tf-h2">{t('prometheus.exposes')}</h2>
        <div className="dk-table-wrap">
          <table className="dk-table dk-table--compact tf-table tf-table--wrap">
            <caption className="dk-sr-only">{t('prometheus.exposes')}</caption>
            <thead>
              <tr>
                <th scope="col">{t('prometheus.col.metric')}</th>
                <th scope="col">{t('prometheus.col.type')}</th>
                <th scope="col" className="dk-hide-phone">{t('prometheus.col.labels')}</th>
                <th scope="col">{t('prometheus.col.seen')}</th>
              </tr>
            </thead>
            <tbody>
              {report.known.map(m => (
                <tr key={m.name} data-metric={m.name}>
                  <td>
                    <span className="dk-table-name dk-mono">{m.name}</span>
                    <span className="dk-table-sub">{t(`prometheus.metric.${m.key}`)}</span>
                  </td>
                  <td className="dk-mono">{m.type}</td>
                  <td className="dk-hide-phone dk-mono tf-sub">{m.labels.length ? m.labels.join(', ') : '-'}</td>
                  <td>
                    <span className="tf-seen" data-seen={m.seen == null ? 'unknown' : m.seen ? 'yes' : 'no'}>
                      {m.seen == null ? t('prometheus.seen.unknown') : m.seen ? t('prometheus.seen.yes') : (m.always ? t('prometheus.seen.missing') : t('prometheus.seen.no'))}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p className="tf-note-line">{t('prometheus.conditional')}</p>
        {report.other && report.other.length > 0 && (
          <details className="tf-more" data-testid="other-metrics">
            <summary>{t('prometheus.other', { count: report.other.length })}</summary>
            <p className="dk-mono tf-sub tf-more__list">{report.other.join(', ')}</p>
          </details>
        )}
      </section>

      <section className="tf-section" data-testid="prometheus-missing">
        <h2 className="tf-h2">{t('prometheus.missing')}</h2>
        <ul className="tf-list">
          <li>{t('prometheus.missing1')}</li>
          <li>{t('prometheus.missing2')}</li>
          <li>{t('prometheus.missing3')}</li>
        </ul>
      </section>

      <section className="tf-section" data-testid="prometheus-config">
        <h2 className="tf-h2">{t('prometheus.config')}</h2>
        <CopyBlock text={config} label={t('prometheus.config')} testId="scrape-config" addToast={addToast} />
        <h3 className="tf-h3">{t('prometheus.example')}</h3>
        <CopyBlock text={query} label={t('prometheus.example')} testId="example-query" addToast={addToast} />
        <p className="tf-note-line">{t('prometheus.exampleNote')}</p>
      </section>
    </div>
  )
}
