import { useState, useEffect, useCallback, useMemo } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { agentsApi } from '../utils/api'

export default function Agents() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const [agents, setAgents] = useState([])
  const [loading, setLoading] = useState(true)
  const [agentHubURL, setAgentHubURL] = useState('')
  const [search, setSearch] = useState('')

  const fetchAgents = useCallback(async () => {
    try {
      const data = await agentsApi.list()
      const names = Array.isArray(data.agents) ? data.agents : []
      const statuses = data.statuses || {}
      if (data.agent_hub_url) setAgentHubURL(data.agent_hub_url)
      
      // Fetch observable counts for each agent
      const agentsWithCounts = await Promise.all(
        names.map(async (name) => {
          let eventsCount = 0
          try {
            const observables = await agentsApi.observables(name)
            eventsCount = observables?.History?.length || 0
          } catch (_err) {
            eventsCount = 0
          }
          return {
            name,
            status: statuses[name] ? 'active' : 'paused',
            eventsCount,
          }
        })
      )
      setAgents(agentsWithCounts)
    } catch (err) {
      addToast(`Failed to load agents: ${err.message}`, 'error')
    } finally {
      setLoading(false)
    }
  }, [addToast])

  useEffect(() => {
    fetchAgents()
    const interval = setInterval(fetchAgents, 5000)
    return () => clearInterval(interval)
  }, [fetchAgents])

  const filtered = useMemo(() => {
    if (!search.trim()) return agents
    const q = search.toLowerCase()
    return agents.filter(a => a.name.toLowerCase().includes(q))
  }, [agents, search])

  const handleDelete = async (name) => {
    if (!window.confirm(`Delete agent "${name}"? This action cannot be undone.`)) return
    try {
      await agentsApi.delete(name)
      addToast(`Agent "${name}" deleted`, 'success')
      fetchAgents()
    } catch (err) {
      addToast(`Failed to delete agent: ${err.message}`, 'error')
    }
  }

  const handlePauseResume = async (agent) => {
    const name = agent.name || agent.id
    const isActive = agent.status === 'active'
    try {
      if (isActive) {
        await agentsApi.pause(name)
        addToast(`Agent "${name}" paused`, 'success')
      } else {
        await agentsApi.resume(name)
        addToast(`Agent "${name}" resumed`, 'success')
      }
      fetchAgents()
    } catch (err) {
      addToast(`Failed to ${isActive ? 'pause' : 'resume'} agent: ${err.message}`, 'error')
    }
  }

  const handleExport = async (name) => {
    try {
      const data = await agentsApi.export(name)
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `${name}.json`
      document.body.appendChild(a)
      a.click()
      document.body.removeChild(a)
      URL.revokeObjectURL(url)
      addToast(`Agent "${name}" exported`, 'success')
    } catch (err) {
      addToast(`Failed to export agent: ${err.message}`, 'error')
    }
  }

  const handleImport = async (e) => {
    const file = e.target.files?.[0]
    if (!file) return
    try {
      const text = await file.text()
      const config = JSON.parse(text)
      navigate('/agents/new', { state: { importedConfig: config } })
    } catch (err) {
      addToast(`Failed to parse agent file: ${err.message}`, 'error')
    }
    e.target.value = ''
  }

  const statusBadge = (status) => {
    const cls = status === 'active' ? 'badge-success' : status === 'paused' ? 'badge-warning' : ''
    return <span className={`badge ${cls}`}>{status || 'unknown'}</span>
  }

  return (
    <div className="page">
      <style>{`
        .agents-import-input { display: none; }
        .agents-toolbar {
          display: flex;
          align-items: center;
          gap: var(--spacing-sm);
          margin-bottom: var(--spacing-md);
          flex-wrap: wrap;
        }
        .agents-search {
          flex: 1;
          min-width: 180px;
          max-width: 360px;
          position: relative;
        }
        .agents-search i {
          position: absolute;
          left: 10px;
          top: 50%;
          transform: translateY(-50%);
          color: var(--color-text-muted);
          font-size: 0.8125rem;
          pointer-events: none;
        }
        .agents-search input {
          padding-left: 32px;
        }
        .agents-action-group {
          display: flex;
          gap: var(--spacing-xs);
          justify-content: flex-end;
        }
        .agents-name {
          cursor: pointer;
          color: var(--color-primary);
          font-weight: 500;
        }
        .agents-name:hover {
          text-decoration: underline;
        }
      `}</style>

      <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <div>
          <h1 className="page-title">Agents</h1>
          <p className="page-subtitle">Manage autonomous AI agents</p>
        </div>
        <div style={{ display: 'flex', gap: 'var(--spacing-sm)', alignItems: 'center' }}>
          {agentHubURL && (
            <a className="btn btn-secondary" href={agentHubURL} target="_blank" rel="noopener noreferrer">
              <i className="fas fa-store" /> Agent Hub
            </a>
          )}
          <label className="btn btn-secondary">
            <i className="fas fa-file-import" /> Import
            <input type="file" accept=".json" className="agents-import-input" onChange={handleImport} />
          </label>
          <button className="btn btn-primary" onClick={() => navigate('/agents/new')}>
            <i className="fas fa-plus" /> Create Agent
          </button>
        </div>
      </div>

      {loading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}>
          <i className="fas fa-spinner fa-spin" style={{ fontSize: '2rem', color: 'var(--color-primary)' }} />
        </div>
      ) : agents.length === 0 ? (
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-robot" /></div>
          <h2 className="empty-state-title">No agents configured</h2>
          <p className="empty-state-text">Create an agent to get started with autonomous AI workflows.</p>
          {agentHubURL && (
            <p className="empty-state-text">
              Don't know where to start? Browse the <a href={agentHubURL} target="_blank" rel="noopener noreferrer">Agent Hub</a> to find ready-made agent configurations you can import.
            </p>
          )}
          <div style={{ display: 'flex', gap: 'var(--spacing-sm)', justifyContent: 'center', flexWrap: 'wrap' }}>
            <button className="btn btn-primary" onClick={() => navigate('/agents/new')}>
              <i className="fas fa-plus" /> Create Agent
            </button>
            <label className="btn btn-secondary">
              <i className="fas fa-file-import" /> Import
              <input type="file" accept=".json" className="agents-import-input" onChange={handleImport} />
            </label>
            {agentHubURL && (
              <a className="btn btn-secondary" href={agentHubURL} target="_blank" rel="noopener noreferrer">
                <i className="fas fa-store" /> Agent Hub
              </a>
            )}
          </div>
        </div>
      ) : (
        <>
          <div className="agents-toolbar">
            <div className="agents-search">
              <i className="fas fa-search" />
              <input
                className="input"
                type="text"
                placeholder="Search agents..."
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
            </div>
            <span style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>
              {filtered.length} of {agents.length} agent{agents.length !== 1 ? 's' : ''}
            </span>
          </div>

          {filtered.length === 0 ? (
            <div className="empty-state">
              <div className="empty-state-icon"><i className="fas fa-search" /></div>
              <h2 className="empty-state-title">No matching agents</h2>
              <p className="empty-state-text">No agents match "{search}"</p>
            </div>
          ) : (
            <div className="table-container">
              <table className="table">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Status</th>
                    <th>Events</th>
                    <th style={{ textAlign: 'right' }}>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {filtered.map(agent => {
                    const name = agent.name || agent.id
                    const isActive = agent.status === 'active'
                    return (
                      <tr key={name}>
                        <td>
                          <a className="agents-name" onClick={() => navigate(`/agents/${encodeURIComponent(name)}/chat`)}>
                            {name}
                          </a>
                        </td>
                        <td>{statusBadge(agent.status)}</td>
                        <td>
                          <a
                            className="agents-name"
                            onClick={() => navigate(`/agents/${encodeURIComponent(name)}/status`)}
                            title={`${agent.eventsCount} events - Click to view`}
                          >
                            {agent.eventsCount}
                          </a>
                        </td>
                        <td>
                          <div className="agents-action-group">
                            <button
                              className={`btn btn-sm ${isActive ? 'btn-warning' : 'btn-success'}`}
                              onClick={() => handlePauseResume(agent)}
                              title={isActive ? 'Pause' : 'Resume'}
                            >
                              <i className={`fas ${isActive ? 'fa-pause' : 'fa-play'}`} />
                            </button>
                            <button
                              className="btn btn-secondary btn-sm"
                              onClick={() => navigate(`/agents/${encodeURIComponent(name)}/edit`)}
                              title="Edit"
                            >
                              <i className="fas fa-edit" />
                            </button>
                            <button
                              className="btn btn-secondary btn-sm"
                              onClick={() => navigate(`/agents/${encodeURIComponent(name)}/chat`)}
                              title="Chat"
                            >
                              <i className="fas fa-comment" />
                            </button>
                            <button
                              className="btn btn-secondary btn-sm"
                              onClick={() => handleExport(name)}
                              title="Export"
                            >
                              <i className="fas fa-download" />
                            </button>
                            <button
                              className="btn btn-danger btn-sm"
                              onClick={() => handleDelete(name)}
                              title="Delete"
                            >
                              <i className="fas fa-trash" />
                            </button>
                          </div>
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
    </div>
  )
}
