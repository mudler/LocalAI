/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { chatConversionApi } from '../utils/api'
import './chatskills.css'
import './chatauto.css'

const STATUSES = ['converted', 'skipped', 'failed', 'dropped']
// A status the page has no colour for reads as a skipped item.
const TONE = { converted: 'done', skipped: 'skipped', failed: 'failed', dropped: 'skipped' }

// What the conversion of the old chat, agents, tasks and skills did for the
// viewer: one line for each item, with what was dropped and why. An
// administrator also sees the totals of every user.
export default function ChatConversion() {
  const { t } = useTranslation('chats')
  const [report, setReport] = useState(null)
  const [error, setError] = useState('')

  useEffect(() => {
    let live = true
    chatConversionApi.get().then((r) => { if (live) setReport(r) }).catch((e) => { if (live) setError(e?.message || t('errors.send')) })
    return () => { live = false }
  }, [t])

  if (error) return <div className="cs-page"><p className="cs-error" role="alert">{error}</p></div>
  if (report === null) return <div className="cs-page" data-testid="chat-conversion-page" />

  const items = report.items || []
  return (
    <div className="cs-page" data-testid="chat-conversion-page">
      <header className="cs-head">
        <h1 className="cs-title">{t('conversionPage.title')}</h1>
      </header>
      <p className="cs-muted">{t('conversionPage.intro')}</p>
      {!report.done && <p className="cs-muted" role="status">{t('conversionPage.notRun')}</p>}
      {report.done && items.length === 0 && <p className="cs-muted" role="status">{t('conversionPage.nothing')}</p>}
      {items.length > 0 && (
        <p className="cs-muted" data-testid="conversion-counts">
          {STATUSES.map((s) => t(`conversionPage.count.${s}`, { count: report.counts?.[s] || 0 })).join(' · ')}
        </p>
      )}
      {report.totals && (
        <p className="cs-muted" data-testid="conversion-totals">
          {t('conversionPage.allUsers')} {STATUSES.map((s) => t(`conversionPage.count.${s}`, { count: report.totals[s] || 0 })).join(' · ')}
        </p>
      )}
      {items.length > 0 && (
        <ul className="cs-list" aria-label={t('conversionPage.title')}>
          {items.map((i, n) => (
            <li className="cs-row" key={`${i.kind}-${i.name}-${n}`} data-testid="conversion-item">
              <span className="cs-row__text">
                <span className="cs-row__name">{i.name}</span>
                <span className="cs-muted">{t(`conversionPage.kind.${i.kind}`, { defaultValue: i.kind })}{i.reason ? ` — ${i.reason}` : ''}</span>
              </span>
              <span className={`ca-status ca-status--${TONE[i.status] || 'skipped'}`}>{t(`conversionPage.status.${i.status}`, { defaultValue: i.status })}</span>
            </li>
          ))}
        </ul>
      )}
      <p className="cs-muted">
        <Link className="cs-link" to="/app/chats/triggers">{t('conversionPage.openTriggers')}</Link>
        {' · '}
        <Link className="cs-link" to="/app/chats/connectors">{t('conversionPage.openConnectors')}</Link>
      </p>
    </div>
  )
}
