import { useState, useEffect, useRef } from 'react'
import { useParams, useNavigate, useOutletContext } from 'react-router-dom'
import { agentJobsApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'

const traceColors = {
  reasoning: { bg: 'rgba(99,102,241,0.1)', border: 'rgba(99,102,241,0.3)', icon: 'fa-brain', color: 'var(--color-primary)' },
  tool_call: { bg: 'rgba(139,92,246,0.1)', border: 'rgba(139,92,246,0.3)', icon: 'fa-wrench', color: 'var(--color-accent)' },
  tool_result: { bg: 'rgba(34,197,94,0.1)', border: 'rgba(34,197,94,0.3)', icon: 'fa-check', color: 'var(--color-success)' },
  status: { bg: 'rgba(245,158,11,0.1)', border: 'rgba(245,158,11,0.3)', icon: 'fa-info-circle', color: 'var(--color-warning)' },
}

function TraceCard({ trace, index }) {
  const [expanded, setExpanded] = useState(true)
  const style = traceColors[trace.type] || traceColors.status

  return (
    <div style={{
      background: style.bg, border: `1px solid ${style.border}`,
      borderRadius: 'var(--radius-md)', marginBottom: 'var(--spacing-sm)', overflow: 'hidden',
    }}>
      <button
        onClick={() => setExpanded(!expanded)}
        style={{
          width: '100%', display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          padding: 'var(--spacing-sm) var(--spacing-md)', background: 'none', border: 'none',
          cursor: 'pointer', color: 'var(--color-text-primary)', textAlign: 'left',
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
          <span style={{
            fontSize: '0.6875rem', fontWeight: 700, color: 'var(--color-text-muted)',
            background: 'var(--color-bg-secondary)', borderRadius: 'var(--radius-sm)',
            padding: '2px 6px', minWidth: 24, textAlign: 'center',
          }}>
            {index + 1}
          </span>
          <i className={`fas ${style.icon}`} style={{ color: style.color, fontSize: '0.875rem' }} />
          <span className="badge" style={{ background: style.border, color: style.color, fontSize: '0.6875rem' }}>
            {trace.type || 'unknown'}
          </span>
          {trace.tool_name && (
            <span style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: '0.75rem', color: 'var(--color-text-secondary)' }}>
              {trace.tool_name}
            </span>
          )}
          {trace.timestamp && (
            <span style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
              {new Date(trace.timestamp).toLocaleTimeString()}
            </span>
          )}
        </div>
        <i className={`fas fa-chevron-${expanded ? 'up' : 'down'}`} style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)' }} />
      </button>
      {expanded && (
        <div style={{ padding: '0 var(--spacing-md) var(--spacing-sm)', fontSize: '0.8125rem' }}>
          {trace.content && (
            <pre style={{
              whiteSpace: 'pre-wrap', wordBreak: 'break-word', margin: 0,
              fontFamily: "'JetBrains Mono', monospace", fontSize: '0.75rem',
              color: 'var(--color-text-secondary)', lineHeight: 1.6,
            }}>
              {trace.content}
            </pre>
          )}
          {trace.arguments && (
            <div style={{ marginTop: 'var(--spacing-xs)' }}>
              <span style={{ fontSize: '0.6875rem', fontWeight: 600, color: 'var(--color-text-muted)' }}>Arguments:</span>
              <pre style={{
                whiteSpace: 'pre-wrap', wordBreak: 'break-word', margin: '4px 0 0',
                fontFamily: "'JetBrains Mono', monospace", fontSize: '0.75rem',
                color: 'var(--color-text-secondary)', lineHeight: 1.5,
              }}>
                {typeof trace.arguments === 'string' ? trace.arguments : JSON.stringify(trace.arguments, null, 2)}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

export default function AgentJobDetails() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const [job, setJob] = useState(null)
  const [task, setTask] = useState(null)
  const [loading, setLoading] = useState(true)
  const intervalRef = useRef(null)

  useEffect(() => {
    if (!id) return

    const fetchJob = async () => {
      try {
        const data = await agentJobsApi.getJob(id)
        setJob(data)

        // Fetch associated task data
        if (data?.task_id && !task) {
          agentJobsApi.getTask(data.task_id).then(setTask).catch(() => {})
        }

        // Stop polling when job is done
        if (data && data.status !== 'running' && data.status !== 'pending') {
          if (intervalRef.current) {
            clearInterval(intervalRef.current)
            intervalRef.current = null
          }
        }
      } catch (err) {
        addToast(`Failed to load job: ${err.message}`, 'error')
      } finally {
        setLoading(false)
      }
    }

    fetchJob()
    intervalRef.current = setInterval(fetchJob, 2000)
    return () => { if (intervalRef.current) clearInterval(intervalRef.current) }
  }, [id, addToast])

  const handleCancel = async () => {
    try {
      await agentJobsApi.cancelJob(id)
      addToast('Job cancelled', 'success')
    } catch (err) {
      addToast(`Cancel failed: ${err.message}`, 'error')
    }
  }

  const formatDate = (d) => d ? new Date(d).toLocaleString() : '-'

  const statusBadge = (status) => {
    const map = {
      pending: { cls: 'badge-warning', icon: 'fa-clock' },
      running: { cls: 'badge-info', icon: 'fa-spinner fa-spin' },
      completed: { cls: 'badge-success', icon: 'fa-check' },
      failed: { cls: 'badge-error', icon: 'fa-xmark' },
      cancelled: { cls: '', icon: 'fa-ban' },
    }
    const m = map[status] || { cls: '', icon: 'fa-question' }
    return (
      <span className={`badge ${m.cls}`} style={{ fontSize: '0.875rem', padding: '4px 12px' }}>
        <i className={`fas ${m.icon}`} style={{ marginRight: 4 }} /> {status || 'unknown'}
      </span>
    )
  }

  // Render the prompt with parameters substituted
  const renderPrompt = () => {
    if (!task?.prompt || !job?.parameters) return null
    let rendered = task.prompt
    Object.entries(job.parameters).forEach(([key, value]) => {
      rendered = rendered.replace(new RegExp(`\\{\\{\\s*\\.${key}\\s*\\}\\}`, 'g'), value)
    })
    return rendered
  }

  if (loading) return <div className="page" style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}><LoadingSpinner size="lg" /></div>
  if (!job) return (
    <div className="page">
      <div className="empty-state">
        <div className="empty-state-icon"><i className="fas fa-search" /></div>
        <h2 className="empty-state-title">Job not found</h2>
        <button className="btn btn-secondary" onClick={() => navigate('/app/agent-jobs')}><i className="fas fa-arrow-left" /> Back</button>
      </div>
    </div>
  )

  const renderedPrompt = renderPrompt()
  const traces = Array.isArray(job.traces) ? job.traces : []

  return (
    <div className="page" style={{ maxWidth: 900 }}>
      <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <div>
          <h1 className="page-title">Job Details</h1>
          <p className="page-subtitle">Live status and reasoning traces</p>
        </div>
        <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
          {(job.status === 'running' || job.status === 'pending') && (
            <button className="btn btn-danger" onClick={handleCancel}>
              <i className="fas fa-stop" /> Cancel
            </button>
          )}
          <button className="btn btn-secondary" onClick={() => navigate('/app/agent-jobs')}>
            <i className="fas fa-arrow-left" /> Back
          </button>
        </div>
      </div>

      {/* Status Card */}
      <div className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-md)' }}>
          <h3 style={{ fontWeight: 600 }}>
            <i className="fas fa-circle-info" style={{ color: 'var(--color-primary)', marginRight: 'var(--spacing-xs)' }} />
            Job Status
          </h3>
          {statusBadge(job.status)}
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 'var(--spacing-md)' }}>
          <div>
            <span className="form-label">Job ID</span>
            <p style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: '0.8125rem', wordBreak: 'break-all' }}>{job.id}</p>
          </div>
          <div>
            <span className="form-label">Task</span>
            <p>
              {job.task_id ? (
                <a onClick={() => navigate(`/app/agent-jobs/tasks/${job.task_id}`)} style={{ cursor: 'pointer', color: 'var(--color-primary)' }}>
                  {job.task_id}
                </a>
              ) : '-'}
            </p>
          </div>
          <div>
            <span className="form-label">Triggered By</span>
            <p style={{ fontSize: '0.875rem' }}>{job.triggered_by || 'manual'}</p>
          </div>
          <div>
            <span className="form-label">Created</span>
            <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>{formatDate(job.created_at)}</p>
          </div>
          <div>
            <span className="form-label">Started</span>
            <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>{formatDate(job.started_at)}</p>
          </div>
          <div>
            <span className="form-label">Completed</span>
            <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>{formatDate(job.completed_at)}</p>
          </div>
        </div>
      </div>

      {/* Prompt Template */}
      {task?.prompt && (
        <div className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
          <h3 style={{ fontWeight: 600, marginBottom: 'var(--spacing-sm)' }}>
            <i className="fas fa-file-lines" style={{ color: 'var(--color-accent)', marginRight: 'var(--spacing-xs)' }} />
            Agent Prompt Template
          </h3>
          <pre style={{
            background: 'var(--color-bg-primary)', padding: 'var(--spacing-sm)',
            borderRadius: 'var(--radius-md)', fontSize: '0.8125rem',
            whiteSpace: 'pre-wrap', overflow: 'auto', maxHeight: 200,
          }}>
            {task.prompt}
          </pre>
        </div>
      )}

      {/* Cron Parameters */}
      {job.triggered_by === 'cron' && job.cron_parameters && Object.keys(job.cron_parameters).length > 0 && (
        <div className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
          <h3 style={{ fontWeight: 600, marginBottom: 'var(--spacing-sm)' }}>
            <i className="fas fa-clock" style={{ color: 'var(--color-warning)', marginRight: 'var(--spacing-xs)' }} />
            Cron Parameters
          </h3>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--spacing-xs)' }}>
            {Object.entries(job.cron_parameters).map(([k, v]) => (
              <span key={k} className="badge badge-info" style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: '0.75rem' }}>
                {k}={v}
              </span>
            ))}
          </div>
        </div>
      )}

      {/* Job Parameters */}
      {job.parameters && Object.keys(job.parameters).length > 0 && (
        <div className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
          <h3 style={{ fontWeight: 600, marginBottom: 'var(--spacing-sm)' }}>
            <i className="fas fa-sliders-h" style={{ color: 'var(--color-primary)', marginRight: 'var(--spacing-xs)' }} />
            Job Parameters
          </h3>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--spacing-xs)' }}>
            {Object.entries(job.parameters).map(([k, v]) => (
              <span key={k} className="badge badge-info" style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: '0.75rem' }}>
                {k}={v}
              </span>
            ))}
          </div>
        </div>
      )}

      {/* Rendered Prompt */}
      {renderedPrompt && renderedPrompt !== task?.prompt && (
        <div className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
          <h3 style={{ fontWeight: 600, marginBottom: 'var(--spacing-sm)' }}>
            <i className="fas fa-spell-check" style={{ color: 'var(--color-success)', marginRight: 'var(--spacing-xs)' }} />
            Rendered Prompt
          </h3>
          <pre style={{
            background: 'var(--color-bg-primary)', padding: 'var(--spacing-sm)',
            borderRadius: 'var(--radius-md)', fontSize: '0.8125rem',
            whiteSpace: 'pre-wrap', overflow: 'auto', maxHeight: 300,
          }}>
            {renderedPrompt}
          </pre>
        </div>
      )}

      {/* Result */}
      {job.result && (
        <div className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
          <h3 style={{ fontWeight: 600, marginBottom: 'var(--spacing-sm)' }}>
            <i className="fas fa-check-circle" style={{ color: 'var(--color-success)', marginRight: 'var(--spacing-xs)' }} />
            Result
          </h3>
          <pre style={{
            background: 'var(--color-bg-primary)', padding: 'var(--spacing-sm)',
            borderRadius: 'var(--radius-md)', fontSize: '0.8125rem',
            whiteSpace: 'pre-wrap', overflow: 'auto', maxHeight: 500,
          }}>
            {typeof job.result === 'string' ? job.result : JSON.stringify(job.result, null, 2)}
          </pre>
        </div>
      )}

      {/* Error */}
      {job.error && (
        <div className="card" style={{ marginBottom: 'var(--spacing-md)', borderColor: 'var(--color-error)' }}>
          <h3 style={{ fontWeight: 600, marginBottom: 'var(--spacing-sm)', color: 'var(--color-error)' }}>
            <i className="fas fa-exclamation-triangle" style={{ marginRight: 'var(--spacing-xs)' }} />
            Error
          </h3>
          <pre style={{
            background: 'rgba(239,68,68,0.05)', padding: 'var(--spacing-sm)',
            borderRadius: 'var(--radius-md)', fontSize: '0.8125rem',
            whiteSpace: 'pre-wrap', overflow: 'auto', color: 'var(--color-error)',
          }}>
            {typeof job.error === 'string' ? job.error : JSON.stringify(job.error, null, 2)}
          </pre>
        </div>
      )}

      {/* Execution Traces */}
      {traces.length > 0 && (
        <div className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
          <h3 style={{ fontWeight: 600, marginBottom: 'var(--spacing-md)' }}>
            <i className="fas fa-wave-square" style={{ color: 'var(--color-accent)', marginRight: 'var(--spacing-xs)' }} />
            Execution Traces ({traces.length} steps)
          </h3>
          {traces.map((trace, i) => (
            <TraceCard key={i} trace={trace} index={i} />
          ))}
        </div>
      )}

      {/* Running indicator */}
      {(job.status === 'running' || job.status === 'pending') && (
        <div style={{
          textAlign: 'center', padding: 'var(--spacing-md)',
          color: 'var(--color-text-muted)', fontSize: '0.8125rem',
        }}>
          <i className="fas fa-spinner fa-spin" style={{ marginRight: 'var(--spacing-xs)' }} />
          Polling for updates every 2 seconds...
        </div>
      )}

      {/* Webhook Status */}
      {(job.webhook_sent !== undefined || job.webhook_error) && (
        <div className="card">
          <h3 style={{ fontWeight: 600, marginBottom: 'var(--spacing-md)' }}>
            <i className="fas fa-globe" style={{ color: 'var(--color-primary)', marginRight: 'var(--spacing-xs)' }} />
            Webhook Status
          </h3>
          <div style={{
            display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)',
            background: 'var(--color-bg-primary)', borderRadius: 'var(--radius-md)',
            padding: 'var(--spacing-sm)',
          }}>
            {job.webhook_sent ? (
              <>
                <span className="badge badge-success"><i className="fas fa-check" /> Delivered</span>
                {job.webhook_sent_at && (
                  <span style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>
                    at {formatDate(job.webhook_sent_at)}
                  </span>
                )}
              </>
            ) : job.webhook_error ? (
              <>
                <span className="badge badge-error"><i className="fas fa-xmark" /> Failed</span>
                <span style={{ fontSize: '0.75rem', color: 'var(--color-error)' }}>{job.webhook_error}</span>
              </>
            ) : (
              <span className="badge badge-warning"><i className="fas fa-clock" /> Pending</span>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
