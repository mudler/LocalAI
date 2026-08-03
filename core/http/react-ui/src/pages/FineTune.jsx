import { useState, useEffect, useRef, useCallback } from 'react'
import { fineTuneApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'
import PageHeader from '../components/PageHeader'
import SectionHeading from '../components/SectionHeading'
import StatCard from '../components/StatCard'
import EmptyState from '../components/EmptyState'
import Toggle from '../components/Toggle'
import ResponsiveTable from '../components/ResponsiveTable'
import UnsavedChangesGuard from '../components/UnsavedChangesGuard'

const TRAINING_METHODS = ['sft', 'dpo', 'grpo', 'rloo', 'reward', 'kto', 'orpo']
const TRAINING_TYPES = ['lora', 'loha', 'lokr', 'full']
const FALLBACK_BACKENDS = ['trl']
const OPTIMIZERS = ['adamw_torch', 'adamw_8bit', 'sgd', 'adafactor', 'prodigy']
const MIXED_PRECISION_OPTS = ['', 'fp16', 'bf16', 'no']

const BUILTIN_REWARDS = [
  { name: 'format_reward', description: 'Checks <think>...</think> then answer format', params: [] },
  { name: 'reasoning_accuracy_reward', description: 'Compares <answer> content to dataset answer column', params: [] },
  { name: 'length_reward', description: 'Score based on proximity to target length', params: [{ key: 'target_length', default: '200', label: 'Target Length' }] },
  { name: 'xml_tag_reward', description: 'Scores properly opened/closed XML tags', params: [] },
  { name: 'no_repetition_reward', description: 'Penalizes n-gram repetition', params: [] },
  { name: 'code_execution_reward', description: 'Checks Python code block syntax validity', params: [] },
]

const ACTIVE_STATUSES = ['queued', 'loading_model', 'loading_dataset', 'training', 'saving']
const TERMINAL_STATUSES = ['completed', 'stopped', 'failed']

const statusBadgeClass = {
  queued: '',
  loading_model: 'badge-warning',
  loading_dataset: 'badge-warning',
  training: 'badge-info',
  saving: 'badge-info',
  completed: 'badge-success',
  failed: 'badge-error',
  stopped: '',
}

function StatusBadge({ status }) {
  return <span className={`badge ${statusBadgeClass[status] || ''}`}>{status}</span>
}

function FormSection({ icon, title, children }) {
  return (
    <section className="form-group">
      <h4 className="form-group__title">
        <i className={icon} />
        {title}
      </h4>
      <div className="form-group__body">{children}</div>
    </section>
  )
}

function KeyValueEditor({ entries, onChange }) {
  const addEntry = () => onChange([...entries, { key: '', value: '' }])
  const removeEntry = (i) => onChange(entries.filter((_, idx) => idx !== i))
  const updateEntry = (i, field, val) => {
    onChange(entries.map((e, idx) => idx === i ? { ...e, [field]: val } : e))
  }

  return (
    <div className="ft-kv">
      {entries.map((entry, i) => (
        <div key={i} className="ft-kv__row">
          <input
            className="input ft-kv__key"
            value={entry.key}
            onChange={e => updateEntry(i, 'key', e.target.value)}
            placeholder="Key"
            aria-label={`Extra option ${i + 1} key`}
          />
          <input
            className="input ft-kv__value"
            value={entry.value}
            onChange={e => updateEntry(i, 'value', e.target.value)}
            placeholder="Value"
            aria-label={`Extra option ${i + 1} value`}
          />
          <button type="button" className="btn btn-danger btn-sm" onClick={() => removeEntry(i)} aria-label={`Remove extra option ${i + 1}`}>
            <i className="fas fa-times" />
          </button>
        </div>
      ))}
      <button type="button" className="btn btn-sm" onClick={addEntry}>
        <i className="fas fa-plus" /> Add option
      </button>
    </div>
  )
}

function CopyButton({ text }) {
  const [copied, setCopied] = useState(false)
  const handleCopy = (e) => {
    e.stopPropagation()
    navigator.clipboard.writeText(text).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }
  return (
    <button className="btn btn-sm btn-ghost" onClick={handleCopy} title="Copy to clipboard" aria-label="Copy to clipboard">
      <i className={`fas fa-${copied ? 'check' : 'copy'}`} />
    </button>
  )
}

function JobCard({ job, onSelect, onUseConfig, onDelete }) {
  return (
    <div className="card ft-job" onClick={() => onSelect(job)}>
      <div className="ft-job__head">
        <div className="ft-job__title">
          <strong>{job.model}</strong>
          <span className="ft-job__backend">{job.backend} / {job.training_method || 'sft'}</span>
        </div>
        <div className="row-actions">
          <button
            className="btn btn-sm"
            onClick={(e) => { e.stopPropagation(); onUseConfig(job) }}
            title="Use this job's configuration for a new job"
          >
            <i className="fas fa-copy" /> Reuse
          </button>
          {TERMINAL_STATUSES.includes(job.status) && (
            <button
              className="btn btn-danger btn-sm"
              onClick={(e) => { e.stopPropagation(); onDelete(job.id) }}
              title="Delete this job and its data"
              aria-label="Delete job"
            >
              <i className="fas fa-trash" />
            </button>
          )}
          <StatusBadge status={job.status} />
        </div>
      </div>
      <div className="ft-job__meta">
        ID: {job.id?.slice(0, 8)}... | Created: {job.created_at}
      </div>
      {job.output_dir && (
        <div className="ft-job__path">
          <i className="fas fa-folder" />
          <span className="cell-truncate" title={job.output_dir}>{job.output_dir}</span>
          <CopyButton text={job.output_dir} />
        </div>
      )}
      {job.message && (
        <div className={`ft-job__message${job.status === 'failed' ? ' ft-job__message--failed' : ''}`}>
          <i className="fas fa-info-circle" /> {job.message}
        </div>
      )}
    </div>
  )
}

function formatEta(seconds) {
  if (!seconds || seconds <= 0) return '--'
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const s = Math.floor(seconds % 60)
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m ${s}s`
  return `${s}s`
}

function formatAxisValue(val, decimals) {
  if (val >= 1) return val.toFixed(Math.min(decimals, 1))
  if (val >= 0.01) return val.toFixed(Math.min(decimals, 3))
  return val.toExponential(1)
}

function SingleMetricChart({ data, valueKey, label, color, formatValue, events }) {
  const [tooltip, setTooltip] = useState(null)
  const svgRef = useRef(null)

  if (!data || data.length < 1) return null

  const pad = { top: 16, right: 12, bottom: 32, left: 52 }
  const W = 400, H = 220
  const cw = W - pad.left - pad.right
  const ch = H - pad.top - pad.bottom

  const steps = data.map(e => e.current_step)
  const values = data.map(e => e[valueKey])

  const minStep = Math.min(...steps), maxStep = Math.max(...steps)
  const stepRange = maxStep - minStep || 1
  const minVal = Math.min(...values), maxVal = Math.max(...values)
  const valRange = maxVal - minVal || 1
  const valPad = valRange * 0.05
  const yMin = Math.max(0, minVal - valPad), yMax = maxVal + valPad
  const yRange = yMax - yMin || 1

  const x = (step) => pad.left + ((step - minStep) / stepRange) * cw
  const y = (val) => pad.top + (1 - (val - yMin) / yRange) * ch

  const points = data.map(e => `${x(e.current_step)},${y(e[valueKey])}`).join(' ')

  const xTickCount = Math.min(5, data.length)
  const xTicks = Array.from({ length: xTickCount }, (_, i) => Math.round(minStep + (stepRange * i) / (xTickCount - 1)))
  const yTickCount = 4
  const yTicks = Array.from({ length: yTickCount }, (_, i) => yMin + (yRange * i) / (yTickCount - 1))

  // Epoch boundaries from the full events list if provided
  const epochBoundaries = []
  const evts = events || data
  for (let i = 1; i < evts.length; i++) {
    const prevEpoch = Math.floor(evts[i - 1].current_epoch || 0)
    const curEpoch = Math.floor(evts[i].current_epoch || 0)
    if (curEpoch > prevEpoch && curEpoch > 0) {
      epochBoundaries.push({ step: evts[i].current_step, epoch: curEpoch })
    }
  }

  const fmtVal = formatValue || ((v) => formatAxisValue(v, 3))

  const handleMouseMove = (e) => {
    if (!svgRef.current) return
    const rect = svgRef.current.getBoundingClientRect()
    const mx = ((e.clientX - rect.left) / rect.width) * W
    const step = minStep + ((mx - pad.left) / cw) * stepRange
    let nearest = data[0], bestDist = Infinity
    for (const d of data) {
      const dist = Math.abs(d.current_step - step)
      if (dist < bestDist) { bestDist = dist; nearest = d }
    }
    setTooltip({ x: x(nearest.current_step), y: y(nearest[valueKey]), data: nearest })
  }

  return (
    <div className="ft-chart" style={{ '--ft-chart-color': color }}>
      <div className="ft-chart__head">
        <span className="ft-chart__key" />
        {label}
      </div>
      <svg
        ref={svgRef}
        className="ft-chart__svg"
        viewBox={`0 0 ${W} ${H}`}
        role="img"
        aria-label={`${label} over training steps`}
        onMouseMove={handleMouseMove}
        onMouseLeave={() => setTooltip(null)}
      >
        {yTicks.map((val, i) => (
          <line key={i} x1={pad.left} x2={W - pad.right} y1={y(val)} y2={y(val)}
            stroke="currentColor" strokeOpacity={0.08} strokeDasharray="3 3" />
        ))}
        {epochBoundaries.map((eb, i) => (
          <line key={i} x1={x(eb.step)} x2={x(eb.step)} y1={pad.top} y2={H - pad.bottom}
            stroke="currentColor" strokeOpacity={0.15} strokeDasharray="4 3" />
        ))}
        <polyline points={points} fill="none" stroke={color} strokeWidth={1.5} strokeLinejoin="round" />
        <line x1={pad.left} x2={W - pad.right} y1={H - pad.bottom} y2={H - pad.bottom}
          stroke="currentColor" strokeOpacity={0.2} />
        {xTicks.map((step, i) => (
          <text key={i} x={x(step)} y={H - pad.bottom + 14} textAnchor="middle"
            fill="currentColor" fillOpacity={0.5} fontSize={9}>{step}</text>
        ))}
        <line x1={pad.left} x2={pad.left} y1={pad.top} y2={H - pad.bottom}
          stroke="currentColor" strokeOpacity={0.2} />
        {yTicks.map((val, i) => (
          <text key={i} x={pad.left - 6} y={y(val) + 3} textAnchor="end"
            fill="currentColor" fillOpacity={0.5} fontSize={9}>{fmtVal(val)}</text>
        ))}
        <text x={pad.left + cw / 2} y={H - 2} textAnchor="middle"
          fill="currentColor" fillOpacity={0.4} fontSize={8}>Step</text>
        {tooltip && (
          <g>
            <line x1={tooltip.x} x2={tooltip.x} y1={pad.top} y2={H - pad.bottom}
              stroke={color} strokeOpacity={0.4} strokeDasharray="2 2" />
            <circle cx={tooltip.x} cy={tooltip.y} r={3} fill={color} />
            <rect x={Math.min(tooltip.x + 8, W - 120)} y={tooltip.y - 24} width={110} height={30} rx={3}
              fill="var(--color-bg)" stroke="var(--color-border)" strokeWidth={1} />
            <text x={Math.min(tooltip.x + 14, W - 114)} y={tooltip.y - 10} fill="currentColor" fontSize={9}>
              Step {tooltip.data.current_step}
            </text>
            <text x={Math.min(tooltip.x + 14, W - 114)} y={tooltip.y + 2} fill={color} fontSize={9} fontWeight="bold">
              {fmtVal(tooltip.data[valueKey])}
            </text>
          </g>
        )}
      </svg>
    </div>
  )
}

function ChartsGrid({ events }) {
  const lossData = events.filter(e => e.loss > 0)
  const evalData = events.filter(e => e.eval_loss > 0)
  const lrData = events.filter(e => e.learning_rate != null && e.learning_rate > 0)
  const gradNormData = events.filter(e => e.grad_norm != null && e.grad_norm > 0)

  const fmtExp = (v) => v.toExponential(1)

  if (lossData.length < 2 && lrData.length < 2 && gradNormData.length < 2) return null

  return (
    <div className="ft-chart-grid">
      <SingleMetricChart data={lossData} valueKey="loss" label="Training Loss" color="var(--color-data-7)" events={events} />
      {evalData.length >= 1 ? (
        <SingleMetricChart data={evalData} valueKey="eval_loss" label="Eval Loss" color="var(--color-data-2)" events={events} />
      ) : (
        <div className="ft-chart-empty">
          <i className="fas fa-chart-area" />
          Eval loss, waiting for eval data
        </div>
      )}
      <SingleMetricChart data={lrData} valueKey="learning_rate" label="Learning Rate" color="var(--color-data-3)" formatValue={fmtExp} events={events} />
      <SingleMetricChart data={gradNormData} valueKey="grad_norm" label="Gradient Norm" color="var(--color-data-6)" events={events} />
    </div>
  )
}

function TrainingMonitor({ job, onStop }) {
  const [events, setEvents] = useState([])
  const [latest, setLatest] = useState(null)
  const [connecting, setConnecting] = useState(true)
  const eventSourceRef = useRef(null)

  useEffect(() => {
    if (!job || !ACTIVE_STATUSES.includes(job.status)) {
      setConnecting(false)
      return
    }

    setConnecting(true)
    setLatest(null)
    setEvents([])

    const url = fineTuneApi.progressUrl(job.id)
    const es = new EventSource(url)
    eventSourceRef.current = es

    es.onmessage = (e) => {
      try {
        setConnecting(false)
        const data = JSON.parse(e.data)
        setLatest(data)
        if (data.loss > 0) {
          setEvents(prev => [...prev, data])
        }
        if (TERMINAL_STATUSES.includes(data.status)) {
          es.close()
        }
      } catch (_) {}
    }

    es.onerror = () => {
      setConnecting(false)
      es.close()
    }

    return () => {
      es.close()
    }
  }, [job?.id])

  if (!job) return null

  const progress = Math.min(latest?.progress_percent || 0, 100)

  return (
    <section className="card">
      <SectionHeading>
        <i className="fas fa-chart-line" /> Training monitor
      </SectionHeading>

      {connecting && !latest && (
        <EmptyState
          icon="fas fa-satellite-dish"
          title="Connecting to training stream"
          body="Waiting for the first progress event from the backend."
        />
      )}

      {latest && (
        <>
          <div className="stat-cards">
            <StatCard icon="fas fa-circle-notch" label="Status" value={latest.status} accentVar="--color-info" />
            <StatCard icon="fas fa-percent" label="Progress" value={`${latest.progress_percent?.toFixed(1)}%`} accentVar="--color-primary" />
            <StatCard icon="fas fa-shoe-prints" label="Step" value={`${latest.current_step} / ${latest.total_steps}`} />
            <StatCard icon="fas fa-arrow-trend-down" label="Loss" value={latest.loss?.toFixed(4)} accentVar="--color-data-7" />
            <StatCard icon="fas fa-repeat" label="Epoch" value={`${latest.current_epoch?.toFixed(2)} / ${latest.total_epochs?.toFixed(0)}`} />
            <StatCard icon="fas fa-gauge-high" label="Learning rate" value={latest.learning_rate?.toExponential(2)} accentVar="--color-data-3" />
            <StatCard icon="fas fa-hourglass-half" label="ETA" value={formatEta(latest.eta_seconds)} />
            {latest.extra_metrics?.tokens_per_second > 0 && (
              <StatCard icon="fas fa-bolt" label="Tokens/sec" value={latest.extra_metrics.tokens_per_second.toFixed(0)} accentVar="--color-success" />
            )}
          </div>

          <div
            className="progress-bar"
            role="progressbar"
            aria-valuenow={Math.round(progress)}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-label="Training progress"
          >
            <div className="progress-bar__fill" style={{ width: `${progress}%` }} />
          </div>
        </>
      )}

      <ChartsGrid events={events} />

      {latest?.message && (
        <p className="form-hint">
          <i className="fas fa-info-circle" /> {latest.message}
        </p>
      )}

      {ACTIVE_STATUSES.includes(latest?.status || job.status) && (
        <button className="btn btn-danger" onClick={() => onStop(job.id)}>
          <i className="fas fa-stop" /> Stop training
        </button>
      )}
    </section>
  )
}

function CheckpointsPanel({ job, onResume, onExportCheckpoint }) {
  const [checkpoints, setCheckpoints] = useState([])
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (!job) return
    setLoading(true)
    fineTuneApi.listCheckpoints(job.id).then(r => {
      setCheckpoints(r.checkpoints || [])
    }).catch(() => {}).finally(() => setLoading(false))
  }, [job?.id])

  if (!job) return null
  if (loading) {
    return (
      <section className="card">
        <SectionHeading><i className="fas fa-save" /> Checkpoints</SectionHeading>
        <p className="form-hint"><LoadingSpinner size="sm" /> Loading checkpoints...</p>
      </section>
    )
  }
  if (checkpoints.length === 0) return null

  return (
    <section className="card">
      <SectionHeading><i className="fas fa-save" /> Checkpoints</SectionHeading>
      <ResponsiveTable>
        <table className="data-table">
          <thead>
            <tr>
              <th>Step</th>
              <th>Epoch</th>
              <th>Loss</th>
              <th>Created</th>
              <th>Path</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {checkpoints.map(cp => (
              <tr key={cp.path}>
                <td>{cp.step}</td>
                <td>{cp.epoch?.toFixed(2)}</td>
                <td>{cp.loss?.toFixed(4)}</td>
                <td>{cp.created_at}</td>
                <td>
                  <span className="cell-truncate cell-mono" title={cp.path}>{cp.path}</span>
                  <CopyButton text={cp.path} />
                </td>
                <td>
                  <div className="row-actions">
                    <button className="btn btn-sm" onClick={() => onResume(cp)} title="Resume training from this checkpoint">
                      <i className="fas fa-play" /> Resume
                    </button>
                    <button className="btn btn-sm" onClick={() => onExportCheckpoint(cp)} title="Export this checkpoint">
                      <i className="fas fa-file-export" /> Export
                    </button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </ResponsiveTable>
    </section>
  )
}

const QUANT_PRESETS = ['q4_k_m', 'q5_k_m', 'q8_0', 'f16', 'q4_0', 'q5_0']

function ExportPanel({ job, prefilledCheckpoint }) {
  const [checkpoints, setCheckpoints] = useState([])
  const [exportFormat, setExportFormat] = useState('lora')
  const [quantMethod, setQuantMethod] = useState('q4_k_m')
  const [modelName, setModelName] = useState('')
  const [selectedCheckpoint, setSelectedCheckpoint] = useState('')
  const [exporting, setExporting] = useState(false)
  const [message, setMessage] = useState('')
  const [exportedModelName, setExportedModelName] = useState('')
  const pollRef = useRef(null)

  useEffect(() => {
    if (!job) return
    fineTuneApi.listCheckpoints(job.id).then(r => {
      setCheckpoints(r.checkpoints || [])
    }).catch(() => {})
  }, [job?.id])

  // Apply prefilled checkpoint when set
  useEffect(() => {
    if (prefilledCheckpoint) {
      setSelectedCheckpoint(prefilledCheckpoint.path || '')
    }
  }, [prefilledCheckpoint])

  // Sync export state from job (e.g. on initial load or job list refresh)
  useEffect(() => {
    if (!job) return
    if (job.export_status === 'exporting') {
      setExporting(true)
      setMessage(job.export_message || 'Export in progress...')
    } else if (job.export_status === 'completed' && job.export_model_name) {
      setExporting(false)
      setExportedModelName(job.export_model_name)
      setMessage(`Model exported and registered as "${job.export_model_name}"`)
    } else if (job.export_status === 'failed') {
      setExporting(false)
      setMessage(`Export failed: ${job.export_message || 'unknown error'}`)
    }
  }, [job?.export_status, job?.export_model_name, job?.export_message])

  // Poll for export completion
  useEffect(() => {
    if (!exporting || !job) return

    pollRef.current = setInterval(async () => {
      try {
        const updated = await fineTuneApi.getJob(job.id)
        if (updated.export_status === 'completed') {
          setExporting(false)
          const name = updated.export_model_name || modelName || 'exported model'
          setExportedModelName(name)
          setMessage(`Model exported and registered as "${name}"`)
          clearInterval(pollRef.current)
        } else if (updated.export_status === 'failed') {
          setExporting(false)
          setMessage(`Export failed: ${updated.export_message || 'unknown error'}`)
          clearInterval(pollRef.current)
        } else if (updated.export_status === 'exporting' && updated.export_message) {
          setMessage(updated.export_message)
        }
      } catch (_) {}
    }, 3000)

    return () => clearInterval(pollRef.current)
  }, [exporting, job?.id])

  const handleExport = async () => {
    setExporting(true)
    setMessage('Export in progress...')
    setExportedModelName('')
    try {
      await fineTuneApi.exportModel(job.id, {
        name: modelName || undefined,
        checkpoint_path: selectedCheckpoint || job.output_dir,
        export_format: exportFormat,
        quantization_method: exportFormat === 'gguf' ? quantMethod : '',
        model: job.model,
      })
      // Polling will pick up completion/failure
    } catch (e) {
      setMessage(`Export failed: ${e.message}`)
      setExporting(false)
    }
  }

  // Show export panel for completed, stopped, and failed jobs (checkpoints may exist)
  if (!job || !TERMINAL_STATUSES.includes(job.status)) return null

  const failed = message.includes('failed')

  return (
    <section className="card">
      <SectionHeading><i className="fas fa-file-export" /> Export model</SectionHeading>

      <div className="ft-stack">
        {checkpoints.length > 0 && (
          <div>
            <label className="form-label" htmlFor="ft-export-checkpoint">Checkpoint</label>
            <select id="ft-export-checkpoint" value={selectedCheckpoint} onChange={e => setSelectedCheckpoint(e.target.value)} className="input">
              <option value="">Final model (output directory)</option>
              {checkpoints.map(cp => (
                <option key={cp.path} value={cp.path}>
                  Step {cp.step} (loss: {cp.loss?.toFixed(4)})
                </option>
              ))}
            </select>
          </div>
        )}

        <div className="form-grid-2col">
          <div>
            <label className="form-label" htmlFor="ft-export-format">Export format</label>
            <select id="ft-export-format" value={exportFormat} onChange={e => setExportFormat(e.target.value)} className="input">
              <option value="lora">LoRA adapter</option>
              <option value="merged_16bit">Merged (16-bit)</option>
              <option value="merged_4bit">Merged (4-bit)</option>
              <option value="gguf">GGUF</option>
            </select>
          </div>
          {exportFormat === 'gguf' && (
            <div>
              <label className="form-label" htmlFor="ft-export-quant">Quantization</label>
              <input
                id="ft-export-quant"
                list="quant-presets"
                value={quantMethod}
                onChange={e => setQuantMethod(e.target.value)}
                placeholder="e.g. q4_k_m, bf16, f32"
                className="input"
              />
              <datalist id="quant-presets">
                {QUANT_PRESETS.map(q => <option key={q} value={q} />)}
              </datalist>
            </div>
          )}
        </div>

        <div>
          <label className="form-label" htmlFor="ft-export-name">Model name</label>
          <input
            id="ft-export-name"
            type="text"
            value={modelName}
            onChange={e => setModelName(e.target.value)}
            placeholder="e.g. my-finetuned-model"
            className="input"
          />
          <p className="form-hint">Leave blank to generate one from the job.</p>
        </div>

        <div className="ft-actions">
          <button className="btn btn-primary" onClick={handleExport} disabled={exporting}>
            {exporting
              ? <><LoadingSpinner size="sm" /> Exporting...</>
              : <><i className="fas fa-download" /> Export</>}
          </button>
        </div>

        {message && (
          <div className={`ft-export-status${failed ? ' ft-export-status--error' : ''}`} role="status">
            {exporting && <LoadingSpinner size="sm" />} {message}
            {exportedModelName && !failed && (
              <span className="ft-export-status__links">
                <a href={`/app/chat/${encodeURIComponent(exportedModelName)}`} className="badge badge-link">
                  Chat with {exportedModelName}
                </a>
                <a href={fineTuneApi.downloadUrl(job.id)} download className="btn btn-sm">
                  <i className="fas fa-download" /> Download archive
                </a>
              </span>
            )}
          </div>
        )}
      </div>
    </section>
  )
}

export default function FineTune() {
  const [jobs, setJobs] = useState([])
  const [selectedJob, setSelectedJob] = useState(null)
  const [showForm, setShowForm] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [backends, setBackends] = useState([])
  const [exportCheckpoint, setExportCheckpoint] = useState(null)
  // Baseline of the assembled config for the unsaved-changes guard.
  const initialConfigRef = useRef(null)

  // Form state
  const [model, setModel] = useState('')
  const [backend, setBackend] = useState('')
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
  // liquid-audio specific knobs (folded into extra_options on submit)
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
  const [rewardFunctions, setRewardFunctions] = useState([]) // [{type, name, code?, params?}]
  const [showAddCustomReward, setShowAddCustomReward] = useState(false)
  const [customRewardName, setCustomRewardName] = useState('')
  const [customRewardCode, setCustomRewardCode] = useState('')

  const loadJobs = useCallback(async () => {
    try {
      const data = await fineTuneApi.listJobs()
      setJobs(data || [])
    } catch (_) {}
  }, [])

  useEffect(() => {
    loadJobs()
    const interval = setInterval(loadJobs, 10000)
    return () => clearInterval(interval)
  }, [loadJobs])

  useEffect(() => {
    fineTuneApi.listBackends()
      .then(data => {
        const names = data && data.length > 0 ? data.map(b => b.name) : FALLBACK_BACKENDS
        setBackends(names)
        setBackend(prev => prev || names[0] || '')
      })
      .catch(() => {
        setBackends(FALLBACK_BACKENDS)
        setBackend(prev => prev || FALLBACK_BACKENDS[0])
      })
  }, [])

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
      for (const { key, value } of extraOptions) {
        if (key.trim()) extra[key.trim()] = value
      }
      // Fold liquid-audio specific fields into extra_options. The Python
      // backend reads `voice` and `val_dataset` directly from there.
      if (backend === 'liquid-audio') {
        if (liquidAudioVoice) extra.voice = liquidAudioVoice
        if (liquidAudioValDataset.trim()) extra.val_dataset = liquidAudioValDataset.trim()
      }

      const isAdapterType = ['lora', 'loha', 'lokr'].includes(trainingType)

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
        adapter_rank: isAdapterType ? adapterRank : 0,
        adapter_alpha: isAdapterType ? adapterAlpha : 0,
        adapter_dropout: isAdapterType && adapterDropout > 0 ? adapterDropout : undefined,
        target_modules: isAdapterType && targetModules.trim() ? targetModules.split(',').map(s => s.trim()) : undefined,
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
      setShowForm(false)
      setResumeFromCheckpoint('')
      // Job submitted: rebaseline so leaving the page no longer warns.
      initialConfigRef.current = JSON.stringify(getFormConfig())
      await loadJobs()

      const newJob = { ...req, id: resp.id, status: 'queued', created_at: new Date().toISOString() }
      setSelectedJob(newJob)
    } catch (err) {
      setError(err.message)
    }
    setLoading(false)
  }

  const handleStop = async (jobId) => {
    try {
      await fineTuneApi.stopJob(jobId, true)
      await loadJobs()
    } catch (err) {
      setError(err.message)
    }
  }

  const handleDelete = async (jobId) => {
    if (!window.confirm('Delete this job and all its data (checkpoints, exported model)? This cannot be undone.')) return
    try {
      await fineTuneApi.deleteJob(jobId)
      if (selectedJob?.id === jobId) setSelectedJob(null)
      await loadJobs()
    } catch (err) {
      setError(err.message)
    }
  }

  const isAdapter = ['lora', 'loha', 'lokr'].includes(trainingType)

  const getFormConfig = () => {
    const extra = {}
    for (const { key, value } of extraOptions) {
      if (key.trim()) extra[key.trim()] = value
    }
    if (backend === 'liquid-audio') {
      if (liquidAudioVoice) extra.voice = liquidAudioVoice
      if (liquidAudioValDataset.trim()) extra.val_dataset = liquidAudioValDataset.trim()
    }
    return {
      model,
      backend,
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
      optimizer,
      seed,
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
      const modules = Array.isArray(config.target_modules)
        ? config.target_modules.join(', ')
        : String(config.target_modules)
      setTargetModules(modules)
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

    // Handle max_seq_length: top-level field or inside extra_options
    if (config.max_seq_length != null) {
      setMaxSeqLength(Number(config.max_seq_length))
    } else if (config.extra_options?.max_seq_length != null) {
      setMaxSeqLength(Number(config.extra_options.max_seq_length))
    }

    // Eval options — detect enabled state from strategy
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
    // Also restore from extra_options if present (overrides top-level)
    const eo = config.extra_options
    if (eo) restoreEval(eo.eval_strategy, eo.eval_steps, eo.eval_split, eo.eval_dataset_source, eo.eval_split_ratio)

    // Handle save_total_limit from extra_options
    if (config.extra_options?.save_total_limit != null) {
      setSaveTotalLimit(Number(config.extra_options.save_total_limit))
    }

    // Restore liquid-audio specific extras (also filtered out of the
    // freeform list below).
    if (config.extra_options?.voice != null) setLiquidAudioVoice(String(config.extra_options.voice))
    if (config.extra_options?.val_dataset != null) setLiquidAudioValDataset(String(config.extra_options.val_dataset))

    // Convert extra_options object to [{key, value}] entries, filtering out handled keys
    if (config.extra_options && typeof config.extra_options === 'object') {
      const entries = Object.entries(config.extra_options)
        .filter(([k]) => !['max_seq_length', 'save_total_limit', 'hf_token', 'eval_strategy', 'eval_steps', 'eval_split', 'eval_dataset_source', 'eval_split_ratio', 'voice', 'val_dataset'].includes(k))
        .map(([key, value]) => ({ key, value: String(value) }))
      setExtraOptions(entries)
    }

    // Restore reward functions
    if (Array.isArray(config.reward_functions)) {
      setRewardFunctions(config.reward_functions)
    } else {
      setRewardFunctions([])
    }
  }

  const handleExportConfig = () => {
    const config = getFormConfig()
    const json = JSON.stringify(config, null, 2)
    const blob = new Blob([json], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
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
          const config = JSON.parse(ev.target.result)
          applyFormConfig(config)
          setShowForm(true)
          setError('')
        } catch {
          setError('Failed to parse config file. Please ensure it is valid JSON.')
        }
      }
      reader.readAsText(file)
    }
    input.click()
  }

  const handleUseConfig = (job) => {
    // Prefer the stored config if available, otherwise use the job fields
    applyFormConfig(job.config || job)
    setResumeFromCheckpoint('')
    setShowForm(true)
  }

  const handleResumeFromCheckpoint = (checkpoint) => {
    if (!selectedJob) return
    // Apply the original job's config
    applyFormConfig(selectedJob.config || selectedJob)
    setResumeFromCheckpoint(checkpoint.path)
    setShowAdvanced(true)
    setShowForm(true)
  }

  const handleExportCheckpoint = (checkpoint) => {
    setExportCheckpoint(checkpoint)
  }

  // Lazy-init the baseline on first render; dirty when the open form diverges.
  if (initialConfigRef.current === null) initialConfigRef.current = JSON.stringify(getFormConfig())
  const dirty = JSON.stringify(getFormConfig()) !== initialConfigRef.current

  return (
    <div className="page page--wide">
      <UnsavedChangesGuard when={dirty && showForm && !loading} />
      <PageHeader
        title={<>Fine-tuning <span className={`badge badge-warning ft-actions btn fas fa-upload btn btn-primary fas fa-${showForm ? 'times' : 'plus'}`}>Experimental</span></>}
        supporting="Create and manage fine-tuning jobs"
        actions={
          <div>
            <button className="btn btn-secondary" onClick={handleImportConfig}>
              <i className="fas fa-file-import" aria-hidden="true" /> Import config
            </button>
            <button className="btn btn-secondary" onClick={() => setShowForm(!showForm)}>
              <i className={`fas ${showForm ? 'fa-xmark' : 'fa-plus'}`} aria-hidden="true" />
              {showForm ? 'Cancel' : 'New job'}
            </button>
          </div>
        }
      />

      {error && (
        <div className="attention-callout attention-callout--error" role="alert">
          <span><i className="fas fa-exclamation-triangle" /> {error}</span>
        </div>
      )}

      {showForm && (
        <form onSubmit={handleSubmit} className="card ft-form">

          {resumeFromCheckpoint && (
            <div className="ft-banner">
              <i className="fas fa-redo ft-banner__icon" />
              <span>Resuming from checkpoint: <code>{resumeFromCheckpoint}</code></span>
              <button type="button" className="btn btn-sm ft-banner__spacer" onClick={() => setResumeFromCheckpoint('')}>
                <i className="fas fa-times" /> Clear
              </button>
            </div>
          )}

          <FormSection icon="fas fa-server" title="Model and backend">
            <div className="ft-grid-model">
              <div>
                <label className="form-label" htmlFor="ft-backend">Backend</label>
                <select id="ft-backend" value={backend} onChange={e => setBackend(e.target.value)} className="input">
                  {backends.length === 0 ? (
                    <option value="" disabled>No backends available</option>
                  ) : (
                    backends.map(b => <option key={b} value={b}>{b}</option>)
                  )}
                </select>
              </div>
              <div>
                <label className="form-label" htmlFor="ft-method">Training method</label>
                <select id="ft-method" value={trainingMethod} onChange={e => setTrainingMethod(e.target.value)} className="input">
                  {TRAINING_METHODS.map(m => <option key={m} value={m}>{m.toUpperCase()}</option>)}
                </select>
              </div>
              <div>
                <label className="form-label" htmlFor="ft-model">Model</label>
                <input id="ft-model" type="text" value={model} onChange={e => setModel(e.target.value)} placeholder="e.g. TinyLlama/TinyLlama-1.1B-Chat-v1.0" className="input" required />
                <p className="form-hint">A HuggingFace ID or a local path.</p>
              </div>
            </div>
            <div>
              <label className="form-label" htmlFor="ft-hf-token">HuggingFace token</label>
              <input id="ft-hf-token" type="password" value={hfToken} onChange={e => setHfToken(e.target.value)} placeholder="hf_..." className="input" />
              <p className="form-hint">Only needed for gated models.</p>
            </div>
          </FormSection>

          <FormSection icon="fas fa-layer-group" title="Training type and adapter">
            <div className="ft-grid-auto">
              <div>
                <label className="form-label" htmlFor="ft-type">Training type</label>
                <select id="ft-type" value={trainingType} onChange={e => setTrainingType(e.target.value)} className="input">
                  {TRAINING_TYPES.map(t => <option key={t} value={t}>{t}</option>)}
                </select>
              </div>
              {isAdapter && (
                <>
                  <div>
                    <label className="form-label" htmlFor="ft-rank">Rank</label>
                    <input id="ft-rank" type="number" value={adapterRank} onChange={e => setAdapterRank(Number(e.target.value))} className="input" min={1} />
                  </div>
                  <div>
                    <label className="form-label" htmlFor="ft-alpha">Alpha</label>
                    <input id="ft-alpha" type="number" value={adapterAlpha} onChange={e => setAdapterAlpha(Number(e.target.value))} className="input" min={1} />
                  </div>
                  <div>
                    <label className="form-label" htmlFor="ft-dropout">Dropout</label>
                    <input id="ft-dropout" type="number" value={adapterDropout} onChange={e => setAdapterDropout(Number(e.target.value))} className="input" min={0} max={1} step={0.05} />
                  </div>
                </>
              )}
            </div>
            {isAdapter && (
              <div>
                <label className="form-label" htmlFor="ft-target-modules">Target modules</label>
                <input id="ft-target-modules" type="text" value={targetModules} onChange={e => setTargetModules(e.target.value)} placeholder="e.g. q_proj, v_proj, k_proj, o_proj" className="input" />
                <p className="form-hint">Comma-separated. Leave blank for the backend default.</p>
              </div>
            )}
          </FormSection>

          <FormSection icon="fas fa-database" title="Dataset">
            <div className="ft-grid-dataset">
              <div>
                <label className="form-label" htmlFor="ft-dataset">Source</label>
                <input id="ft-dataset" type="text" value={datasetSource} onChange={e => setDatasetSource(e.target.value)} placeholder="e.g. tatsu-lab/alpaca" className="input" />
                <p className="form-hint">A HuggingFace ID, or leave blank and upload a file.</p>
              </div>
              <div>
                <label className="form-label" htmlFor="ft-split">Split</label>
                <input id="ft-split" type="text" value={datasetSplit} onChange={e => setDatasetSplit(e.target.value)} placeholder="e.g. train" className="input" />
              </div>
              <div>
                <label className="form-label" htmlFor="ft-dataset-file">Upload file</label>
                <input id="ft-dataset-file" type="file" onChange={e => setDatasetFile(e.target.files[0])} accept=".json,.jsonl,.csv" className="input input--file" />
              </div>
            </div>
          </FormSection>

          {trainingMethod === 'grpo' && (
            <FormSection icon="fas fa-trophy" title="Reward functions (GRPO)">
              <p className="ft-hint">
                GRPO requires at least one reward function. Select built-in functions or add custom ones.
              </p>

              <div className="ft-rewards">
                {BUILTIN_REWARDS.map(builtin => {
                  const isSelected = rewardFunctions.some(rf => rf.type === 'builtin' && rf.name === builtin.name)
                  const selectedRf = rewardFunctions.find(rf => rf.type === 'builtin' && rf.name === builtin.name)
                  return (
                    <div key={builtin.name} className={`ft-reward${isSelected ? ' ft-reward--on' : ''}`}>
                      <label className="ft-reward__label">
                        <input
                          type="checkbox"
                          checked={isSelected}
                          onChange={e => {
                            if (e.target.checked) {
                              setRewardFunctions(prev => [...prev, { type: 'builtin', name: builtin.name }])
                            } else {
                              setRewardFunctions(prev => prev.filter(rf => !(rf.type === 'builtin' && rf.name === builtin.name)))
                            }
                          }}
                        />
                        <span>
                          <span className="ft-reward__name">{builtin.name}</span>
                          <span className="ft-reward__desc">{builtin.description}</span>
                        </span>
                      </label>
                      {isSelected && builtin.params.length > 0 && (
                        <div className="ft-reward__params">
                          {builtin.params.map(param => (
                            <div key={param.key} className="ft-reward__param">
                              <label className="ft-reward__param-label" htmlFor={`ft-reward-${builtin.name}-${param.key}`}>
                                {param.label}
                              </label>
                              <input
                                id={`ft-reward-${builtin.name}-${param.key}`}
                                type="text"
                                className="input"
                                value={selectedRf?.params?.[param.key] || param.default}
                                onChange={e => {
                                  setRewardFunctions(prev => prev.map(rf =>
                                    rf.type === 'builtin' && rf.name === builtin.name
                                      ? { ...rf, params: { ...(rf.params || {}), [param.key]: e.target.value } }
                                      : rf
                                  ))
                                }}
                              />
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  )
                })}
              </div>

              {rewardFunctions.filter(rf => rf.type === 'inline').map((rf, idx) => (
                <div key={`inline-${idx}`} className="ft-reward ft-reward--on">
                  <div className="ft-job__head">
                    <span className="ft-reward__name">
                      <i className="fas fa-code" /> {rf.name}
                    </span>
                    <button
                      type="button"
                      className="btn btn-danger btn-sm"
                      aria-label={`Remove ${rf.name}`}
                      onClick={() => setRewardFunctions(prev => prev.filter((_, i) => i !== rewardFunctions.indexOf(rf)))}
                    >
                      <i className="fas fa-times" />
                    </button>
                  </div>
                  <pre className="ft-reward__code">{rf.code}</pre>
                </div>
              ))}

              {showAddCustomReward ? (
                <div className="ft-reward-draft">
                  <div>
                    <label className="form-label" htmlFor="ft-reward-name">Function name</label>
                    <input id="ft-reward-name" type="text" className="input" value={customRewardName} onChange={e => setCustomRewardName(e.target.value)} placeholder="e.g. my_custom_reward" />
                  </div>
                  <div>
                    <label className="form-label" htmlFor="ft-reward-code">Function body</label>
                    <textarea
                      id="ft-reward-code"
                      className="textarea"
                      value={customRewardCode}
                      onChange={e => setCustomRewardCode(e.target.value)}
                      placeholder={"return [1.0 if '<think>' in c else 0.0 for c in completions]"}
                      rows={4}
                    />
                    <p className="form-hint">
                      Receives <code>completions, **kwargs</code> and must return <code>list[float]</code>.
                      Available: re, math, json, string.
                    </p>
                  </div>
                  <div className="ft-actions">
                    <button
                      type="button"
                      className="btn btn-primary btn-sm"
                      disabled={!customRewardName.trim() || !customRewardCode.trim()}
                      onClick={() => {
                        setRewardFunctions(prev => [...prev, {
                          type: 'inline',
                          name: customRewardName.trim(),
                          code: customRewardCode,
                        }])
                        setCustomRewardName('')
                        setCustomRewardCode('')
                        setShowAddCustomReward(false)
                      }}
                    >
                      <i className="fas fa-plus" /> Add
                    </button>
                    <button
                      type="button"
                      className="btn btn-sm"
                      onClick={() => { setShowAddCustomReward(false); setCustomRewardName(''); setCustomRewardCode('') }}
                    >
                      Cancel
                    </button>
                  </div>
                </div>
              ) : (
                <button type="button" className="btn btn-sm" onClick={() => setShowAddCustomReward(true)}>
                  <i className="fas fa-plus" /> Add custom reward function
                </button>
              )}
            </FormSection>
          )}

          <FormSection icon="fas fa-sliders-h" title="Hyperparameters">
            <div className="ft-grid-auto-sm">
              <div>
                <label className="form-label" htmlFor="ft-epochs">Epochs</label>
                <input id="ft-epochs" type="number" value={numEpochs} onChange={e => setNumEpochs(Number(e.target.value))} className="input" min={1} />
              </div>
              <div>
                <label className="form-label" htmlFor="ft-batch">Batch size</label>
                <input id="ft-batch" type="number" value={batchSize} onChange={e => setBatchSize(Number(e.target.value))} className="input" min={1} />
              </div>
              <div>
                <label className="form-label" htmlFor="ft-lr">Learning rate</label>
                <input
                  id="ft-lr"
                  type="text"
                  value={learningRateText}
                  onChange={e => {
                    setLearningRateText(e.target.value)
                    const parsed = Number(e.target.value)
                    if (!isNaN(parsed) && parsed > 0) setLearningRate(parsed)
                  }}
                  className="input"
                  placeholder="e.g. 5e-5 or 0.00005"
                />
              </div>
              <div>
                <label className="form-label" htmlFor="ft-grad-accum">Grad accum steps</label>
                <input id="ft-grad-accum" type="number" value={gradAccum} onChange={e => setGradAccum(Number(e.target.value))} className="input" min={1} />
              </div>
              <div>
                <label className="form-label" htmlFor="ft-warmup">Warmup steps</label>
                <input id="ft-warmup" type="number" value={warmupSteps} onChange={e => setWarmupSteps(Number(e.target.value))} className="input" min={0} />
              </div>
              <div>
                <label className="form-label" htmlFor="ft-seq-len">Max seq length</label>
                <input id="ft-seq-len" type="number" value={maxSeqLength} onChange={e => setMaxSeqLength(Number(e.target.value))} className="input" min={64} />
              </div>
              <div>
                <label className="form-label" htmlFor="ft-optimizer">Optimizer</label>
                <select id="ft-optimizer" value={optimizer} onChange={e => setOptimizer(e.target.value)} className="input">
                  {OPTIMIZERS.map(o => <option key={o} value={o}>{o}</option>)}
                </select>
              </div>
              <label className="ft-checkbox">
                <input type="checkbox" checked={gradCheckpointing} onChange={e => setGradCheckpointing(e.target.checked)} />
                Grad checkpointing
              </label>
            </div>
          </FormSection>

          <section className="form-group">
            <button
              type="button"
              className="ft-disclosure"
              onClick={() => setShowAdvanced(!showAdvanced)}
              aria-expanded={showAdvanced}
            >
              <i className={`fas fa-chevron-${showAdvanced ? 'down' : 'right'} ft-disclosure__chevron`} />
              <i className="fas fa-cog ft-disclosure__icon" />
              Advanced options
            </button>

            {showAdvanced && (
              <div className="ft-advanced">
                <div className="ft-grid-auto">
                  <div>
                    <label className="form-label" htmlFor="ft-max-steps">Max steps</label>
                    <input id="ft-max-steps" type="number" value={maxSteps} onChange={e => setMaxSteps(Number(e.target.value))} className="input" min={0} />
                    <p className="form-hint">0 for automatic.</p>
                  </div>
                  <div>
                    <label className="form-label" htmlFor="ft-save-steps">Save steps</label>
                    <input id="ft-save-steps" type="number" value={saveSteps} onChange={e => setSaveSteps(Number(e.target.value))} className="input" min={0} />
                  </div>
                  <div>
                    <label className="form-label" htmlFor="ft-save-limit">Save total limit</label>
                    <input id="ft-save-limit" type="number" value={saveTotalLimit} onChange={e => setSaveTotalLimit(Number(e.target.value))} className="input" min={0} />
                    <p className="form-hint">0 keeps every checkpoint.</p>
                  </div>
                  <div>
                    <label className="form-label" htmlFor="ft-weight-decay">Weight decay</label>
                    <input id="ft-weight-decay" type="number" value={weightDecay} onChange={e => setWeightDecay(Number(e.target.value))} className="input" min={0} step={0.01} />
                  </div>
                  <div>
                    <label className="form-label" htmlFor="ft-seed">Seed</label>
                    <input id="ft-seed" type="number" value={seed} onChange={e => setSeed(Number(e.target.value))} className="input" min={0} />
                    <p className="form-hint">0 picks a random seed.</p>
                  </div>
                  <div>
                    <label className="form-label" htmlFor="ft-precision">Mixed precision</label>
                    <select id="ft-precision" value={mixedPrecision} onChange={e => setMixedPrecision(e.target.value)} className="input">
                      {MIXED_PRECISION_OPTS.map(o => <option key={o} value={o}>{o || 'Auto'}</option>)}
                    </select>
                  </div>
                </div>

                <div>
                  <div className="ft-toggle-row">
                    <Toggle checked={evalEnabled} onChange={setEvalEnabled} />
                    <span>Enable evaluation</span>
                  </div>
                  {evalEnabled && (
                    <div className="ft-grid-auto">
                      <div>
                        <label className="form-label" htmlFor="ft-eval-strategy">Eval strategy</label>
                        <select id="ft-eval-strategy" value={evalStrategy} onChange={e => setEvalStrategy(e.target.value)} className="input">
                          <option value="steps">Steps</option>
                          <option value="epoch">Epoch</option>
                        </select>
                      </div>
                      <div>
                        <label className="form-label" htmlFor="ft-eval-steps">Eval steps</label>
                        <input id="ft-eval-steps" type="number" value={evalSteps} onChange={e => setEvalSteps(Number(e.target.value))} className="input" min={0} />
                        <p className="form-hint">0 matches save steps.</p>
                      </div>
                      <div>
                        <label className="form-label" htmlFor="ft-eval-split">Eval split</label>
                        <input id="ft-eval-split" type="text" value={evalSplit} onChange={e => setEvalSplit(e.target.value)} placeholder="e.g. validation" className="input" />
                      </div>
                      <div>
                        <label className="form-label" htmlFor="ft-eval-dataset">Eval dataset source</label>
                        <input id="ft-eval-dataset" type="text" value={evalDatasetSource} onChange={e => setEvalDatasetSource(e.target.value)} placeholder="Separate HF dataset" className="input" />
                      </div>
                      <div>
                        <label className="form-label" htmlFor="ft-eval-ratio">Auto-split ratio</label>
                        <input id="ft-eval-ratio" type="number" value={evalSplitRatio} onChange={e => setEvalSplitRatio(Number(e.target.value))} className="input" min={0.01} max={0.5} step={0.01} />
                      </div>
                    </div>
                  )}
                </div>

                {resumeFromCheckpoint && (
                  <div>
                    <label className="form-label" htmlFor="ft-resume">Resume from checkpoint</label>
                    <div className="ft-kv__row">
                      <input id="ft-resume" type="text" value={resumeFromCheckpoint} onChange={e => setResumeFromCheckpoint(e.target.value)} className="input ft-kv__key" />
                      <button type="button" className="btn btn-sm" onClick={() => setResumeFromCheckpoint('')} aria-label="Clear checkpoint">
                        <i className="fas fa-times" />
                      </button>
                    </div>
                  </div>
                )}

                {backend === 'liquid-audio' && (
                  <div>
                    <SectionHeading>Liquid Audio</SectionHeading>
                    <p className="ft-hint">
                      Dataset must be preprocessed by <code>LFM2AudioChatMapper</code> (a directory of
                      LFM2DataLoader-ready arrow files). See <code>liquid_audio/examples/preprocess_jenny_tts.py</code>
                      {' '}for the conversion recipe.
                    </p>
                    <div className="ft-grid-liquid">
                      <div>
                        <label className="form-label" htmlFor="ft-la-voice">TTS voice</label>
                        <select id="ft-la-voice" value={liquidAudioVoice} onChange={e => setLiquidAudioVoice(e.target.value)} className="input">
                          <option value="">Inherit from system prompt</option>
                          <option value="us_male">us_male</option>
                          <option value="us_female">us_female</option>
                          <option value="uk_male">uk_male</option>
                          <option value="uk_female">uk_female</option>
                        </select>
                      </div>
                      <div>
                        <label className="form-label" htmlFor="ft-la-val">Validation dataset</label>
                        <input id="ft-la-val" type="text" value={liquidAudioValDataset} onChange={e => setLiquidAudioValDataset(e.target.value)} placeholder="e.g. /data/jenny_tts/val" className="input" />
                      </div>
                    </div>
                  </div>
                )}

                <div>
                  <label className="form-label">Extra options</label>
                  <p className="form-hint">Backend-specific key/value pairs.</p>
                  <KeyValueEditor entries={extraOptions} onChange={setExtraOptions} />
                </div>
              </div>
            )}
          </section>

          <div className="ft-actions">
            <button type="submit" className="btn btn-primary" disabled={loading || (!datasetSource && !datasetFile)}>
              {loading
                ? <><LoadingSpinner size="sm" /> Starting...</>
                : resumeFromCheckpoint
                  ? <><i className="fas fa-redo" /> Resume training</>
                  : <><i className="fas fa-play" /> Start fine-tuning</>}
            </button>
            <button type="button" className="btn" onClick={handleExportConfig}>
              <i className="fas fa-download" /> Export config
            </button>
          </div>
        </form>
      )}

      {/* Either show job detail OR job list, not side-by-side */}
      {selectedJob ? (
        <div className="ft-stack">
          <div>
            <button className="btn" onClick={() => setSelectedJob(null)}>
              <i className="fas fa-arrow-left" /> Back to jobs
            </button>
          </div>
          <div className="card">
            <div className="ft-job__head">
              <div className="ft-job__title">
                <strong>{selectedJob.model}</strong>
                <div className="ft-job__meta">
                  {selectedJob.backend} / {selectedJob.training_method || 'sft'} | ID: {selectedJob.id?.slice(0, 8)}... | {selectedJob.created_at}
                </div>
              </div>
              <StatusBadge status={selectedJob.status} />
            </div>
          </div>
          <TrainingMonitor job={selectedJob} onStop={handleStop} />
          <CheckpointsPanel job={selectedJob} onResume={handleResumeFromCheckpoint} onExportCheckpoint={handleExportCheckpoint} />
          <ExportPanel job={selectedJob} prefilledCheckpoint={exportCheckpoint} />
        </div>
      ) : (
        <div>
          <SectionHeading>Jobs</SectionHeading>
          {jobs.length === 0 ? (
            <EmptyState
              icon="fas fa-graduation-cap"
              title="No fine-tuning jobs yet"
              body="Start one to train an adapter on your own data, then export it as a model you can chat with."
              actions={
                <button className="btn btn-primary" onClick={() => setShowForm(true)}>
                  <i className="fas fa-plus" aria-hidden="true" /> New job
                </button>
              }
            />
          ) : (
            <div className="ft-jobs">
              {jobs.map(job => (
                <JobCard
                  key={job.id}
                  job={job}
                  onSelect={setSelectedJob}
                  onUseConfig={handleUseConfig}
                  onDelete={handleDelete}
                />
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
