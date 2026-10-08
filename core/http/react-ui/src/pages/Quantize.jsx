/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { quantizationApi } from '../utils/api'
import { useAuth } from '../context/AuthContext'
import { useResources } from '../hooks/useResources'
import { QZ_STAGES, TERMINAL, appendLog, blocked, logLines, machineFacts, quantizeChecks, warned } from '../utils/tools'
import PageHeader from '../components/PageHeader'
import Dialog from '../components/Dialog'
import Icon from '../components/Icon'
import LoadingSpinner from '../components/LoadingSpinner'
import ToolSteps from '../components/tools/ToolSteps'
import Checks from '../components/tools/Checks'
import StageLine from '../components/tools/StageLine'
import JobLog from '../components/tools/JobLog'
import '../components/tools/tools.css'

const QUANT_PRESETS = [
  'q2_k', 'q3_k_s', 'q3_k_m', 'q3_k_l',
  'q4_0', 'q4_k_s', 'q4_k_m',
  'q5_0', 'q5_k_s', 'q5_k_m',
  'q6_k', 'q8_0', 'f16',
]
const DEFAULT_QUANT = 'q4_k_m'
const FALLBACK_BACKEND = 'llama-cpp-quantization'
const BADGE = {
  queued: '', downloading: 'dk-badge--warn', converting: 'dk-badge--warn', quantizing: 'dk-badge--accent',
  completed: 'dk-badge--ok', failed: 'dk-badge--error', stopped: '',
}

function StatusBadge({ status }) {
  const { t } = useTranslation('tools')
  return <span className={`dk-badge ${BADGE[status] || ''}`} data-status={status}>{t(`quantize.status.${status}`, { defaultValue: String(status || '') })}</span>
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

const startedAt = (job) => {
  const d = new Date(job.created_at)
  return Number.isNaN(d.getTime()) ? (job.created_at || '') : d.toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

// What to do with a finished quantization: import it as a model, then use it.
function ResultPanel({ job, onRefresh }) {
  const { t } = useTranslation('tools')
  const [modelName, setModelName] = useState('')
  const [importing, setImporting] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (job?.import_status !== 'importing') return undefined
    const timer = setInterval(() => { onRefresh().catch(() => {}) }, 3000)
    return () => clearInterval(timer)
  }, [job?.import_status, onRefresh])

  if (!job || job.status !== 'completed') return null

  const handleImport = async () => {
    setImporting(true)
    setError('')
    try {
      await quantizationApi.importModel(job.id, { name: modelName || undefined })
      await onRefresh()
    } catch (e) {
      setError(e.message || t('quantize.importFailed'))
    } finally {
      setImporting(false)
    }
  }

  const imported = job.import_status === 'completed' && job.import_model_name

  return (
    <>
      {imported && (
        <section className="bt-next dk-card" data-testid="quantize-next">
          <span className="bt-next__mark"><Icon name="check-circle" /></span>
          <div>
            <h2 className="bt-h2">{t('quantize.nextTitle', { name: job.import_model_name })}</h2>
            <p className="dk-hint">{t('quantize.nextText')}</p>
            <div className="bt-next__acts">
              <Link className="dk-btn dk-btn--primary" to={`/app/chat/${encodeURIComponent(job.import_model_name)}`}><Icon name="chat" /> {t('quantize.chat', { name: job.import_model_name })}</Link>
              <Link className="dk-btn dk-btn--secondary" to="/app/models?view=installed"><Icon name="cube" /> {t('quantize.models')}</Link>
              <a className="dk-btn dk-btn--ghost" href={quantizationApi.downloadUrl(job.id)} download><Icon name="download" /> {t('quantize.download')}</a>
            </div>
          </div>
        </section>
      )}
      <section className="bt-block dk-card" aria-labelledby="bt-qz-out" data-testid="quantize-output">
        <h2 className="bt-h2" id="bt-qz-out">{t('quantize.outputTitle')}</h2>
        <p className="dk-hint">{t('quantize.outputText', { type: job.quantization_type })}</p>
        {job.output_file && <p className="bt-pathrow"><span className="dk-label">{t('quantize.file')}</span> <span className="dk-mono bt-path" title={job.output_file}>{job.output_file}</span></p>}
        {error && <p className="bt-status bt-status--error" role="alert">{error}</p>}
        {job.import_status === 'failed' && <p className="bt-status bt-status--error" role="alert">{t('quantize.importFailedWith', { message: job.import_message })}</p>}
        {!imported && (
          <div className="bt-out">
            {job.import_status === 'importing' ? (
              <p className="bt-status" role="status"><LoadingSpinner size="sm" /> {t('quantize.importing')} {job.import_message}</p>
            ) : (
              <>
                <Field id="qz-import-name" label={t('quantize.importName')} className="bt-out__name">
                  <input id="qz-import-name" className="dk-input dk-input--mono" placeholder={t('quantize.importNamePlaceholder')} value={modelName} onChange={e => setModelName(e.target.value)} />
                </Field>
                <div className="bt-actions">
                  <button type="button" className="dk-btn dk-btn--primary" onClick={handleImport} disabled={importing} aria-busy={importing || undefined} data-testid="quantize-import">
                    <Icon name="import" /> {t('quantize.import')}
                  </button>
                  <a className="dk-btn dk-btn--secondary" href={quantizationApi.downloadUrl(job.id)} download><Icon name="download" /> {t('quantize.download')}</a>
                </div>
              </>
            )}
          </div>
        )}
      </section>
    </>
  )
}

