import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link, useNavigate, useOutletContext } from 'react-router-dom'
// eslint-disable-next-line no-unused-vars
import { useTranslation, Trans } from 'react-i18next'
import { agentsApi } from '../utils/api'
import { useAuth } from '../context/AuthContext'
import { useUserMap } from '../hooks/useUserMap'
// eslint-disable-next-line no-unused-vars
import UserGroupSection from '../components/UserGroupSection'
// eslint-disable-next-line no-unused-vars
import PageHeader from '../components/PageHeader'
// eslint-disable-next-line no-unused-vars
import ConfirmDialog from '../components/ConfirmDialog'
// eslint-disable-next-line no-unused-vars
import ActionMenu from '../components/ActionMenu'
import Icon from '../components/Icon'
// eslint-disable-next-line no-unused-vars
import { StatusMark, RunStrip, RecordLine, Chip, LibraryChips, agentPath } from '../components/agents/AgentBits'
import { agentInfo, workingLine } from '../utils/agentInfo'
import { effectiveStatus, loadRuns, outcomeLine, recordStatus } from '../utils/agentRuns'
import { AGENT_TEMPLATES } from '../utils/agentConfigTools'
import './agents.css'

const DAY = 24 * 60 * 60 * 1000

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
  const [configs, setConfigs] = useState({})
  const [tick, setTick] = useState(0)
  const asked = useRef(new Set())

  const fetchAgents = useCallback(async () => {
    try {
      const data = await agentsApi.list(isAdmin && authEnabled)
      const names = Array.isArray(data.agents) ? data.agents : []
      const statuses = data.statuses || {}
      if (data.agent_hub_url) setAgentHubURL(data.agent_hub_url)
      setUserGroups(data.user_groups || null)

      // An observable with no completion is an action still running: that is
      // what "working" means here. The count stays for the status page link.
      const withState = await Promise.all(
        names.map(async (name) => {
          let eventsCount = 0
          let working = null
          try {
            const observables = await agentsApi.observables(name)
            const history = observables?.History || []
            eventsCount = history.length
            working = workingLine(history)
          } catch (_err) {
            eventsCount = 0
          }
          return {
            name,
            status: statuses[name] ? 'active' : 'paused',
            eventsCount,
            working,
          }
        })
      )
      setAgents(withState)
      setTick(n => n + 1)
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

  // The model and what an agent has attached live in its saved config. Read
  // each once; the list poll does not repeat it.
  useEffect(() => {
    agents.forEach(({ name }) => {
      if (asked.current.has(name)) return
      asked.current.add(name)
      agentsApi.getConfig(name)
        .then(cfg => setConfigs(prev => ({ ...prev, [name]: cfg })))
        .catch(() => setConfigs(prev => ({ ...prev, [name]: null })))
    })
  }, [agents])

  const rows = useMemo(() => agents.map(a => {
    const runs = loadRuns(a.name)
    return { ...a, runs, info: agentInfo(configs[a.name]), configLoaded: a.name in configs }
  // tick re-reads the run log after every poll
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }), [agents, configs, tick])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return rows
    return rows.filter(a => a.name.toLowerCase().includes(q) || a.info.description.toLowerCase().includes(q) || a.info.model.toLowerCase().includes(q))
  }, [rows, search])

  // Now: agents with work in flight, and agents whose latest run failed in
  // the last day. Both are only what this browser and the observables know.
  const now = useMemo(() => {
    const items = []
    const at = Date.now()
    for (const a of rows) {
      const live = a.runs.find(r => effectiveStatus(r, at) === 'running')
      if (a.working || live) {
        items.push({ kind: 'running', agent: a, runId: live?.id, line: a.working || live.turns.at(-1).task })
        continue
      }
      const last = a.runs.find(r => !r.legacy) || a.runs[0]
      if (last && recordStatus(last, at) === 'failed' && at - last.startedAt < DAY) {
        items.push({ kind: 'failed', agent: a, runId: last.id, line: outcomeLine(last, 140) || last.turns[0].task })
      }
    }
    return items
  }, [rows])

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

  const importLabel = (
    <label className="btn btn-secondary">
      <Icon name="import" /> {t('actions.import')}
      <input type="file" accept=".json" hidden onChange={handleImport} />
    </label>
  )

  return (
    <div className="page page--medium ag-page">
      <PageHeader
        title={t('title')}
        supporting={t('subtitle')}
        actions={
          <div className="header-actions">
            {agentHubURL && (
              <a className="btn btn-secondary" href={agentHubURL} target="_blank" rel="noopener noreferrer">
                <Icon name="store" /> {t('actions.agentHub')}
              </a>
            )}
            {/* A label styled as a button, wrapping the file input it triggers,
                so the control looks and behaves like its neighbours. */}
            {importLabel}
            <button className="btn btn-primary" onClick={() => navigate('/app/agents/new')}>
              <Icon name="plus" /> {t('actions.createAgent')}
            </button>
          </div>
        }
      />

      {loading ? (
        <div className="loading-center">
          <Icon name="spinner" spin className="icon-xl text-primary" />
        </div>
      ) : agents.length === 0 && !userGroups ? (
        <div className="ag-empty" data-testid="agents-empty">
          <h2 className="empty-state-title">{t('empty.noConfigured')}</h2>
          <p>{t('empty.noConfiguredText')}</p>
          <p className="ag-eyebrow">{t('empty.startWith')}</p>
          <div className="ag-list">
            {AGENT_TEMPLATES.filter(x => x.id !== 'blank').map(x => (
              <div key={x.id} className="ag-row ag-row--two">
                <div className="ag-row__main">
                  <Link className="ag-row__name" to={`/app/agents/new?template=${x.id}`}>{t(`templates.${x.id}.label`)}</Link>
                  <span className="ag-row__desc">{x.description}</span>
                </div>
                <Link className="btn btn-secondary btn-sm" to={`/app/agents/new?template=${x.id}`}>{t('empty.useTemplate')}</Link>
              </div>
            ))}
          </div>
          {agentHubURL && (
            <p className="ag-note ag-note--gap">
              <Trans
                i18nKey="agents:empty.browseHub"
                values={{}}
                components={{
                  1: <a href={agentHubURL} target="_blank" rel="noopener noreferrer" />,
                }}
              />
            </p>
          )}
          <div className="ag-empty__acts ag-empty__acts--gap">
            <button className="btn btn-primary" onClick={() => navigate('/app/agents/new')}>
              <Icon name="plus" /> {t('actions.createAgent')}
            </button>
            {importLabel}
            {agentHubURL && (
              <a className="btn btn-secondary" href={agentHubURL} target="_blank" rel="noopener noreferrer">
                <Icon name="store" /> {t('actions.agentHub')}
              </a>
            )}
          </div>
        </div>
      ) : (
        <div className="ag-launch">
          {now.length > 0 && (
            <section aria-labelledby="ag-now-h" data-testid="agents-now">
              <div className="ag-section-head"><h2 className="ag-eyebrow" id="ag-now-h">{t('now.title')}</h2></div>
              <div className="ag-now">
                {now.map(item => (
                  <div key={`${item.kind}-${item.agent.name}`} className="ag-now__item" data-state={item.kind}>
                    <div className="ag-now__main">
                      <StatusMark status={item.kind === 'running' ? 'running' : 'failed'} label={t(item.kind === 'running' ? 'now.working' : 'now.failed')} />
                      <span className="ag-now__name">{item.agent.name}</span>
                      <span className="ag-now__line">{item.line}</span>
                    </div>
                    <div className="ag-now__acts">
                      <Link
                        className="btn btn-secondary btn-sm"
                        to={item.runId ? agentPath(item.agent.name, undefined, `/runs/${item.runId}`) : agentPath(item.agent.name)}
                      >
                        {t('now.open')}
                      </Link>
                      {item.kind === 'failed' && (
                        <Link
                          className="btn btn-secondary btn-sm"
                          to={agentPath(item.agent.name)}
                          state={{ task: item.agent.runs.find(r => r.id === item.runId)?.turns[0].task }}
                        >
                          {t('now.runAgain')}
                        </Link>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            </section>
          )}

          <section aria-labelledby="ag-mine-h">
            <div className="ag-section-head">
              <h2 className="ag-eyebrow" id="ag-mine-h">{userGroups ? t('sections.yourAgents') : t('sections.agents')}</h2>
              <span className="ag-muted ag-small">
                {t('search.summary', { shown: filtered.length, total: agents.length, count: agents.length })}
              </span>
            </div>
            <div className="ag-tools">
              <div className="ag-search">
                <Icon name="search" />
                <input
                  className="input"
                  type="text"
                  aria-label={t('search.placeholder')}
                  placeholder={t('search.placeholder')}
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                />
              </div>
            </div>

            {filtered.length === 0 ? (
              <div className="ag-empty">
                <h3>{t('empty.noMatching')}</h3>
                <p>{t('empty.noMatchingText', { query: search })}</p>
              </div>
            ) : (
              <div className="ag-list" data-testid="agents-list">
                {filtered.map(agent => {
                  const name = agent.name
                  const isActive = agent.status === 'active'
                  const state = agent.working ? 'running' : isActive ? 'ready' : 'paused'
                  return (
                    <article key={name} className="ag-row" data-agent={name}>
                      <div className="ag-row__main">
                        <div className="ag-row__head">
                          <Link className="ag-row__name" to={agentPath(name)}>{name}</Link>
                          <StatusMark status={state} />
                        </div>
                        {agent.info.description && <span className="ag-row__desc">{agent.info.description}</span>}
                        {agent.configLoaded && (
                          <div className="ag-chips ag-row__chips">
                            {agent.info.model && <Chip mono title={t('facts.model')}>{agent.info.model}</Chip>}
                            {agent.info.memory.length > 0 && <LibraryChips info={agent.info} kind="memory" />}
                            {agent.info.skills.length > 0 && <LibraryChips info={agent.info} kind="skills" />}
                          </div>
                        )}
                      </div>
                      <div className="ag-row__record">
                        <RunStrip runs={agent.runs} />
                        <RecordLine runs={agent.runs} />
                      </div>
                      <div className="ag-row__end">
                        <Link className="btn btn-secondary btn-sm" to={agentPath(name)}>{t('actions.open')}</Link>
                        <ActionMenu
                          ariaLabel={t('actions.moreFor', { name })}
                          items={[
                            { key: 'edit', icon: 'edit', label: t('actions.edit'), onClick: () => navigate(`/app/agents/${encodeURIComponent(name)}/edit`) },
                            { key: 'pause', icon: isActive ? 'pause' : 'play', label: isActive ? t('actions.pause') : t('actions.resume'), onClick: () => handlePauseResume(agent) },
                            { key: 'status', icon: 'chart-bar', label: t('actions.statusCount', { count: agent.eventsCount }), onClick: () => navigate(`/app/agents/${encodeURIComponent(name)}/status`) },
                            { key: 'export', icon: 'download', label: t('actions.export'), onClick: () => handleExport(name) },
                            { divider: true },
                            { key: 'delete', icon: 'trash', label: t('actions.delete'), danger: true, onClick: () => handleDelete(name) },
                          ]}
                        />
                      </div>
                    </article>
                  )
                })}
              </div>
            )}
            <p className="ag-note ag-note--gap">{t('record.where')}</p>
          </section>
        </div>
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
              <table>
                <thead>
                  <tr>
                    <th>{t('table.name')}</th>
                    <th>{t('table.status')}</th>
                    <th>{t('table.actions')}</th>
                  </tr>
                </thead>
                <tbody>
                  {(items || []).map(a => {
                    const isActive = a.active === true
                    return (
                      <tr key={a.name}>
                        <td>
                          <Link to={agentPath(a.name, userId)}>{a.name}</Link>
                        </td>
                        <td><StatusMark status={isActive ? 'ready' : 'paused'} /></td>
                        <td>
                          <div className="ag-row__end">
                            <button
                              className="btn btn-secondary btn-sm"
                              onClick={() => handlePauseResume(a, userId)}
                              title={isActive ? t('actions.pause') : t('actions.resume')}
                              aria-label={isActive ? t('actions.pause') : t('actions.resume')}
                            >
                              <Icon name={isActive ? 'pause' : 'play'} />
                            </button>
                            <button
                              className="btn btn-secondary btn-sm"
                              onClick={() => navigate(`/app/agents/${encodeURIComponent(a.name)}/edit?user_id=${encodeURIComponent(userId)}`)}
                              title={t('actions.edit')}
                              aria-label={t('actions.edit')}
                            >
                              <Icon name="pencil" />
                            </button>
                            <button
                              className="btn btn-secondary btn-sm"
                              onClick={() => handleExport(a.name, userId)}
                              title={t('actions.export')}
                              aria-label={t('actions.export')}
                            >
                              <Icon name="export" />
                            </button>
                            <button
                              className="btn btn-danger btn-sm"
                              onClick={() => handleDelete(a.name, userId)}
                              title={t('actions.delete')}
                              aria-label={t('actions.delete')}
                            >
                              <Icon name="trash" />
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
