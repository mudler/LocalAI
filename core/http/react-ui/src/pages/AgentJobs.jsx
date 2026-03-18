import { useState, useEffect, useCallback, useRef } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { agentJobsApi, modelsApi } from '../utils/api'
import { useModels } from '../hooks/useModels'
import { useAuth } from '../context/AuthContext'
import { useUserMap } from '../hooks/useUserMap'
import LoadingSpinner from '../components/LoadingSpinner'
import { fileToBase64 } from '../utils/api'
import Modal from '../components/Modal'
import UserGroupSection from '../components/UserGroupSection'

export default function AgentJobs() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const { models } = useModels()
  const { isAdmin, authEnabled, user } = useAuth()
  const userMap = useUserMap()
  const [activeTab, setActiveTab] = useState('tasks')
  const [tasks, setTasks] = useState([])
  const [jobs, setJobs] = useState([])
  const [loading, setLoading] = useState(true)
  const [jobFilter, setJobFilter] = useState('all')
  const [hasMCPModels, setHasMCPModels] = useState(false)
  const [taskUserGroups, setTaskUserGroups] = useState(null)
  const [jobUserGroups, setJobUserGroups] = useState(null)

  // Execute modal state
  const [executeModal, setExecuteModal] = useState(null)
  const [executeTab, setExecuteTab] = useState('parameters')
  const [executeParams, setExecuteParams] = useState('')
  const [executeMultimedia, setExecuteMultimedia] = useState({ images: [], videos: [], audios: [], files: [] })
  const [executing, setExecuting] = useState(false)
  const fileInputRef = useRef(null)
  const fileTypeRef = useRef('images')

  const fetchData = useCallback(async () => {
    const allUsers = isAdmin && authEnabled
    try {
      const [t, j] = await Promise.allSettled([
        agentJobsApi.listTasks(allUsers),
        agentJobsApi.listJobs(allUsers),
      ])
      if (t.status === 'fulfilled') {
        const tv = t.value
        // Handle wrapped response (admin) or flat array
        if (Array.isArray(tv)) {
          setTasks(tv)
          setTaskUserGroups(null)
        } else if (tv && tv.tasks) {
          setTasks(Array.isArray(tv.tasks) ? tv.tasks : [])
          setTaskUserGroups(tv.user_groups || null)
        } else {
          setTasks(Array.isArray(tv) ? tv : [])
          setTaskUserGroups(null)
        }
      }
      if (j.status === 'fulfilled') {
        const jv = j.value
        if (Array.isArray(jv)) {
          setJobs(jv)
          setJobUserGroups(null)
        } else if (jv && jv.jobs) {
          setJobs(Array.isArray(jv.jobs) ? jv.jobs : [])
          setJobUserGroups(jv.user_groups || null)
        } else {
          setJobs(Array.isArray(jv) ? jv : [])
          setJobUserGroups(null)
        }
      }
    } catch (err) {
      addToast(`Failed to load: ${err.message}`, 'error')
    } finally {
      setLoading(false)
    }
  }, [addToast, isAdmin, authEnabled])

  useEffect(() => {
    fetchData()
    const interval = setInterval(fetchData, 5000)
    return () => clearInterval(interval)
  }, [fetchData])

  // Check for MCP-enabled models
  useEffect(() => {
    if (models.length === 0) { setHasMCPModels(false); return }
    let cancelled = false
    Promise.all(
      models.map(m => modelsApi.getConfigJson(m.id).catch(() => null))
    ).then(configs => {
      if (cancelled) return
      const hasMcp = configs.some(cfg => cfg && (cfg.mcp?.remote || cfg.mcp?.stdio))
      setHasMCPModels(hasMcp)
    })
    return () => { cancelled = true }
  }, [models])

  const handleDeleteTask = async (id) => {
    if (!confirm('Delete this task?')) return
    try {
      await agentJobsApi.deleteTask(id)
      addToast('Task deleted', 'success')
      fetchData()
    } catch (err) {
      addToast(`Failed to delete: ${err.message}`, 'error')
    }
  }

  const handleCancelJob = async (id) => {
    try {
      await agentJobsApi.cancelJob(id)
      addToast('Job cancelled', 'success')
      fetchData()
    } catch (err) {
      addToast(`Failed to cancel: ${err.message}`, 'error')
    }
  }

  const handleClearHistory = async () => {
    if (!confirm('Clear all job history?')) return
    try {
      // Cancel all running jobs first, then refetch
      const running = jobs.filter(j => j.status === 'running' || j.status === 'pending')
      await Promise.all(running.map(j => agentJobsApi.cancelJob(j.id).catch(() => {})))
      addToast('Job history cleared', 'success')
      fetchData()
    } catch (err) {
      addToast(`Failed to clear: ${err.message}`, 'error')
    }
  }

  const openExecuteModal = (task) => {
    setExecuteModal(task)
    setExecuteTab('parameters')
    setExecuteParams('')
    setExecuteMultimedia({ images: [], videos: [], audios: [], files: [] })
  }

  const handleExecute = async () => {
    if (!executeModal) return
    setExecuting(true)
    try {
      const body = { name: executeModal.name || executeModal.id }

      // Parse parameters
      if (executeParams.trim()) {
        const params = {}
        executeParams.split('\n').forEach(line => {
          const [key, ...rest] = line.split('=')
          if (key?.trim() && rest.length > 0) {
            params[key.trim()] = rest.join('=').trim()
          }
        })
        body.parameters = params
      }

      // Add multimedia
      const mm = {}
      if (executeMultimedia.images.length > 0) mm.images = executeMultimedia.images
      if (executeMultimedia.videos.length > 0) mm.videos = executeMultimedia.videos
      if (executeMultimedia.audios.length > 0) mm.audios = executeMultimedia.audios
      if (executeMultimedia.files.length > 0) mm.files = executeMultimedia.files
      if (Object.keys(mm).length > 0) body.multimedia = mm

      await agentJobsApi.executeTask(executeModal.name || executeModal.id)
      addToast(`Task "${executeModal.name}" started`, 'success')
      setExecuteModal(null)
      fetchData()
    } catch (err) {
      addToast(`Failed to execute: ${err.message}`, 'error')
    } finally {
      setExecuting(false)
    }
  }

  const handleFileUpload = async (e, type) => {
    for (const file of e.target.files) {
      const base64 = await fileToBase64(file)
      const url = `data:${file.type};base64,${base64}`
      setExecuteMultimedia(prev => ({
        ...prev,
        [type]: [...prev[type], { url, name: file.name }]
      }))
    }
    e.target.value = ''
  }

  const removeMultimedia = (type, index) => {
    setExecuteMultimedia(prev => ({
      ...prev,
      [type]: prev[type].filter((_, i) => i !== index)
    }))
  }

  const filteredJobs = jobFilter === 'all' ? jobs : jobs.filter(j => j.status === jobFilter)

  const statusBadge = (status) => {
    const cls = status === 'completed' ? 'badge-success' : status === 'failed' ? 'badge-error' : status === 'running' ? 'badge-info' : status === 'cancelled' ? '' : 'badge-warning'
    return <span className={`badge ${cls}`}>{status || 'unknown'}</span>
  }

  const formatDate = (d) => {
    if (!d) return '-'
    return new Date(d).toLocaleString()
  }

  // Wizard: no models installed
  if (!loading && models.length === 0) {
    return (
      <div className="page">
        <div className="page-header">
          <h1 className="page-title">Agent Jobs</h1>
          <p className="page-subtitle">Manage agent tasks and automated workflows</p>
        </div>
        <div className="card" style={{ textAlign: 'center', padding: 'var(--spacing-xl)' }}>
          <i className="fas fa-exclamation-triangle" style={{ fontSize: '3rem', color: 'var(--color-warning)', marginBottom: 'var(--spacing-md)' }} />
          <h2 style={{ marginBottom: 'var(--spacing-sm)' }}>No Models Installed</h2>
          <p style={{ color: 'var(--color-text-secondary)', marginBottom: 'var(--spacing-md)', maxWidth: 500, margin: '0 auto var(--spacing-md)' }}>
            Agent Jobs require at least one model with MCP (Model Context Protocol) support. Install a model first, then configure MCP in the model settings.
          </p>
          <div style={{ display: 'flex', gap: 'var(--spacing-sm)', justifyContent: 'center' }}>
            <button className="btn btn-primary" onClick={() => navigate('/app/models')}>
              <i className="fas fa-store" /> Browse Models
            </button>
            <a className="btn btn-secondary" href="https://localai.io/features/agents/" target="_blank" rel="noopener noreferrer">
              <i className="fas fa-book" /> Documentation
            </a>
          </div>
        </div>
      </div>
    )
  }

  // Wizard: models but no MCP
  if (!loading && models.length > 0 && !hasMCPModels && tasks.length === 0) {
    return (
      <div className="page">
        <div className="page-header">
          <h1 className="page-title">Agent Jobs</h1>
          <p className="page-subtitle">Manage agent tasks and automated workflows</p>
        </div>
        <div className="card" style={{ textAlign: 'center', padding: 'var(--spacing-xl)' }}>
          <i className="fas fa-plug" style={{ fontSize: '3rem', color: 'var(--color-primary)', marginBottom: 'var(--spacing-md)' }} />
          <h2 style={{ marginBottom: 'var(--spacing-sm)' }}>MCP Not Configured</h2>
          <p style={{ color: 'var(--color-text-secondary)', marginBottom: 'var(--spacing-md)', maxWidth: 600, margin: '0 auto var(--spacing-md)' }}>
            You have models installed, but none have MCP (Model Context Protocol) enabled. Agent Jobs require MCP to interact with tools and external services. Edit a model configuration to add MCP servers.
          </p>
          <div style={{ background: 'var(--color-bg-primary)', borderRadius: 'var(--radius-md)', padding: 'var(--spacing-md)', maxWidth: 500, margin: '0 auto var(--spacing-md)', textAlign: 'left' }}>
            <p style={{ fontSize: '0.8125rem', fontWeight: 600, marginBottom: 'var(--spacing-xs)' }}>Example MCP configuration (YAML):</p>
            <pre style={{ fontSize: '0.75rem', fontFamily: "'JetBrains Mono', monospace", color: 'var(--color-text-secondary)', whiteSpace: 'pre-wrap' }}>{`mcp:
  stdio:
    - name: my-tool
      command: /path/to/tool
      args: ["--flag"]`}</pre>
          </div>
          <div style={{ display: 'flex', gap: 'var(--spacing-sm)', justifyContent: 'center' }}>
            <button className="btn btn-primary" onClick={() => navigate('/app/manage')}>
              <i className="fas fa-cog" /> Manage Models
            </button>
            <a className="btn btn-secondary" href="https://localai.io/features/agents/" target="_blank" rel="noopener noreferrer">
              <i className="fas fa-book" /> Documentation
            </a>
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="page">
      <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <div>
          <h1 className="page-title">Agent Jobs</h1>
          <p className="page-subtitle">Manage agent tasks and automated workflows</p>
        </div>
        <button className="btn btn-primary" onClick={() => navigate('/app/agent-jobs/tasks/new')}>
          <i className="fas fa-plus" /> New Task
        </button>
      </div>

      <div className="tabs">
        <button className={`tab ${activeTab === 'tasks' ? 'tab-active' : ''}`} onClick={() => setActiveTab('tasks')}>
          <i className="fas fa-list-check" /> Tasks ({tasks.length})
        </button>
        <button className={`tab ${activeTab === 'jobs' ? 'tab-active' : ''}`} onClick={() => setActiveTab('jobs')}>
          <i className="fas fa-clock-rotate-left" /> Job History ({jobs.length})
        </button>
      </div>

      {loading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}><LoadingSpinner size="lg" /></div>
      ) : activeTab === 'tasks' ? (
        tasks.length === 0 && !taskUserGroups ? (
          <div className="empty-state">
            <div className="empty-state-icon"><i className="fas fa-robot" /></div>
            <h2 className="empty-state-title">No tasks defined</h2>
            <p className="empty-state-text">Create a task to get started with agent workflows.</p>
            <button className="btn btn-primary" onClick={() => navigate('/app/agent-jobs/tasks/new')}>
              <i className="fas fa-plus" /> Create Task
            </button>
          </div>
        ) : (
          <>
            {taskUserGroups && <h2 style={{ fontSize: '1.1rem', fontWeight: 600, marginBottom: 'var(--spacing-md)' }}>Your Tasks</h2>}
            {tasks.length === 0 ? (
              <p style={{ color: 'var(--color-text-secondary)', marginBottom: 'var(--spacing-md)' }}>You have no tasks yet.</p>
            ) : (
            <div className="table-container">
              <table className="table">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Description</th>
                    <th>Model</th>
                    <th>Cron</th>
                    <th>Status</th>
                    <th style={{ textAlign: 'right' }}>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {tasks.map(task => (
                    <tr key={task.id || task.name}>
                      <td>
                        <a onClick={() => navigate(`/app/agent-jobs/tasks/${task.id || task.name}`)} style={{ cursor: 'pointer', color: 'var(--color-primary)', fontWeight: 500 }}>
                          {task.name || task.id}
                        </a>
                      </td>
                      <td>
                        <span style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)', maxWidth: 200, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', display: 'inline-block' }}>
                          {task.description || '-'}
                        </span>
                      </td>
                      <td>
                        {task.model ? (
                          <a onClick={() => navigate(`/app/model-editor/${encodeURIComponent(task.model)}`)} style={{ cursor: 'pointer', color: 'var(--color-primary)', fontSize: '0.8125rem' }}>
                            {task.model}
                          </a>
                        ) : '-'}
                      </td>
                      <td>
                        {task.cron ? (
                          <span className="badge badge-info" style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: '0.6875rem' }}>
                            {task.cron}
                          </span>
                        ) : '-'}
                      </td>
                      <td>
                        {task.enabled === false ? (
                          <span className="badge" style={{ background: 'var(--color-bg-tertiary)', color: 'var(--color-text-muted)' }}>Disabled</span>
                        ) : (
                          <span className="badge badge-success">Enabled</span>
                        )}
                      </td>
                      <td>
                        <div style={{ display: 'flex', gap: 'var(--spacing-xs)', justifyContent: 'flex-end' }}>
                          <button className="btn btn-primary btn-sm" onClick={() => openExecuteModal(task)} title="Execute">
                            <i className="fas fa-play" />
                          </button>
                          <button className="btn btn-secondary btn-sm" onClick={() => navigate(`/app/agent-jobs/tasks/${task.id || task.name}/edit`)} title="Edit">
                            <i className="fas fa-edit" />
                          </button>
                          <button className="btn btn-danger btn-sm" onClick={() => handleDeleteTask(task.id || task.name)} title="Delete">
                            <i className="fas fa-trash" />
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            )}

          </>
        )
      ) : (
        <>
          {jobUserGroups && <h2 style={{ fontSize: '1.1rem', fontWeight: 600, marginBottom: 'var(--spacing-md)' }}>Your Jobs</h2>}
          {/* Job History Controls */}
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-md)' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
              <select className="input" value={jobFilter} onChange={(e) => setJobFilter(e.target.value)} style={{ width: 'auto', minWidth: 140 }}>
                <option value="all">All Status</option>
                <option value="pending">Pending</option>
                <option value="running">Running</option>
                <option value="completed">Completed</option>
                <option value="failed">Failed</option>
                <option value="cancelled">Cancelled</option>
              </select>
              <span style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>
                {filteredJobs.length} job{filteredJobs.length !== 1 ? 's' : ''}
              </span>
            </div>
            {jobs.length > 0 && (
              <button className="btn btn-secondary btn-sm" onClick={handleClearHistory}>
                <i className="fas fa-broom" /> Clear History
              </button>
            )}
          </div>

          {filteredJobs.length === 0 ? (
            <div className="empty-state">
              <div className="empty-state-icon"><i className="fas fa-list-check" /></div>
              <h2 className="empty-state-title">No jobs {jobFilter !== 'all' ? `with status "${jobFilter}"` : ''}</h2>
              <p className="empty-state-text">Execute a task to create a job.</p>
            </div>
          ) : (
            <div className="table-container">
              <table className="table">
                <thead>
                  <tr>
                    <th>Job ID</th>
                    <th>Task</th>
                    <th>Status</th>
                    <th>Created</th>
                    <th style={{ textAlign: 'right' }}>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredJobs.map(job => (
                    <tr key={job.id}>
                      <td>
                        <a onClick={() => navigate(`/app/agent-jobs/jobs/${job.id}`)} style={{ cursor: 'pointer', color: 'var(--color-primary)', fontFamily: "'JetBrains Mono', monospace", fontSize: '0.8125rem' }}>
                          {job.id?.slice(0, 12)}...
                        </a>
                      </td>
                      <td>{job.task_id || '-'}</td>
                      <td>{statusBadge(job.status)}</td>
                      <td style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>
                        {formatDate(job.created_at)}
                      </td>
                      <td>
                        <div style={{ display: 'flex', gap: 'var(--spacing-xs)', justifyContent: 'flex-end' }}>
                          <button className="btn btn-secondary btn-sm" onClick={() => navigate(`/app/agent-jobs/jobs/${job.id}`)} title="View">
                            <i className="fas fa-eye" />
                          </button>
                          {(job.status === 'running' || job.status === 'pending') && (
                            <button className="btn btn-danger btn-sm" onClick={() => handleCancelJob(job.id)} title="Cancel">
                              <i className="fas fa-stop" />
                            </button>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

        </>
      )}

      {activeTab === 'tasks' && taskUserGroups && (
        <UserGroupSection
          title="Other Users' Tasks"
          userGroups={taskUserGroups}
          userMap={userMap}
          currentUserId={user?.id}
          itemKey="tasks"
          renderGroup={(items) => (
            <div className="table-container">
              <table className="table">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Description</th>
                    <th>Model</th>
                  </tr>
                </thead>
                <tbody>
                  {(items || []).map(task => (
                    <tr key={task.id || task.name}>
                      <td style={{ fontWeight: 500 }}>{task.name || task.id}</td>
                      <td style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>{task.description || '-'}</td>
                      <td style={{ fontSize: '0.8125rem' }}>{task.model || '-'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        />
      )}

      {activeTab === 'jobs' && jobUserGroups && (
        <UserGroupSection
          title="Other Users' Jobs"
          userGroups={jobUserGroups}
          userMap={userMap}
          currentUserId={user?.id}
          itemKey="jobs"
          renderGroup={(items) => (
            <div className="table-container">
              <table className="table">
                <thead>
                  <tr>
                    <th>Job ID</th>
                    <th>Task</th>
                    <th>Status</th>
                    <th>Created</th>
                  </tr>
                </thead>
                <tbody>
                  {(items || []).map(job => (
                    <tr key={job.id}>
                      <td style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: '0.8125rem' }}>{job.id?.slice(0, 12)}...</td>
                      <td>{job.task_id || '-'}</td>
                      <td>{statusBadge(job.status)}</td>
                      <td style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>{formatDate(job.created_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        />
      )}

      {/* Execute Task Modal */}
      {executeModal && (
        <Modal onClose={() => setExecuteModal(null)}>
          <div style={{ padding: 'var(--spacing-md)' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-md)' }}>
              <h3 style={{ fontWeight: 600 }}>
                <i className="fas fa-play" style={{ color: 'var(--color-primary)', marginRight: 'var(--spacing-xs)' }} />
                Execute: {executeModal.name}
              </h3>
              <button className="btn btn-secondary btn-sm" onClick={() => setExecuteModal(null)}>
                <i className="fas fa-xmark" />
              </button>
            </div>

            {/* Tabs */}
            <div className="tabs" style={{ marginBottom: 'var(--spacing-md)' }}>
              <button className={`tab ${executeTab === 'parameters' ? 'tab-active' : ''}`} onClick={() => setExecuteTab('parameters')}>
                <i className="fas fa-sliders-h" /> Parameters
              </button>
              <button className={`tab ${executeTab === 'multimedia' ? 'tab-active' : ''}`} onClick={() => setExecuteTab('multimedia')}>
                <i className="fas fa-photo-film" /> Multimedia
              </button>
            </div>

            {executeTab === 'parameters' ? (
              <div>
                <label className="form-label">Parameters (key=value, one per line)</label>
                <textarea
                  className="textarea"
                  value={executeParams}
                  onChange={(e) => setExecuteParams(e.target.value)}
                  rows={5}
                  placeholder={`topic=AI trends\nformat=markdown`}
                  style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: '0.8125rem' }}
                />
                <p style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginTop: 'var(--spacing-xs)' }}>
                  These will be available as {'{{.parameter_name}}'} in the prompt template.
                </p>
              </div>
            ) : (
              <div>
                {['images', 'videos', 'audios', 'files'].map(type => (
                  <div key={type} style={{ marginBottom: 'var(--spacing-md)' }}>
                    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-xs)' }}>
                      <label className="form-label" style={{ marginBottom: 0, textTransform: 'capitalize' }}>
                        <i className={`fas ${type === 'images' ? 'fa-image' : type === 'videos' ? 'fa-video' : type === 'audios' ? 'fa-headphones' : 'fa-file'}`} style={{ marginRight: 4 }} />
                        {type} ({executeMultimedia[type].length})
                      </label>
                      <button className="btn btn-secondary btn-sm" onClick={() => { fileTypeRef.current = type; fileInputRef.current?.click() }}>
                        <i className="fas fa-plus" /> Add
                      </button>
                    </div>
                    {executeMultimedia[type].length > 0 && (
                      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                        {executeMultimedia[type].map((item, i) => (
                          <div key={i} style={{
                            display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                            background: 'var(--color-bg-primary)', borderRadius: 'var(--radius-sm)', padding: '4px 8px', fontSize: '0.75rem',
                          }}>
                            <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{item.name || item.url?.slice(0, 40)}</span>
                            <button onClick={() => removeMultimedia(type, i)} style={{ background: 'none', border: 'none', color: 'var(--color-error)', cursor: 'pointer', padding: '2px 4px' }}>
                              <i className="fas fa-xmark" />
                            </button>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                ))}
                <input ref={fileInputRef} type="file" multiple style={{ display: 'none' }} onChange={(e) => handleFileUpload(e, fileTypeRef.current)} />
              </div>
            )}

            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--spacing-sm)', marginTop: 'var(--spacing-md)' }}>
              <button className="btn btn-secondary" onClick={() => setExecuteModal(null)}>Cancel</button>
              <button className="btn btn-primary" onClick={handleExecute} disabled={executing}>
                {executing ? <><i className="fas fa-spinner fa-spin" /> Running...</> : <><i className="fas fa-play" /> Execute</>}
              </button>
            </div>
          </div>
        </Modal>
      )}
    </div>
  )
}