function JobView({ job, onStop, onReuse, onTerminal, onRefresh }) {
  const { t } = useTranslation('tools')
  const [latest, setLatest] = useState(null)
  const [log, setLog] = useState([])
  const previousRef = useRef(null)
  const terminalRef = useRef(onTerminal)
  terminalRef.current = onTerminal

  useEffect(() => {
    previousRef.current = null
    setLatest(null)
    setLog([])
    if (!job || TERMINAL.includes(job.status)) return undefined
    const es = new EventSource(quantizationApi.progressUrl(job.id))
    es.onmessage = (e) => {
      try {
        const data = JSON.parse(e.data)
        setLatest(data)
        setLog(prev => appendLog(prev, logLines(data, previousRef.current)))
        previousRef.current = data
        if (TERMINAL.includes(data.status)) { es.close(); terminalRef.current?.() }
      } catch { /* a partial frame */ }
    }
    es.onerror = () => es.close()
    return () => es.close()
  // eslint-disable-next-line react-hooks/exhaustive-deps -- keyed on the job id: the effect restarts per job, not per poll refresh
  }, [job?.id])

  const status = latest?.status || job.status
  const active = QZ_STAGES.includes(status)
  const failed = status === 'failed'
  const progress = Math.min(latest?.progress_percent ?? 0, 100)
  const message = latest?.message || job.message || ''
  const stageLabels = Object.fromEntries(QZ_STAGES.map(s => [s, t(`quantize.status.${s}`)]))

  return (
    <div className="bt-stack" data-testid="job-view" data-status={status}>
      {failed && (
        <section className="bt-alert" role="alert" data-testid="job-failed">
          <Icon name="alert-circle" />
          <div>
            <p className="bt-alert__title">{t('quantize.failed')}</p>
            <p className="bt-alert__text" data-testid="job-failed-message">{message || t('fineTune.noMessage')}</p>
          </div>
        </section>
      )}
      <section className="bt-job dk-card" aria-labelledby="bt-job-title">
        <div className="bt-job__top">
          <div className="bt-job__who">
            <h2 className="bt-job__title" id="bt-job-title">{job.model}</h2>
            <p className="bt-job__meta">
              {t('quantize.jobMeta', { type: job.quantization_type, backend: job.backend, started: startedAt(job) })}
              <span className="dk-mono"> · {job.id?.slice(0, 8)}</span>
            </p>
          </div>
          <div className="bt-job__acts">
            <StatusBadge status={status} />
            {active && (
              <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => onStop(job.id)} data-testid="job-stop">
                <Icon name="stop" /> {t('run.stop')}
              </button>
            )}
          </div>
        </div>
        {(active || latest) && (
          <>
            <div className="bt-job__figures">
              <span className="bt-job__percent">{status === 'completed' ? '100' : progress.toFixed(0)}<small>%</small></span>
            </div>
            <div
              className={`dk-progress${failed ? ' dk-progress--error' : status === 'completed' ? ' dk-progress--ok' : ''}`}
              role="progressbar" aria-valuemin={0} aria-valuemax={100}
              aria-valuenow={Math.round(status === 'completed' ? 100 : progress)} aria-label={t('run.progress')}
              style={{ '--dk-value': `${status === 'completed' ? 100 : progress}%` }}
            >
              <span className="dk-progress-bar" />
            </div>
          </>
        )}
        {active && <StageLine stages={QZ_STAGES} labels={stageLabels} status={status} failed={failed} label={t('run.stages')} />}
        {!failed && message && <p className="dk-hint bt-job__message">{message}</p>}
        {status === 'completed' && <p className="bt-job__done" data-testid="job-finished"><Icon name="check-circle" /> {t('quantize.finished', { type: job.quantization_type })}</p>}
        {status === 'stopped' && <p className="dk-hint">{t('quantize.stopped')}</p>}
      </section>

      {failed && (
        <section className="bt-try dk-card" data-testid="job-try">
          <h2 className="bt-h2">{t('fineTune.tryTitle')}</h2>
          <p className="dk-hint">{t('quantize.tryText')}</p>
          <div className="bt-try__acts">
            <button type="button" className="dk-btn dk-btn--primary" onClick={() => onReuse(job)} data-testid="job-retry"><Icon name="refresh" /> {t('fineTune.reuse')}</button>
          </div>
        </section>
      )}

      {(active || log.length > 0) && <JobLog lines={log} name={`quantize-${job.id?.slice(0, 8)}`} />}
      <ResultPanel job={job} onRefresh={onRefresh} />
    </div>
  )
}

