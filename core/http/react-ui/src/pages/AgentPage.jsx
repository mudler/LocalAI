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
import Icon from '../components/Icon'
// eslint-disable-next-line no-unused-vars
import { Button, EmptyState, LoadError, Page, PageHeader, SkeletonRows } from '../components/ui'
import { deleteAgentWithUndo } from '../components/agents/agentUndo'
import { firstLine } from '../utils/agentRuns'
import './agents.css'

// An agent as a home: what it is, its record, a button to chat with it, the
// triggers that start it, and the chats that ran with it. The facts come from
// its saved definition; a run is a chat with this agent_id.
export default function AgentPage() {
  const { id } = useParams()
  const { t } = useTranslation(['agents', 'common'])
  const { addToast } = useOutletContext()
  const navigate = useNavigate()

  const [agent, setAgent] = useState(null)
  const [chats, setChats] = useState([])
  const [triggers, setTriggers] = useState([])
  const [missing, setMissing] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [showAll, setShowAll] = useState(false)

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
      setError(null)
    } catch (err) {
      // Said in the page, not as a toast; a later poll that fails keeps
      // what is on screen and says so above it.
      setError(err)
    } finally {
      setLoading(false)
    }
  }, [id])

  useEffect(() => {
    load()
    const interval = setInterval(load, 5000)
    return () => clearInterval(interval)
  }, [load])

  const info = useMemo(() => agentInfo(agent), [agent])
  const runs = useMemo(() => chats.map(runOfChat).sort((a, b) => b.startedAt - a.startedAt), [chats])
  const state = runs.some(r => recordStatus(r) === 'running') ? 'running' : 'ready'

  // Delete waits out its Undo window; the list it returns to hides the agent.
  const remove = (a) => {
    deleteAgentWithUndo(a, { addToast, t })
    navigate('/app/agents')
  }

  const back = <Link className="ag-back" to="/app/agents"><Icon name="arrow-left" /> {t('agent.back')}</Link>

  if (loading || (error && !agent)) {
    return (
      <Page className="ag-page">
        <div className="ag-home">
          {back}
          {error
            ? <LoadError title={t('load.agent')} error={error} onRetry={load} />
            : <SkeletonRows count={4} />}
        </div>
      </Page>
    )
  }

  if (missing) {
    return (
      <Page className="ag-page">
        <div className="ag-home" data-testid="agent-missing">
          {back}
          <PageHeader title={t('agent.missingTitle', { name: id })} supporting={t('agent.missingText')} />
          <div><Button variant="primary" icon="arrow-left" to="/app/agents">{t('agent.backToList')}</Button></div>
        </div>
      </Page>
    )
  }

  const shown = showAll ? runs : runs.slice(0, STRIP_LENGTH)
  const hasTools = info.tools.length + info.mcp.length + info.connectors.length > 0

  return (
    <Page className="ag-page" data-testid="agent-page">
      <div className="ag-home">
        {back}
        <PageHeader
          title={agent.name}
          supporting={info.description || undefined}
          actions={(
            <div className="ag-bar__acts">
              <StatusMark status={state} />
              <Button variant="primary" icon="chat" onClick={() => navigate('/app/chats', { state: { agentId: agent.id } })} data-testid="agent-chat">
                {t('actions.chat')}
              </Button>
              <Button icon="edit" to={agentPath(agent.id, '/edit')}>{t('actions.edit')}</Button>
              <ActionMenu
                ariaLabel={t('actions.moreFor', { name: agent.name })}
                items={[
                  { key: 'export', icon: 'download', label: t('actions.export'), onClick: () => downloadAgentFile(agent, addToast, t) },
                  { divider: true },
                  { key: 'delete', icon: 'trash', label: t('actions.delete'), danger: true, onClick: () => remove(agent) },
                ]}
              />
            </div>
          )}
        />

        {error && <LoadError title={t('load.agentStale')} error={error} onRetry={load} />}

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

        <section className="ag-group" aria-labelledby="ag-triggers-h" data-testid="agent-triggers">
          <div className="ag-section-head">
            <h2 className="ag-h2" id="ag-triggers-h">{t('agent.triggers')}</h2>
            <Button variant="ghost" size="sm" icon="plus" to="/app/triggers/new">{t('agent.newTrigger')}</Button>
          </div>
          {triggers.length === 0 ? (
            <p className="ag-note">{t('agent.noTriggers')}</p>
          ) : (
            <div className="ag-runs">
              {triggers.map(x => (
                <Link key={x.id} className="ag-run-row" to={`/app/triggers/${encodeURIComponent(x.id)}`}>
                  <StatusMark status={x.enabled ? 'done' : 'paused'} label={t(x.enabled ? 'agent.triggerOn' : 'agent.triggerOff')} />
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

        <section className="ag-group" aria-labelledby="ag-runs-h">
          <div className="ag-section-head">
            <h2 className="ag-h2" id="ag-runs-h">{t('agent.runs')}</h2>
            {runs.length > 0 && <span className="ag-muted ag-small">{t('agent.runsCount', { count: runs.length })}</span>}
          </div>
          {runs.length === 0 ? (
            <EmptyState level={3} title={t('agent.noRunsTitle')} body={t('agent.noRunsText')} />
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
            <div><Button variant="ghost" size="sm" onClick={() => setShowAll(v => !v)}>
              {showAll ? t('agent.showFewer') : t('agent.showOlder', { count: runs.length - STRIP_LENGTH })}
            </Button></div>
          )}
          <p className="ag-note ag-note--gap">{t('record.where')}</p>
        </section>
      </div>

    </Page>
  )
}
