import { useState, useEffect, useCallback } from 'react'
import { useParams, useNavigate, useOutletContext, useLocation } from 'react-router-dom'
import { agentJobsApi } from '../utils/api'
import { basePath } from '../utils/basePath'
import ModelSelector from '../components/ModelSelector'
import { CAP_CHAT } from '../utils/capabilities'
import LoadingSpinner from '../components/LoadingSpinner'

export default function AgentTaskDetails() {
  const { id } = useParams()
  const navigate = useNavigate()
  const location = useLocation()
  const { addToast } = useOutletContext()
  const isNew = !id || location.pathname.endsWith('/new')
  const isEdit = location.pathname.endsWith('/edit')

  const [task, setTask] = useState({
    name: '', description: '', model: '', prompt: '', context: '',
    enabled: true, cron: '', cron_parameters: '',
    webhooks: [], multimedia_sources: [],
  })
  const [loading, setLoading] = useState(!isNew)
  const [saving, setSaving] = useState(false)
  const [jobHistory, setJobHistory] = useState([])
  const [cronError, setCronError] = useState('')

  useEffect(() => {
    if (!isNew && id) {
      agentJobsApi.getTask(id).then(data => {
        if (data) {
          setTask({
            name: data.name || '',
            description: data.description || '',
            model: data.model || '',
            prompt: data.prompt || '',
            context: data.context || '',
            enabled: data.enabled !== false,
            cron: data.cron || '',
            cron_parameters: typeof data.cron_parameters === 'object'
              ? Object.entries(data.cron_parameters).map(([k, v]) => `${k}=${v}`).join('\n')
              : (data.cron_parameters || ''),
            webhooks: Array.isArray(data.webhooks) ? data.webhooks : [],
            multimedia_sources: Array.isArray(data.multimedia_sources) ? data.multimedia_sources : [],
          })
        }
        setLoading(false)
      }).catch(err => {
        addToast(`Failed to load task: ${err.message}`, 'error')
        setLoading(false)
      })

      // Fetch job history for this task
      agentJobsApi.listJobs().then(jobs => {
        if (Array.isArray(jobs)) {
          setJobHistory(jobs.filter(j => j.task_id === id).slice(0, 20))
        }
      }).catch(() => {})
    }
  }, [id, isNew, addToast])

  const updateField = (field, value) => {
    setTask(prev => ({ ...prev, [field]: value }))
  }

  const validateCron = (expr) => {
    if (!expr) { setCronError(''); return }
    const parts = expr.trim().split(/\s+/)
    if (parts.length < 5 || parts.length > 6) {
      setCronError('Cron must have 5 or 6 fields (min hour day month weekday [year])')
    } else {
      setCronError('')
    }
  }

  // Webhook management
  const addWebhook = () => {
    updateField('webhooks', [...task.webhooks, { url: '', method: 'POST', headers: '{}', payload_template: '' }])
  }
  const updateWebhook = (i, field, value) => {
    const wh = [...task.webhooks]
    wh[i] = { ...wh[i], [field]: value }
    updateField('webhooks', wh)
  }
  const removeWebhook = (i) => {
    updateField('webhooks', task.webhooks.filter((_, idx) => idx !== i))
  }

  // Multimedia source management
  const addMultimediaSource = () => {
    updateField('multimedia_sources', [...task.multimedia_sources, { type: 'image', url: '', headers: '{}' }])
  }
  const updateMultimediaSource = (i, field, value) => {
    const ms = [...task.multimedia_sources]
    ms[i] = { ...ms[i], [field]: value }
    updateField('multimedia_sources', ms)
  }
  const removeMultimediaSource = (i) => {
    updateField('multimedia_sources', task.multimedia_sources.filter((_, idx) => idx !== i))
  }

  const handleSave = async (e) => {
    e.preventDefault()
    if (!task.name?.trim()) { addToast('Task name is required', 'warning'); return }
    if (cronError) { addToast('Fix cron expression errors first', 'warning'); return }

    setSaving(true)
    try {
      const body = { ...task }

      // Parse cron_parameters from key=value lines to object
      if (body.cron_parameters && typeof body.cron_parameters === 'string') {
        const params = {}
        body.cron_parameters.split('\n').forEach(line => {
          const [key, ...rest] = line.split('=')
          if (key?.trim() && rest.length > 0) {
            params[key.trim()] = rest.join('=').trim()
          }
        })
        body.cron_parameters = params
      } else if (!body.cron_parameters || body.cron_parameters === '') {
        delete body.cron_parameters
      }

      // Parse webhook headers from JSON strings
      if (body.webhooks) {
        body.webhooks = body.webhooks.map(wh => ({
          ...wh,
          headers: typeof wh.headers === 'string' ? JSON.parse(wh.headers || '{}') : wh.headers,
        }))
      }

      // Parse multimedia source headers
      if (body.multimedia_sources) {
        body.multimedia_sources = body.multimedia_sources.map(ms => ({
          ...ms,
          headers: typeof ms.headers === 'string' ? JSON.parse(ms.headers || '{}') : ms.headers,
        }))
      }

      if (isNew) {
        await agentJobsApi.createTask(body)
        addToast('Task created', 'success')
      } else {
        await agentJobsApi.updateTask(id, body)
        addToast('Task updated', 'success')
      }
      navigate('/app/agent-jobs')
    } catch (err) {
      addToast(`Save failed: ${err.message}`, 'error')
    } finally {
      setSaving(false)
    }
  }

  const statusBadge = (status) => {
    const cls = status === 'completed' ? 'badge-success' : status === 'failed' ? 'badge-error' : status === 'running' ? 'badge-info' : status === 'cancelled' ? '' : 'badge-warning'
    return <span className={`badge ${cls}`}>{status || 'unknown'}</span>
  }

  const formatDate = (d) => d ? new Date(d).toLocaleString() : '-'

  if (loading) return <div className="page page--narrow" style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}><LoadingSpinner size="lg" /></div>

  // View mode
  if (!isNew && !isEdit) {
    return (
      <div className="page page--narrow">
        <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <div>
            <h1 className="page-title">{task.name || 'Task Details'}</h1>
            {task.description && <p className="page-subtitle">{task.description}</p>}
          </div>
          <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
            <button className="btn btn-primary btn-sm" onClick={() => navigate(`/app/agent-jobs/tasks/${id}/edit`)}>
              <i className="fas fa-edit" /> Edit
            </button>
            <button className="btn btn-secondary btn-sm" onClick={() => navigate('/app/agent-jobs')}>
              <i className="fas fa-arrow-left" /> Back
            </button>
          </div>
        </div>

        {/* Task Info */}
        <div className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
          <h3 style={{ fontWeight: 600, marginBottom: 'var(--spacing-md)' }}>
            <i className="fas fa-info-circle" style={{ color: 'var(--color-primary)', marginRight: 'var(--spacing-xs)' }} />
            Task Information
          </h3>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--spacing-md)' }}>
            <div>
              <span className="form-label">Model</span>
              <p style={{ fontSize: '0.875rem' }}>{task.model || '-'}</p>
            </div>
            <div>
              <span className="form-label">Status</span>
              <p>{task.enabled !== false ? <span className="badge badge-success">Enabled</span> : <span className="badge">Disabled</span>}</p>
            </div>
            {task.cron && (
              <div>
                <span className="form-label">Cron Schedule</span>
                <p style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' }}>{task.cron}</p>
              </div>
            )}
          </div>
          {task.prompt && (
            <div style={{ marginTop: 'var(--spacing-md)' }}>
              <span className="form-label">Prompt Template</span>
              <pre style={{ background: 'var(--color-bg-primary)', padding: 'var(--spacing-sm)', borderRadius: 'var(--radius-md)', fontSize: '0.8125rem', whiteSpace: 'pre-wrap', overflow: 'auto', maxHeight: 300 }}>
                {task.prompt}
              </pre>
            </div>
          )}
          {task.context && (
            <div style={{ marginTop: 'var(--spacing-md)' }}>
              <span className="form-label">Context</span>
              <pre style={{ background: 'var(--color-bg-primary)', padding: 'var(--spacing-sm)', borderRadius: 'var(--radius-md)', fontSize: '0.8125rem', whiteSpace: 'pre-wrap' }}>
                {task.context}
              </pre>
            </div>
          )}
        </div>

        {/* API Usage Examples */}
        <div className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
          <h3 style={{ fontWeight: 600, marginBottom: 'var(--spacing-md)' }}>
            <i className="fas fa-code" style={{ color: 'var(--color-accent)', marginRight: 'var(--spacing-xs)' }} />
            API Usage
          </h3>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)' }}>
            <div>
              <span className="form-label">Execute by name</span>
              <pre style={{ background: 'var(--color-bg-primary)', padding: 'var(--spacing-sm)', borderRadius: 'var(--radius-md)', fontSize: '0.75rem', fontFamily: 'var(--font-mono)', whiteSpace: 'pre-wrap', overflow: 'auto' }}>
{`curl -X POST ${window.location.origin}${basePath}/api/agent/tasks/${encodeURIComponent(task.name)}/execute`}
              </pre>
            </div>
            <div>
              <span className="form-label">Execute with multimedia</span>
              <pre style={{ background: 'var(--color-bg-primary)', padding: 'var(--spacing-sm)', borderRadius: 'var(--radius-md)', fontSize: '0.75rem', fontFamily: 'var(--font-mono)', whiteSpace: 'pre-wrap', overflow: 'auto' }}>
{`curl -X POST ${window.location.origin}${basePath}/api/agent/tasks/${encodeURIComponent(task.name)}/execute \\
  -H "Content-Type: application/json" \\
  -d '{"multimedia": {"images": [{"url": "https://example.com/image.jpg"}]}}'`}
              </pre>
            </div>
            <div>
              <span className="form-label">Check job status</span>
              <pre style={{ background: 'var(--color-bg-primary)', padding: 'var(--spacing-sm)', borderRadius: 'var(--radius-md)', fontSize: '0.75rem', fontFamily: 'var(--font-mono)', whiteSpace: 'pre-wrap', overflow: 'auto' }}>
{`curl ${window.location.origin}${basePath}/api/agent/jobs/<job-id>`}
              </pre>
            </div>
          </div>
        </div>

        {/* Webhooks */}
        {task.webhooks && task.webhooks.length > 0 && (
          <div className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
            <h3 style={{ fontWeight: 600, marginBottom: 'var(--spacing-md)' }}>
              <i className="fas fa-globe" style={{ color: 'var(--color-success)', marginRight: 'var(--spacing-xs)' }} />
              Webhooks ({task.webhooks.length})
            </h3>
            {task.webhooks.map((wh, i) => (
              <div key={i} style={{ background: 'var(--color-bg-primary)', borderRadius: 'var(--radius-md)', padding: 'var(--spacing-sm)', marginBottom: 'var(--spacing-sm)' }}>
                <div style={{ display: 'flex', gap: 'var(--spacing-sm)', fontSize: '0.8125rem' }}>
                  <span className="badge badge-info">{wh.method || 'POST'}</span>
                  <span style={{ fontFamily: 'var(--font-mono)' }}>{wh.url}</span>
                </div>
              </div>
            ))}
          </div>
        )}

        {/* Job History */}
        {jobHistory.length > 0 && (
          <div className="card">
            <h3 style={{ fontWeight: 600, marginBottom: 'var(--spacing-md)' }}>
              <i className="fas fa-clock-rotate-left" style={{ color: 'var(--color-warning)', marginRight: 'var(--spacing-xs)' }} />
              Recent Jobs ({jobHistory.length})
            </h3>
            <div className="table-container">
              <table className="table">
                <thead>
                  <tr><th>Job ID</th><th>Status</th><th>Created</th><th>Actions</th></tr>
                </thead>
                <tbody>
                  {jobHistory.map(job => (
                    <tr key={job.id}>
                      <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' }}>
                        {job.id?.slice(0, 12)}...
                      </td>
                      <td>{statusBadge(job.status)}</td>
                      <td style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>{formatDate(job.created_at)}</td>
                      <td>
                        <button className="btn btn-secondary btn-sm" onClick={() => navigate(`/app/agent-jobs/jobs/${job.id}`)}>
                          <i className="fas fa-eye" /> View
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}
      </div>
    )
  }

  // Edit/Create form
  return (
    <div className="page page--narrow">
      <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h1 className="page-title">{isNew ? 'Create Task' : 'Edit Task'}</h1>
        <button className="btn btn-secondary btn-sm" onClick={() => navigate('/app/agent-jobs')}>
          <i className="fas fa-arrow-left" /> Back
        </button>
      </div>

      <form onSubmit={handleSave}>
        {/* Basic Info */}
        <div className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
          <h3 style={{ fontWeight: 600, marginBottom: 'var(--spacing-md)' }}>Basic Information</h3>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--spacing-md)' }}>
            <div className="form-group">
              <label className="form-label">Task Name *</label>
              <input className="input" value={task.name} onChange={(e) => updateField('name', e.target.value)} placeholder="my-task" required />
            </div>
            <div className="form-group">
              <label className="form-label">Model</label>
              <ModelSelector value={task.model} onChange={(model) => updateField('model', model)} capability={CAP_CHAT} />
            </div>
          </div>
          <div className="form-group">
            <label className="form-label">Description</label>
            <input className="input" value={task.description} onChange={(e) => updateField('description', e.target.value)} placeholder="Brief description of what this task does" />
          </div>
          <div className="form-group">
            <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', cursor: 'pointer' }}>
              <input type="checkbox" checked={task.enabled} onChange={(e) => updateField('enabled', e.target.checked)} />
              <span className="form-label" style={{ marginBottom: 0 }}>Enabled</span>
            </label>
          </div>
        </div>

        {/* Prompt Template */}
        <div className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
          <h3 style={{ fontWeight: 600, marginBottom: 'var(--spacing-md)' }}>Prompt Template</h3>
          <div className="form-group">
            <label className="form-label">Prompt</label>
            <textarea
              className="textarea"
              value={task.prompt}
              onChange={(e) => updateField('prompt', e.target.value)}
              rows={8}
              placeholder={`Write a summary about {{.topic}} in {{.format}} format.`}
              style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' }}
            />
            <p style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginTop: 'var(--spacing-xs)' }}>
              Use {'{{.parameter_name}}'} for dynamic parameters. Parameters are provided when executing the task.
            </p>
          </div>
          <div className="form-group">
            <label className="form-label">Context (optional)</label>
            <textarea className="textarea" value={task.context} onChange={(e) => updateField('context', e.target.value)} rows={3} placeholder="Additional context for the agent..." />
          </div>
        </div>

        {/* Cron Schedule */}
        <div className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
          <h3 style={{ fontWeight: 600, marginBottom: 'var(--spacing-md)' }}>
            <i className="fas fa-clock" style={{ marginRight: 'var(--spacing-xs)' }} />
            Cron Schedule (optional)
          </h3>
          <div className="form-group">
            <label className="form-label">Cron Expression</label>
            <input
              className="input"
              value={task.cron}
              onChange={(e) => { updateField('cron', e.target.value); validateCron(e.target.value) }}
              placeholder="0 */6 * * *"
              style={{ fontFamily: 'var(--font-mono)' }}
            />
            {cronError && <p style={{ color: 'var(--color-error)', fontSize: '0.75rem', marginTop: 4 }}>{cronError}</p>}
            <p style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginTop: 'var(--spacing-xs)' }}>
              Format: minute hour day month weekday (e.g., "0 */6 * * *" = every 6 hours)
            </p>
          </div>
          {task.cron && (
            <div className="form-group">
              <label className="form-label">Cron Parameters (key=value, one per line)</label>
              <textarea
                className="textarea"
                value={task.cron_parameters}
                onChange={(e) => updateField('cron_parameters', e.target.value)}
                rows={3}
                placeholder={`topic=daily news\nformat=bullet points`}
                style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' }}
              />
              <p style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginTop: 'var(--spacing-xs)' }}>
                Default parameters used when the cron triggers the task.
              </p>
            </div>
          )}
        </div>

        {/* Multimedia Sources */}
        <div className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-md)' }}>
            <h3 style={{ fontWeight: 600 }}>
              <i className="fas fa-photo-film" style={{ marginRight: 'var(--spacing-xs)' }} />
              Multimedia Sources (optional)
            </h3>
            <button type="button" className="btn btn-secondary btn-sm" onClick={addMultimediaSource}>
              <i className="fas fa-plus" /> Add Source
            </button>
          </div>
          {task.multimedia_sources.length === 0 ? (
            <p style={{ color: 'var(--color-text-muted)', fontSize: '0.8125rem' }}>No multimedia sources configured.</p>
          ) : (
            task.multimedia_sources.map((ms, i) => (
              <div key={i} style={{ background: 'var(--color-bg-primary)', borderRadius: 'var(--radius-md)', padding: 'var(--spacing-sm)', marginBottom: 'var(--spacing-sm)' }}>
                <div style={{ display: 'flex', gap: 'var(--spacing-sm)', alignItems: 'flex-start' }}>
                  <div className="form-group" style={{ minWidth: 120 }}>
                    <label className="form-label">Type</label>
                    <select className="input" value={ms.type} onChange={(e) => updateMultimediaSource(i, 'type', e.target.value)}>
                      <option value="image">Image</option>
                      <option value="video">Video</option>
                      <option value="audio">Audio</option>
                      <option value="file">File</option>
                    </select>
                  </div>
                  <div className="form-group" style={{ flex: 1 }}>
                    <label className="form-label">URL</label>
                    <input className="input" value={ms.url} onChange={(e) => updateMultimediaSource(i, 'url', e.target.value)} placeholder="https://example.com/media.jpg" />
                  </div>
                  <button type="button" className="btn btn-danger btn-sm" onClick={() => removeMultimediaSource(i)} style={{ marginTop: 24 }}>
                    <i className="fas fa-trash" />
                  </button>
                </div>
                <div className="form-group" style={{ marginTop: 'var(--spacing-xs)' }}>
                  <label className="form-label">Headers (JSON)</label>
                  <input className="input" value={ms.headers} onChange={(e) => updateMultimediaSource(i, 'headers', e.target.value)} placeholder='{"Authorization": "Bearer ..."}' style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' }} />
                </div>
              </div>
            ))
          )}
        </div>

        {/* Webhooks */}
        <div className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-md)' }}>
            <h3 style={{ fontWeight: 600 }}>
              <i className="fas fa-globe" style={{ marginRight: 'var(--spacing-xs)' }} />
              Webhooks (optional)
            </h3>
            <button type="button" className="btn btn-secondary btn-sm" onClick={addWebhook}>
              <i className="fas fa-plus" /> Add Webhook
            </button>
          </div>
          {task.webhooks.length === 0 ? (
            <p style={{ color: 'var(--color-text-muted)', fontSize: '0.8125rem' }}>No webhooks configured.</p>
          ) : (
            task.webhooks.map((wh, i) => (
              <div key={i} style={{ background: 'var(--color-bg-primary)', borderRadius: 'var(--radius-md)', padding: 'var(--spacing-sm)', marginBottom: 'var(--spacing-sm)' }}>
                <div style={{ display: 'flex', gap: 'var(--spacing-sm)', alignItems: 'flex-start' }}>
                  <div className="form-group" style={{ minWidth: 100 }}>
                    <label className="form-label">Method</label>
                    <select className="input" value={wh.method} onChange={(e) => updateWebhook(i, 'method', e.target.value)}>
                      <option value="POST">POST</option>
                      <option value="PUT">PUT</option>
                      <option value="PATCH">PATCH</option>
                    </select>
                  </div>
                  <div className="form-group" style={{ flex: 1 }}>
                    <label className="form-label">URL</label>
                    <input className="input" value={wh.url} onChange={(e) => updateWebhook(i, 'url', e.target.value)} placeholder="https://hooks.slack.com/..." />
                  </div>
                  <button type="button" className="btn btn-danger btn-sm" onClick={() => removeWebhook(i)} style={{ marginTop: 24 }}>
                    <i className="fas fa-trash" />
                  </button>
                </div>
                <div className="form-group" style={{ marginTop: 'var(--spacing-xs)' }}>
                  <label className="form-label">Headers (JSON)</label>
                  <input className="input" value={wh.headers} onChange={(e) => updateWebhook(i, 'headers', e.target.value)} placeholder='{"Content-Type": "application/json"}' style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' }} />
                </div>
                <div className="form-group" style={{ marginTop: 'var(--spacing-xs)' }}>
                  <label className="form-label">Payload Template (Go template syntax)</label>
                  <textarea
                    className="textarea"
                    value={wh.payload_template}
                    onChange={(e) => updateWebhook(i, 'payload_template', e.target.value)}
                    rows={3}
                    placeholder={`{"text": "Job {{.Status}}: {{if .Error}}Error: {{.Error}}{{else}}{{.Result}}{{end}}"}`}
                    style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' }}
                  />
                  <p style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginTop: 2 }}>
                    Available: {'{{.Job}}'} {'{{.Task}}'} {'{{.Result}}'} {'{{.Error}}'} {'{{.Status}}'}
                  </p>
                </div>
              </div>
            ))
          )}
        </div>

        <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
          <button type="submit" className="btn btn-primary" disabled={saving}>
            {saving ? <><i className="fas fa-spinner fa-spin" /> Saving...</> : <><i className="fas fa-save" /> {isNew ? 'Create Task' : 'Save Changes'}</>}
          </button>
          <button type="button" className="btn btn-secondary" onClick={() => navigate('/app/agent-jobs')}>Cancel</button>
        </div>
      </form>
    </div>
  )
}
