import { useState, useEffect, useCallback, useMemo } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation, Trans } from 'react-i18next'
import { agentsApi } from '../utils/api'
import { useAuth } from '../context/AuthContext'
import { useUserMap } from '../hooks/useUserMap'
import UserGroupSection from '../components/UserGroupSection'
import ConfirmDialog from '../components/ConfirmDialog'

export default function Agents() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const { t } = useTranslation('agents')
  const { isAdmin, authEnabled, user } = useAuth()
  const userMap = useUserMap()
  const [agents, setAgents] = useState([])
  const [loading, setLoading] = useState(true)
  const [agentHubURL, setAgentHubURL] = useState('')
  const [search, setSearch] = useState('')
  const [userGroups, setUserGroups] = useState(null)
  const [confirmDialog, setConfirmDialog] = useState(null)

  const fetchAgents = useCallback(async () => {
    try {
      const data = await agentsApi.list(isAdmin && authEnabled)
      const names = Array.isArray(data.agents) ? data.agents : []
      const statuses = data.statuses || {}
      if (data.agent_hub_url) setAgentHubURL(data.agent_hub_url)
      setUserGroups(data.user_groups || null)

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
      addToast(t('toasts.loadFailed', { message: err.message }), 'error')
    } finally {
      setLoading(false)
    }
  }, [addToast, isAdmin, authEnabled, t])

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

  const handleDelete = (name, userId) => {
    setConfirmDialog({
      title: t('deleteDialog.title'),
      message: t('deleteDialog.message', { name }),
      confirmLabel: t('deleteDialog.confirm'),
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        try {
          await agentsApi.delete(name, userId)
          addToast(t('toasts.deleted', { name }), 'success')
          fetchAgents()
        } catch (err) {
          addToast(t('toasts.deleteFailed', { message: err.message }), 'error')
        }
      },
    })
  }

  const handlePauseResume = async (agent, userId) => {
    const name = agent.name || agent.id
    const isActive = agent.status === 'active' || agent.active === true
    try {
      if (isActive) {
        await agentsApi.pause(name, userId)
        addToast(t('toasts.paused', { name }), 'success')
      } else {
        await agentsApi.resume(name, userId)
        addToast(t('toasts.resumed', { name }), 'success')
      }
      fetchAgents()
    } catch (err) {
      addToast(t(isActive ? 'toasts.pauseFailed' : 'toasts.resumeFailed', { message: err.message }), 'error')
    }
  }

  const handleExport = async (name, userId) => {
    try {
      const data = await agentsApi.export(name, userId)
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `${name}.json`
      document.body.appendChild(a)
      a.click()
      document.body.removeChild(a)
      URL.revokeObjectURL(url)
      addToast(t('toasts.exported', { name }), 'success')
    } catch (err) {
      addToast(t('toasts.exportFailed', { message: err.message }), 'error')
    }
  }

  const handleImport = async (e) => {
    const file = e.target.files?.[0]
    if (!file) return
    try {
      const text = await file.text()
      const config = JSON.parse(text)
      navigate('/app/agents/new', { state: { importedConfig: config } })
    } catch (err) {
      addToast(t('toasts.parseFailed', { message: err.message }), 'error')
    }
    e.target.value = ''
  }

  const statusBadge = (status) => {
    const cls = status === 'active' ? 'badge-success' : status === 'paused' ? 'badge-warning' : ''
    return <span className={`badge ${cls}`}>{status || 'unknown'}</span>
  }

  return (
    <div className="page page--wide">
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
          <h1 className="page-title">{t('title')}</h1>
          <p className="page-subtitle">{t('subtitle')}</p>
        </div>
        <div style={{ display: 'flex', gap: 'var(--spacing-sm)', alignItems: 'center' }}>
          {agentHubURL && (
            <a className="btn btn-secondary" href={agentHubURL} target="_blank" rel="noopener noreferrer">
              <i className="fas fa-store" /> {t('actions.agentHub')}
            </a>
          )}
          <label className="btn btn-secondary">
            <i className="fas fa-file-import" /> {t('actions.import')}
            <input type="file" accept=".json" className="agents-import-input" onChange={handleImport} />
          </label>
          <button className="btn btn-primary" onClick={() => navigate('/app/agents/new')}>
            <i className="fas fa-plus" /> {t('actions.createAgent')}
          </button>
        </div>
      </div>

      {loading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}>
          <i className="fas fa-spinner fa-spin" style={{ fontSize: '2rem', color: 'var(--color-primary)' }} />
        </div>
      ) : agents.length === 0 && !userGroups ? (
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-robot" /></div>
          <h2 className="empty-state-title">{t('empty.noConfigured')}</h2>
          <p className="empty-state-text">{t('empty.noConfiguredText')}</p>
          {agentHubURL && (
            <p className="empty-state-text">
              <Trans
                i18nKey="agents:empty.browseHub"
                values={{}}
                components={{
                  1: <a href={agentHubURL} target="_blank" rel="noopener noreferrer" />,
                }}
              />
            </p>
          )}
          <div style={{ display: 'flex', gap: 'var(--spacing-sm)', justifyContent: 'center', flexWrap: 'wrap' }}>
            <button className="btn btn-primary" onClick={() => navigate('/app/agents/new')}>
              <i className="fas fa-plus" /> {t('actions.createAgent')}
            </button>
            <label className="btn btn-secondary">
              <i className="fas fa-file-import" /> {t('actions.import')}
              <input type="file" accept=".json" className="agents-import-input" onChange={handleImport} />
            </label>
            {agentHubURL && (
              <a className="btn btn-secondary" href={agentHubURL} target="_blank" rel="noopener noreferrer">
                <i className="fas fa-store" /> {t('actions.agentHub')}
              </a>
            )}
          </div>
        </div>
      ) : (
        <>
          {userGroups && <h2 style={{ fontSize: '1.1rem', fontWeight: 600, marginBottom: 'var(--spacing-md)' }}>{t('sections.yourAgents')}</h2>}
          <div className="agents-toolbar">
            <div className="agents-search">
              <i className="fas fa-search" />
              <input
                className="input"
                type="text"
                placeholder={t('search.placeholder')}
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
            </div>
            <span style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>
              {t('search.summary', { shown: filtered.length, total: agents.length, count: agents.length })}
            </span>
          </div>

          {filtered.length === 0 ? (
            <div className="empty-state">
              <div className="empty-state-icon"><i className="fas fa-search" /></div>
              <h2 className="empty-state-title">{t('empty.noMatching')}</h2>
              <p className="empty-state-text">{t('empty.noMatchingText', { query: search })}</p>
            </div>
          ) : (
            <div className="table-container">
              <table className="table">
                <thead>
                  <tr>
                    <th>{t('table.name')}</th>
                    <th>{t('table.status')}</th>
                    <th>{t('table.events')}</th>
                    <th style={{ textAlign: 'right' }}>{t('table.actions')}</th>
                  </tr>
                </thead>
                <tbody>
                  {filtered.map(agent => {
                    const name = agent.name || agent.id
                    const isActive = agent.status === 'active'
                    return (
                      <tr key={name}>
                        <td>
                          <a className="agents-name" onClick={() => navigate(`/app/agents/${encodeURIComponent(name)}/chat`)}>
                            {name}
                          </a>
                        </td>
                        <td>{statusBadge(agent.status)}</td>
                        <td>
                          <a
                            className="agents-name"
                            onClick={() => navigate(`/app/agents/${encodeURIComponent(name)}/status`)}
                            title={t('table.eventsTooltip', { count: agent.eventsCount })}
                          >
                            {agent.eventsCount}
                          </a>
                        </td>
                        <td>
                          <div className="agents-action-group">
                            <button
                              className={`btn btn-sm ${isActive ? 'btn-warning' : 'btn-success'}`}
                              onClick={() => handlePauseResume(agent)}
                              title={isActive ? t('actions.pause') : t('actions.resume')}
                            >
                              <i className={`fas ${isActive ? 'fa-pause' : 'fa-play'}`} />
                            </button>
                            <button
                              className="btn btn-secondary btn-sm"
                              onClick={() => navigate(`/app/agents/${encodeURIComponent(name)}/edit`)}
                              title={t('actions.edit')}
                            >
                              <i className="fas fa-edit" />
                            </button>
                            <button
                              className="btn btn-secondary btn-sm"
                              onClick={() => navigate(`/app/agents/${encodeURIComponent(name)}/chat`)}
                              title={t('actions.chat')}
                            >
                              <i className="fas fa-comment" />
                            </button>
                            <button
                              className="btn btn-secondary btn-sm"
                              onClick={() => handleExport(name)}
                              title={t('actions.export')}
                            >
                              <i className="fas fa-download" />
                            </button>
                            <button
                              className="btn btn-danger btn-sm"
                              onClick={() => handleDelete(name)}
                              title={t('actions.delete')}
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

      {userGroups && (
        <UserGroupSection
          title={t('sections.otherUsersAgents')}
          userGroups={userGroups}
          userMap={userMap}
          currentUserId={user?.id}
          itemKey="agents"
          renderGroup={(items, userId) => (
            <div className="table-container">
              <table className="table">
                <thead>
                  <tr>
                    <th>{t('table.name')}</th>
                    <th>{t('table.status')}</th>
                    <th style={{ textAlign: 'right' }}>{t('table.actions')}</th>
                  </tr>
                </thead>
                <tbody>
                  {(items || []).map(a => {
                    const isActive = a.active === true
                    return (
                      <tr key={a.name}>
                        <td>
                          <a className="agents-name" onClick={() => navigate(`/app/agents/${encodeURIComponent(a.name)}/chat?user_id=${encodeURIComponent(userId)}`)}>
                            {a.name}
                          </a>
                        </td>
                        <td>{statusBadge(isActive ? 'active' : 'paused')}</td>
                        <td>
                          <div className="agents-action-group">
                            <button
                              className={`btn btn-sm ${isActive ? 'btn-warning' : 'btn-success'}`}
                              onClick={() => handlePauseResume(a, userId)}
                              title={isActive ? t('actions.pause') : t('actions.resume')}
                            >
                              <i className={`fas ${isActive ? 'fa-pause' : 'fa-play'}`} />
                            </button>
                            <button
                              className="btn btn-secondary btn-sm"
                              onClick={() => navigate(`/app/agents/${encodeURIComponent(a.name)}/edit?user_id=${encodeURIComponent(userId)}`)}
                              title={t('actions.edit')}
                            >
                              <i className="fas fa-edit" />
                            </button>
                            <button
                              className="btn btn-secondary btn-sm"
                              onClick={() => navigate(`/app/agents/${encodeURIComponent(a.name)}/chat?user_id=${encodeURIComponent(userId)}`)}
                              title={t('actions.chat')}
                            >
                              <i className="fas fa-comment" />
                            </button>
                            <button
                              className="btn btn-secondary btn-sm"
                              onClick={() => handleExport(a.name, userId)}
                              title={t('actions.export')}
                            >
                              <i className="fas fa-download" />
                            </button>
                            <button
                              className="btn btn-danger btn-sm"
                              onClick={() => handleDelete(a.name, userId)}
                              title={t('actions.delete')}
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
        />
      )}

      <ConfirmDialog
        open={!!confirmDialog}
        title={confirmDialog?.title}
        message={confirmDialog?.message}
        confirmLabel={confirmDialog?.confirmLabel}
        danger={confirmDialog?.danger}
        onConfirm={confirmDialog?.onConfirm}
        onCancel={() => setConfirmDialog(null)}
      />
    </div>
  )
}
