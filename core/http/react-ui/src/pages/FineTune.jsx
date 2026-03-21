import { useState, useEffect, useRef, useCallback } from 'react'
import { fineTuneApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'

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

function FormSection({ icon, title, children }) {
  return (
    <div style={{ marginBottom: 'var(--spacing-lg)' }}>
      <h4 style={{
        fontSize: '0.8125rem', fontWeight: 600, textTransform: 'uppercase',
        letterSpacing: '0.05em', color: 'var(--color-text-secondary)',
        display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)',
        marginBottom: 'var(--spacing-md)', paddingBottom: 'var(--spacing-sm)',
        borderBottom: '1px solid var(--color-border-subtle)',
      }}>
        <i className={icon} style={{ color: 'var(--color-primary)' }} />
        {title}
      </h4>
      {children}
    </div>
  )
}

function KeyValueEditor({ entries, onChange }) {
  const addEntry = () => onChange([...entries, { key: '', value: '' }])
  const removeEntry = (i) => onChange(entries.filter((_, idx) => idx !== i))
  const updateEntry = (i, field, val) => {
    const updated = entries.map((e, idx) => idx === i ? { ...e, [field]: val } : e)
    onChange(updated)
  }

  return (
    <div>
      {entries.map((entry, i) => (
        <div key={i} style={{ display: 'flex', gap: 'var(--spacing-sm)', marginBottom: 'var(--spacing-sm)', alignItems: 'center' }}>
          <input
            className="input"
            value={entry.key}
            onChange={e => updateEntry(i, 'key', e.target.value)}
            placeholder="Key"
            style={{ flex: 1 }}
          />
          <input
            className="input"
            value={entry.value}
            onChange={e => updateEntry(i, 'value', e.target.value)}
            placeholder="Value"
            style={{ flex: 2 }}
          />
          <button type="button" className="btn btn-danger" style={{ padding: 'var(--spacing-xs) var(--spacing-sm)' }} onClick={() => removeEntry(i)}>
            <i className="fas fa-times" />
          </button>
        </div>
      ))}
      <button type="button" className="btn" onClick={addEntry} style={{ fontSize: '0.8125rem' }}>
        <i className="fas fa-plus" style={{ marginRight: 'var(--spacing-xs)' }} />
        Add option
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
    <button className="btn" style={{ padding: '1px 4px', fontSize: '0.7rem' }} onClick={handleCopy} title="Copy to clipboard">
      <i className={`fas fa-${copied ? 'check' : 'copy'}`} />
    </button>
  )
}

function JobCard({ job, isSelected, onSelect, onUseConfig, onDelete }) {
  return (
    <div
      className="card"
      style={{
        cursor: 'pointer', marginBottom: 'var(--spacing-sm)',
        border: isSelected ? '2px solid var(--color-primary)' : undefined,
      }}
      onClick={() => onSelect(job)}
    >
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <div>
          <strong>{job.model}</strong>
          <span style={{ marginLeft: 'var(--spacing-sm)', fontSize: '0.875rem', color: 'var(--color-text-muted)' }}>
            {job.backend} / {job.training_method || 'sft'}
          </span>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
          <button
            className="btn"
            style={{ fontSize: '0.75rem', padding: '2px 6px' }}
            onClick={(e) => { e.stopPropagation(); onUseConfig(job) }}
            title="Use this job's configuration for a new job"
          >
            <i className="fas fa-copy" /> Reuse
          </button>
          {['completed', 'stopped', 'failed'].includes(job.status) && (
            <button
              className="btn btn-danger"
              style={{ fontSize: '0.75rem', padding: '2px 6px' }}
              onClick={(e) => { e.stopPropagation(); onDelete(job.id) }}
              title="Delete this job and its data"
            >
              <i className="fas fa-trash" />
            </button>
          )}
          <span className={`badge ${statusBadgeClass[job.status] || ''}`}>
            {job.status}
          </span>
        </div>
      </div>
      <div style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)', marginTop: 'var(--spacing-xs)' }}>
        ID: {job.id?.slice(0, 8)}... | Created: {job.created_at}
      </div>
      {job.output_dir && (
        <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginTop: '2px', display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)' }}>
          <i className="fas fa-folder" />
          <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: '300px' }} title={job.output_dir}>
            {job.output_dir}
          </span>
          <CopyButton text={job.output_dir} />
        </div>
      )}
      {job.message && (
        <div style={{ fontSize: '0.75rem', color: job.status === 'failed' ? 'var(--color-error)' : 'var(--color-text-muted)', marginTop: '2px' }}>
          <i className="fas fa-info-circle" style={{ marginRight: '2px' }} />
          {job.message}
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
    <div>
      <div style={{ fontSize: '0.8125rem', fontWeight: 600, marginBottom: 4, display: 'flex', alignItems: 'center', gap: 6 }}>
        <span style={{ display: 'inline-block', width: 12, height: 3, background: color, borderRadius: 2 }} />
        {label}
      </div>
      <svg
        ref={svgRef}
        viewBox={`0 0 ${W} ${H}`}
        style={{ width: '100%', height: 'auto', maxHeight: 220, background: 'var(--color-bg-secondary)', borderRadius: 'var(--radius-sm)' }}
        onMouseMove={handleMouseMove}
        onMouseLeave={() => setTooltip(null)}
      >
        {yTicks.map((val, i) => (
          <line key={i} x1={pad.left} x2={W - pad.right} y1={y(val)} y2={y(val)}
            stroke="currentColor" strokeOpacity={0.08} strokeDasharray="3 3" />
        ))}
        {epochBoundaries.map((eb, i) => (
          <g key={i}>
            <line x1={x(eb.step)} x2={x(eb.step)} y1={pad.top} y2={H - pad.bottom}
              stroke="currentColor" strokeOpacity={0.15} strokeDasharray="4 3" />
          </g>
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
    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--spacing-md)', marginBottom: 'var(--spacing-md)' }}>
      <SingleMetricChart data={lossData} valueKey="loss" label="Training Loss" color="#3b82f6" events={events} />
      {evalData.length >= 1 ? (
        <SingleMetricChart data={evalData} valueKey="eval_loss" label="Eval Loss" color="#ef4444" events={events} />
      ) : (
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'var(--color-bg-secondary)', borderRadius: 'var(--radius-sm)', minHeight: 120 }}>
          <span style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>
            <i className="fas fa-chart-area" style={{ marginRight: 6 }} />
            Eval Loss — waiting for eval data
          </span>
        </div>
      )}
      <SingleMetricChart data={lrData} valueKey="learning_rate" label="Learning Rate" color="#8b5cf6" formatValue={fmtExp} events={events} />
      <SingleMetricChart data={gradNormData} valueKey="grad_norm" label="Gradient Norm" color="#f97316" events={events} />
    </div>
  )
}

