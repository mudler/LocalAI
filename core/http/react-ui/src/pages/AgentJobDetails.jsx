import { useState, useEffect, useRef } from 'react'
import { useParams, useNavigate, useOutletContext } from 'react-router-dom'
import { agentJobsApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'
import PageHeader from '../components/PageHeader'

const traceColors = {
  reasoning: { bg: 'rgba(99,102,241,0.1)', border: 'rgba(99,102,241,0.3)', icon: 'fa-brain', color: 'var(--color-primary)' },
  tool_call: { bg: 'rgba(139,92,246,0.1)', border: 'rgba(139,92,246,0.3)', icon: 'fa-wrench', color: 'var(--color-accent)' },
  tool_result: { bg: 'rgba(34,197,94,0.1)', border: 'rgba(34,197,94,0.3)', icon: 'fa-check', color: 'var(--color-success)' },
  status: { bg: 'rgba(245,158,11,0.1)', border: 'rgba(245,158,11,0.3)', icon: 'fa-info-circle', color: 'var(--color-warning)' },
  stream_reasoning: { bg: 'rgba(99,102,241,0.06)', border: 'rgba(99,102,241,0.2)', icon: 'fa-lightbulb', color: 'var(--color-primary)' },
  stream_content: { bg: 'rgba(59,130,246,0.08)', border: 'rgba(59,130,246,0.25)', icon: 'fa-pen-nib', color: 'var(--color-info, #3b82f6)' },
  stream_tool_call: { bg: 'rgba(139,92,246,0.06)', border: 'rgba(139,92,246,0.2)', icon: 'fa-bolt', color: 'var(--color-accent)' },
}

function TraceCard({ trace, index }) {
  const [expanded, setExpanded] = useState(true)
  const style = traceColors[trace.type] || traceColors.status

  return (
    <div className="ajd-trace mb-sm" style={{ background: style.bg, border: `1px solid ${style.border}` }}>
      <button
        onClick={() => setExpanded(!expanded)}
        className="ajd-trace__toggle"
      >
        <div className="hstack">
          <span className="ajd-trace__index">
            {index + 1}
          </span>
          <i className={`fas ${style.icon} text-base`} style={{ color: style.color }} />
          <span className="badge text-xs" style={{ background: style.border, color: style.color }}>
            {trace.type || 'unknown'}
          </span>
          {trace.tool_name && (
            <span className="text-mono text-xs text-secondary">
              {trace.tool_name}
            </span>
          )}
          {trace.timestamp && (
            <span className="text-meta">
              {new Date(trace.timestamp).toLocaleTimeString()}
            </span>
          )}
        </div>
        <i className={`fas fa-chevron-${expanded ? 'up' : 'down'} text-meta`} />
      </button>
      {expanded && (
        <div className="ajd-trace__body">
          {trace.content && (
            <pre className="ajd-pre">
              {trace.content}
            </pre>
          )}
          {trace.arguments && (
            <div className="mt-xs">
              <span className="text-meta fw-semibold">Arguments:</span>
              <pre className="ajd-pre ajd-pre--nested">
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
      <span className={`badge ${m.cls} badge--md`}>
        <i className={`fas ${m.icon} icon-before`} /> {status || 'unknown'}
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

  if (loading) return <div className="page page--narrow loading-center"><LoadingSpinner size="lg" /></div>
  if (!job) return (
    <div className="page page--narrow">
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
    <div className="page page--narrow">
      <PageHeader
        title="Job Details"
        supporting="Live status and reasoning traces"
        actions={
          <div className="hstack">
            <button className="btn btn-secondary" onClick={() => navigate('/app/agent-jobs')}>
              <i className="fas fa-arrow-left" aria-hidden="true" /> Back
            </button>
            {(job.status === 'running' || job.status === 'pending') && (
              <button className="btn btn-danger" onClick={handleCancel}>
                <i className="fas fa-ban" aria-hidden="true" /> Cancel
              </button>
            )}
          </div>
        }
      />

      {/* Status Card */}
      <div className="card mb-md">
        <div className="hstack hstack--between mb-md">
          <h3 className="fw-semibold">
            <i className="fas fa-circle-info text-primary icon-before" />
            Job Status
          </h3>
          {statusBadge(job.status)}
        </div>
        <div className="grid-3">
          <div>
            <span className="form-label">Job ID</span>
            <p className="cell-mono wrap-anywhere">{job.id}</p>
          </div>
          <div>
            <span className="form-label">Task</span>
            <p>
              {job.task_id ? (
                <a onClick={() => navigate(`/app/agent-jobs/tasks/${job.task_id}`)} className="link-plain">
                  {job.task_id}
                </a>
              ) : '-'}
            </p>
          </div>
          <div>
            <span className="form-label">Triggered By</span>
            <p className="text-base">{job.triggered_by || 'manual'}</p>
          </div>
          <div>
            <span className="form-label">Created</span>
            <p className="text-sub">{formatDate(job.created_at)}</p>
          </div>
          <div>
            <span className="form-label">Started</span>
            <p className="text-sub">{formatDate(job.started_at)}</p>
          </div>
          <div>
            <span className="form-label">Completed</span>
            <p className="text-sub">{formatDate(job.completed_at)}</p>
          </div>
        </div>
      </div>

      {/* Prompt Template */}
      {task?.prompt && (
        <div className="card mb-md">
          <h3 className="group-label group-label--tight">
            <i className="fas fa-file-lines text-accent icon-before" />
            Agent Prompt Template
          </h3>
          <pre className="ajd-code">
            {task.prompt}
          </pre>
        </div>
      )}

      {/* Cron Parameters */}
      {job.triggered_by === 'cron' && job.cron_parameters && Object.keys(job.cron_parameters).length > 0 && (
        <div className="card mb-md">
          <h3 className="group-label group-label--tight">
            <i className="fas fa-clock text-warning icon-before" />
            Cron Parameters
          </h3>
          <div className="hstack hstack--xs">
            {Object.entries(job.cron_parameters).map(([k, v]) => (
              <span key={k} className="badge badge-info text-mono text-xs">
                {k}={v}
              </span>
            ))}
          </div>
        </div>
      )}

      {/* Job Parameters */}
      {job.parameters && Object.keys(job.parameters).length > 0 && (
        <div className="card mb-md">
          <h3 className="group-label group-label--tight">
            <i className="fas fa-sliders-h text-primary icon-before" />
            Job Parameters
          </h3>
          <div className="hstack hstack--xs">
            {Object.entries(job.parameters).map(([k, v]) => (
              <span key={k} className="badge badge-info text-mono text-xs">
                {k}={v}
              </span>
            ))}
          </div>
        </div>
      )}

      {/* Rendered Prompt */}
      {renderedPrompt && renderedPrompt !== task?.prompt && (
        <div className="card mb-md">
          <h3 className="group-label group-label--tight">
            <i className="fas fa-spell-check text-success icon-before" />
            Rendered Prompt
          </h3>
          <pre className="ajd-code ajd-code--tall">
            {renderedPrompt}
          </pre>
        </div>
      )}

      {/* Result */}
      {job.result && (
        <div className="card mb-md">
          <h3 className="group-label group-label--tight">
            <i className="fas fa-check-circle text-success icon-before" />
            Result
          </h3>
          <pre className="ajd-code ajd-code--taller">
            {typeof job.result === 'string' ? job.result : JSON.stringify(job.result, null, 2)}
          </pre>
        </div>
      )}

      {/* Error */}
      {job.error && (
        <div className="card mb-md" style={{ borderColor: 'var(--color-error)' }}>
          <h3 className="fw-semibold text-error mb-sm">
            <i className="fas fa-exclamation-triangle icon-before" />
            Error
          </h3>
          <pre className="ajd-code ajd-code--error">
            {typeof job.error === 'string' ? job.error : JSON.stringify(job.error, null, 2)}
          </pre>
        </div>
      )}

      {/* Execution Traces */}
      {traces.length > 0 && (
        <div className="card mb-md">
          <h3 className="group-label">
            <i className="fas fa-wave-square text-accent icon-before" />
            Execution Traces ({traces.length} steps)
          </h3>
          {traces.map((trace, i) => (
            <TraceCard key={i} trace={trace} index={i} />
          ))}
        </div>
      )}

      {/* Running indicator */}
      {(job.status === 'running' || job.status === 'pending') && (
        <div className="ajd-polling">
          <i className="fas fa-spinner fa-spin icon-before" />
          Polling for updates every 2 seconds...
        </div>
      )}

      {/* Webhook Status */}
      {(job.webhook_sent !== undefined || job.webhook_error) && (
        <div className="card">
          <h3 className="group-label">
            <i className="fas fa-globe text-primary icon-before" />
            Webhook Status
          </h3>
          <div className="ajd-webhook">
            {job.webhook_sent ? (
              <>
                <span className="badge badge-success"><i className="fas fa-check" /> Delivered</span>
                {job.webhook_sent_at && (
                  <span className="text-meta">
                    at {formatDate(job.webhook_sent_at)}
                  </span>
                )}
              </>
            ) : job.webhook_error ? (
              <>
                <span className="badge badge-error"><i className="fas fa-xmark" /> Failed</span>
                <span className="text-xs text-error">{job.webhook_error}</span>
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
