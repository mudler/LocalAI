/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { fineTuneApi } from '../utils/api'
import { useAuth } from '../context/AuthContext'
import { useResources } from '../hooks/useResources'
import { FT_STAGES, TERMINAL, appendLog, blocked, fineTuneChecks, logLines, looksLikeMemoryFailure, machineFacts, warned } from '../utils/tools'
import PageHeader from '../components/PageHeader'
import UnsavedChangesGuard from '../components/UnsavedChangesGuard'
import Dialog from '../components/Dialog'
import Icon from '../components/Icon'
import LoadingSpinner from '../components/LoadingSpinner'
import ToolSteps from '../components/tools/ToolSteps'
import Checks from '../components/tools/Checks'
import StageLine from '../components/tools/StageLine'
import JobLog from '../components/tools/JobLog'
import LossChart from '../components/tools/LossChart'
import '../components/tools/tools.css'

const TRAINING_METHODS = ['sft', 'dpo', 'grpo', 'rloo', 'reward', 'kto', 'orpo']
const ADAPTER_KINDS = ['lora', 'loha', 'lokr']
const FALLBACK_BACKEND = 'trl'
const OPTIMIZERS = ['adamw_torch', 'adamw_8bit', 'sgd', 'adafactor', 'prodigy']
const MIXED_PRECISION_OPTS = ['', 'fp16', 'bf16', 'no']
const QUANT_PRESETS = ['q4_k_m', 'q5_k_m', 'q8_0', 'f16', 'q4_0', 'q5_0']

const BUILTIN_REWARDS = [
  { name: 'format_reward', params: [] },
  { name: 'reasoning_accuracy_reward', params: [] },
  { name: 'length_reward', params: [{ key: 'target_length', default: '200' }] },
  { name: 'xml_tag_reward', params: [] },
  { name: 'no_repetition_reward', params: [] },
  { name: 'code_execution_reward', params: [] },
]

const ACTIVE_STATUSES = FT_STAGES
const BADGE = {
  queued: '', loading_model: 'dk-badge--warn', loading_dataset: 'dk-badge--warn', training: 'dk-badge--accent',
  saving: 'dk-badge--accent', completed: 'dk-badge--ok', failed: 'dk-badge--error', stopped: '',
}
const EXTRA_HANDLED = ['max_seq_length', 'save_total_limit', 'hf_token', 'eval_strategy', 'eval_steps', 'eval_split', 'eval_dataset_source', 'eval_split_ratio', 'voice', 'val_dataset']

function StatusBadge({ status }) {
  const { t } = useTranslation('tools')
  return <span className={`dk-badge ${BADGE[status] || ''}`} data-status={status}>{t(`fineTune.status.${status}`, { defaultValue: String(status || '').replace(/_/g, ' ') })}</span>
}

function Field({ id, label, hint, className = '', children }) {
  return (
    <div className={`dk-field ${className}`.trim()}>
      <label className="dk-label" htmlFor={id}>{label}</label>
      {children}
      {hint && <p className="dk-hint">{hint}</p>}
    </div>
  )
}

function Select({ id, value, onChange, children }) {
  return (
    <span className="dk-select-wrap">
      <select id={id} className="dk-select" value={value} onChange={onChange}>{children}</select>
    </span>
  )
}

function Section({ done, title, children }) {
  return (
    <section className="bt-section" data-done={done ? 'true' : 'false'}>
      <h2 className="bt-section__title">
        <span className="bt-section__mark" aria-hidden="true">{done ? <Icon name="check" /> : null}</span>
        {title}
      </h2>
      <div className="bt-section__body">{children}</div>
    </section>
  )
}

function KeyValueEditor({ entries, onChange }) {
  const { t } = useTranslation('tools')
  const update = (i, field, val) => onChange(entries.map((e, idx) => (idx === i ? { ...e, [field]: val } : e)))
  return (
    <div className="bt-kv">
      {entries.map((entry, i) => (
        <div key={i} className="bt-kv__row">
          <input className="dk-input" value={entry.key} onChange={e => update(i, 'key', e.target.value)} placeholder={t('form.key')} aria-label={`Extra option ${i + 1} key`} />
          <input className="dk-input" value={entry.value} onChange={e => update(i, 'value', e.target.value)} placeholder={t('form.value')} aria-label={`Extra option ${i + 1} value`} />
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" onClick={() => onChange(entries.filter((_, idx) => idx !== i))} aria-label={`Remove extra option ${i + 1}`}>
            <Icon name="close" />
          </button>
        </div>
      ))}
      <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => onChange([...entries, { key: '', value: '' }])}>
        <Icon name="plus" /> {t('form.addOption')}
      </button>
    </div>
  )
}

function CopyButton({ text }) {
  const { t } = useTranslation('tools')
  const [copied, setCopied] = useState(false)
  const copy = (e) => {
    e.stopPropagation()
    navigator.clipboard?.writeText(text).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500) }).catch(() => {})
  }
  return (
    <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" onClick={copy} title={t('form.copy')} aria-label={t('form.copy')}>
      <Icon name={copied ? 'check' : 'copy'} />
    </button>
  )
}

function etaText(t, seconds) {
  if (!seconds || seconds <= 0) return ''
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (h > 0) return t('run.eta', { time: `${h}h ${m}m` })
  if (m > 0) return t('run.eta', { time: `${m} min` })
  return t('run.etaSeconds', { seconds: Math.max(1, Math.floor(seconds)) })
}

