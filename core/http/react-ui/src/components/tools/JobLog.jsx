import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { logText } from '../../utils/tools'
import Icon from '../Icon'
import './tools.css'

const pad = (n) => String(n).padStart(2, '0')
const stamp = (d) => `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`

// What the job has reported since this page opened, one line per status change
// or message. The server sends progress events and keeps no log, so a line from
// before the page opened is not here, and the heading says so.
export default function JobLog({ lines, name = 'job' }) {
  const { t } = useTranslation('tools')
  const [only, setOnly] = useState(false)
  const [copied, setCopied] = useState(false)
  const bodyRef = useRef(null)
  const shown = only ? lines.filter(l => l.level !== 'info') : lines

  useEffect(() => {
    const el = bodyRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [shown.length])

  const copy = () => {
    navigator.clipboard?.writeText(logText(shown)).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    }).catch(() => {})
  }
  const download = () => {
    const url = URL.createObjectURL(new Blob([logText(shown)], { type: 'text/plain' }))
    const a = document.createElement('a')
    a.href = url
    a.download = `${name}-log.txt`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
  }

  return (
    <section className="bt-log dk-card" aria-labelledby="bt-log-title" data-testid="job-log">
      <header className="bt-log__head">
        <div>
          <h2 className="bt-h2" id="bt-log-title">{t('log.title')}</h2>
          <p className="dk-hint">{t('log.note')}</p>
        </div>
        <div className="bt-log__tools">
          <label className="dk-choice">
            <input className="dk-check" type="checkbox" checked={only} onChange={e => setOnly(e.target.checked)} data-testid="job-log-filter" />
            {t('log.only')}
          </label>
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={copy} disabled={shown.length === 0}>
            <Icon name={copied ? 'check' : 'copy'} /> {copied ? t('log.copied') : t('log.copy')}
          </button>
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={download} disabled={shown.length === 0}>
            <Icon name="download" /> {t('log.download')}
          </button>
        </div>
      </header>
      <div className="bt-log__body" ref={bodyRef} role="log" aria-live="off" tabIndex={0} aria-label={t('log.title')}>
        {shown.length === 0 ? (
          <p className="bt-log__empty">{only ? t('log.noneFiltered') : t('log.empty')}</p>
        ) : shown.map((line, i) => (
          <div key={i} className="bt-log__line" data-level={line.level}>
            <span className="bt-log__time">{stamp(line.at)}</span>
            <span className="bt-log__tag">{line.tag}</span>
            <span className="bt-log__text">{line.text}</span>
          </div>
        ))}
      </div>
    </section>
  )
}
