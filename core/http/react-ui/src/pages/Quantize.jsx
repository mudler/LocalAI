import { useState, useEffect, useCallback, useRef } from 'react'
import { quantizationApi } from '../utils/api'

const QUANT_PRESETS = [
  'q2_k', 'q3_k_s', 'q3_k_m', 'q3_k_l',
  'q4_0', 'q4_k_s', 'q4_k_m',
  'q5_0', 'q5_k_s', 'q5_k_m',
  'q6_k', 'q8_0', 'f16',
]
const DEFAULT_QUANT = 'q4_k_m'
const FALLBACK_BACKENDS = ['llama-cpp-quantization']

const statusBadgeClass = {
  queued: '', downloading: 'badge-warning', converting: 'badge-warning',
  quantizing: 'badge-info', completed: 'badge-success',
  failed: 'badge-error', stopped: '',
}

// ── Reusable sub-components ──────────────────────────────────────

function FormSection({ icon, title, children }) {
  return (
    <div className="form-group">
      <div className="form-group__title">
        {icon && <i className={icon} />}
        <span>{title}</span>
      </div>
      <div className="form-group__body">
        {children}
      </div>
    </div>
  )
}

function ProgressMonitor({ job, onClose }) {
  const [events, setEvents] = useState([])
  const [latestEvent, setLatestEvent] = useState(null)
  const esRef = useRef(null)

  useEffect(() => {
    if (!job) return
    const terminal = ['completed', 'failed', 'stopped']
    if (terminal.includes(job.status)) return

    const es = new EventSource(quantizationApi.progressUrl(job.id))
    esRef.current = es
    es.onmessage = (e) => {
      try {
        const data = JSON.parse(e.data)
        setLatestEvent(data)
        setEvents(prev => [...prev.slice(-100), data])
        if (terminal.includes(data.status)) {
          es.close()
        }
      } catch { /* ignore parse errors */ }
    }
    es.onerror = () => { es.close() }
    return () => { es.close() }
  }, [job?.id])

  if (!job) return null

  const progress = latestEvent?.progress_percent ?? 0
  const status = latestEvent?.status ?? job.status
  const message = latestEvent?.message ?? job.message ?? ''

  return (
    <div className="card quantize-progress-card">
      <div className="quantize-progress-card__header">
        <h4 className="quantize-progress-card__title">
          <i className="fas fa-chart-line" />
          <span>Progress: {job.model}</span>
        </h4>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} title="Close">
          <i className="fas fa-times" />
        </button>
      </div>

      <div className="quantize-progress-card__status">
        <span className={`badge ${statusBadgeClass[status] || ''}`}>{status}</span>
        {message && <span className="quantize-progress-card__message">{message}</span>}
      </div>

      <div className="progress-bar">
        <div
          className={`progress-bar__fill${status === 'failed' ? ' progress-bar__fill--error' : ''}`}
          style={{ width: `${Math.min(progress, 100)}%` }}
        >
          {progress > 8 ? `${progress.toFixed(1)}%` : ''}
        </div>
      </div>

      <div className="log-tail">
        {events.slice(-20).map((ev, i) => (
          <div
            key={i}
            className={`log-tail__line${ev.status === 'failed' ? ' log-tail__line--error' : ''}`}
          >
            [{ev.status}] {ev.message}
          </div>
        ))}
      </div>
    </div>
  )
}

function ImportPanel({ job, onRefresh }) {
  const [modelName, setModelName] = useState('')
  const [importing, setImporting] = useState(false)
  const [error, setError] = useState('')
  const pollRef = useRef(null)

  // Poll for import status
  useEffect(() => {
    if (job?.import_status !== 'importing') return
    pollRef.current = setInterval(async () => {
      try {
        await onRefresh()
      } catch { /* ignore */ }
    }, 3000)
    return () => clearInterval(pollRef.current)
  }, [job?.import_status, onRefresh])

  if (!job || job.status !== 'completed') return null

  const handleImport = async () => {
    setImporting(true)
    setError('')
    try {
      await quantizationApi.importModel(job.id, { name: modelName || undefined })
      await onRefresh()
    } catch (e) {
      setError(e.message || 'Import failed')
    } finally {
      setImporting(false)
    }
  }

  return (
    <div className="card quantize-import-card">
      <h4 className="quantize-import-card__title">
        <i className="fas fa-file-export" />
        <span>Output</span>
      </h4>

      {error && <div className="alert alert-error">{error}</div>}

      <div className="quantize-import-card__row">
        <a
          href={quantizationApi.downloadUrl(job.id)}
          className="btn btn-secondary"
          download
        >
          <i className="fas fa-download" />
          <span>Download GGUF</span>
        </a>

        {job.import_status === 'completed' ? (
          <a href={`/app/chat/${encodeURIComponent(job.import_model_name)}`} className="btn btn-primary">
            <i className="fas fa-comments" />
            <span>Chat with {job.import_model_name}</span>
          </a>
        ) : job.import_status === 'importing' ? (
          <button type="button" className="btn btn-secondary" disabled>
            <i className="fas fa-spinner fa-spin" />
            <span>Importing... {job.import_message}</span>
          </button>
        ) : (
          <>
            <input
              className="input quantize-import-card__name"
              placeholder="Model name (auto-generated if empty)"
              value={modelName}
              onChange={e => setModelName(e.target.value)}
            />
            <button type="button" className="btn btn-primary" onClick={handleImport} disabled={importing}>
              <i className="fas fa-file-import" />
              <span>Import to LocalAI</span>
            </button>
          </>
        )}
      </div>

      {job.import_status === 'failed' && (
        <div className="alert alert-error">Import failed: {job.import_message}</div>
      )}
    </div>
  )
}