function TrainingMonitor({ job, onStop }) {
  const [events, setEvents] = useState([])
  const [latest, setLatest] = useState(null)
  const [connecting, setConnecting] = useState(true)
  const eventSourceRef = useRef(null)

  useEffect(() => {
    if (!job || !['queued', 'loading_model', 'loading_dataset', 'training', 'saving'].includes(job.status)) {
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
        if (['completed', 'failed', 'stopped'].includes(data.status)) {
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

  return (
    <div className="card" style={{ marginTop: 'var(--spacing-md)' }}>
      <h3 style={{ margin: '0 0 var(--spacing-md) 0' }}>
        <i className="fas fa-chart-line" style={{ marginRight: 'var(--spacing-sm)' }} />
        Training Monitor
      </h3>

      {connecting && !latest && (
        <div style={{ textAlign: 'center', padding: 'var(--spacing-lg)', color: 'var(--color-text-muted)' }}>
          <LoadingSpinner size="sm" /> Connecting to training stream...
        </div>
      )}

      {latest && (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(130px, 1fr))', gap: 'var(--spacing-sm)', marginBottom: 'var(--spacing-md)' }}>
          <div className="card" style={{ padding: 'var(--spacing-sm)', textAlign: 'center' }}>
            <div style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>Status</div>
            <div style={{ fontWeight: 'bold' }}>{latest.status}</div>
          </div>
          <div className="card" style={{ padding: 'var(--spacing-sm)', textAlign: 'center' }}>
            <div style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>Progress</div>
            <div style={{ fontWeight: 'bold' }}>{latest.progress_percent?.toFixed(1)}%</div>
          </div>
          <div className="card" style={{ padding: 'var(--spacing-sm)', textAlign: 'center' }}>
            <div style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>Step</div>
            <div style={{ fontWeight: 'bold' }}>{latest.current_step} / {latest.total_steps}</div>
          </div>
          <div className="card" style={{ padding: 'var(--spacing-sm)', textAlign: 'center' }}>
            <div style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>Loss</div>
            <div style={{ fontWeight: 'bold' }}>{latest.loss?.toFixed(4)}</div>
          </div>
          <div className="card" style={{ padding: 'var(--spacing-sm)', textAlign: 'center' }}>
            <div style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>Epoch</div>
            <div style={{ fontWeight: 'bold' }}>{latest.current_epoch?.toFixed(2)} / {latest.total_epochs?.toFixed(0)}</div>
          </div>
          <div className="card" style={{ padding: 'var(--spacing-sm)', textAlign: 'center' }}>
            <div style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>Learning Rate</div>
            <div style={{ fontWeight: 'bold' }}>{latest.learning_rate?.toExponential(2)}</div>
          </div>
          <div className="card" style={{ padding: 'var(--spacing-sm)', textAlign: 'center' }}>
            <div style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>ETA</div>
            <div style={{ fontWeight: 'bold' }}>{formatEta(latest.eta_seconds)}</div>
          </div>
          {latest.extra_metrics?.tokens_per_second > 0 && (
            <div className="card" style={{ padding: 'var(--spacing-sm)', textAlign: 'center' }}>
              <div style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>Tokens/sec</div>
              <div style={{ fontWeight: 'bold' }}>{latest.extra_metrics.tokens_per_second.toFixed(0)}</div>
            </div>
          )}
        </div>
      )}

      {/* Progress bar */}
      {latest && (
        <div style={{ background: 'var(--color-bg-secondary)', borderRadius: 'var(--radius-sm)', height: '8px', marginBottom: 'var(--spacing-md)' }}>
          <div style={{
            background: 'var(--color-primary)', borderRadius: 'var(--radius-sm)', height: '100%',
            width: `${Math.min(latest.progress_percent || 0, 100)}%`, transition: 'width 0.3s'
          }} />
        </div>
      )}

      {/* Training charts (2x2 grid) */}
      <ChartsGrid events={events} />

      {latest?.message && (
        <div style={{ fontSize: '0.875rem', color: 'var(--color-text-muted)' }}>
          <i className="fas fa-info-circle" style={{ marginRight: 'var(--spacing-xs)' }} />
          {latest.message}
        </div>
      )}

      {['queued', 'loading_model', 'loading_dataset', 'training', 'saving'].includes(latest?.status || job.status) && (
        <button
          className="btn btn-danger"
          style={{ marginTop: 'var(--spacing-sm)' }}
          onClick={() => onStop(job.id)}
        >
          <i className="fas fa-stop" style={{ marginRight: 'var(--spacing-xs)' }} />
          Stop Training
        </button>
      )}
    </div>
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
  if (loading) return <div style={{ padding: 'var(--spacing-md)', fontSize: '0.875rem' }}><LoadingSpinner size="sm" /> Loading checkpoints...</div>
  if (checkpoints.length === 0) return null

  return (
    <div className="card" style={{ marginTop: 'var(--spacing-md)' }}>
      <h3 style={{ margin: '0 0 var(--spacing-md) 0' }}>
        <i className="fas fa-save" style={{ marginRight: 'var(--spacing-sm)' }} />
        Checkpoints
      </h3>
      <div style={{ overflowX: 'auto' }}>
        <table style={{ width: '100%', fontSize: '0.8125rem', borderCollapse: 'collapse' }}>
          <thead>
            <tr style={{ borderBottom: '1px solid var(--color-border-subtle)', textAlign: 'left' }}>
              <th style={{ padding: '4px 8px' }}>Step</th>
              <th style={{ padding: '4px 8px' }}>Epoch</th>
              <th style={{ padding: '4px 8px' }}>Loss</th>
              <th style={{ padding: '4px 8px' }}>Created</th>
              <th style={{ padding: '4px 8px' }}>Path</th>
              <th style={{ padding: '4px 8px' }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {checkpoints.map(cp => (
              <tr key={cp.path} style={{ borderBottom: '1px solid var(--color-border-subtle)' }}>
                <td style={{ padding: '4px 8px' }}>{cp.step}</td>
                <td style={{ padding: '4px 8px' }}>{cp.epoch?.toFixed(2)}</td>
                <td style={{ padding: '4px 8px' }}>{cp.loss?.toFixed(4)}</td>
                <td style={{ padding: '4px 8px' }}>{cp.created_at}</td>
                <td style={{ padding: '4px 8px', maxWidth: '200px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={cp.path}>
                  {cp.path} <CopyButton text={cp.path} />
                </td>
                <td style={{ padding: '4px 8px', whiteSpace: 'nowrap' }}>
                  <button className="btn" style={{ fontSize: '0.7rem', padding: '2px 6px', marginRight: '4px' }}
                    onClick={() => onResume(cp)} title="Resume training from this checkpoint">
                    <i className="fas fa-play" /> Resume
                  </button>
                  <button className="btn" style={{ fontSize: '0.7rem', padding: '2px 6px' }}
                    onClick={() => onExportCheckpoint(cp)} title="Export this checkpoint">
                    <i className="fas fa-file-export" /> Export
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
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
  if (!job || !['completed', 'stopped', 'failed'].includes(job.status)) return null

  return (
    <div className="card" style={{ marginTop: 'var(--spacing-md)' }}>
      <h3 style={{ margin: '0 0 var(--spacing-md) 0' }}>
        <i className="fas fa-file-export" style={{ marginRight: 'var(--spacing-sm)' }} />
        Export Model
      </h3>

      {checkpoints.length > 0 && (
        <div style={{ marginBottom: 'var(--spacing-md)' }}>
          <label className="form-label">Checkpoint</label>
          <select value={selectedCheckpoint} onChange={e => setSelectedCheckpoint(e.target.value)} className="input">
            <option value="">Final model (output directory)</option>
            {checkpoints.map(cp => (
              <option key={cp.path} value={cp.path}>
                Step {cp.step} (loss: {cp.loss?.toFixed(4)})
              </option>
            ))}
          </select>
        </div>
      )}

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--spacing-md)', marginBottom: 'var(--spacing-md)' }}>
        <div>
          <label className="form-label">Export Format</label>
          <select value={exportFormat} onChange={e => setExportFormat(e.target.value)} className="input">
            <option value="lora">LoRA Adapter</option>
            <option value="merged_16bit">Merged (16-bit)</option>
            <option value="merged_4bit">Merged (4-bit)</option>
            <option value="gguf">GGUF</option>
          </select>
        </div>
        {exportFormat === 'gguf' && (
          <div>
            <label className="form-label">Quantization</label>
            <input
              list="quant-presets"
              value={quantMethod}
              onChange={e => setQuantMethod(e.target.value)}
              placeholder="e.g. q4_k_m, bf16, f32"
              className="input"
            />
            <datalist id="quant-presets">
              {QUANT_PRESETS.map(q => (
                <option key={q} value={q} />
              ))}
            </datalist>
          </div>
        )}
      </div>

      <div style={{ marginBottom: 'var(--spacing-md)' }}>
        <label className="form-label">Model Name (leave blank to auto-generate)</label>
        <input
          type="text"
          value={modelName}
          onChange={e => setModelName(e.target.value)}
          placeholder="e.g. my-finetuned-model"
          className="input"
        />
      </div>

      <button className="btn btn-primary" onClick={handleExport} disabled={exporting}>
        {exporting ? <><LoadingSpinner size="sm" /> Exporting...</> :
          <><i className="fas fa-download" style={{ marginRight: 'var(--spacing-xs)' }} /> Export</>}
      </button>

      {message && (
        <div style={{ marginTop: 'var(--spacing-sm)', fontSize: '0.875rem', color: message.includes('failed') ? 'var(--color-error)' : 'var(--color-success)' }}>
          {exporting && <LoadingSpinner size="sm" />} {message}
          {exportedModelName && !message.includes('failed') && (
            <span style={{ marginLeft: 'var(--spacing-sm)' }}>
              <a href={`/app/chat/${encodeURIComponent(exportedModelName)}`} style={{ color: 'var(--color-primary)', textDecoration: 'underline' }}>
                Chat with {exportedModelName}
              </a>
              <a href={fineTuneApi.downloadUrl(job.id)} download className="btn" style={{ marginLeft: 'var(--spacing-sm)', fontSize: '0.8125rem', padding: '2px 8px' }}>
                <i className="fas fa-download" /> Download Archive
              </a>
            </span>
          )}
        </div>
      )}
    </div>
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

      const isAdapter = ['lora', 'loha', 'lokr'].includes(trainingType)

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
      setShowForm(false)
      setResumeFromCheckpoint('')
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

    // Convert extra_options object to [{key, value}] entries, filtering out handled keys
    if (config.extra_options && typeof config.extra_options === 'object') {
      const entries = Object.entries(config.extra_options)
        .filter(([k]) => !['max_seq_length', 'save_total_limit', 'hf_token', 'eval_strategy', 'eval_steps', 'eval_split', 'eval_dataset_source', 'eval_split_ratio'].includes(k))
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

  return (
    <div className="page">
      <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <div>
          <h1 className="page-title">Fine-Tuning</h1>
          <p className="page-subtitle">Create and manage fine-tuning jobs</p>
        </div>
        <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
          <button className="btn" onClick={handleImportConfig}>
            <i className="fas fa-upload" style={{ marginRight: 'var(--spacing-xs)' }} /> Import Config
          </button>
          <button className="btn btn-primary" onClick={() => setShowForm(!showForm)}>
            <i className={`fas fa-${showForm ? 'times' : 'plus'}`} style={{ marginRight: 'var(--spacing-xs)' }} />
            {showForm ? 'Cancel' : 'New Job'}
          </button>
        </div>
      </div>

      {error && (
        <div className="card" style={{ background: 'var(--color-error-light)', borderColor: 'var(--color-error-border)', color: 'var(--color-error)', marginBottom: 'var(--spacing-md)', padding: 'var(--spacing-md)' }}>
          <i className="fas fa-exclamation-triangle" style={{ marginRight: 'var(--spacing-xs)' }} /> {error}
        </div>
      )}

      {showForm && (
        <form onSubmit={handleSubmit} className="card" style={{ marginBottom: 'var(--spacing-md)' }}>

          {resumeFromCheckpoint && (
            <div style={{ marginBottom: 'var(--spacing-md)', padding: 'var(--spacing-sm) var(--spacing-md)', background: 'var(--color-bg-secondary)', borderRadius: 'var(--radius-sm)', display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
              <i className="fas fa-redo" style={{ color: 'var(--color-primary)' }} />
              <span style={{ fontSize: '0.875rem' }}>
                Resuming from checkpoint: <code style={{ fontSize: '0.8rem' }}>{resumeFromCheckpoint}</code>
              </span>
              <button type="button" className="btn" style={{ padding: '2px 6px', fontSize: '0.75rem', marginLeft: 'auto' }} onClick={() => setResumeFromCheckpoint('')}>
                <i className="fas fa-times" /> Clear
              </button>
            </div>
          )}

          <FormSection icon="fas fa-server" title="Model & Backend">
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 2fr', gap: 'var(--spacing-md)' }}>
              <div>
                <label className="form-label">Backend</label>
                <select value={backend} onChange={e => setBackend(e.target.value)} className="input">
                  {backends.length === 0 ? (
                    <option value="" disabled>No backends available</option>
                  ) : (
                    backends.map(b => <option key={b} value={b}>{b}</option>)
                  )}
                </select>
              </div>
              <div>
                <label className="form-label">Training Method</label>
                <select value={trainingMethod} onChange={e => setTrainingMethod(e.target.value)} className="input">
                  {TRAINING_METHODS.map(m => <option key={m} value={m}>{m.toUpperCase()}</option>)}
                </select>
              </div>
              <div>
                <label className="form-label">Model (HuggingFace ID or local path)</label>
                <input type="text" value={model} onChange={e => setModel(e.target.value)} placeholder="e.g. TinyLlama/TinyLlama-1.1B-Chat-v1.0" className="input" required />
              </div>
            </div>
            <div style={{ marginTop: 'var(--spacing-md)' }}>
              <label className="form-label">HuggingFace Token (for gated models)</label>
              <input type="password" value={hfToken} onChange={e => setHfToken(e.target.value)} placeholder="hf_..." className="input" />
            </div>
          </FormSection>

          <FormSection icon="fas fa-layer-group" title="Training Type & Adapter">
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(160px, 1fr))', gap: 'var(--spacing-md)' }}>
              <div>
                <label className="form-label">Training Type</label>
                <select value={trainingType} onChange={e => setTrainingType(e.target.value)} className="input">
                  {TRAINING_TYPES.map(t => <option key={t} value={t}>{t}</option>)}
                </select>
              </div>
              {isAdapter && (
                <>
                  <div>
                    <label className="form-label">Rank</label>
                    <input type="number" value={adapterRank} onChange={e => setAdapterRank(Number(e.target.value))} className="input" min={1} />
                  </div>
                  <div>
                    <label className="form-label">Alpha</label>
                    <input type="number" value={adapterAlpha} onChange={e => setAdapterAlpha(Number(e.target.value))} className="input" min={1} />
                  </div>
                  <div>
                    <label className="form-label">Dropout</label>
                    <input type="number" value={adapterDropout} onChange={e => setAdapterDropout(Number(e.target.value))} className="input" min={0} max={1} step={0.05} />
                  </div>
                </>
              )}
            </div>
            {isAdapter && (
              <div style={{ marginTop: 'var(--spacing-md)' }}>
                <label className="form-label">Target Modules (comma-separated, blank for default)</label>
                <input type="text" value={targetModules} onChange={e => setTargetModules(e.target.value)} placeholder="e.g. q_proj, v_proj, k_proj, o_proj" className="input" />
              </div>
            )}
          </FormSection>

          <FormSection icon="fas fa-database" title="Dataset">
            <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr 1fr', gap: 'var(--spacing-md)' }}>
              <div>
                <label className="form-label">Source (HuggingFace ID or leave blank to upload)</label>
                <input type="text" value={datasetSource} onChange={e => setDatasetSource(e.target.value)} placeholder="e.g. tatsu-lab/alpaca" className="input" />
              </div>
              <div>
                <label className="form-label">Split</label>
                <input type="text" value={datasetSplit} onChange={e => setDatasetSplit(e.target.value)} placeholder="e.g. train" className="input" />
              </div>
              <div>
                <label className="form-label">Upload File</label>
                <input type="file" onChange={e => setDatasetFile(e.target.files[0])} accept=".json,.jsonl,.csv" className="input" style={{ padding: '6px' }} />
              </div>
            </div>
          </FormSection>

          {trainingMethod === 'grpo' && (
            <FormSection icon="fas fa-trophy" title="Reward Functions (GRPO)">
              <div style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)', marginBottom: 'var(--spacing-md)' }}>
                GRPO requires at least one reward function. Select built-in functions or add custom ones.
              </div>

              {/* Built-in reward functions */}
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: 'var(--spacing-sm)', marginBottom: 'var(--spacing-md)' }}>
                {BUILTIN_REWARDS.map(builtin => {
                  const isSelected = rewardFunctions.some(rf => rf.type === 'builtin' && rf.name === builtin.name)
                  const selectedRf = rewardFunctions.find(rf => rf.type === 'builtin' && rf.name === builtin.name)
                  return (
                    <div key={builtin.name} style={{
                      padding: 'var(--spacing-sm) var(--spacing-md)',
                      border: `1px solid ${isSelected ? 'var(--color-primary)' : 'var(--color-border-subtle)'}`,
                      borderRadius: 'var(--radius-sm)',
                      background: isSelected ? 'var(--color-bg-secondary)' : 'transparent',
                    }}>
                      <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', cursor: 'pointer' }}>
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
                        <div>
                          <div style={{ fontWeight: 600, fontSize: '0.8125rem' }}>{builtin.name}</div>
                          <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>{builtin.description}</div>
                        </div>
                      </label>
                      {isSelected && builtin.params.length > 0 && (
                        <div style={{ marginTop: 'var(--spacing-sm)', paddingLeft: 'var(--spacing-lg)' }}>
                          {builtin.params.map(param => (
                            <div key={param.key} style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', marginBottom: 'var(--spacing-xs)' }}>
                              <label style={{ fontSize: '0.75rem', minWidth: '80px' }}>{param.label}:</label>
                              <input
                                type="text"
                                className="input"
                                style={{ width: '100px', fontSize: '0.8125rem', padding: '2px 6px' }}
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

              {/* Custom inline reward functions */}
              {rewardFunctions.filter(rf => rf.type === 'inline').map((rf, idx) => (
                <div key={`inline-${idx}`} style={{
                  padding: 'var(--spacing-sm) var(--spacing-md)',
                  border: '1px solid var(--color-primary)',
                  borderRadius: 'var(--radius-sm)',
                  background: 'var(--color-bg-secondary)',
                  marginBottom: 'var(--spacing-sm)',
                }}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-xs)' }}>
                    <span style={{ fontWeight: 600, fontSize: '0.8125rem' }}>
                      <i className="fas fa-code" style={{ marginRight: 'var(--spacing-xs)' }} />
                      {rf.name}
                    </span>
                    <button type="button" className="btn btn-danger" style={{ padding: '2px 6px', fontSize: '0.75rem' }}
                      onClick={() => setRewardFunctions(prev => prev.filter((_, i) => i !== rewardFunctions.indexOf(rf)))}>
                      <i className="fas fa-times" />
                    </button>
                  </div>
                  <pre style={{ fontSize: '0.75rem', margin: 0, whiteSpace: 'pre-wrap', color: 'var(--color-text-muted)' }}>
                    {rf.code}
                  </pre>
                </div>
              ))}

              {/* Add custom reward button / form */}
              {showAddCustomReward ? (
                <div style={{
                  padding: 'var(--spacing-md)',
                  border: '1px dashed var(--color-border)',
                  borderRadius: 'var(--radius-sm)',
                  marginTop: 'var(--spacing-sm)',
                }}>
                  <div style={{ marginBottom: 'var(--spacing-sm)' }}>
                    <label className="form-label">Function Name</label>
                    <input type="text" className="input" value={customRewardName} onChange={e => setCustomRewardName(e.target.value)}
                      placeholder="e.g. my_custom_reward" style={{ maxWidth: '300px' }} />
                  </div>
                  <div style={{ marginBottom: 'var(--spacing-sm)' }}>
                    <label className="form-label">
                      Function Body
                      <span style={{ fontWeight: 'normal', fontSize: '0.75rem', color: 'var(--color-text-muted)', marginLeft: 'var(--spacing-sm)' }}>
                        (receives: completions, **kwargs; must return list[float]; available: re, math, json, string)
                      </span>
                    </label>
                    <textarea
                      className="input"
                      value={customRewardCode}
                      onChange={e => setCustomRewardCode(e.target.value)}
                      placeholder={"return [1.0 if '<think>' in c else 0.0 for c in completions]"}
                      rows={4}
                      style={{ fontFamily: 'monospace', fontSize: '0.8125rem' }}
                    />
                  </div>
                  <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
                    <button type="button" className="btn btn-primary" style={{ fontSize: '0.8125rem' }}
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
                      }}>
                      <i className="fas fa-plus" style={{ marginRight: 'var(--spacing-xs)' }} /> Add
                    </button>
                    <button type="button" className="btn" style={{ fontSize: '0.8125rem' }}
                      onClick={() => { setShowAddCustomReward(false); setCustomRewardName(''); setCustomRewardCode('') }}>
                      Cancel
                    </button>
                  </div>
                </div>
              ) : (
                <button type="button" className="btn" onClick={() => setShowAddCustomReward(true)} style={{ fontSize: '0.8125rem' }}>
                  <i className="fas fa-plus" style={{ marginRight: 'var(--spacing-xs)' }} />
                  Add Custom Reward Function
                </button>
              )}
            </FormSection>
          )}

          <FormSection icon="fas fa-sliders-h" title="Hyperparameters">
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(140px, 1fr))', gap: 'var(--spacing-md)' }}>
              <div>
                <label className="form-label">Epochs</label>
                <input type="number" value={numEpochs} onChange={e => setNumEpochs(Number(e.target.value))} className="input" min={1} />
              </div>
              <div>
                <label className="form-label">Batch Size</label>
                <input type="number" value={batchSize} onChange={e => setBatchSize(Number(e.target.value))} className="input" min={1} />
              </div>
              <div>
                <label className="form-label">Learning Rate</label>
                <input type="text" value={learningRateText} onChange={e => {
                  setLearningRateText(e.target.value)
                  const parsed = Number(e.target.value)
                  if (!isNaN(parsed) && parsed > 0) setLearningRate(parsed)
                }} className="input" placeholder="e.g. 5e-5 or 0.00005" />
              </div>
              <div>
                <label className="form-label">Grad Accum Steps</label>
                <input type="number" value={gradAccum} onChange={e => setGradAccum(Number(e.target.value))} className="input" min={1} />
              </div>
              <div>
                <label className="form-label">Warmup Steps</label>
                <input type="number" value={warmupSteps} onChange={e => setWarmupSteps(Number(e.target.value))} className="input" min={0} />
              </div>
              <div>
                <label className="form-label">Max Seq Length</label>
                <input type="number" value={maxSeqLength} onChange={e => setMaxSeqLength(Number(e.target.value))} className="input" min={64} />
              </div>
              <div>
                <label className="form-label">Optimizer</label>
                <select value={optimizer} onChange={e => setOptimizer(e.target.value)} className="input">
                  {OPTIMIZERS.map(o => <option key={o} value={o}>{o}</option>)}
                </select>
              </div>
              <div style={{ display: 'flex', alignItems: 'end' }}>
                <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', cursor: 'pointer' }}>
                  <input type="checkbox" checked={gradCheckpointing} onChange={e => setGradCheckpointing(e.target.checked)} />
                  <span style={{ fontSize: '0.875rem' }}>Grad Checkpointing</span>
                </label>
              </div>
            </div>
          </FormSection>

          {/* Collapsible advanced section */}
          <div style={{ marginBottom: 'var(--spacing-lg)' }}>
            <button
              type="button"
              onClick={() => setShowAdvanced(!showAdvanced)}
              style={{
                background: 'none', border: 'none', cursor: 'pointer', padding: 0,
                fontSize: '0.8125rem', fontWeight: 600, textTransform: 'uppercase',
                letterSpacing: '0.05em', color: 'var(--color-text-secondary)',
                display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)',
                marginBottom: showAdvanced ? 'var(--spacing-md)' : 0,
                paddingBottom: 'var(--spacing-sm)',
                borderBottom: '1px solid var(--color-border-subtle)',
                width: '100%', fontFamily: 'inherit',
              }}
            >
              <i className={`fas fa-chevron-${showAdvanced ? 'down' : 'right'}`} style={{ color: 'var(--color-primary)', fontSize: '0.75rem', width: '0.75rem' }} />
              <i className="fas fa-cog" style={{ color: 'var(--color-primary)' }} />
              Advanced Options
            </button>

            {showAdvanced && (
              <div>
                <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(160px, 1fr))', gap: 'var(--spacing-md)', marginBottom: 'var(--spacing-md)' }}>
                  <div>
                    <label className="form-label">Max Steps (0 = auto)</label>
                    <input type="number" value={maxSteps} onChange={e => setMaxSteps(Number(e.target.value))} className="input" min={0} />
                  </div>
                  <div>
                    <label className="form-label">Save Steps</label>
                    <input type="number" value={saveSteps} onChange={e => setSaveSteps(Number(e.target.value))} className="input" min={0} />
                  </div>
                  <div>
                    <label className="form-label">Save Total Limit (0 = unlimited)</label>
                    <input type="number" value={saveTotalLimit} onChange={e => setSaveTotalLimit(Number(e.target.value))} className="input" min={0} />
                  </div>
                  <div>
                    <label className="form-label">Weight Decay</label>
                    <input type="number" value={weightDecay} onChange={e => setWeightDecay(Number(e.target.value))} className="input" min={0} step={0.01} />
                  </div>
                  <div>
                    <label className="form-label">Seed (0 = random)</label>
                    <input type="number" value={seed} onChange={e => setSeed(Number(e.target.value))} className="input" min={0} />
                  </div>
                  <div>
                    <label className="form-label">Mixed Precision</label>
                    <select value={mixedPrecision} onChange={e => setMixedPrecision(e.target.value)} className="input">
                      {MIXED_PRECISION_OPTS.map(o => <option key={o} value={o}>{o || 'Auto'}</option>)}
                    </select>
                  </div>
                </div>

                <div style={{ marginBottom: 'var(--spacing-md)' }}>
                  <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', cursor: 'pointer', marginBottom: 'var(--spacing-sm)' }}>
                    <div
                      onClick={() => setEvalEnabled(!evalEnabled)}
                      style={{
                        width: 36, height: 20, borderRadius: 10, position: 'relative',
                        background: evalEnabled ? 'var(--color-primary)' : 'var(--color-border)',
                        transition: 'background 0.2s', cursor: 'pointer', flexShrink: 0,
                      }}
                    >
                      <div style={{
                        width: 16, height: 16, borderRadius: '50%', background: '#fff',
                        position: 'absolute', top: 2, left: evalEnabled ? 18 : 2,
                        transition: 'left 0.2s', boxShadow: '0 1px 2px rgba(0,0,0,0.2)',
                      }} />
                    </div>
                    <span style={{ fontSize: '0.875rem', fontWeight: 600 }}>Enable Evaluation</span>
                  </label>
                  {evalEnabled && (
                    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(160px, 1fr))', gap: 'var(--spacing-md)', paddingLeft: 'var(--spacing-sm)' }}>
                      <div>
                        <label className="form-label">Eval Strategy</label>
                        <select value={evalStrategy} onChange={e => setEvalStrategy(e.target.value)} className="input">
                          <option value="steps">Steps</option>
                          <option value="epoch">Epoch</option>
                        </select>
                      </div>
                      <div>
                        <label className="form-label">Eval Steps (0 = same as save)</label>
                        <input type="number" value={evalSteps} onChange={e => setEvalSteps(Number(e.target.value))} className="input" min={0} />
                      </div>
                      <div>
                        <label className="form-label">Eval Split</label>
                        <input type="text" value={evalSplit} onChange={e => setEvalSplit(e.target.value)} placeholder="e.g. validation" className="input" />
                      </div>
                      <div>
                        <label className="form-label">Eval Dataset Source</label>
                        <input type="text" value={evalDatasetSource} onChange={e => setEvalDatasetSource(e.target.value)} placeholder="Separate HF dataset" className="input" />
                      </div>
                      <div>
                        <label className="form-label">Auto-split Ratio</label>
                        <input type="number" value={evalSplitRatio} onChange={e => setEvalSplitRatio(Number(e.target.value))} className="input" min={0.01} max={0.5} step={0.01} />
                      </div>
                    </div>
                  )}
                </div>

                {resumeFromCheckpoint && (
                  <div style={{ marginBottom: 'var(--spacing-md)' }}>
                    <label className="form-label">Resume from Checkpoint</label>
                    <div style={{ display: 'flex', gap: 'var(--spacing-sm)', alignItems: 'center' }}>
                      <input type="text" value={resumeFromCheckpoint} onChange={e => setResumeFromCheckpoint(e.target.value)} className="input" style={{ flex: 1 }} />
                      <button type="button" className="btn" style={{ padding: 'var(--spacing-xs) var(--spacing-sm)' }} onClick={() => setResumeFromCheckpoint('')}>
                        <i className="fas fa-times" />
                      </button>
                    </div>
                  </div>
                )}

                <div>
                  <label className="form-label">Extra Options (backend-specific key-value pairs)</label>
                  <KeyValueEditor entries={extraOptions} onChange={setExtraOptions} />
                </div>
              </div>
            )}
          </div>

          <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
            <button type="submit" className="btn btn-primary" disabled={loading || (!datasetSource && !datasetFile)}>
              {loading ? <><LoadingSpinner size="sm" /> Starting...</> :
                resumeFromCheckpoint ?
                  <><i className="fas fa-redo" style={{ marginRight: 'var(--spacing-xs)' }} /> Resume Training</> :
                  <><i className="fas fa-play" style={{ marginRight: 'var(--spacing-xs)' }} /> Start Fine-Tuning</>}
            </button>
            <button type="button" className="btn" onClick={handleExportConfig}>
              <i className="fas fa-download" style={{ marginRight: 'var(--spacing-xs)' }} /> Export Config
            </button>
          </div>
        </form>
      )}

      {/* Either show job detail OR job list — not side-by-side */}
      {selectedJob ? (
        <div>
          <button className="btn" onClick={() => setSelectedJob(null)} style={{ marginBottom: 'var(--spacing-md)' }}>
            <i className="fas fa-arrow-left" style={{ marginRight: 'var(--spacing-xs)' }} />
            Back to Jobs
          </button>
          <div className="card" style={{ marginBottom: 'var(--spacing-md)', padding: 'var(--spacing-md)' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <div>
                <h3 style={{ margin: 0 }}>{selectedJob.model}</h3>
                <div style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)', marginTop: 'var(--spacing-xs)' }}>
                  {selectedJob.backend} / {selectedJob.training_method || 'sft'} | ID: {selectedJob.id?.slice(0, 8)}... | {selectedJob.created_at}
                </div>
              </div>
              <span className={`badge ${statusBadgeClass[selectedJob.status] || ''}`}>
                {selectedJob.status}
              </span>
            </div>
          </div>
          <TrainingMonitor job={selectedJob} onStop={handleStop} />
          <CheckpointsPanel job={selectedJob} onResume={handleResumeFromCheckpoint} onExportCheckpoint={handleExportCheckpoint} />
          <ExportPanel job={selectedJob} prefilledCheckpoint={exportCheckpoint} />
        </div>
      ) : (
        <div>
          <h3 style={{ margin: '0 0 var(--spacing-sm) 0' }}>Jobs</h3>
          {jobs.length === 0 ? (
            <div className="empty-state">
              <div className="empty-state-icon"><i className="fas fa-graduation-cap" /></div>
              <h2 className="empty-state-title">No fine-tuning jobs yet</h2>
              <p className="empty-state-text">Click "New Job" to get started</p>
            </div>
          ) : (
            jobs.map(job => (
              <JobCard key={job.id} job={job} isSelected={false} onSelect={setSelectedJob} onUseConfig={handleUseConfig} onDelete={handleDelete} />
            ))
          )}
        </div>
      )}
    </div>
  )
}
