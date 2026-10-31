/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import Button from '../ui/Button'
import { fileSize } from '../../utils/fileInfo'
import { KIND_ICON, kindOf } from '../../utils/fileBrowser'
import { formatDuration, summarize } from '../../utils/uploadQueue'

// A finished upload stays this long, then leaves the tray.
const DONE_MS = 4000
const FADE_MS = 280

// The meter of one upload, or of all of them in the head of the tray: a
// native progress element, which a screen reader reads as such.
function Meter({ percent, tone, label }) {
  return <progress className={`ws-meter${tone ? ` ws-meter--${tone}` : ''}`} value={percent} max={100} aria-label={label} />
}

function UploadRow({ item, leaving, onCancel, onRetry, onDismiss, onOpen, t, locale }) {
  const pct = item.size > 0 ? Math.round((item.loaded / item.size) * 100) : (item.status === 'done' ? 100 : 0)
  const icon = KIND_ICON[kindOf({ kind: 'file', name: item.name })]
  let line
  if (item.status === 'queued') line = t('uploads.waiting')
  else if (item.status === 'sending') {
    const left = item.rate > 0 ? (item.size - item.loaded) / item.rate : null
    line = [
      t('uploads.progress', { loaded: fileSize(item.loaded), total: fileSize(item.size) }),
      item.rate > 0 && item.size > 4 * 1024 * 1024 ? t('uploads.rate', { rate: fileSize(Math.round(item.rate)) }) : '',
      left !== null && left > 3 ? t('uploads.left', { time: formatDuration(left, locale) }) : '',
    ].filter(Boolean).join(' · ')
  } else if (item.status === 'done') line = item.result?.conflict_of ? t('uploads.renamed', { name: item.result.name }) : t('uploads.done')
  else if (item.status === 'canceled') line = t('uploads.canceled')
  else line = [t(item.error?.key || 'uploads.errors.server', { limit: fileSize(item.error?.limit || 0) }), item.error?.reason ? t('uploads.said', { reason: item.error.reason }) : ''].filter(Boolean).join(' ')
  return (
    <li className="ws-up" data-status={item.status} data-leaving={leaving || undefined} data-testid="ws-upload-row">
      <Icon name={item.status === 'done' ? 'check-circle' : item.status === 'failed' ? 'alert-circle' : icon} className="ws-up__icon" />
      <div className="ws-up__main">
        <span className="ws-up__name" title={item.path}>{item.name}</span>
        <span className="ws-up__line" data-testid="ws-upload-line">{line}</span>
        {(item.status === 'sending' || item.status === 'queued') && (
          <Meter percent={pct} label={t('uploads.meterOne', { name: item.name, percent: pct })} />
        )}
      </div>
      {item.status === 'sending' && <span className="ws-up__pct" aria-hidden="true">{pct}%</span>}
      <span className="ws-up__acts">
        {(item.status === 'queued' || item.status === 'sending') && (
          <Button variant="ghost" size="sm" icon="close" aria-label={t('uploads.cancelOne', { name: item.name })} title={t('uploads.cancel')} onClick={() => onCancel(item.id)} />
        )}
        {(item.status === 'failed' || item.status === 'canceled') && item.error?.retry !== false && (
          <Button variant="ghost" size="sm" icon="refresh" aria-label={t('uploads.retryOne', { name: item.name })} title={t('uploads.retry')} onClick={() => onRetry(item.id)} />
        )}
        {(item.status === 'failed' || item.status === 'canceled') && (
          <Button variant="ghost" size="sm" icon="close" aria-label={t('uploads.dismissOne', { name: item.name })} title={t('uploads.dismiss')} onClick={() => onDismiss(item.id)} />
        )}
        {item.status === 'done' && item.result?.path && (
          <Button variant="ghost" size="sm" icon="eye" aria-label={t('uploads.showOne', { name: item.name })} title={t('uploads.show')} onClick={() => onOpen(item)} />
        )}
      </span>
    </li>
  )
}