// ── Main page ────────────────────────────────────────────────────

export default function Quantize() {
  // Form state
  const [model, setModel] = useState('')
  const [quantType, setQuantType] = useState(DEFAULT_QUANT)
  const [customQuantType, setCustomQuantType] = useState('')
  const [useCustomQuant, setUseCustomQuant] = useState(false)
  const [backend, setBackend] = useState('')
  const [hfToken, setHfToken] = useState('')
  const [backends, setBackends] = useState([])

  // Jobs state
  const [jobs, setJobs] = useState([])
  const [selectedJob, setSelectedJob] = useState(null)
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  // Load backends and jobs
  const loadJobs = useCallback(async () => {
    try {
      const data = await quantizationApi.listJobs()
      setJobs(data)
      // Refresh selected job if it exists
      if (selectedJob) {
        const updated = data.find(j => j.id === selectedJob.id)
        if (updated) setSelectedJob(updated)
      }
    } catch { /* ignore */ }
  }, [selectedJob?.id])

  useEffect(() => {
    quantizationApi.listBackends().then(b => {
      setBackends(b.length ? b : FALLBACK_BACKENDS.map(name => ({ name })))
      if (b.length) setBackend(b[0].name)
      else setBackend(FALLBACK_BACKENDS[0])
    }).catch(() => {
      setBackends(FALLBACK_BACKENDS.map(name => ({ name })))
      setBackend(FALLBACK_BACKENDS[0])
    })
    loadJobs()
    const interval = setInterval(loadJobs, 10000)
    return () => clearInterval(interval)
  }, [])

  const handleSubmit = async (e) => {
    e.preventDefault()
    setError('')
    setSubmitting(true)
    try {
      const req = {
        model,
        backend: backend || FALLBACK_BACKENDS[0],
        quantization_type: useCustomQuant ? customQuantType : quantType,
      }
      if (hfToken) {
        req.extra_options = { hf_token: hfToken }
      }

      const resp = await quantizationApi.startJob(req)
      setModel('')
      await loadJobs()

      // Select the new job
      const refreshed = await quantizationApi.getJob(resp.id)
      setSelectedJob(refreshed)
    } catch (err) {
      setError(err.message || 'Failed to start job')
    } finally {
      setSubmitting(false)
    }
  }

  const handleStop = async (jobId) => {
    try {
      await quantizationApi.stopJob(jobId)
      await loadJobs()
    } catch (err) {
      setError(err.message || 'Failed to stop job')
    }
  }

  const handleDelete = async (jobId) => {
    try {
      await quantizationApi.deleteJob(jobId)
      if (selectedJob?.id === jobId) setSelectedJob(null)
      await loadJobs()
    } catch (err) {
      setError(err.message || 'Failed to delete job')
    }
  }

  const effectiveQuantType = useCustomQuant ? customQuantType : quantType

  return (
    <div className="page page--narrow quantize-page">
      <div className="page-header quantize-page__header">
        <div>
          <h1 className="page-title">
            <i className="fas fa-compress" /> Model Quantization
          </h1>
          <p className="page-subtitle">Quantize and import GGUF models directly into LocalAI</p>
        </div>
        <span className="badge badge-warning">Experimental</span>
      </div>

      {error && (
        <div className="alert alert-error">
          <i className="fas fa-exclamation-triangle" /> {error}
        </div>
      )}

      {/* ── New Job Form ── */}
      <form onSubmit={handleSubmit} className="card quantize-form">
        <FormSection icon="fas fa-cube" title="Model">
          <input
            className="input btn-full"
            placeholder="HuggingFace model name (e.g. meta-llama/Llama-3.2-1B) or local path"
            value={model}
            onChange={e => setModel(e.target.value)}
            required
          />
        </FormSection>

        <div className="form-grid-2col">
          <FormSection icon="fas fa-sliders-h" title="Quantization Type">
            <div className="quantize-form__quant-row">
              <select
                className="input"
                value={useCustomQuant ? '__custom__' : quantType}
                onChange={e => {
                  if (e.target.value === '__custom__') {
                    setUseCustomQuant(true)
                  } else {
                    setUseCustomQuant(false)
                    setQuantType(e.target.value)
                  }
                }}
              >
                {QUANT_PRESETS.map(q => (
                  <option key={q} value={q}>{q}</option>
                ))}
                <option value="__custom__">Custom...</option>
              </select>
              {useCustomQuant && (
                <input
                  className="input"
                  placeholder="Custom quantization type"
                  value={customQuantType}
                  onChange={e => setCustomQuantType(e.target.value)}
                  required
                />
              )}
            </div>
          </FormSection>

          <FormSection icon="fas fa-server" title="Backend">
            <select
              className="input btn-full"
              value={backend}
              onChange={e => setBackend(e.target.value)}
            >
              {backends.map(b => (
                <option key={b.name || b} value={b.name || b}>{b.name || b}</option>
              ))}
            </select>
          </FormSection>
        </div>

        <FormSection icon="fas fa-key" title="HuggingFace Token (optional)">
          <input
            className="input btn-full"
            type="password"
            placeholder="hf_... (required for gated models)"
            value={hfToken}
            onChange={e => setHfToken(e.target.value)}
          />
        </FormSection>

        <div className="form-group__actions">
          <button
            className="btn btn-primary"
            type="submit"
            disabled={submitting || !model || (useCustomQuant && !customQuantType)}
          >
            {submitting ? (
              <><i className="fas fa-spinner fa-spin" /> <span>Starting...</span></>
            ) : (
              <><i className="fas fa-play" /> <span>Quantize ({effectiveQuantType})</span></>
            )}
          </button>
        </div>
      </form>

      {/* ── Selected Job Detail ── */}
      {selectedJob && (
        <>
          <ProgressMonitor
            job={selectedJob}
            onClose={() => setSelectedJob(null)}
          />
          <ImportPanel
            job={selectedJob}
            onRefresh={async () => {
              const updated = await quantizationApi.getJob(selectedJob.id)
              setSelectedJob(updated)
              await loadJobs()
            }}
          />
        </>
      )}

      {/* ── Jobs List ── */}
      {jobs.length > 0 && (
        <div className="card quantize-jobs">
          <h4 className="quantize-jobs__title">
            <i className="fas fa-list" />
            <span>Jobs</span>
          </h4>
          <div className="quantize-jobs__scroll">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Model</th>
                  <th>Quant</th>
                  <th>Status</th>
                  <th>Created</th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {jobs.map(job => {
                  const isActive = ['queued', 'downloading', 'converting', 'quantizing'].includes(job.status)
                  const isSelected = selectedJob?.id === job.id
                  return (
                    <tr
                      key={job.id}
                      className={isSelected ? 'is-selected' : ''}
                      onClick={() => setSelectedJob(job)}
                      style={{ cursor: 'pointer' }}
                    >
                      <td className="data-table__truncate">{job.model}</td>
                      <td><code>{job.quantization_type}</code></td>
                      <td>
                        <span className={`badge ${statusBadgeClass[job.status] || ''}`}>{job.status}</span>
                        {job.import_status === 'completed' && (
                          <span className="badge badge-success" style={{ marginLeft: 'var(--spacing-xs)' }}>imported</span>
                        )}
                      </td>
                      <td style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-secondary)' }}>
                        {new Date(job.created_at).toLocaleString()}
                      </td>
                      <td>
                        <div className="data-table__actions" onClick={e => e.stopPropagation()}>
                          {isActive ? (
                            <button type="button" className="btn btn-sm btn-danger" onClick={() => handleStop(job.id)} title="Stop">
                              <i className="fas fa-stop" />
                            </button>
                          ) : (
                            <button type="button" className="btn btn-sm btn-ghost" onClick={() => handleDelete(job.id)} title="Delete">
                              <i className="fas fa-trash" />
                            </button>
                          )}
                        </div>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  )
}
