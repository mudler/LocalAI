import { useState, useEffect, useCallback, useMemo } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link, useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { chatAgentsApi, chatsApi } from '../utils/api'
// eslint-disable-next-line no-unused-vars
import ActionMenu from '../components/ActionMenu'
import Icon from '../components/Icon'
// eslint-disable-next-line no-unused-vars
import { Button, Callout, EmptyState, LoadError, Page, PageHeader, SearchField, SkeletonRows, StatusChip } from '../components/ui'
// eslint-disable-next-line no-unused-vars
import { AgentViews, RunStrip, RecordLine, agentPath } from '../components/agents/AgentBits'
import { deleteAgentWithUndo, usePendingAgentDeletes } from '../components/agents/agentUndo'
import { agentInfo } from '../utils/agentInfo'
import { outcomeLine, recordStatus, runOfChat } from '../utils/agentRuns'
import { AGENT_TEMPLATES } from '../utils/agentConfigTools'
import { downloadAgentFile, importAgentFile } from '../utils/agentFiles'
import './agents.css'

const DAY = 24 * 60 * 60 * 1000

export default function Agents() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const { t } = useTranslation(['agents', 'common'])
  const [agents, setAgents] = useState([])
  const [chats, setChats] = useState([])
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState(null)
  const [off, setOff] = useState('')
  const [search, setSearch] = useState('')
  const deleting = usePendingAgentDeletes()
  // The starter agents of the server; null until read, [] when it has none.
  const [templates, setTemplates] = useState(null)
  useEffect(() => {
    chatAgentsApi.templates().then(r => setTemplates(r.templates || [])).catch(() => setTemplates([]))
  }, [])

  // A failed read is said in the list's place, never as a toast and never as
  // an empty list. A later poll that fails keeps the rows it already has.
  const load = useCallback(async () => {
    try {
      const [list, convs] = await Promise.all([
        chatAgentsApi.list(),
        chatsApi.list(200).catch(() => ({ conversations: [] })),
      ])
      setAgents(list.agents || [])
      setChats(convs.conversations || [])
      setOff('')
      setError(null)
      setLoaded(true)
    } catch (err) {
      if (err?.status === 409 && err.body?.error?.type === 'agents_off') setOff(err.body.error.message)
      else setError(err)
    }
  }, [])

  useEffect(() => {
    load()
    const interval = setInterval(load, 5000)
    return () => clearInterval(interval)
  }, [load])

  // The runs of an agent are the chats that ran with it, newest first. An
  // agent waiting out its Undo window is already gone from the list.
  const rows = useMemo(() => agents.filter(a => !deleting.has(a.id)).map(a => ({
    ...a,
    info: agentInfo(a),
    runs: chats.filter(c => c.agent_id === a.id && c.kind !== 'subagent').map(runOfChat).sort((x, y) => y.startedAt - x.startedAt),
  })), [agents, chats, deleting])

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

  // Delete is reversible for the Undo window, so it asks nothing.
  const handleDelete = (agent) => deleteAgentWithUndo(agent, { addToast, t, onDeleted: load })

  const handleExport = (agent) => downloadAgentFile(agent, addToast, t)

  // The server makes the new agent; the page opens it and says what changed.
  const handleImport = async (e) => {
    const file = e.target.files?.[0]
    e.target.value = ''
    if (!file) return
    try {
      const { agent, warnings, dropped } = await importAgentFile(await file.text())
      const notes = [...warnings, ...(dropped.length ? [t('toasts.importDropped', { fields: dropped.join(', ') })] : [])]
      addToast(notes.length ? t('toasts.importedWith', { name: agent.name, notes: notes.join(' ') }) : t('toasts.imported', { name: agent.name }), notes.length ? 'warning' : 'success', 6000)
      navigate(agentPath(agent.id))
    } catch (err) {
      addToast(t('toasts.parseFailed', { message: err.message }), 'error')
    }
  }

  const startFromTemplate = async (tpl) => {
    try {
      const agent = await chatAgentsApi.fromTemplate(tpl.id)
      addToast(t('toasts.fromTemplate', { name: agent.name }), 'success')
      navigate(agentPath(agent.id, '/edit'))
    } catch (err) {
      addToast(t('toasts.templateFailed', { message: err.message }), 'error')
    }
  }

  // A label drawn as a button, wrapping the file input it opens, so the
  // control looks and behaves like its neighbours.
  const importButton = (variant) => (
    <label className={`dk-btn dk-btn--${variant}`}>
      <Icon name="import" className="ui-btn__icon" /> <span className="ui-btn__label">{t('actions.import')}</span>
      <input type="file" accept=".json" hidden onChange={handleImport} />
    </label>
  )
  const newAgent = <Button variant="primary" icon="plus" onClick={() => navigate('/app/agents/new')}>{t('actions.newAgent')}</Button>
  const empty = loaded && !error && rows.length === 0 && agents.length === 0

  return (
    <Page width="full" className="ag-page">
      <PageHeader
        title={t('title')}
        supporting={t('subtitle')}
        actions={!off && !empty && (
          <div className="header-actions">
            {importButton('ghost')}
            {newAgent}
          </div>
        )}
      />
      <AgentViews />

      {off ? (
        <div role="note">
          <EmptyState variant="unavailable" title={off} />
        </div>
      ) : error && !loaded ? (
        <LoadError title={t('load.agents')} error={error} onRetry={load} />
      ) : !loaded ? (
        <SkeletonRows count={3} />
      ) : empty ? (
        <div className="ag-launch" data-testid="agents-empty">
          <EmptyState
            title={t('empty.noConfigured')}
            body={t('empty.noConfiguredText')}
            actions={<>{newAgent}{importButton('ghost')}</>}
          />
          {templates && (templates.length > 0 || AGENT_TEMPLATES.length > 1) && (
            <section className="ag-group" aria-labelledby="ag-start-h">
              <h2 className="ag-eyebrow" id="ag-start-h">{t('empty.startWith')}</h2>
              <ul className="ag-list">
                {templates.length > 0 ? templates.map(x => (
                  <li key={x.id} className="ag-row ag-row--two" data-testid="agent-template">
                    <span className="ag-tile" aria-hidden="true"><Icon name="robot" /></span>
                    <div className="ag-row__main">
                      <span className="ag-row__name">{x.name}</span>
                      <span className="ag-row__desc">{x.description}</span>
                    </div>
                    <div className="ag-row__end">
                      <Button size="sm" onClick={() => startFromTemplate(x)}>{t('empty.useTemplate')}</Button>
                    </div>
                  </li>
                )) : AGENT_TEMPLATES.filter(x => x.id !== 'blank').map(x => (
                  <li key={x.id} className="ag-row ag-row--two">
                    <span className="ag-tile" aria-hidden="true"><Icon name="robot" /></span>
                    <div className="ag-row__main">
                      <Link className="ag-row__name" to={`/app/agents/new?template=${x.id}`}>{t(`templates.${x.id}.label`)}</Link>
                      <span className="ag-row__desc">{x.description}</span>
                    </div>
                    <div className="ag-row__end">
                      <Button size="sm" to={`/app/agents/new?template=${x.id}`}>{t('empty.useTemplate')}</Button>
                    </div>
                  </li>
                ))}
              </ul>
            </section>
          )}
        </div>
      ) : (
        <div className="ag-launch">
          {error && <LoadError title={t('load.agentsStale')} error={error} onRetry={load} />}

          {now.length > 0 && (
            <section className="ag-now" aria-label={t('now.title')} data-testid="agents-now">
              {now.map(item => (
                <Callout
                  key={`${item.kind}-${item.agent.id}`}
                  tone={item.kind === 'running' ? 'info' : 'error'}
                  icon={item.kind === 'running' ? 'spinner' : undefined}
                  className="ag-now__item"
                  data-state={item.kind}
                  title={t(item.kind === 'running' ? 'now.workingTitle' : 'now.failedTitle', { name: item.agent.name })}
                  actions={<Button size="sm" to={`/app/chats/${encodeURIComponent(item.run.chatId)}`}>{t('now.openRun')}</Button>}
                >
                  {item.kind === 'running' ? item.run.turns[0].task : outcomeLine(item.run, 140) || item.run.turns[0].task}
                </Callout>
              ))}
            </section>
          )}

          <section className="ag-group" aria-label={t('sections.agents')}>
            <div className="ag-tools">
              <SearchField value={search} onChange={setSearch} label={t('search.placeholder')} />
              <span className="ag-muted ag-small ag-count">
                {t('search.summary', { shown: filtered.length, total: rows.length, count: rows.length })}
              </span>
            </div>

            {filtered.length === 0 ? (
              <EmptyState
                variant="no-results"
                title={t('empty.noMatching')}
                body={t('empty.noMatchingText', { query: search })}
                actions={<Button variant="ghost" size="sm" onClick={() => setSearch('')}>{t('empty.clearSearch')}</Button>}
              />
            ) : (
              <ul className="ag-list" data-testid="agents-list">
                {filtered.map(agent => <AgentRow key={agent.id} agent={agent} t={t} navigate={navigate} onExport={handleExport} onDelete={handleDelete} />)}
              </ul>
            )}
            <p className="ag-note ag-note--gap">{t('record.where')}</p>
          </section>
        </div>
      )}
    </Page>
  )
}