// The upload tray: every upload of this visit, what is left and how long it
// takes. It stays at the bottom of the page while files go, folds to its head
// line, and leaves by itself a moment after the last file arrived. Failed
// files stay with their reason until they are retried or dismissed.
//
// A live region tells a screen reader when uploads start, when one fails and
// when they are all done.
export default function UploadTray({ state, queue, onOpen }) {
  const { t, i18n } = useTranslation('workspace')
  const [folded, setFolded] = useState(false)
  const [now, setNow] = useState(() => Date.now())
  const [said, setSaid] = useState('')
  const [leaving, setLeaving] = useState(() => new Set())
  const prev = useRef({ pending: 0, failed: 0 })
  const items = state.items
  const sum = summarize(items, now)

  // A done row leaves after DONE_MS; when nothing is left the tray goes.
  useEffect(() => {
    const done = items.filter(i => i.status === 'done' || (i.status === 'canceled' && !i.error))
    if (done.length === 0) return undefined
    const next = Math.min(...done.map(i => (i.finishedAt || 0) + DONE_MS))
    // The row fades first, then leaves.
    const fade = setTimeout(() => {
      const at = Date.now() + FADE_MS
      setLeaving(new Set(done.filter(i => (i.finishedAt || 0) + DONE_MS <= at).map(i => i.id)))
    }, Math.max(0, next - FADE_MS - Date.now()))
    const id = setTimeout(() => {
      const at = Date.now()
      for (const i of done) if ((i.finishedAt || 0) + DONE_MS <= at) queue.dismiss(i.id)
      setNow(at)
    }, Math.max(50, next - Date.now()))
    return () => { clearTimeout(fade); clearTimeout(id) }
  }, [items, queue])

  useEffect(() => {
    if (sum.pending === 0) return undefined
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [sum.pending])

  useEffect(() => {
    const was = prev.current
    const lastFailed = items.filter(i => i.status === 'failed').at(-1)
    if (sum.pending > 0 && was.pending === 0) setSaid(t('uploads.say.start', { count: sum.pending }))
    else if (sum.pending === 0 && was.pending > 0) {
      if (sum.failed === 0) setSaid(t('uploads.say.done', { count: sum.done }))
      else if (sum.done > 0) setSaid(t('uploads.say.doneFailed', { count: sum.done, failed: sum.failed }))
      else if (sum.failed === 1 && lastFailed) setSaid(t('uploads.say.failed', { name: lastFailed.name }))
      else setSaid(t('uploads.head.failed', { count: sum.failed }))
    } else if (sum.failed > was.failed && lastFailed) setSaid(t('uploads.say.failed', { name: lastFailed.name }))
    prev.current = { pending: sum.pending, failed: sum.failed }
  }, [sum.pending, sum.failed, sum.done, items, t])

  const live = <p className="sr-only" aria-live="polite" role="status" data-testid="ws-upload-live">{said}</p>
  if (items.length === 0) return live

  const title = sum.pending > 0
    ? t('uploads.head.sending', { count: sum.pending })
    : sum.failed > 0 ? t('uploads.head.failed', { count: sum.failed }) : t('uploads.head.done', { count: sum.done })
  const sub = sum.pending > 0
    ? [t('uploads.progress', { loaded: fileSize(sum.loaded), total: fileSize(sum.total) }), sum.eta ? t('uploads.left', { time: formatDuration(sum.eta, i18n.language) }) : ''].filter(Boolean).join(' · ')
    : ''
  return (
    <>
      {live}
      <section className={`ws-tray${folded ? ' ws-tray--folded' : ''}`} aria-labelledby="ws-tray-title" data-testid="ws-upload-tray" data-pending={sum.pending}>
        <header className="ws-tray__head">
          <Icon name={sum.pending > 0 ? 'cloud-upload' : sum.failed > 0 ? 'alert-circle' : 'check-circle'} className={`ws-tray__icon${sum.failed > 0 && sum.pending === 0 ? ' ws-tray__icon--error' : ''}`} />
          <div className="ws-tray__title">
            <h2 id="ws-tray-title">{title}</h2>
            {sub && <p className="ws-tray__sub">{sub}</p>}
          </div>
          {sum.pending > 1 && (
            <Button variant="ghost" size="sm" onClick={() => queue.cancelAll()}>{t('uploads.cancelAll')}</Button>
          )}
          {sum.pending === 0 && sum.failed > 1 && items.some(i => i.status === 'failed' && i.error?.retry !== false) && (
            <Button variant="ghost" size="sm" icon="refresh" onClick={() => queue.retryAll()}>{t('uploads.retryAll')}</Button>
          )}
          <Button
            variant="ghost" size="sm" icon={folded ? 'chevron-up' : 'chevron-down'} aria-expanded={!folded} aria-controls="ws-tray-list"
            aria-label={folded ? t('uploads.unfold') : t('uploads.fold')} onClick={() => setFolded(f => !f)}
          />
          {sum.pending === 0 && (
            <Button variant="ghost" size="sm" icon="close" aria-label={t('uploads.close')} onClick={() => queue.dismiss()} />
          )}
        </header>
        {sum.pending > 0 && <Meter percent={sum.percent} tone="head" label={t('uploads.meterAll', { percent: sum.percent })} />}
        <ul className="ws-tray__list" id="ws-tray-list" hidden={folded}>
          {items.map(i => (
            <UploadRow
              key={i.id} item={i} t={t} locale={i18n.language} leaving={leaving.has(i.id)}
              onCancel={queue.cancel} onRetry={queue.retry} onDismiss={queue.dismiss} onOpen={onOpen}
            />
          ))}
        </ul>
      </section>
    </>
  )
}
