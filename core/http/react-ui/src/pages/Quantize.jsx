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
    <div style={{ marginBottom: 'var(--spacing-md)' }}>
      <h4 style={{ marginBottom: 'var(--spacing-sm)', display: 'flex', alignItems: 'center', gap: '0.5em' }}>
        {icon && <i className={icon} />} {title}
      </h4>
      {children}
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
    <div className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-sm)' }}>
        <h4 style={{ margin: 0 }}>
          <i className="fas fa-chart-line" style={{ marginRight: '0.5em' }} />
          Progress: {job.model}
        </h4>
        <button className="btn btn-sm" onClick={onClose}><i className="fas fa-times" /></button>
      </div>

      <div style={{ marginBottom: 'var(--spacing-sm)' }}>
        <span className={`badge ${statusBadgeClass[status] || ''}`}>{status}</span>
        <span style={{ marginLeft: '1em', fontSize: '0.9em', color: 'var(--color-text-secondary)' }}>{message}</span>
      </div>

      <div style={{
        width: '100%', height: '24px', borderRadius: '12px',
        background: 'var(--color-bg-tertiary)', overflow: 'hidden', marginBottom: 'var(--spacing-sm)',
      }}>
        <div style={{
          width: `${Math.min(progress, 100)}%`, height: '100%',
          background: status === 'failed' ? 'var(--color-error)' : 'var(--color-primary)',
          transition: 'width 0.3s ease', borderRadius: '12px',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          fontSize: '0.75em', fontWeight: 'bold', color: '#fff',
        }}>
          {progress > 8 ? `${progress.toFixed(1)}%` : ''}
        </div>
      </div>

      {/* Log tail */}
      <div style={{
        maxHeight: '150px', overflow: 'auto', fontSize: '0.8em',
        background: 'var(--color-bg-secondary)', borderRadius: '6px', padding: '0.5em',
        fontFamily: 'monospace',
      }}>
        {events.slice(-20).map((ev, i) => (
          <div key={i} style={{ color: ev.status === 'failed' ? 'var(--color-error)' : 'var(--color-text-secondary)' }}>
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
    <div className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
      <h4 style={{ marginBottom: 'var(--spacing-sm)' }}>
        <i className="fas fa-file-export" style={{ marginRight: '0.5em' }} />
        Output
      </h4>

      {error && (
        <div className="alert alert-error" style={{ marginBottom: 'var(--spacing-sm)' }}>{error}</div>
      )}

      <div style={{ display: 'flex', gap: 'var(--spacing-sm)', flexWrap: 'wrap', alignItems: 'flex-end' }}>
        {/* Download */}
        <a
          href={quantizationApi.downloadUrl(job.id)}
          className="btn btn-secondary"
          download
        >
          <i className="fas fa-download" style={{ marginRight: '0.4em' }} />
          Download GGUF
        </a>

        {/* Import */}
        {job.import_status === 'completed' ? (
          <a href={`/app/chat/${encodeURIComponent(job.import_model_name)}`} className="btn btn-success">
            <i className="fas fa-comments" style={{ marginRight: '0.4em' }} />
            Chat with {job.import_model_name}
          </a>
        ) : job.import_status === 'importing' ? (
          <button className="btn" disabled>
            <i className="fas fa-spinner fa-spin" style={{ marginRight: '0.4em' }} />
            Importing... {job.import_message}
          </button>
        ) : (
          <>
            <input
              className="input"
              placeholder="Model name (auto-generated if empty)"
              value={modelName}
              onChange={e => setModelName(e.target.value)}
              style={{ maxWidth: '280px' }}
            />
            <button className="btn btn-primary" onClick={handleImport} disabled={importing}>
              <i className="fas fa-file-import" style={{ marginRight: '0.4em' }} />
              Import to LocalAI
            </button>
          </>
        )}
      </div>

      {job.import_status === 'failed' && (
        <div className="alert alert-error" style={{ marginTop: 'var(--spacing-sm)' }}>
          Import failed: {job.import_message}
        </div>
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
    <div style={{ maxWidth: '900px', margin: '0 auto', padding: 'var(--spacing-md)' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', marginBottom: 'var(--spacing-md)' }}>
        <h2 style={{ margin: 0 }}>
          <i className="fas fa-compress" style={{ marginRight: '0.4em' }} />
          Model Quantization
        </h2>
        <span className="badge badge-warning" style={{ fontSize: '0.7em' }}>Experimental</span>
      </div>

      {error && (
        <div className="card" style={{ borderColor: 'var(--color-error)', marginBottom: 'var(--spacing-md)' }}>
          <div style={{ color: 'var(--color-error)' }}><i className="fas fa-exclamation-triangle" /> {error}</div>
        </div>
      )}

      {/* ── New Job Form ── */}
      <form onSubmit={handleSubmit}>
        <div className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
          <FormSection icon="fas fa-cube" title="Model">
            <input
              className="input"
              placeholder="HuggingFace model name (e.g. meta-llama/Llama-3.2-1B) or local path"
              value={model}
              onChange={e => setModel(e.target.value)}
              required
              style={{ width: '100%' }}
            />
          </FormSection>

          <FormSection icon="fas fa-sliders-h" title="Quantization Type">
            <div style={{ display: 'flex', gap: 'var(--spacing-sm)', alignItems: 'center', flexWrap: 'wrap' }}>
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
                style={{ maxWidth: '200px' }}
              >
                {QUANT_PRESETS.map(q => (
                  <option key={q} value={q}>{q}</option>
                ))}
                <option value="__custom__">Custom...</option>
              </select>
              {useCustomQuant && (
                <input
                  className="input"
                  placeholder="Enter custom quantization type"
                  value={customQuantType}
                  onChange={e => setCustomQuantType(e.target.value)}
                  required
                  style={{ maxWidth: '220px' }}
                />
              )}
            </div>
          </FormSection>

          <FormSection icon="fas fa-server" title="Backend">
            <select
              className="input"
              value={backend}
              onChange={e => setBackend(e.target.value)}
              style={{ maxWidth: '280px' }}
            >
              {backends.map(b => (
                <option key={b.name || b} value={b.name || b}>{b.name || b}</option>
              ))}
            </select>
          </FormSection>

          <FormSection icon="fas fa-key" title="HuggingFace Token (optional)">
            <input
              className="input"
              type="password"
              placeholder="hf_... (required for gated models)"
              value={hfToken}
              onChange={e => setHfToken(e.target.value)}
              style={{ maxWidth: '400px' }}
            />
          </FormSection>

          <button
            className="btn btn-primary"
            type="submit"
            disabled={submitting || !model || (useCustomQuant && !customQuantType)}
          >
            {submitting ? (
              <><i className="fas fa-spinner fa-spin" style={{ marginRight: '0.4em' }} /> Starting...</>
            ) : (
              <><i className="fas fa-play" style={{ marginRight: '0.4em' }} /> Quantize ({effectiveQuantType})</>
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
        <div className="card">
          <h4 style={{ marginBottom: 'var(--spacing-sm)' }}>
            <i className="fas fa-list" style={{ marginRight: '0.5em' }} />
            Jobs
          </h4>
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.9em' }}>
              <thead>
                <tr style={{ borderBottom: '1px solid var(--color-border)' }}>
                  <th style={{ textAlign: 'left', padding: '0.5em' }}>Model</th>
                  <th style={{ textAlign: 'left', padding: '0.5em' }}>Quant</th>
                  <th style={{ textAlign: 'left', padding: '0.5em' }}>Status</th>
                  <th style={{ textAlign: 'left', padding: '0.5em' }}>Created</th>
                  <th style={{ textAlign: 'right', padding: '0.5em' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {jobs.map(job => {
                  const isActive = ['queued', 'downloading', 'converting', 'quantizing'].includes(job.status)
                  const isSelected = selectedJob?.id === job.id
                  return (
                    <tr
                      key={job.id}
                      style={{
                        borderBottom: '1px solid var(--color-border)',
                        background: isSelected ? 'var(--color-bg-secondary)' : undefined,
                        cursor: 'pointer',
                      }}
                      onClick={() => setSelectedJob(job)}
                    >
                      <td style={{ padding: '0.5em', maxWidth: '250px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                        {job.model}
                      </td>
                      <td style={{ padding: '0.5em' }}>
                        <code>{job.quantization_type}</code>
                      </td>
                      <td style={{ padding: '0.5em' }}>
                        <span className={`badge ${statusBadgeClass[job.status] || ''}`}>{job.status}</span>
                        {job.import_status === 'completed' && (
                          <span className="badge badge-success" style={{ marginLeft: '0.3em' }}>imported</span>
                        )}
                      </td>
                      <td style={{ padding: '0.5em', fontSize: '0.85em', color: 'var(--color-text-secondary)' }}>
                        {new Date(job.created_at).toLocaleString()}
                      </td>
                      <td style={{ padding: '0.5em', textAlign: 'right' }}>
                        <div style={{ display: 'flex', gap: '0.3em', justifyContent: 'flex-end' }} onClick={e => e.stopPropagation()}>
                          {isActive && (
                            <button className="btn btn-sm btn-error" onClick={() => handleStop(job.id)} title="Stop">
                              <i className="fas fa-stop" />
                            </button>
                          )}
                          {!isActive && (
                            <button className="btn btn-sm" onClick={() => handleDelete(job.id)} title="Delete">
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