// One agent: a tile, its name and line, what it may use, how its runs went,
// and Chat. The rest is in the menu.
// eslint-disable-next-line no-unused-vars
function AgentRow({ agent, t, navigate, onExport, onDelete }) {
  const { info, runs } = agent
  const running = runs.some(r => recordStatus(r) === 'running')
  const lastFailed = !running && runs[0] && recordStatus(runs[0]) === 'failed'
  return (
    <li className="ag-row" data-agent={agent.name}>
      <span className="ag-tile" aria-hidden="true"><Icon name="robot" /></span>
      <div className="ag-row__main">
        <Link className="ag-row__name" to={agentPath(agent.id)}>{agent.name}</Link>
        {info.description && <span className="ag-row__desc">{info.description}</span>}
        <span className="ag-meta">
          {info.model
            ? <span className="ag-mono" title={t('facts.model')}>{info.model}</span>
            : <span>{t('meta.anyModel')}</span>}
          {info.tools.length > 0 && <span><Icon name="puzzle" />{t('meta.addons', { count: info.tools.length })}</span>}
          {info.memory.map(m => <Link key={`m-${m}`} to="/app/collections"><Icon name="database" />{m}</Link>)}
          {info.skills.map(s => <Link key={`s-${s}`} to="/app/skills"><Icon name="sparkles" />{s}</Link>)}
        </span>
      </div>
      <div className="ag-row__record">
        {runs.length === 0 ? (
          <StatusChip status="no-runs" />
        ) : (
          <>
            {running && <StatusChip status="starting" tone="accent" label={t('state.running')} />}
            {lastFailed && <StatusChip status="failed" label={t('chips.lastFailed')} />}
            <RunStrip runs={runs} />
            <RecordLine runs={runs} />
          </>
        )}
      </div>
      <div className="ag-row__end">
        <Button size="sm" icon="chat" className="ag-row__chat" onClick={() => navigate('/app/chats', { state: { agentId: agent.id } })}>
          {t('actions.chat')}
        </Button>
        <ActionMenu
          ariaLabel={t('actions.moreFor', { name: agent.name })}
          items={[
            { key: 'open', icon: 'eye', label: t('actions.open'), onClick: () => navigate(agentPath(agent.id)) },
            { key: 'edit', icon: 'edit', label: t('actions.edit'), onClick: () => navigate(agentPath(agent.id, '/edit')) },
            { key: 'export', icon: 'download', label: t('actions.export'), onClick: () => onExport(agent) },
            { divider: true },
            { key: 'delete', icon: 'trash', label: t('actions.delete'), danger: true, onClick: () => onDelete(agent) },
          ]}
        />
      </div>
    </li>
  )
}
