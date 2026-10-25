import { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import { agentJobsApi, fileToBase64 } from '../../utils/api'
import { parseKeyValues, promptParams } from '../../utils/agentJobs'
import Icon from '../Icon'

const MEDIA = [
  { type: 'images', icon: 'image' },
  { type: 'videos', icon: 'video' },
  { type: 'audios', icon: 'headphones' },
  { type: 'files', icon: 'file' },
]

// "Run now" for a task: the values its prompt uses, optional extra parameters
// and optional media, then one call. Without media the call is the run-by-name
// one the page always used. With media it is the job call, which is the only one
// that takes attachments.
export default function RunTaskDialog({ task, onClose, onStarted, addToast }) {
  const { t } = useTranslation('agents')
  const gaps = useMemo(() => promptParams(task.prompt), [task.prompt])
  const [values, setValues] = useState(() => Object.fromEntries(gaps.map(g => [g, task.cron_parameters?.[g] ?? ''])))
  const [more, setMore] = useState('')
  const [media, setMedia] = useState({ images: [], videos: [], audios: [], files: [] })
  const [busy, setBusy] = useState(false)
  const dialogRef = useRef(null)
  const fileRef = useRef(null)
  const typeRef = useRef('images')
  const onCloseRef = useRef(onClose)
  onCloseRef.current = onClose

  useEffect(() => {
    const opener = document.activeElement
    const dialog = dialogRef.current
    dialog?.querySelector('input, textarea, button')?.focus()
    const onKey = (e) => {
      if (e.key === 'Escape') { e.preventDefault(); onCloseRef.current(); return }
      if (e.key !== 'Tab' || !dialog) return
      const focusable = Array.from(dialog.querySelectorAll('button:not([disabled]), input:not([type="file"]), textarea, [tabindex]:not([tabindex="-1"])'))
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus() }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
    }
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('keydown', onKey)
      if (opener && document.contains(opener)) opener.focus?.()
    }
  }, [])

  const attach = async (e) => {
    const type = typeRef.current
    for (const file of e.target.files) {
      const base64 = await fileToBase64(file)
      const url = `data:${file.type};base64,${base64}`
      setMedia(prev => ({ ...prev, [type]: [...prev[type], { url, name: file.name }] }))
    }
    e.target.value = ''
  }

  const start = async () => {
    setBusy(true)
    try {
      const params = {}
      for (const g of gaps) if (values[g] !== '') params[g] = values[g]
      Object.assign(params, parseKeyValues(more))
      const attached = MEDIA.some(m => media[m.type].length > 0)
      let res
      if (attached) {
        const body = { task_id: task.id, parameters: params }
        for (const m of MEDIA) if (media[m.type].length) body[m.type] = media[m.type].map(x => x.url)
        res = await agentJobsApi.executeJob(body)
      } else {
        res = await agentJobsApi.executeTask(task.name || task.id, params)
      }
      addToast(t('jobs.run.started', { name: task.name }), 'success')
      onStarted?.(res)
      onClose()
    } catch (err) {
      addToast(t('jobs.run.failed', { message: err.message }), 'error')
      setBusy(false)
    }
  }

  return createPortal(
    <div className="dk-veil" data-state="open" onMouseDown={onClose}>
      <div
        ref={dialogRef}
        className="dk-dialog aj-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="aj-run-title"
        data-state="open"
        data-testid="run-task-dialog"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div className="dk-dialog-head">
          <div>
            <h3 className="dk-dialog-title" id="aj-run-title">{t('jobs.run.title', { name: task.name })}</h3>
            <p className="dk-dialog-desc">{t('jobs.run.desc')}</p>
          </div>
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" aria-label={t('jobs.run.close')} onClick={onClose}>
            <Icon name="close" />
          </button>
        </div>
        <div className="dk-dialog-body aj-dialog__body">
          {gaps.length === 0 ? (
            <p className="ag-note">{t('jobs.run.noGaps')}</p>
          ) : gaps.map(g => (
            <div className="dk-field" key={g}>
              <label className="dk-label ag-mono" htmlFor={`aj-gap-${g}`}>{g}</label>
              <input
                id={`aj-gap-${g}`}
                className="dk-input"
                value={values[g]}
                onChange={(e) => setValues(v => ({ ...v, [g]: e.target.value }))}
                onKeyDown={(e) => { if (e.key === 'Enter' && !busy) { e.preventDefault(); start() } }}
              />
            </div>
          ))}
          <div className="dk-field">
            <label className="dk-label" htmlFor="aj-run-more">{t('jobs.run.more')}</label>
            <textarea
              id="aj-run-more"
              className="dk-textarea ag-mono"
              rows={3}
              value={more}
              onChange={(e) => setMore(e.target.value)}
              placeholder={'topic=AI trends\nformat=markdown'}
            />
            <p className="dk-hint">{t('jobs.run.moreHint', { gap: '{{.name}}' })}</p>
          </div>
          <details className="aj-media">
            <summary>{t('jobs.run.media')}{MEDIA.some(m => media[m.type].length) && <span className="dk-badge dk-badge--count">{MEDIA.reduce((n, m) => n + media[m.type].length, 0)}</span>}</summary>
            <p className="ag-note">{t('jobs.run.mediaHint')}</p>
            {MEDIA.map(m => (
              <div key={m.type} className="aj-media__group">
                <div className="aj-media__head">
                  <span><Icon name={m.icon} /> {t(`jobs.run.${m.type}`)} ({media[m.type].length})</span>
                  <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => { typeRef.current = m.type; fileRef.current?.click() }}>
                    <Icon name="plus" /> {t('jobs.run.add')}
                  </button>
                </div>
                {media[m.type].map((item, i) => (
                  <div key={i} className="aj-media__item">
                    <span className="aj-media__name">{item.name || item.url.slice(0, 40)}</span>
                    <button
                      type="button"
                      className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm"
                      aria-label={t('jobs.run.remove', { name: item.name || item.url.slice(0, 20) })}
                      onClick={() => setMedia(prev => ({ ...prev, [m.type]: prev[m.type].filter((_, j) => j !== i) }))}
                    >
                      <Icon name="close" />
                    </button>
                  </div>
                ))}
              </div>
            ))}
            <input ref={fileRef} type="file" multiple className="hidden" onChange={attach} />
          </details>
        </div>
        <div className="dk-dialog-foot">
          <button type="button" className="dk-btn dk-btn--secondary" onClick={onClose}>{t('jobs.run.cancel')}</button>
          <button type="button" className="dk-btn dk-btn--primary" disabled={busy} onClick={start}>
            {busy ? <><Icon name="spinner" spin /> {t('jobs.run.starting')}</> : <><Icon name="play" /> {t('jobs.run.start')}</>}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  )
}
