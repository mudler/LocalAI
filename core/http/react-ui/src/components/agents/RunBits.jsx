/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { copyToClipboard } from '../../utils/clipboard'
import { relativeDate } from '../../utils/fileBrowser'

// A time in words ("5 minutes ago", "in 2 hours"), with the full date as its
// tooltip and the machine value on <time>.
export function RelTime({ iso, className = '' }) {
  const { i18n } = useTranslation()
  if (!iso) return null
  const { label, exact } = relativeDate(iso, { locale: i18n.language })
  if (!label) return null
  return <time className={className} dateTime={iso} title={exact}>{label}</time>
}

// The clock time of a moment ("08:09"), for a list grouped by day; the full
// date is its tooltip.
export function ClockTime({ iso, className = '' }) {
  const { i18n } = useTranslation()
  const at = Date.parse(iso || '')
  if (!at) return null
  const label = new Date(at).toLocaleTimeString(i18n.language, { hour: '2-digit', minute: '2-digit' })
  const exact = new Intl.DateTimeFormat(i18n.language, { dateStyle: 'full', timeStyle: 'short' }).format(at)
  return <time className={className} dateTime={iso} title={exact}>{label}</time>
}

// One run in a history list: a toggle, its state, what it was and what
// happened, when it started and how long it took. The open run shows
// `children` under it. Used by the run history of the triggers and of the
// agent tasks, so both read the same.
//
//   status      the state chip
//   title       the task or trigger it belongs to (a link or text)
//   line        one sentence: what happened
//   when, took  start time (a node) and duration (text)
//   actions     small buttons at the end of the row
//   open        whether the details show
//   onToggle    flips `open`
//   toggleLabel the accessible name of the toggle
//   data        data-* attributes for the row (data-job, data-status)
//   idScope     a prefix for the id of the details, when one run shows twice
export function RunItem({ status, title, line, when, took, actions, open, onToggle, toggleLabel, data = {}, error = false, detailTestId, idScope = '', children }) {
  const bodyId = `run-${idScope.replace(/[^a-z0-9-]/gi, '')}${data['data-job'] || ''}`
  return (
    <li className="aj-run" data-open={open || undefined} data-error={error || undefined} data-row {...data}>
      <div className="aj-run__head">
        <button type="button" className="aj-run__toggle" aria-expanded={!!open} aria-controls={bodyId} aria-label={toggleLabel} onClick={onToggle}>
          <Icon name="chevron-right" />
        </button>
        <span className="aj-run__state">{status}</span>
        <span className="aj-run__main">
          <span className="aj-run__title">{title}</span>
          {line && <span className="aj-run__line">{line}</span>}
        </span>
        <span className="aj-run__when">{when}</span>
        <span className="aj-run__took">{took}</span>
        {actions && <span className="aj-run__acts">{actions}</span>}
      </div>
      <div className="dk-collapse aj-run__collapse" data-open={!!open} id={bodyId}>
        <div className="aj-collapse-in">
          {open && <div className="aj-run__body" data-testid={detailTestId}>{children}</div>}
        </div>
      </div>
    </li>
  )
}

// A block of text a run produced (its answer, its error, its log) in mono,
// with a Copy button. `tone="error"` draws it on the error wash.
export function OutputBlock({ label, text, tone, testId }) {
  const { t } = useTranslation('common')
  const [copied, setCopied] = useState(false)
  const timer = useRef(null)
  useEffect(() => () => clearTimeout(timer.current), [])
  const copy = async () => {
    if (await copyToClipboard(text)) {
      setCopied(true)
      clearTimeout(timer.current)
      timer.current = setTimeout(() => setCopied(false), 1600)
    }
  }
  return (
    <div className="aj-out" data-tone={tone}>
      <div className="aj-out__head">
        <span className="ag-eyebrow">{label}</span>
        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm aj-out__copy" onClick={copy} aria-live="polite">
          <Icon name={copied ? 'check' : 'copy'} className="ui-btn__icon" />
          <span className="ui-btn__label">{copied ? t('actions.copied') : t('actions.copy')}</span>
        </button>
      </div>
      <pre className="aj-out__text" data-testid={testId}>{text}</pre>
    </div>
  )
}
