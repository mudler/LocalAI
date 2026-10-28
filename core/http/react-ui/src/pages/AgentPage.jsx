import { useCallback, useEffect, useMemo, useState } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link, useNavigate, useOutletContext, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { chatAgentsApi, chatTriggersApi } from '../utils/api'
import { agentInfo } from '../utils/agentInfo'
import { downloadAgentFile } from '../utils/agentFiles'
import { outcomeLine, recordStatus, runOfChat, STRIP_LENGTH, titleOf } from '../utils/agentRuns'
// eslint-disable-next-line no-unused-vars
import { StatusMark, RunStrip, RecordLine, Chip, LibraryChips, formatWhen, agentPath } from '../components/agents/AgentBits'
// eslint-disable-next-line no-unused-vars
import ActionMenu from '../components/ActionMenu'
// eslint-disable-next-line no-unused-vars
import ChatDeleteConfirm from '../components/ChatDeleteConfirm'
import Icon from '../components/Icon'
import { firstLine } from '../utils/agentRuns'
import './agents.css'

// An agent as a home: what it is, its record, a button to chat with it, the
// triggers that start it, and the chats that ran with it. The facts come from
// its saved definition; a run is a chat with this agent_id.
export default function AgentPage() {
  const { id } = useParams()
  const { t } = useTranslation('agents')
  const { addToast } = useOutletContext()
  const navigate = useNavigate()

  const [agent, setAgent] = useState(null)
  const [chats, setChats] = useState([])
  const [triggers, setTriggers] = useState([])
  const [missing, setMissing] = useState(false)
  const [loading, setLoading] = useState(true)
  const [showAll, setShowAll] = useState(false)
  const [toDelete, setToDelete] = useState(null)

  const load = useCallback(async () => {
    try {
      // The address holds the id; an old address held the name, so it is found too.
      const list = (await chatAgentsApi.list()).agents || []
      const found = list.find(a => a.id === id) || list.find(a => a.name === id)
      if (!found) { setMissing(true); return }
      setAgent(found)
      setMissing(false)
      // The server's run history holds the chats you had with the agent and
      // the ones its triggers and connectors started.
      const [hist, trig] = await Promise.all([
        chatAgentsApi.runs(found.id, 200).catch(() => ({ runs: [] })),
        chatTriggersApi.list().catch(() => ({ triggers: [] })),
      ])
      setChats((hist.runs || []).map(r => ({ ...r, id: r.chat_id })))
      setTriggers((trig.triggers || []).filter(x => x.agent_id === found.id))
    } catch (err) {
      addToast(t('toasts.loadFailed', { message: err.message }), 'error')
    } finally {
      setLoading(false)
    }
  }, [id, addToast, t])

  useEffect(() => {
    load()
    const interval = setInterval(load, 5000)
    return () => clearInterval(interval)
  }, [load])

  const info = useMemo(() => agentInfo(agent), [agent])
  const runs = useMemo(() => chats.map(runOfChat).sort((a, b) => b.startedAt - a.startedAt), [chats])
  const state = runs.some(r => recordStatus(r) === 'running') ? 'running' : 'ready'

  const remove = async (a) => {
    setToDelete(null)
    try {
      await chatAgentsApi.remove(a.id)
      addToast(t('toasts.deleted', { name: a.name }), 'success')
      navigate('/app/agents')
    } catch (err) {
      addToast(t('toasts.deleteFailed', { message: err.message }), 'error')
    }
  }

  if (loading) {
    return <div className="page page--medium loading-center"><Icon name="spinner" spin className="icon-xl text-primary" /></div>
  }

  if (missing) {
    return (
      <div className="page page--medium ag-page">
        <div className="ag-home">
          <Link className="ag-back" to="/app/agents"><Icon name="arrow-left" /> {t('agent.back')}</Link>
          <div className="ag-empty" data-testid="agent-missing">
            <h1 className="ag-title">{t('agent.missingTitle', { name: id })}</h1>
            <p>{t('agent.missingText')}</p>
            <Link className="btn btn-primary" to="/app/agents">{t('agent.back')}</Link>
          </div>
        </div>
      </div>
    )
  }

  const shown = showAll ? runs : runs.slice(0, STRIP_LENGTH)
  const hasTools = info.tools.length + info.mcp.length + info.connectors.length > 0

  return (
    <div className="page page--medium ag-page" data-testid="agent-page">
      <div className="ag-home">
        <div className="ag-bar">
          <Link className="ag-back" to="/app/agents"><Icon name="arrow-left" /> {t('agent.back')}</Link>
          <div className="ag-bar__acts">
            <button type="button" className="btn btn-primary btn-sm" onClick={() => navigate('/app/chats', { state: { agentId: agent.id } })} data-testid="agent-chat">
              <Icon name="message" /> {t('actions.chat')}
            </button>
            <Link className="btn btn-secondary btn-sm" to={agentPath(agent.id, '/edit')}>
              <Icon name="edit" /> {t('actions.edit')}
            </Link>
            <ActionMenu
              ariaLabel={t('actions.moreFor', { name: agent.name })}
              items={[
                { key: 'export', icon: 'download', label: t('actions.export'), onClick: () => downloadAgentFile(agent, addToast, t) },
                { divider: true },
                { key: 'delete', icon: 'trash', label: t('actions.delete'), danger: true, onClick: () => setToDelete(agent) },
              ]}
            />
          </div>
        </div>

        <header>
          <div className="ag-title">
            <h1>{agent.name}</h1>
            <StatusMark status={state} />
          </div>
          {info.description && <p className="ag-lede">{info.description}</p>}
        </header>

        <dl className="ag-facts" data-testid="agent-facts">
          <dt>{t('facts.model')}</dt>
          <dd className="ag-facts__model">{info.model || <span className="ag-muted">{t('facts.noModel')}</span>}</dd>
          <dt>{t('facts.tools')}</dt>
          <dd>
            {hasTools
              ? <>
                {info.tools.map(x => <Chip key={x} mono>{x}</Chip>)}
                {info.mcp.map(x => <Chip key={`mcp-${x}`} mono>{`mcp: ${x}`}</Chip>)}
                {info.connectors.map(x => <Chip key={`conn-${x}`} to="/app/mcp-connectors" icon="plug">{t('facts.connector')}</Chip>)}
              </>
              : <span className="ag-muted">{t('chips.none')}</span>}
          </dd>
          <dt>{t('facts.memory')}</dt>
          <dd><LibraryChips info={info} kind="memory" /></dd>
          <dt>{t('facts.skills')}</dt>
          <dd><LibraryChips info={info} kind="skills" /></dd>
          {info.instructions && (
            <>
              <dt>{t('facts.instructions')}</dt>
              <dd className="ag-facts__text">{firstLine(info.instructions, 220)}</dd>
            </>
          )}
          <dt>{t('facts.record')}</dt>
          <dd><span className="ag-record"><RunStrip runs={runs} /><RecordLine runs={runs} /></span></dd>
        </dl>

        <section aria-labelledby="ag-triggers-h" data-testid="agent-triggers">
          <div className="ag-section-head">
            <h2 className="ag-eyebrow" id="ag-triggers-h">{t('agent.triggers')}</h2>
            <Link className="ag-link ag-small" to="/app/triggers/new">{t('agent.newTrigger')}</Link>
          </div>
          {triggers.length === 0 ? (
            <p className="ag-note">{t('agent.noTriggers')}</p>
          ) : (
            <div className="ag-runs">
              {triggers.map(x => (
                <Link key={x.id} className="ag-run-row" to={`/app/triggers/${encodeURIComponent(x.id)}`}>
                  <StatusMark status={x.enabled ? 'ready' : 'paused'} label={t(x.enabled ? 'agent.triggerOn' : 'agent.triggerOff')} />
                  <span className="ag-run-row__main">
                    <span className="ag-run-row__title">{x.name}</span>
                    <span className="ag-run-row__line">{x.summary}</span>
                  </span>
                  <span className="ag-run-row__when">{x.last_run ? formatWhen(Date.parse(x.last_run.started || x.last_run.due)) : ''}</span>
                  <Icon name="chevron-right" />
                </Link>
              ))}
            </div>
          )}
        </section>

        <section aria-labelledby="ag-runs-h">
          <div className="ag-section-head">
            <h2 className="ag-eyebrow" id="ag-runs-h">{t('agent.runs')}</h2>
            {runs.length > 0 && <span className="ag-muted ag-small">{t('agent.runsCount', { count: runs.length })}</span>}
          </div>
          {runs.length === 0 ? (
            <div className="ag-empty">
              <h3>{t('agent.noRunsTitle')}</h3>
              <p>{t('agent.noRunsText')}</p>
            </div>
          ) : (
            <div className="ag-runs" data-testid="agent-runs">
              {shown.map(r => {
                const st = recordStatus(r)
                const first = r.turns[0]
                return (
                  <Link key={r.id} className="ag-run-row" to={`/app/chats/${encodeURIComponent(r.chatId)}`} data-run={r.id}>
                    <StatusMark status={st} />
                    <span className="ag-run-row__main">
                      <span className="ag-run-row__title">{titleOf(first.task, 90) || t('agent.untitled')}</span>
                      <span className="ag-run-row__line">{st === 'running' ? t('run.workingLine') : r.origin ? t(`agent.origin.${r.origin}`, { defaultValue: r.origin }) : outcomeLine(r)}</span>
                    </span>
                    <span className="ag-run-row__when">{formatWhen(r.startedAt)}</span>
                    <Icon name="chevron-right" />
                  </Link>
                )
              })}
            </div>
          )}
          {runs.length > STRIP_LENGTH && (
            <button type="button" className="ag-btn-quiet" onClick={() => setShowAll(v => !v)}>
              {showAll ? t('agent.showFewer') : t('agent.showOlder', { count: runs.length - STRIP_LENGTH })}
            </button>
          )}
          <p className="ag-note ag-note--gap">{t('record.where')}</p>
        </section>
      </div>

      <ChatDeleteConfirm item={toDelete} kind="agent" onConfirm={remove} onCancel={() => setToDelete(null)} />
    </div>
  )
}