const kindOf = (job) => `${job.training_type || 'lora'}, ${job.training_method || 'sft'}`
const startedAt = (job) => {
  const d = new Date(job.created_at)
  return Number.isNaN(d.getTime()) ? (job.created_at || '') : d.toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

// A running, failed or finished job: progress, the loss chart, the log, the
// checkpoints and what to do with the result. The server streams progress
// events while the job is active; a finished job is read from the job record.
function JobView({ job, onStop, onReuse, onResume, onTerminal, exportCheckpoint }) {
  const { t } = useTranslation('tools')
  const [events, setEvents] = useState([])
  const [latest, setLatest] = useState(null)
  const [log, setLog] = useState([])
  const [connecting, setConnecting] = useState(true)
  const [stopOpen, setStopOpen] = useState(false)
  const [checkpoints, setCheckpoints] = useState([])
  const previousRef = useRef(null)
  const terminalRef = useRef(onTerminal)
  terminalRef.current = onTerminal

  useEffect(() => {
    previousRef.current = null
    setEvents([])
    setLatest(null)
    setLog([])
    if (!job || !ACTIVE_STATUSES.includes(job.status)) { setConnecting(false); return undefined }
    setConnecting(true)
    const es = new EventSource(fineTuneApi.progressUrl(job.id))
    es.onmessage = (e) => {
      try {
        const data = JSON.parse(e.data)
        setConnecting(false)
        setLatest(data)
        setLog(prev => appendLog(prev, logLines(data, previousRef.current)))
        previousRef.current = data
        if (data.loss > 0 || data.eval_loss > 0 || data.learning_rate > 0 || data.grad_norm > 0) {
          setEvents(prev => {
            const last = prev[prev.length - 1]
            return last && last.current_step === data.current_step ? [...prev.slice(0, -1), data] : [...prev, data]
          })
        }
        if (TERMINAL.includes(data.status)) { es.close(); terminalRef.current?.() }
      } catch (_) { /* a partial frame */ }
    }
    es.onerror = () => { setConnecting(false); es.close() }
    return () => es.close()
  // eslint-disable-next-line react-hooks/exhaustive-deps -- keyed on the job id: the effect restarts per job, not per poll refresh
  }, [job?.id])

  const status = latest?.status || job.status
  const active = ACTIVE_STATUSES.includes(status)
  const failed = status === 'failed'
  const progress = Math.min(latest?.progress_percent || 0, 100)

  useEffect(() => {
    if (!job || active) return
    fineTuneApi.listCheckpoints(job.id).then(r => setCheckpoints(r.checkpoints || [])).catch(() => {})
  // eslint-disable-next-line react-hooks/exhaustive-deps -- keyed on the job id: the effect restarts per job, not per poll refresh
  }, [job?.id, active])

  const message = latest?.message || job.message || ''
  const lastCheckpoint = checkpoints.length ? checkpoints[checkpoints.length - 1] : null
  const [fixes, setFixes] = useState({ batch: true, checkpointing: true })

  const stageLabels = Object.fromEntries(FT_STAGES.map(s => [s, t(`fineTune.status.${s}`)]))

  return (
    <div className="bt-stack" data-testid="job-view" data-status={status}>
      {failed && (
        <section className="bt-alert" role="alert" data-testid="job-failed">
          <Icon name="alert-circle" />
          <div>
            <p className="bt-alert__title">
              {latest?.current_step > 0
                ? t('fineTune.failedAt', { step: latest.current_step, total: latest.total_steps })
                : t('fineTune.failed')}
            </p>
            <p className="bt-alert__text" data-testid="job-failed-message">{message || t('fineTune.noMessage')}</p>
          </div>
        </section>
      )}

      <section className="bt-job dk-card" aria-labelledby="bt-job-title">
        <div className="bt-job__top">
          <div className="bt-job__who">
            <h2 className="bt-job__title" id="bt-job-title">{job.model}</h2>
            <p className="bt-job__meta">
              {t('fineTune.jobMeta', { backend: job.backend || FALLBACK_BACKEND, method: job.training_method || 'sft', kind: job.training_type || 'lora', started: startedAt(job) })}
              <span className="dk-mono"> · {job.id?.slice(0, 8)}</span>
            </p>
          </div>
          <div className="bt-job__acts">
            <StatusBadge status={status} />
            {active && (
              <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => setStopOpen(true)} data-testid="job-stop">
                <Icon name="stop" /> {t('run.stop')}
              </button>
            )}
          </div>
        </div>

        {connecting && !latest && active ? (
          <p className="bt-job__wait" role="status"><LoadingSpinner size="sm" /> {t('run.connecting')}</p>
        ) : (
          <>
            {(latest || status === 'completed') && (
              <div className="bt-job__figures">
                <span className="bt-job__percent">{status === 'completed' ? '100' : progress.toFixed(0)}<small>%</small></span>
                {latest && latest.total_steps > 0 && <span>{t('run.step')} <b className="dk-mono">{latest.current_step}</b> {t('run.of')} <b className="dk-mono">{latest.total_steps}</b></span>}
                {latest && latest.total_epochs > 0 && <span>{t('run.epoch')} <b className="dk-mono">{latest.current_epoch?.toFixed(1)}</b> {t('run.of')} <b className="dk-mono">{latest.total_epochs?.toFixed(0)}</b></span>}
                {active && etaText(t, latest?.eta_seconds) && <span>{etaText(t, latest.eta_seconds)}</span>}
                {latest?.extra_metrics?.tokens_per_second > 0 && <span><b className="dk-mono">{latest.extra_metrics.tokens_per_second.toFixed(0)}</b> {t('run.tokens')}</span>}
              </div>
            )}
            {(active || latest) && (
              <div
                className={`dk-progress${failed ? ' dk-progress--error' : status === 'completed' ? ' dk-progress--ok' : ''}`}
                role="progressbar" aria-valuemin={0} aria-valuemax={100}
                aria-valuenow={Math.round(status === 'completed' ? 100 : progress)} aria-label={t('run.progress')}
                style={{ '--dk-value': `${status === 'completed' ? 100 : progress}%` }}
              >
                <span className="dk-progress-bar" />
              </div>
            )}
            {active && (
              <StageLine stages={FT_STAGES} labels={stageLabels} status={status} failed={failed} label={t('run.stages')} />
            )}
            {!failed && message && <p className="dk-hint bt-job__message">{message}</p>}
            {status === 'completed' && (
              <p className="bt-job__done" data-testid="job-finished">
                <Icon name="check-circle" /> {events.length && events[events.length - 1].loss > 0
                  ? t('fineTune.finishedLoss', { loss: events[events.length - 1].loss.toFixed(4) })
                  : t('fineTune.finished')}
              </p>
            )}
            {status === 'stopped' && <p className="dk-hint">{t('fineTune.stopped')}</p>}
          </>
        )}
      </section>

      {failed && (
        <section className="bt-try dk-card" data-testid="job-try">
          <h2 className="bt-h2">{t('fineTune.tryTitle')}</h2>
          {looksLikeMemoryFailure(message) ? (
            <>
              <p className="dk-hint">{t('fineTune.tryText')}</p>
              <label className="dk-choice"><input className="dk-check" type="checkbox" checked={fixes.batch} onChange={e => setFixes({ ...fixes, batch: e.target.checked })} /> {t('fineTune.fixBatch')}</label>
              <label className="dk-choice"><input className="dk-check" type="checkbox" checked={fixes.checkpointing} onChange={e => setFixes({ ...fixes, checkpointing: e.target.checked })} /> {t('fineTune.fixCheckpointing')}</label>
            </>
          ) : (
            <p className="dk-hint">{t('fineTune.tryPlain')}</p>
          )}
          <div className="bt-try__acts">
            <button type="button" className="dk-btn dk-btn--primary" onClick={() => onReuse(job, looksLikeMemoryFailure(message) ? fixes : null)} data-testid="job-retry">
              <Icon name="refresh" /> {looksLikeMemoryFailure(message) ? t('fineTune.retryChanges') : t('fineTune.reuse')}
            </button>
            {lastCheckpoint && (
              <button type="button" className="dk-btn dk-btn--secondary" onClick={() => onResume(lastCheckpoint)}>
                <Icon name="play" /> {t('fineTune.resumeFrom', { step: lastCheckpoint.step })}
              </button>
            )}
          </div>
        </section>
      )}

      {(active || events.length > 0) && <LossChart events={events} totalSteps={latest?.total_steps} />}
      {(active || log.length > 0) && <JobLog lines={log} name={`finetune-${job.id?.slice(0, 8)}`} />}

      {!active && checkpoints.length > 0 && (
        <section className="bt-block dk-card" aria-labelledby="bt-cp-title">
          <h2 className="bt-h2" id="bt-cp-title">{t('checkpoints.title')}</h2>
          <div className="dk-table-wrap" role="region" aria-labelledby="bt-cp-title" tabIndex={0}>
            <table className="dk-table dk-table--compact" data-testid="job-checkpoints">
              <caption className="dk-sr-only">{t('checkpoints.title')}</caption>
              <thead><tr><th>{t('checkpoints.step')}</th><th className="dk-num dk-hide-phone">{t('checkpoints.epoch')}</th><th className="dk-num">{t('checkpoints.loss')}</th><th className="dk-hide-phone">{t('checkpoints.path')}</th><th><span className="dk-sr-only">{t('checkpoints.actions')}</span></th></tr></thead>
              <tbody>
                {checkpoints.map(cp => (
                  <tr key={cp.path} data-row>
                    <td><span className="dk-table-name">{cp.step}</span><span className="dk-table-sub">{cp.created_at}</span></td>
                    <td className="dk-num dk-hide-phone">{cp.epoch?.toFixed(2)}</td>
                    <td className="dk-num">{cp.loss?.toFixed(4)}</td>
                    <td className="dk-hide-phone"><span className="bt-path dk-mono" title={cp.path}>{cp.path}</span><CopyButton text={cp.path} /></td>
                    <td>
                      <div className="bt-rowacts">
                        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => onResume(cp)} title={t('checkpoints.resumeTitle')}><Icon name="play" /> {t('checkpoints.resume')}</button>
                        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => exportCheckpoint(cp)} title={t('checkpoints.exportTitle')}><Icon name="export" /> {t('checkpoints.export')}</button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}

      {stopOpen && (
        <Dialog
          title={t('stop.title')}
          description={t('stop.text')}
          role="alertdialog"
          onClose={() => setStopOpen(false)}
          testId="stop-dialog"
          labelId="bt-stop-title"
          foot={(
            <>
              <button type="button" className="dk-btn dk-btn--ghost" onClick={() => setStopOpen(false)}>{t('stop.cancel')}</button>
              <button type="button" className="dk-btn dk-btn--secondary" data-testid="stop-discard" onClick={() => { setStopOpen(false); onStop(job.id, false) }}>{t('stop.discard')}</button>
              <button type="button" className="dk-btn dk-btn--primary" data-testid="stop-keep" onClick={() => { setStopOpen(false); onStop(job.id, true) }}>{t('stop.keep')}</button>
            </>
          )}
        />
      )}
    </div>
  )
}

// What to do with a finished job: export it as a model, then use it.
function ExportPanel({ job, prefilledCheckpoint }) {
  const { t } = useTranslation('tools')
  const [checkpoints, setCheckpoints] = useState([])
  const [exportFormat, setExportFormat] = useState('lora')
  const [quantMethod, setQuantMethod] = useState('q4_k_m')
  const [modelName, setModelName] = useState('')
  const [selectedCheckpoint, setSelectedCheckpoint] = useState('')
  const [exporting, setExporting] = useState(false)
  const [message, setMessage] = useState('')
  const [exportedModelName, setExportedModelName] = useState('')
  const [exportFailed, setExportFailed] = useState(false)
  const pollRef = useRef(null)

  useEffect(() => {
    if (!job) return
    fineTuneApi.listCheckpoints(job.id).then(r => setCheckpoints(r.checkpoints || [])).catch(() => {})
  // eslint-disable-next-line react-hooks/exhaustive-deps -- keyed on the job id: the effect restarts per job, not per poll refresh
  }, [job?.id])

  useEffect(() => { if (prefilledCheckpoint) setSelectedCheckpoint(prefilledCheckpoint.path || '') }, [prefilledCheckpoint])

  // Sync export state from the job record (first load, list refresh).
  useEffect(() => {
    if (!job) return
    if (job.export_status === 'exporting') {
      setExporting(true); setExportFailed(false)
      setMessage(job.export_message || t('export.working'))
    } else if (job.export_status === 'completed' && job.export_model_name) {
      setExporting(false); setExportFailed(false)
      setExportedModelName(job.export_model_name)
      setMessage(t('export.done', { name: job.export_model_name }))
    } else if (job.export_status === 'failed') {
      setExporting(false); setExportFailed(true)
      setMessage(t('export.failed', { message: job.export_message || t('export.unknown') }))
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps -- keyed on the job id: the effect restarts per job, not per poll refresh
  }, [job?.export_status, job?.export_model_name, job?.export_message])

  useEffect(() => {
    if (!exporting || !job) return undefined
    pollRef.current = setInterval(async () => {
      try {
        const updated = await fineTuneApi.getJob(job.id)
        if (updated.export_status === 'completed') {
          setExporting(false); setExportFailed(false)
          const name = updated.export_model_name || modelName || 'exported model'
          setExportedModelName(name)
          setMessage(t('export.done', { name }))
          clearInterval(pollRef.current)
        } else if (updated.export_status === 'failed') {
          setExporting(false); setExportFailed(true)
          setMessage(t('export.failed', { message: updated.export_message || t('export.unknown') }))
          clearInterval(pollRef.current)
        } else if (updated.export_status === 'exporting' && updated.export_message) {
          setMessage(updated.export_message)
        }
      } catch (_) { /* try again at the next tick */ }
    }, 3000)
    return () => clearInterval(pollRef.current)
  // eslint-disable-next-line react-hooks/exhaustive-deps -- keyed on the job id: the effect restarts per job, not per poll refresh
  }, [exporting, job?.id])

  const handleExport = async () => {
    setExporting(true); setExportFailed(false)
    setMessage(t('export.working'))
    setExportedModelName('')
    try {
      await fineTuneApi.exportModel(job.id, {
        name: modelName || undefined,
        checkpoint_path: selectedCheckpoint || job.output_dir,
        export_format: exportFormat,
        quantization_method: exportFormat === 'gguf' ? quantMethod : '',
        model: job.model,
      })
    } catch (e) {
      setMessage(t('export.failed', { message: e.message }))
      setExportFailed(true); setExporting(false)
    }
  }

  if (!job || !TERMINAL.includes(job.status)) return null

  return (
    <>
      {exportedModelName && !exportFailed && (
        <section className="bt-next dk-card" data-testid="export-next">
          <span className="bt-next__mark"><Icon name="check-circle" /></span>
          <div>
            <h2 className="bt-h2">{t('export.nextTitle', { name: exportedModelName })}</h2>
            <p className="dk-hint">{t('export.nextText')}</p>
            <div className="bt-next__acts">
              <Link className="dk-btn dk-btn--primary" to={`/app/chat/${encodeURIComponent(exportedModelName)}`}><Icon name="chat" /> {t('export.chat', { name: exportedModelName })}</Link>
              <Link className="dk-btn dk-btn--secondary" to="/app/models?view=installed"><Icon name="cube" /> {t('export.models')}</Link>
              <a className="dk-btn dk-btn--ghost" href={fineTuneApi.downloadUrl(job.id)} download><Icon name="download" /> {t('export.archive')}</a>
            </div>
          </div>
        </section>
      )}
      <section className="bt-block dk-card" aria-labelledby="bt-export-title">
        <h2 className="bt-h2" id="bt-export-title">{t('export.title')}</h2>
        <p className="dk-hint">{t('export.lede')}</p>
        <div className="bt-form">
          {checkpoints.length > 0 && (
            <Field id="ft-export-checkpoint" label={t('export.checkpoint')}>
              <Select id="ft-export-checkpoint" value={selectedCheckpoint} onChange={e => setSelectedCheckpoint(e.target.value)}>
                <option value="">{t('export.final')}</option>
                {checkpoints.map(cp => <option key={cp.path} value={cp.path}>{t('export.checkpointOption', { step: cp.step, loss: cp.loss?.toFixed(4) })}</option>)}
              </Select>
            </Field>
          )}
          <div className="bt-pair">
            <Field id="ft-export-format" label={t('export.format')}>
              <Select id="ft-export-format" value={exportFormat} onChange={e => setExportFormat(e.target.value)}>
                <option value="lora">{t('export.lora')}</option>
                <option value="merged_16bit">{t('export.merged16')}</option>
                <option value="merged_4bit">{t('export.merged4')}</option>
                <option value="gguf">GGUF</option>
              </Select>
            </Field>
            {exportFormat === 'gguf' && (
              <Field id="ft-export-quant" label={t('export.quant')}>
                <input id="ft-export-quant" className="dk-input" list="quant-presets" value={quantMethod} onChange={e => setQuantMethod(e.target.value)} placeholder="q4_k_m, bf16, f32" />
                <datalist id="quant-presets">{QUANT_PRESETS.map(q => <option key={q} value={q} />)}</datalist>
              </Field>
            )}
          </div>
          <Field id="ft-export-name" label={t('export.name')} hint={t('export.nameHint')}>
            <input id="ft-export-name" type="text" className="dk-input" value={modelName} onChange={e => setModelName(e.target.value)} placeholder="my-finetuned-model" />
          </Field>
          <div className="bt-actions">
            <button type="button" className="dk-btn dk-btn--primary" onClick={handleExport} disabled={exporting} aria-busy={exporting || undefined}>
              {exporting ? <><LoadingSpinner size="sm" /> {t('export.exporting')}</> : <><Icon name="download" /> {t('export.go')}</>}
            </button>
          </div>
          {message && (!exportedModelName || exportFailed) && (
            <p className={`bt-status${exportFailed ? ' bt-status--error' : ''}`} role="status" data-testid="export-status">
              {exporting && <LoadingSpinner size="sm" />} {message}
            </p>
          )}
        </div>
      </section>
    </>
  )
}

export default function FineTune() {
  const { t } = useTranslation('tools')
  const { isAdmin } = useAuth()
  const { resources } = useResources(10000)
  const facts = useMemo(() => machineFacts(resources), [resources])

  const [jobs, setJobs] = useState([])
  const [jobsLoaded, setJobsLoaded] = useState(false)
  const [selectedJob, setSelectedJob] = useState(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [backends, setBackends] = useState(null)
  const [exportCheckpoint, setExportCheckpoint] = useState(null)
  const [deleting, setDeleting] = useState(null)
  const initialConfigRef = useRef(null)

  // Form state
  const [model, setModel] = useState('')
  const [backend, setBackend] = useState(FALLBACK_BACKEND)
  const [trainingMethod, setTrainingMethod] = useState('sft')
  const [trainingType, setTrainingType] = useState('lora')
  const [datasetSource, setDatasetSource] = useState('')
  const [datasetFile, setDatasetFile] = useState(null)
  const [datasetSplit, setDatasetSplit] = useState('')
  const [numEpochs, setNumEpochs] = useState(3)
  const [batchSize, setBatchSize] = useState(2)
  const [learningRate, setLearningRate] = useState(0.0002)
  const [learningRateText, setLearningRateText] = useState('0.0002')
  const [adapterRank, setAdapterRank] = useState(16)
  const [adapterAlpha, setAdapterAlpha] = useState(16)
  const [adapterDropout, setAdapterDropout] = useState(0)
  const [targetModules, setTargetModules] = useState('')
  const [gradAccum, setGradAccum] = useState(4)
  const [warmupSteps, setWarmupSteps] = useState(5)
  const [maxSteps, setMaxSteps] = useState(0)
  const [saveSteps, setSaveSteps] = useState(500)
  const [weightDecay, setWeightDecay] = useState(0)
  const [maxSeqLength, setMaxSeqLength] = useState(2048)
  const [optimizer, setOptimizer] = useState('adamw_torch')
  const [gradCheckpointing, setGradCheckpointing] = useState(false)
  const [seed, setSeed] = useState(0)
  const [mixedPrecision, setMixedPrecision] = useState('')
  const [extraOptions, setExtraOptions] = useState([])
  const [liquidAudioVoice, setLiquidAudioVoice] = useState('')
  const [liquidAudioValDataset, setLiquidAudioValDataset] = useState('')
  const [hfToken, setHfToken] = useState('')
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [resumeFromCheckpoint, setResumeFromCheckpoint] = useState('')
  const [saveTotalLimit, setSaveTotalLimit] = useState(0)
  const [evalEnabled, setEvalEnabled] = useState(false)
  const [evalStrategy, setEvalStrategy] = useState('steps')
  const [evalSteps, setEvalSteps] = useState(0)
  const [evalSplit, setEvalSplit] = useState('')
  const [evalDatasetSource, setEvalDatasetSource] = useState('')
  const [evalSplitRatio, setEvalSplitRatio] = useState(0.1)
  const [rewardFunctions, setRewardFunctions] = useState([])
  const [showAddCustomReward, setShowAddCustomReward] = useState(false)
  const [customRewardName, setCustomRewardName] = useState('')
  const [customRewardCode, setCustomRewardCode] = useState('')

  const loadJobs = useCallback(async () => {
    try {
      const data = await fineTuneApi.listJobs()
      const list = data || []
      setJobs(list)
      setSelectedJob(prev => {
        if (!prev) return prev
        const fresh = list.find(j => j.id === prev.id)
        return fresh ? { ...prev, ...fresh } : prev
      })
    } catch (_) { /* the next poll tries again */ } finally {
      setJobsLoaded(true)
    }
  }, [])

  useEffect(() => {
    loadJobs()
    const interval = setInterval(loadJobs, 10000)
    return () => clearInterval(interval)
  }, [loadJobs])

  useEffect(() => {
    fineTuneApi.listBackends()
      .then(data => {
        const list = Array.isArray(data) ? data : []
        setBackends(list)
        if (list.length > 0) setBackend(prev => (list.some(b => b.name === prev) ? prev : list[0].name))
      })
      .catch(() => setBackends(null))
  }, [])

  const backendNames = backends && backends.length > 0 ? backends.map(b => b.name) : [FALLBACK_BACKEND]
  const isAdapter = ADAPTER_KINDS.includes(trainingType)

  const handleSubmit = async (e) => {
    e.preventDefault()
    setLoading(true)
    setError('')
    try {
      let dsSource = datasetSource
      if (datasetFile) {
        const result = await fineTuneApi.uploadDataset(datasetFile)
        dsSource = result.path
      }
      const extra = {}
      if (maxSeqLength) extra.max_seq_length = String(maxSeqLength)
      if (hfToken.trim()) extra.hf_token = hfToken.trim()
      if (saveTotalLimit > 0) extra.save_total_limit = String(saveTotalLimit)
      if (evalEnabled) {
        extra.eval_strategy = evalStrategy || 'steps'
        if (evalSteps > 0) extra.eval_steps = String(evalSteps)
        if (evalSplit.trim()) extra.eval_split = evalSplit.trim()
        if (evalDatasetSource.trim()) extra.eval_dataset_source = evalDatasetSource.trim()
        if (evalSplitRatio > 0 && evalSplitRatio !== 0.1) extra.eval_split_ratio = String(evalSplitRatio)
      } else {
        extra.eval_strategy = 'no'
      }
      for (const { key, value } of extraOptions) if (key.trim()) extra[key.trim()] = value
      // The Python backend reads `voice` and `val_dataset` from extra_options.
      if (backend === 'liquid-audio') {
        if (liquidAudioVoice) extra.voice = liquidAudioVoice
        if (liquidAudioValDataset.trim()) extra.val_dataset = liquidAudioValDataset.trim()
      }

      const req = {
        model,
        backend,
        training_method: trainingMethod,
        training_type: trainingType,
        dataset_source: dsSource,
        dataset_split: datasetSplit || undefined,
        num_epochs: numEpochs,
        batch_size: batchSize,
        learning_rate: learningRate,
        adapter_rank: isAdapter ? adapterRank : 0,
        adapter_alpha: isAdapter ? adapterAlpha : 0,
        adapter_dropout: isAdapter && adapterDropout > 0 ? adapterDropout : undefined,
        target_modules: isAdapter && targetModules.trim() ? targetModules.split(',').map(s => s.trim()) : undefined,
        gradient_accumulation_steps: gradAccum,
        warmup_steps: warmupSteps,
        max_steps: maxSteps > 0 ? maxSteps : undefined,
        save_steps: saveSteps > 0 ? saveSteps : undefined,
        weight_decay: weightDecay > 0 ? weightDecay : undefined,
        gradient_checkpointing: gradCheckpointing,
        optimizer,
        seed: seed > 0 ? seed : undefined,
        mixed_precision: mixedPrecision || undefined,
        resume_from_checkpoint: resumeFromCheckpoint || undefined,
        extra_options: Object.keys(extra).length > 0 ? extra : undefined,
        reward_functions: trainingMethod === 'grpo' && rewardFunctions.length > 0 ? rewardFunctions : undefined,
      }

      const resp = await fineTuneApi.startJob(req)
      setResumeFromCheckpoint('')
      // Job submitted: rebaseline so leaving the page no longer warns.
      initialConfigRef.current = JSON.stringify(getFormConfig())
      await loadJobs()
      setSelectedJob({ ...req, id: resp.id, status: 'queued', created_at: new Date().toISOString() })
    } catch (err) {
      setError(err.message)
    }
    setLoading(false)
  }

  const handleStop = async (jobId, saveCheckpoint = true) => {
    try {
      await fineTuneApi.stopJob(jobId, saveCheckpoint)
      await loadJobs()
    } catch (err) {
      setError(err.message)
    }
  }

  const confirmDelete = async () => {
    const jobId = deleting
    setDeleting(null)
    try {
      await fineTuneApi.deleteJob(jobId)
      if (selectedJob?.id === jobId) setSelectedJob(null)
      await loadJobs()
    } catch (err) {
      setError(err.message)
    }
  }

  const getFormConfig = () => {
    const extra = {}
    for (const { key, value } of extraOptions) if (key.trim()) extra[key.trim()] = value
    if (backend === 'liquid-audio') {
      if (liquidAudioVoice) extra.voice = liquidAudioVoice
      if (liquidAudioValDataset.trim()) extra.val_dataset = liquidAudioValDataset.trim()
    }
    return {
      model, backend,
      training_method: trainingMethod,
      training_type: trainingType,
      adapter_rank: adapterRank,
      adapter_alpha: adapterAlpha,
      adapter_dropout: adapterDropout,
      target_modules: targetModules.trim() ? targetModules.split(',').map(s => s.trim()) : [],
      dataset_source: datasetSource,
      dataset_split: datasetSplit,
      num_epochs: numEpochs,
      batch_size: batchSize,
      learning_rate: learningRate,
      gradient_accumulation_steps: gradAccum,
      warmup_steps: warmupSteps,
      max_steps: maxSteps,
      save_steps: saveSteps,
      weight_decay: weightDecay,
      gradient_checkpointing: gradCheckpointing,
      optimizer, seed,
      mixed_precision: mixedPrecision,
      max_seq_length: maxSeqLength,
      eval_strategy: evalEnabled ? (evalStrategy || 'steps') : 'no',
      eval_steps: evalSteps,
      eval_split: evalSplit,
      eval_dataset_source: evalDatasetSource,
      eval_split_ratio: evalSplitRatio,
      extra_options: Object.keys(extra).length > 0 ? extra : {},
      reward_functions: rewardFunctions.length > 0 ? rewardFunctions : undefined,
    }
  }

  const applyFormConfig = (config) => {
    if (config.model != null) setModel(config.model)
    if (config.backend != null) setBackend(config.backend)
    if (config.training_method != null) setTrainingMethod(config.training_method)
    if (config.training_type != null) setTrainingType(config.training_type)
    if (config.adapter_rank != null) setAdapterRank(Number(config.adapter_rank))
    if (config.adapter_alpha != null) setAdapterAlpha(Number(config.adapter_alpha))
    if (config.adapter_dropout != null) setAdapterDropout(Number(config.adapter_dropout))
    if (config.target_modules != null) {
      setTargetModules(Array.isArray(config.target_modules) ? config.target_modules.join(', ') : String(config.target_modules))
    }
    if (config.dataset_source != null) setDatasetSource(config.dataset_source)
    if (config.dataset_split != null) setDatasetSplit(config.dataset_split)
    if (config.num_epochs != null) setNumEpochs(Number(config.num_epochs))
    if (config.batch_size != null) setBatchSize(Number(config.batch_size))
    if (config.learning_rate != null) { setLearningRate(Number(config.learning_rate)); setLearningRateText(String(config.learning_rate)) }
    if (config.gradient_accumulation_steps != null) setGradAccum(Number(config.gradient_accumulation_steps))
    if (config.warmup_steps != null) setWarmupSteps(Number(config.warmup_steps))
    if (config.max_steps != null) setMaxSteps(Number(config.max_steps))
    if (config.save_steps != null) setSaveSteps(Number(config.save_steps))
    if (config.weight_decay != null) setWeightDecay(Number(config.weight_decay))
    if (config.gradient_checkpointing != null) setGradCheckpointing(Boolean(config.gradient_checkpointing))
    if (config.optimizer != null) setOptimizer(config.optimizer)
    if (config.seed != null) setSeed(Number(config.seed))
    if (config.mixed_precision != null) setMixedPrecision(config.mixed_precision)

    // max_seq_length is a top-level field or sits inside extra_options.
    if (config.max_seq_length != null) {
      setMaxSeqLength(Number(config.max_seq_length))
    } else if (config.extra_options?.max_seq_length != null) {
      setMaxSeqLength(Number(config.extra_options.max_seq_length))
    }

    // Eval options: the strategy tells whether evaluation was on.
    const restoreEval = (strategy, steps, split, src, ratio) => {
      if (strategy != null && strategy !== 'no') {
        setEvalEnabled(true)
        setEvalStrategy(strategy)
      } else if (strategy === 'no') {
        setEvalEnabled(false)
      }
      if (steps != null) setEvalSteps(Number(steps))
      if (split != null) setEvalSplit(split)
      if (src != null) setEvalDatasetSource(src)
      if (ratio != null) setEvalSplitRatio(Number(ratio))
    }
    restoreEval(config.eval_strategy, config.eval_steps, config.eval_split, config.eval_dataset_source, config.eval_split_ratio)
    // extra_options overrides the top level when it carries them.
    const eo = config.extra_options
    if (eo) restoreEval(eo.eval_strategy, eo.eval_steps, eo.eval_split, eo.eval_dataset_source, eo.eval_split_ratio)
    if (eo?.save_total_limit != null) setSaveTotalLimit(Number(eo.save_total_limit))

    // liquid-audio extras; they are also kept out of the free-form list below.
    if (eo?.voice != null) setLiquidAudioVoice(String(eo.voice))
    if (eo?.val_dataset != null) setLiquidAudioValDataset(String(eo.val_dataset))

    if (config.extra_options && typeof config.extra_options === 'object') {
      setExtraOptions(Object.entries(config.extra_options)
        .filter(([k]) => !EXTRA_HANDLED.includes(k))
        .map(([key, value]) => ({ key, value: String(value) })))
    }
    setRewardFunctions(Array.isArray(config.reward_functions) ? config.reward_functions : [])
  }

  const handleExportConfig = () => {
    const url = URL.createObjectURL(new Blob([JSON.stringify(getFormConfig(), null, 2)], { type: 'application/json' }))
    const a = document.createElement('a')
    a.href = url
    a.download = 'finetune-config.json'
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
  }

  const handleImportConfig = () => {
    const input = document.createElement('input')
    input.type = 'file'
    input.accept = '.json'
    input.onchange = (e) => {
      const file = e.target.files[0]
      if (!file) return
      const reader = new FileReader()
      reader.onload = (ev) => {
        try {
          applyFormConfig(JSON.parse(ev.target.result))
          setSelectedJob(null)
          setError('')
        } catch {
          setError(t('fineTune.badConfig'))
        }
      }
      reader.readAsText(file)
    }
    input.click()
  }

  // Put a job's setup back in the form. `fixes` are the changes ticked on a
  // memory failure: they are applied to this copy and nothing starts until the
  // person presses Start.
  const handleUseConfig = (job, fixes) => {
    applyFormConfig(job.config || job)
    setResumeFromCheckpoint('')
    if (fixes?.batch) setBatchSize(1)
    if (fixes?.checkpointing) setGradCheckpointing(true)
    setSelectedJob(null)
  }

  const handleResumeFromCheckpoint = (checkpoint) => {
    if (!selectedJob) return
    applyFormConfig(selectedJob.config || selectedJob)
    setResumeFromCheckpoint(checkpoint.path)
    setShowAdvanced(true)
    setSelectedJob(null)
  }

  // Baseline for the unsaved-changes guard: lazily taken on first render.
  if (initialConfigRef.current === null) initialConfigRef.current = JSON.stringify(getFormConfig())
  const dirty = JSON.stringify(getFormConfig()) !== initialConfigRef.current

  const checks = useMemo(() => fineTuneChecks({
    form: {
      model, datasetSource, datasetFileName: datasetFile?.name || '', backend, batchSize, maxSeqLength, gradAccum,
      gradCheckpointing, trainingType, trainingMethod, rewardCount: rewardFunctions.length,
      resumeFrom: resumeFromCheckpoint, hfToken: hfToken.trim(),
    },
    facts,
    backends: { fineTune: backends },
  }), [model, datasetSource, datasetFile, backend, batchSize, maxSeqLength, gradAccum, gradCheckpointing, trainingType, trainingMethod, rewardFunctions.length, resumeFromCheckpoint, hfToken, facts, backends])
  const stop = blocked(checks)
  const warnings = warned(checks)
  const firstMissing = checks.find(c => c.tone === 'fail')

  const steps = [
    { key: 'setup', label: t('steps.setup') },
    { key: 'check', label: t('steps.check') },
    { key: 'run', label: t('steps.run') },
    { key: 'result', label: t('steps.result') },
  ]
  let current = stop ? 0 : 1
  let failed = false
  if (selectedJob) {
    if (selectedJob.status === 'failed') { current = 2; failed = true } else if (TERMINAL.includes(selectedJob.status)) current = 3
    else current = 2
  }

  const kindWords = trainingType === 'full' ? t('form.summaryFull') : t(`form.summary_${trainingType}`)
  const modelShort = model.split('/').pop() || model

  return (
    <div className="page page--medium bt-page" data-testid="fine-tune-page">
      <UnsavedChangesGuard when={dirty && !selectedJob && !loading} />
      <PageHeader
        title={<>{t('fineTune.title')} <span className="dk-badge dk-badge--warn bt-experimental">{t('landing.experimental')}</span></>}
        supporting={t('fineTune.lede')}
        actions={(
          <div className="bt-head-acts">
            {selectedJob && (
              <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => setSelectedJob(null)} data-testid="job-back">
                <Icon name="plus" /> {t('fineTune.newJob')}
              </button>
            )}
            <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={handleImportConfig}>
              <Icon name="upload" /> {t('fineTune.importConfig')}
            </button>
          </div>
        )}
      />
      <ToolSteps steps={steps} current={current} failed={failed} label={t('steps.label')} />

      {error && (
        <div className="bt-alert" role="alert" data-testid="tool-error">
          <Icon name="warning" />
          <div><p className="bt-alert__text">{error}</p></div>
        </div>
      )}

      {selectedJob ? (
        <>
          <JobView
            job={selectedJob}
            onStop={handleStop}
            onReuse={handleUseConfig}
            onResume={handleResumeFromCheckpoint}
            onTerminal={loadJobs}
            exportCheckpoint={setExportCheckpoint}
          />
          <ExportPanel job={selectedJob} prefilledCheckpoint={exportCheckpoint} />
        </>
      ) : (
        <form onSubmit={handleSubmit} className="bt-form-page" data-testid="fine-tune-form">
          {resumeFromCheckpoint && (
            <div className="bt-banner" role="status">
              <Icon name="refresh" />
              <span>{t('fineTune.resuming')} <code className="dk-mono">{resumeFromCheckpoint}</code></span>
              <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => setResumeFromCheckpoint('')}><Icon name="close" /> {t('fineTune.clear')}</button>
            </div>
          )}

          <Section done={!!model.trim()} title={t('form.baseModel')}>
            <Field id="ft-model" label={t('form.model')} hint={t('form.modelHint')}>
              <input id="ft-model" type="text" className="dk-input dk-input--mono" value={model} onChange={e => setModel(e.target.value)} placeholder="TinyLlama/TinyLlama-1.1B-Chat-v1.0" required />
            </Field>
          </Section>

          <Section done={!!(datasetSource.trim() || datasetFile)} title={t('form.data')}>
            <div className="bt-pair bt-pair--wide">
              <Field id="ft-dataset" label={t('form.dataset')} hint={t('form.datasetHint')}>
                <input id="ft-dataset" type="text" className="dk-input dk-input--mono" value={datasetSource} onChange={e => setDatasetSource(e.target.value)} placeholder="tatsu-lab/alpaca" />
              </Field>
              <Field id="ft-split" label={t('form.split')}>
                <input id="ft-split" type="text" className="dk-input dk-input--mono" value={datasetSplit} onChange={e => setDatasetSplit(e.target.value)} placeholder="train" />
              </Field>
            </div>
            <div className="bt-upload">
              <input id="ft-dataset-file" className="bt-upload__input" type="file" onChange={e => setDatasetFile(e.target.files[0] || null)} accept=".json,.jsonl,.csv" />
              <label className="dk-btn dk-btn--secondary dk-btn--sm" htmlFor="ft-dataset-file"><Icon name="upload" /> {t('form.upload')}</label>
              {datasetFile ? (
                <span className="dk-chip dk-chip--sm">
                  <span className="dk-mono">{datasetFile.name}</span>
                  <button type="button" className="dk-chip-x" aria-label={t('form.removeFile')} onClick={() => setDatasetFile(null)}><Icon name="close" /></button>
                </span>
              ) : (
                <span className="dk-hint">{t('form.uploadHint')}</span>
              )}
            </div>
          </Section>

          <Section done title={t('form.train')}>
            <fieldset className="bt-choices">
              <legend className="dk-label">{t('form.kind')}</legend>
              {[['adapter', isAdapter], ['full', trainingType === 'full']].map(([id, on]) => (
                <label key={id} className="bt-choice" data-checked={on ? 'true' : 'false'}>
                  <input
                    type="radio" className="dk-radio" name="ft-kind" checked={on}
                    onChange={() => setTrainingType(id === 'full' ? 'full' : (ADAPTER_KINDS.includes(trainingType) ? trainingType : 'lora'))}
                  />
                  <span><b>{t(`form.kinds.${id}.title`)}</b><span className="bt-choice__text">{t(`form.kinds.${id}.text`)}</span></span>
                </label>
              ))}
            </fieldset>
            <div className="bt-triple">
              <Field id="ft-epochs" label={t('form.epochs')}>
                <input id="ft-epochs" type="number" className="dk-input" value={numEpochs} onChange={e => setNumEpochs(Number(e.target.value))} min={1} />
              </Field>
              <Field id="ft-batch" label={t('form.batch')}>
                <input id="ft-batch" type="number" className="dk-input" value={batchSize} onChange={e => setBatchSize(Number(e.target.value))} min={1} />
              </Field>
              <Field id="ft-lr" label={t('form.lr')}>
                <input
                  id="ft-lr" type="text" className="dk-input dk-input--mono" value={learningRateText}
                  onChange={e => {
                    setLearningRateText(e.target.value)
                    const parsed = Number(e.target.value)
                    if (!isNaN(parsed) && parsed > 0) setLearningRate(parsed)
                  }}
                  placeholder="5e-5"
                />
              </Field>
            </div>

            <div className="bt-more">
              <button type="button" className="bt-more__toggle" onClick={() => setShowAdvanced(!showAdvanced)} aria-expanded={showAdvanced} aria-controls="ft-more" data-testid="ft-more-toggle">
                <Icon name={showAdvanced ? 'chevron-down' : 'chevron-right'} />
                <b>{t('form.more')}</b>
                {!showAdvanced && <span className="dk-hint">{t('form.moreHint')}</span>}
              </button>
              {showAdvanced && (
                <div className="bt-more__body" id="ft-more">
                  <div className="bt-grid">
                    <Field id="ft-backend" label={t('form.backend')}>
                      <Select id="ft-backend" value={backend} onChange={e => setBackend(e.target.value)}>
                        {backendNames.map(b => <option key={b} value={b}>{b}</option>)}
                      </Select>
                    </Field>
                    <Field id="ft-method" label={t('form.method')}>
                      <Select id="ft-method" value={trainingMethod} onChange={e => setTrainingMethod(e.target.value)}>
                        {TRAINING_METHODS.map(m => <option key={m} value={m}>{m.toUpperCase()}</option>)}
                      </Select>
                    </Field>
                    {isAdapter && (
                      <Field id="ft-type" label={t('form.adapterKind')}>
                        <Select id="ft-type" value={trainingType} onChange={e => setTrainingType(e.target.value)}>
                          {ADAPTER_KINDS.map(k => <option key={k} value={k}>{k}</option>)}
                        </Select>
                      </Field>
                    )}
                    {isAdapter && (
                      <>
                        <Field id="ft-rank" label={t('form.rank')}>
                          <input id="ft-rank" type="number" className="dk-input" value={adapterRank} onChange={e => setAdapterRank(Number(e.target.value))} min={1} />
                        </Field>
                        <Field id="ft-alpha" label={t('form.alpha')}>
                          <input id="ft-alpha" type="number" className="dk-input" value={adapterAlpha} onChange={e => setAdapterAlpha(Number(e.target.value))} min={1} />
                        </Field>
                        <Field id="ft-dropout" label={t('form.dropout')}>
                          <input id="ft-dropout" type="number" className="dk-input" value={adapterDropout} onChange={e => setAdapterDropout(Number(e.target.value))} min={0} max={1} step={0.05} />
                        </Field>
                      </>
                    )}
                    <Field id="ft-grad-accum" label={t('form.gradAccum')}>
                      <input id="ft-grad-accum" type="number" className="dk-input" value={gradAccum} onChange={e => setGradAccum(Number(e.target.value))} min={1} />
                    </Field>
                    <Field id="ft-warmup" label={t('form.warmup')}>
                      <input id="ft-warmup" type="number" className="dk-input" value={warmupSteps} onChange={e => setWarmupSteps(Number(e.target.value))} min={0} />
                    </Field>
                    <Field id="ft-seq-len" label={t('form.seqLen')}>
                      <input id="ft-seq-len" type="number" className="dk-input" value={maxSeqLength} onChange={e => setMaxSeqLength(Number(e.target.value))} min={64} />
                    </Field>
                    <Field id="ft-optimizer" label={t('form.optimizer')}>
                      <Select id="ft-optimizer" value={optimizer} onChange={e => setOptimizer(e.target.value)}>
                        {OPTIMIZERS.map(o => <option key={o} value={o}>{o}</option>)}
                      </Select>
                    </Field>
                    <Field id="ft-max-steps" label={t('form.maxSteps')} hint={t('form.maxStepsHint')}>
                      <input id="ft-max-steps" type="number" className="dk-input" value={maxSteps} onChange={e => setMaxSteps(Number(e.target.value))} min={0} />
                    </Field>
                    <Field id="ft-save-steps" label={t('form.saveSteps')}>
                      <input id="ft-save-steps" type="number" className="dk-input" value={saveSteps} onChange={e => setSaveSteps(Number(e.target.value))} min={0} />
                    </Field>
                    <Field id="ft-save-limit" label={t('form.saveLimit')} hint={t('form.saveLimitHint')}>
                      <input id="ft-save-limit" type="number" className="dk-input" value={saveTotalLimit} onChange={e => setSaveTotalLimit(Number(e.target.value))} min={0} />
                    </Field>
                    <Field id="ft-weight-decay" label={t('form.weightDecay')}>
                      <input id="ft-weight-decay" type="number" className="dk-input" value={weightDecay} onChange={e => setWeightDecay(Number(e.target.value))} min={0} step={0.01} />
                    </Field>
                    <Field id="ft-seed" label={t('form.seed')} hint={t('form.seedHint')}>
                      <input id="ft-seed" type="number" className="dk-input" value={seed} onChange={e => setSeed(Number(e.target.value))} min={0} />
                    </Field>
                    <Field id="ft-precision" label={t('form.precision')}>
                      <Select id="ft-precision" value={mixedPrecision} onChange={e => setMixedPrecision(e.target.value)}>
                        {MIXED_PRECISION_OPTS.map(o => <option key={o} value={o}>{o || t('form.auto')}</option>)}
                      </Select>
                    </Field>
                  </div>
                  {isAdapter && (
                    <Field id="ft-target-modules" label={t('form.targetModules')} hint={t('form.targetModulesHint')}>
                      <input id="ft-target-modules" type="text" className="dk-input dk-input--mono" value={targetModules} onChange={e => setTargetModules(e.target.value)} placeholder="q_proj, v_proj, k_proj, o_proj" />
                    </Field>
                  )}
                  <label className="dk-choice"><input className="dk-check" type="checkbox" checked={gradCheckpointing} onChange={e => setGradCheckpointing(e.target.checked)} /> {t('form.gradCheckpointing')}</label>
                  <Field id="ft-hf-token" label={t('form.token')} hint={t('form.tokenHint')}>
                    <input id="ft-hf-token" type="password" className="dk-input dk-input--mono" value={hfToken} onChange={e => setHfToken(e.target.value)} placeholder="hf_..." autoComplete="off" />
                  </Field>

                  <div className="bt-eval">
                    <div className="bt-switchrow">
                      <button type="button" className="dk-switch" role="switch" aria-checked={evalEnabled} aria-label={t('form.evalEnable')} onClick={() => setEvalEnabled(!evalEnabled)} />
                      <span>{t('form.evalEnable')}</span>
                    </div>
                    {evalEnabled && (
                      <div className="bt-grid">
                        <Field id="ft-eval-strategy" label={t('form.evalStrategy')}>
                          <Select id="ft-eval-strategy" value={evalStrategy} onChange={e => setEvalStrategy(e.target.value)}>
                            <option value="steps">{t('form.evalSteps_')}</option>
                            <option value="epoch">{t('form.evalEpoch')}</option>
                          </Select>
                        </Field>
                        <Field id="ft-eval-steps" label={t('form.evalSteps')} hint={t('form.evalStepsHint')}>
                          <input id="ft-eval-steps" type="number" className="dk-input" value={evalSteps} onChange={e => setEvalSteps(Number(e.target.value))} min={0} />
                        </Field>
                        <Field id="ft-eval-split" label={t('form.evalSplit')}>
                          <input id="ft-eval-split" type="text" className="dk-input" value={evalSplit} onChange={e => setEvalSplit(e.target.value)} placeholder="validation" />
                        </Field>
                        <Field id="ft-eval-dataset" label={t('form.evalDataset')}>
                          <input id="ft-eval-dataset" type="text" className="dk-input" value={evalDatasetSource} onChange={e => setEvalDatasetSource(e.target.value)} placeholder={t('form.evalDatasetPlaceholder')} />
                        </Field>
                        <Field id="ft-eval-ratio" label={t('form.evalRatio')}>
                          <input id="ft-eval-ratio" type="number" className="dk-input" value={evalSplitRatio} onChange={e => setEvalSplitRatio(Number(e.target.value))} min={0.01} max={0.5} step={0.01} />
                        </Field>
                      </div>
                    )}
                  </div>

                  {trainingMethod === 'grpo' && (
                    <div className="bt-rewards" data-testid="ft-rewards">
                      <h3 className="bt-h3">{t('form.rewards')}</h3>
                      <p className="dk-hint">{t('form.rewardsHint')}</p>
                      {BUILTIN_REWARDS.map(builtin => {
                        const selected = rewardFunctions.find(rf => rf.type === 'builtin' && rf.name === builtin.name)
                        return (
                          <div key={builtin.name} className="bt-reward" data-on={selected ? 'true' : 'false'}>
                            <label className="dk-choice">
                              <input
                                className="dk-check" type="checkbox" checked={!!selected}
                                onChange={e => {
                                  if (e.target.checked) setRewardFunctions(prev => [...prev, { type: 'builtin', name: builtin.name }])
                                  else setRewardFunctions(prev => prev.filter(rf => !(rf.type === 'builtin' && rf.name === builtin.name)))
                                }}
                              />
                              <span><span className="bt-reward__name dk-mono">{builtin.name}</span><span className="bt-choice__text">{t(`rewards.${builtin.name}`)}</span></span>
                            </label>
                            {selected && builtin.params.map(param => (
                              <Field key={param.key} id={`ft-reward-${builtin.name}-${param.key}`} label={t(`rewards.param_${param.key}`)}>
                                <input
                                  id={`ft-reward-${builtin.name}-${param.key}`} type="text" className="dk-input"
                                  value={selected.params?.[param.key] || param.default}
                                  onChange={e => setRewardFunctions(prev => prev.map(rf => (rf.type === 'builtin' && rf.name === builtin.name ? { ...rf, params: { ...(rf.params || {}), [param.key]: e.target.value } } : rf)))}
                                />
                              </Field>
                            ))}
                          </div>
                        )
                      })}
                      {rewardFunctions.filter(rf => rf.type === 'inline').map((rf, idx) => (
                        <div key={`inline-${idx}`} className="bt-reward" data-on="true">
                          <div className="bt-reward__head">
                            <span className="bt-reward__name dk-mono"><Icon name="code" /> {rf.name}</span>
                            <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" aria-label={`Remove ${rf.name}`} onClick={() => setRewardFunctions(prev => prev.filter(x => x !== rf))}><Icon name="close" /></button>
                          </div>
                          <pre className="bt-code">{rf.code}</pre>
                        </div>
                      ))}
                      {showAddCustomReward ? (
                        <div className="bt-reward-draft">
                          <Field id="ft-reward-name" label={t('rewards.fnName')}>
                            <input id="ft-reward-name" type="text" className="dk-input dk-input--mono" value={customRewardName} onChange={e => setCustomRewardName(e.target.value)} placeholder="my_custom_reward" />
                          </Field>
                          <Field id="ft-reward-code" label={t('rewards.fnBody')} hint={t('rewards.fnHint')}>
                            <textarea id="ft-reward-code" className="dk-textarea dk-input--mono" value={customRewardCode} onChange={e => setCustomRewardCode(e.target.value)} placeholder={"return [1.0 if '<think>' in c else 0.0 for c in completions]"} rows={4} />
                          </Field>
                          <div className="bt-actions">
                            <button
                              type="button" className="dk-btn dk-btn--primary dk-btn--sm"
                              disabled={!customRewardName.trim() || !customRewardCode.trim()}
                              onClick={() => {
                                setRewardFunctions(prev => [...prev, { type: 'inline', name: customRewardName.trim(), code: customRewardCode }])
                                setCustomRewardName(''); setCustomRewardCode(''); setShowAddCustomReward(false)
                              }}
                            ><Icon name="plus" /> {t('rewards.add')}</button>
                            <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => { setShowAddCustomReward(false); setCustomRewardName(''); setCustomRewardCode('') }}>{t('rewards.cancel')}</button>
                          </div>
                        </div>
                      ) : (
                        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => setShowAddCustomReward(true)}><Icon name="plus" /> {t('rewards.custom')}</button>
                      )}
                    </div>
                  )}

                  {resumeFromCheckpoint && (
                    <Field id="ft-resume" label={t('form.resume')}>
                      <div className="bt-kv__row">
                        <input id="ft-resume" type="text" className="dk-input dk-input--mono" value={resumeFromCheckpoint} onChange={e => setResumeFromCheckpoint(e.target.value)} />
                        <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" onClick={() => setResumeFromCheckpoint('')} aria-label={t('form.clearCheckpoint')}><Icon name="close" /></button>
                      </div>
                    </Field>
                  )}

                  {backend === 'liquid-audio' && (
                    <div className="bt-liquid">
                      <h3 className="bt-h3">Liquid Audio</h3>
                      <p className="dk-hint">{t('form.liquidHint')}</p>
                      <div className="bt-pair">
                        <Field id="ft-la-voice" label={t('form.liquidVoice')}>
                          <Select id="ft-la-voice" value={liquidAudioVoice} onChange={e => setLiquidAudioVoice(e.target.value)}>
                            <option value="">{t('form.liquidInherit')}</option>
                            {['us_male', 'us_female', 'uk_male', 'uk_female'].map(v => <option key={v} value={v}>{v}</option>)}
                          </Select>
                        </Field>
                        <Field id="ft-la-val" label={t('form.liquidVal')}>
                          <input id="ft-la-val" type="text" className="dk-input dk-input--mono" value={liquidAudioValDataset} onChange={e => setLiquidAudioValDataset(e.target.value)} placeholder="/data/jenny_tts/val" />
                        </Field>
                      </div>
                    </div>
                  )}

                  <div>
                    <p className="dk-label">{t('form.extra')}</p>
                    <p className="dk-hint">{t('form.extraHint')}</p>
                    <KeyValueEditor entries={extraOptions} onChange={setExtraOptions} />
                  </div>
                </div>
              )}
            </div>
          </Section>

          <p className="bt-recipe" data-testid="ft-recipe">
            {model.trim() && (datasetSource.trim() || datasetFile)
              ? t('form.recipe', { kind: kindWords, model: modelShort, method: trainingMethod.toUpperCase(), epochs: numEpochs, data: datasetFile?.name || datasetSource })
              : t('form.recipeEmpty')}
          </p>

          <section className="bt-block" aria-labelledby="bt-check-title">
            <header className="bt-block__head">
              <h2 className="bt-h2" id="bt-check-title">{t('checks.heading')}</h2>
              <p className="dk-hint">{t('checks.live')}</p>
            </header>
            <div className="dk-card bt-checks-card">
              <Checks checks={checks} isAdmin={isAdmin} label={t('checks.heading')} />
            </div>
          </section>

          <div className="bt-bar" data-testid="ft-bar">
            <p className="bt-bar__text" role="status">
              {stop
                ? t(`checks.${firstMissing.key}`)
                : warnings > 0
                  ? t('checks.barWarn', { count: warnings })
                  : t('checks.barOk')}
              {' '}{t('fineTune.barNote')}
            </p>
            <div className="bt-bar__acts">
              <button type="button" className="dk-btn dk-btn--ghost" onClick={handleExportConfig}><Icon name="download" /> {t('fineTune.exportConfig')}</button>
              <button type="submit" className="dk-btn dk-btn--primary" disabled={loading || stop} aria-busy={loading || undefined} data-testid="ft-start">
                {loading
                  ? <><LoadingSpinner size="sm" /> {t('fineTune.starting')}</>
                  : resumeFromCheckpoint
                    ? <><Icon name="refresh" /> {t('fineTune.resume')}</>
                    : <><Icon name="play" /> {t('fineTune.start')}</>}
              </button>
            </div>
          </div>
        </form>
      )}

      <section className="bt-jobs" aria-labelledby="bt-jobs-title" data-testid="ft-jobs">
        <header className="bt-block__head">
          <h2 className="bt-h2" id="bt-jobs-title">{t('jobs.title')}</h2>
          {jobs.length > 0 && <p className="dk-hint">{t('jobs.count', { count: jobs.length })}</p>}
        </header>
        {jobs.length === 0 ? (
          <div className="dk-empty bt-empty-card">
            <div className="dk-empty-icon"><Icon name="graduation-cap" /></div>
            <h3 className="dk-empty-title">{jobsLoaded ? t('jobs.emptyTitle') : t('jobs.loading')}</h3>
            {jobsLoaded && <p className="dk-empty-text">{t('jobs.emptyText')}</p>}
          </div>
        ) : (
          <div className="dk-table-wrap" role="region" aria-labelledby="bt-jobs-title" tabIndex={0}>
            <table className="dk-table">
              <caption className="dk-sr-only">{t('jobs.title')}</caption>
              <thead>
                <tr>
                  <th>{t('jobs.model')}</th>
                  <th className="dk-hide-phone">{t('jobs.kind')}</th>
                  <th>{t('jobs.status')}</th>
                  <th className="dk-hide-phone">{t('jobs.started')}</th>
                  <th><span className="dk-sr-only">{t('jobs.actions')}</span></th>
                </tr>
              </thead>
              <tbody>
                {jobs.map(job => (
                  <tr key={job.id} data-row data-selected={selectedJob?.id === job.id ? 'true' : undefined} data-error={job.status === 'failed' ? '' : undefined}>
                    <td>
                      <button type="button" className="bt-linkcell" onClick={() => setSelectedJob(job)} title={t('jobs.open')}>
                        <span className="dk-table-name">{job.model}</span>
                      </button>
                      <span className="dk-table-sub">{job.backend} · <span className="dk-mono">{job.id?.slice(0, 8)}</span></span>
                      {job.status === 'failed' && job.message && <span className="dk-table-sub bt-jobs__msg">{job.message}</span>}
                    </td>
                    <td className="dk-hide-phone">{kindOf(job)}</td>
                    <td><StatusBadge status={job.status} /></td>
                    <td className="dk-hide-phone dk-mono">{startedAt(job)}</td>
                    <td>
                      <div className="bt-rowacts">
                        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => handleUseConfig(job)} title={t('jobs.reuseTitle')}>{t('jobs.reuse')}</button>
                        {TERMINAL.includes(job.status) && (
                          <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" onClick={() => setDeleting(job.id)} aria-label={t('jobs.delete')} title={t('jobs.deleteTitle')}><Icon name="trash" /></button>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {deleting && (
        <Dialog
          title={t('jobs.deleteDialogTitle')}
          description={t('jobs.deleteDialogText')}
          role="alertdialog"
          onClose={() => setDeleting(null)}
          testId="delete-dialog"
          labelId="bt-delete-title"
          foot={(
            <>
              <button type="button" className="dk-btn dk-btn--ghost" onClick={() => setDeleting(null)}>{t('stop.cancelDelete')}</button>
              <button type="button" className="dk-btn dk-btn--danger" onClick={confirmDelete} data-testid="delete-confirm">{t('jobs.delete')}</button>
            </>
          )}
        />
      )}
    </div>
  )
}