export default function Quantize() {
  const { t } = useTranslation('tools')
  const { isAdmin } = useAuth()
  const { resources } = useResources(10000)
  const facts = useMemo(() => machineFacts(resources), [resources])

  const [model, setModel] = useState('')
  const [quantType, setQuantType] = useState(DEFAULT_QUANT)
  const [customQuantType, setCustomQuantType] = useState('')
  const [useCustomQuant, setUseCustomQuant] = useState(false)
  const [backend, setBackend] = useState(FALLBACK_BACKEND)
  const [hfToken, setHfToken] = useState('')
  const [backends, setBackends] = useState(null)
  const [showMore, setShowMore] = useState(false)

  const [jobs, setJobs] = useState([])
  const [jobsLoaded, setJobsLoaded] = useState(false)
  const [selectedJob, setSelectedJob] = useState(null)
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [deleting, setDeleting] = useState(null)

  const loadJobs = useCallback(async () => {
    try {
      const data = await quantizationApi.listJobs()
      const list = Array.isArray(data) ? data : []
      setJobs(list)
      setSelectedJob(prev => {
        if (!prev) return prev
        const fresh = list.find(j => j.id === prev.id)
        return fresh || prev
      })
    } catch { /* the next poll tries again */ } finally {
      setJobsLoaded(true)
    }
  }, [])

  useEffect(() => {
    quantizationApi.listBackends().then(b => {
      const list = Array.isArray(b) ? b : []
      setBackends(list)
      if (list.length) setBackend(list[0].name)
    }).catch(() => setBackends(null))
    loadJobs()
    const interval = setInterval(loadJobs, 10000)
    return () => clearInterval(interval)
  }, [loadJobs])

  const handleSubmit = async (e) => {
    e.preventDefault()
    setError('')
    setSubmitting(true)
    try {
      const req = {
        model,
        backend: backend || FALLBACK_BACKEND,
        quantization_type: useCustomQuant ? customQuantType : quantType,
      }
      if (hfToken) req.extra_options = { hf_token: hfToken }
      const resp = await quantizationApi.startJob(req)
      setModel('')
      await loadJobs()
      setSelectedJob(await quantizationApi.getJob(resp.id))
    } catch (err) {
      setError(err.message || t('quantize.startFailed'))
    } finally {
      setSubmitting(false)
    }
  }

  const handleStop = async (jobId) => {
    try {
      await quantizationApi.stopJob(jobId)
      await loadJobs()
    } catch (err) {
      setError(err.message || t('quantize.stopFailed'))
    }
  }

  const confirmDelete = async () => {
    const jobId = deleting
    setDeleting(null)
    try {
      await quantizationApi.deleteJob(jobId)
      if (selectedJob?.id === jobId) setSelectedJob(null)
      await loadJobs()
    } catch (err) {
      setError(err.message || t('quantize.deleteFailed'))
    }
  }

  // Put a job's setup back in the form.
  const reuse = (job) => {
    setModel(job.model || '')
    if (job.backend) setBackend(job.backend)
    const type = job.quantization_type || DEFAULT_QUANT
    if (QUANT_PRESETS.includes(type)) { setUseCustomQuant(false); setQuantType(type) } else { setUseCustomQuant(true); setCustomQuantType(type) }
    setSelectedJob(null)
  }

  const effectiveQuantType = useCustomQuant ? customQuantType : quantType
  const backendNames = backends && backends.length > 0 ? backends.map(b => b.name) : [FALLBACK_BACKEND]
  const checks = useMemo(() => quantizeChecks({
    form: { model, backend, useCustom: useCustomQuant, customType: customQuantType, hfToken: hfToken.trim() },
    facts,
    backends: { quantize: backends },
  }), [model, backend, useCustomQuant, customQuantType, hfToken, facts, backends])
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

  const refreshSelected = useCallback(async () => {
    if (!selectedJob) return
    const updated = await quantizationApi.getJob(selectedJob.id)
    setSelectedJob(updated)
    await loadJobs()
  // eslint-disable-next-line react-hooks/exhaustive-deps -- keyed on the job id: the effect restarts per job, not per poll refresh
  }, [selectedJob?.id, loadJobs])

  return (
    <div className="page page--medium bt-page quantize-page" data-testid="quantize-page">
      <PageHeader
        title={<>{t('quantize.title')} <span className="dk-badge dk-badge--warn bt-experimental">{t('landing.experimental')}</span></>}
        supporting={t('quantize.lede')}
        actions={selectedJob && (
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => setSelectedJob(null)} data-testid="job-back">
            <Icon name="plus" /> {t('quantize.newJob')}
          </button>
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
        <JobView job={selectedJob} onStop={handleStop} onReuse={reuse} onTerminal={loadJobs} onRefresh={refreshSelected} />
      ) : (
        <form onSubmit={handleSubmit} className="bt-form-page" data-testid="quantize-form">
          <Section done={!!model.trim()} title={t('quantize.sectionModel')}>
            <Field id="qz-model" label={t('quantize.model')} hint={t('quantize.modelHint')}>
              <input id="qz-model" className="dk-input dk-input--mono" placeholder="meta-llama/Llama-3.2-1B" value={model} onChange={e => setModel(e.target.value)} required />
            </Field>
          </Section>

          <Section done={!useCustomQuant || !!customQuantType.trim()} title={t('quantize.sectionPrecision')}>
            <div className="bt-pair">
              <Field id="qz-type" label={t('quantize.type')} hint={t('quantize.typeHint')}>
                <span className="dk-select-wrap">
                  <select
                    id="qz-type" className="dk-select" value={useCustomQuant ? '__custom__' : quantType}
                    onChange={e => {
                      if (e.target.value === '__custom__') setUseCustomQuant(true)
                      else { setUseCustomQuant(false); setQuantType(e.target.value) }
                    }}
                  >
                    {QUANT_PRESETS.map(q => <option key={q} value={q}>{q}</option>)}
                    <option value="__custom__">{t('quantize.custom')}</option>
                  </select>
                </span>
              </Field>
              {useCustomQuant && (
                <Field id="qz-custom" label={t('quantize.customLabel')}>
                  <input id="qz-custom" className="dk-input dk-input--mono" placeholder={t('quantize.customPlaceholder')} value={customQuantType} onChange={e => setCustomQuantType(e.target.value)} required />
                </Field>
              )}
            </div>
            <div className="bt-more">
              <button type="button" className="bt-more__toggle" onClick={() => setShowMore(v => !v)} aria-expanded={showMore} aria-controls="qz-more">
                <Icon name={showMore ? 'chevron-down' : 'chevron-right'} />
                <b>{t('form.more')}</b>
                {!showMore && <span className="dk-hint">{t('quantize.moreHint')}</span>}
              </button>
              {showMore && (
                <div className="bt-more__body" id="qz-more">
                  <div className="bt-pair">
                    <Field id="qz-backend" label={t('form.backend')}>
                      <span className="dk-select-wrap">
                        <select id="qz-backend" className="dk-select" value={backend} onChange={e => setBackend(e.target.value)}>
                          {backendNames.map(b => <option key={b} value={b}>{b}</option>)}
                        </select>
                      </span>
                    </Field>
                    <Field id="qz-token" label={t('form.token')} hint={t('quantize.tokenHint')}>
                      <input id="qz-token" type="password" className="dk-input dk-input--mono" placeholder="hf_..." value={hfToken} onChange={e => setHfToken(e.target.value)} autoComplete="off" />
                    </Field>
                  </div>
                </div>
              )}
            </div>
          </Section>

          <p className="bt-recipe" data-testid="qz-recipe">
            {model.trim() ? t('quantize.recipe', { model: model.split('/').pop(), type: effectiveQuantType || '?' }) : t('quantize.recipeEmpty')}
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

          <div className="bt-bar" data-testid="qz-bar">
            <p className="bt-bar__text" role="status">
              {stop ? t(`checks.${firstMissing.key}`) : warnings > 0 ? t('checks.barWarn', { count: warnings }) : t('checks.barOk')}
              {' '}{t('quantize.barNote')}
            </p>
            <div className="bt-bar__acts">
              <button type="submit" className="dk-btn dk-btn--primary" disabled={submitting || stop} aria-busy={submitting || undefined} data-testid="qz-start">
                {submitting
                  ? <><LoadingSpinner size="sm" /> {t('fineTune.starting')}</>
                  : <><Icon name="play" /> {t('quantize.start', { type: effectiveQuantType || '' })}</>}
              </button>
            </div>
          </div>
        </form>
      )}

      <section className="bt-jobs" aria-labelledby="bt-jobs-title" data-testid="qz-jobs">
        <header className="bt-block__head">
          <h2 className="bt-h2" id="bt-jobs-title">{t('jobs.title')}</h2>
          {jobs.length > 0 && <p className="dk-hint">{t('jobs.count', { count: jobs.length })}</p>}
        </header>
        {jobs.length === 0 ? (
          <div className="dk-empty bt-empty-card">
            <div className="dk-empty-icon"><Icon name="minimize" /></div>
            <h3 className="dk-empty-title">{jobsLoaded ? t('quantize.emptyTitle') : t('jobs.loading')}</h3>
            {jobsLoaded && <p className="dk-empty-text">{t('quantize.emptyText')}</p>}
          </div>
        ) : (
          <div className="dk-table-wrap" role="region" aria-labelledby="bt-jobs-title" tabIndex={0}>
            <table className="dk-table">
              <caption className="dk-sr-only">{t('jobs.title')}</caption>
              <thead>
                <tr>
                  <th>{t('jobs.model')}</th>
                  <th className="dk-hide-phone">{t('quantize.type')}</th>
                  <th>{t('jobs.status')}</th>
                  <th className="dk-hide-phone">{t('jobs.started')}</th>
                  <th><span className="dk-sr-only">{t('jobs.actions')}</span></th>
                </tr>
              </thead>
              <tbody>
                {jobs.map(job => {
                  const active = QZ_STAGES.includes(job.status)
                  return (
                    <tr key={job.id} data-row data-selected={selectedJob?.id === job.id ? 'true' : undefined} data-error={job.status === 'failed' ? '' : undefined}>
                      <td>
                        <button type="button" className="bt-linkcell" onClick={() => setSelectedJob(job)} title={t('jobs.open')}>
                          <span className="dk-table-name">{job.model}</span>
                        </button>
                        {job.import_status === 'completed' && <span className="dk-table-sub">{t('quantize.imported', { name: job.import_model_name })}</span>}
                        {job.status === 'failed' && job.message && <span className="dk-table-sub bt-jobs__msg">{job.message}</span>}
                      </td>
                      <td className="dk-hide-phone dk-mono">{job.quantization_type}</td>
                      <td><StatusBadge status={job.status} /></td>
                      <td className="dk-hide-phone dk-mono">{startedAt(job)}</td>
                      <td>
                        <div className="bt-rowacts">
                          {active ? (
                            <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => handleStop(job.id)}><Icon name="stop" /> {t('run.stop')}</button>
                          ) : (
                            <>
                              <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => reuse(job)} title={t('jobs.reuseTitle')}>{t('jobs.reuse')}</button>
                              <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" onClick={() => setDeleting(job.id)} aria-label={t('jobs.delete')} title={t('jobs.deleteTitle')}><Icon name="trash" /></button>
                            </>
                          )}
                        </div>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {deleting && (
        <Dialog
          title={t('jobs.deleteDialogTitle')}
          description={t('quantize.deleteDialogText')}
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
