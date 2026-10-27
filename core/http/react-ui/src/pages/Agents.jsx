import { useState, useEffect, useCallback, useMemo } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link, useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { chatAgentsApi, chatsApi } from '../utils/api'
// eslint-disable-next-line no-unused-vars
import PageHeader from '../components/PageHeader'
// eslint-disable-next-line no-unused-vars
import ChatDeleteConfirm from '../components/ChatDeleteConfirm'
// eslint-disable-next-line no-unused-vars
import ActionMenu from '../components/ActionMenu'
import Icon from '../components/Icon'
// eslint-disable-next-line no-unused-vars
import { StatusMark, RunStrip, RecordLine, Chip, LibraryChips, agentPath } from '../components/agents/AgentBits'
import { agentInfo } from '../utils/agentInfo'
import { outcomeLine, recordStatus, runOfChat } from '../utils/agentRuns'
import { AGENT_TEMPLATES, exportAgent, importAgent } from '../utils/agentConfigTools'
import { downloadJSON } from '../utils/download'
import './agents.css'

const DAY = 24 * 60 * 60 * 1000

export default function Agents() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const { t } = useTranslation('agents')
  const [agents, setAgents] = useState([])
  const [chats, setChats] = useState([])
  const [loading, setLoading] = useState(true)
  const [off, setOff] = useState('')
  const [search, setSearch] = useState('')
  const [toDelete, setToDelete] = useState(null)

  const load = useCallback(async () => {
    try {
      const [list, convs] = await Promise.all([
        chatAgentsApi.list(),
        chatsApi.list(200).catch(() => ({ conversations: [] })),
      ])
      setAgents(list.agents || [])
      setChats(convs.conversations || [])
      setOff('')
    } catch (err) {
      if (err?.status === 409 && err.body?.error?.type === 'agents_off') setOff(err.body.error.message)
      else addToast(t('toasts.loadFailed', { message: err.message }), 'error')
    } finally {
      setLoading(false)
    }
  }, [addToast, t])

  useEffect(() => {
    load()
    const interval = setInterval(load, 5000)
    return () => clearInterval(interval)
  }, [load])

  // The runs of an agent are the chats that ran with it, newest first.
  const rows = useMemo(() => agents.map(a => ({
    ...a,
    info: agentInfo(a),
    runs: chats.filter(c => c.agent_id === a.id && c.kind !== 'subagent').map(runOfChat).sort((x, y) => y.startedAt - x.startedAt),
  })), [agents, chats])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return rows
    return rows.filter(a => a.name.toLowerCase().includes(q) || a.info.description.toLowerCase().includes(q) || a.info.model.toLowerCase().includes(q))
  }, [rows, search])

  // Now: agents with a turn in flight, and agents whose latest run failed in
  // the last day.
  const now = useMemo(() => {
    const items = []
    const at = Date.now()
    for (const a of rows) {
      const live = a.runs.find(r => recordStatus(r) === 'running')
      if (live) {
        items.push({ kind: 'running', agent: a, run: live })
        continue
      }
      const last = a.runs[0]
      if (last && recordStatus(last) === 'failed' && at - last.startedAt < DAY) items.push({ kind: 'failed', agent: a, run: last })
    }
    return items
  }, [rows])

  const handleDelete = async (agent) => {
    setToDelete(null)
    try {
      await chatAgentsApi.remove(agent.id)
      addToast(t('toasts.deleted', { name: agent.name }), 'success')
      load()
    } catch (err) {
      addToast(t('toasts.deleteFailed', { message: err.message }), 'error')
    }
  }

  const handleExport = (agent) => {
    downloadJSON(`${agent.name}.json`, exportAgent(agent))
    addToast(t('toasts.exported', { name: agent.name }), 'success')
  }

  const handleImport = async (e) => {
    const file = e.target.files?.[0]
    if (!file) return
    try {
      const read = importAgent(JSON.parse(await file.text()))
      if (!read) throw new Error(t('toasts.notAnAgent'))
      navigate('/app/agents/new', { state: { importedConfig: read } })
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
            {/* A label styled as a button, wrapping the file input it triggers,
                so the control looks and behaves like its neighbours. */}
            {importLabel}
            <button className="btn btn-primary" onClick={() => navigate('/app/agents/new')}>
              <Icon name="plus" /> {t('actions.createAgent')}
            </button>
          </div>
        }
      />

      {off ? (
        <div className="ag-empty" role="note"><p>{off}</p></div>
      ) : loading ? (
        <div className="loading-center">
          <Icon name="spinner" spin className="icon-xl text-primary" />
        </div>
      ) : agents.length === 0 ? (
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
          <div className="ag-empty__acts ag-empty__acts--gap">
            <button className="btn btn-primary" onClick={() => navigate('/app/agents/new')}>
              <Icon name="plus" /> {t('actions.createAgent')}
            </button>
            {importLabel}
          </div>
        </div>
      ) : (
        <div className="ag-launch">
          {now.length > 0 && (
            <section aria-labelledby="ag-now-h" data-testid="agents-now">
              <div className="ag-section-head"><h2 className="ag-eyebrow" id="ag-now-h">{t('now.title')}</h2></div>
              <div className="ag-now">
                {now.map(item => (
                  <div key={`${item.kind}-${item.agent.id}`} className="ag-now__item" data-state={item.kind}>
                    <div className="ag-now__main">
                      <StatusMark status={item.kind === 'running' ? 'running' : 'failed'} label={t(item.kind === 'running' ? 'now.working' : 'now.failed')} />
                      <span className="ag-now__name">{item.agent.name}</span>
                      <span className="ag-now__line">{item.kind === 'running' ? item.run.turns[0].task : outcomeLine(item.run, 140) || item.run.turns[0].task}</span>
                    </div>
                    <div className="ag-now__acts">
                      <Link className="btn btn-secondary btn-sm" to={`/app/chats/${encodeURIComponent(item.run.chatId)}`}>
                        {t('now.open')}
                      </Link>
                    </div>
                  </div>
                ))}
              </div>
            </section>
          )}

          <section aria-labelledby="ag-mine-h">
            <div className="ag-section-head">
              <h2 className="ag-eyebrow" id="ag-mine-h">{t('sections.agents')}</h2>
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
                  const state = agent.runs.some(r => recordStatus(r) === 'running') ? 'running' : 'ready'
                  return (
                    <article key={agent.id} className="ag-row" data-agent={agent.name}>
                      <div className="ag-row__main">
                        <div className="ag-row__head">
                          <Link className="ag-row__name" to={agentPath(agent.id)}>{agent.name}</Link>
                          <StatusMark status={state} />
                        </div>
                        {agent.info.description && <span className="ag-row__desc">{agent.info.description}</span>}
                        <div className="ag-chips ag-row__chips">
                          {agent.info.model && <Chip mono title={t('facts.model')}>{agent.info.model}</Chip>}
                          {agent.info.tools.length > 0 && <Chip icon="puzzle" count={agent.info.tools.length}>{t('facts.addons')}</Chip>}
                          {agent.info.memory.length > 0 && <LibraryChips info={agent.info} kind="memory" />}
                          {agent.info.skills.length > 0 && <LibraryChips info={agent.info} kind="skills" />}
                        </div>
                      </div>
                      <div className="ag-row__record">
                        <RunStrip runs={agent.runs} />
                        <RecordLine runs={agent.runs} />
                      </div>
                      <div className="ag-row__end">
                        <button type="button" className="btn btn-secondary btn-sm" onClick={() => navigate('/app/chats', { state: { agentId: agent.id } })}>
                          {t('actions.chat')}
                        </button>
                        <ActionMenu
                          ariaLabel={t('actions.moreFor', { name: agent.name })}
                          items={[
                            { key: 'open', icon: 'eye', label: t('actions.open'), onClick: () => navigate(agentPath(agent.id)) },
                            { key: 'edit', icon: 'edit', label: t('actions.edit'), onClick: () => navigate(agentPath(agent.id, '/edit')) },
                            { key: 'export', icon: 'download', label: t('actions.export'), onClick: () => handleExport(agent) },
                            { divider: true },
                            { key: 'delete', icon: 'trash', label: t('actions.delete'), danger: true, onClick: () => setToDelete(agent) },
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

      <ChatDeleteConfirm item={toDelete} kind="agent" onConfirm={handleDelete} onCancel={() => setToDelete(null)} />
    </div>
  )
}
