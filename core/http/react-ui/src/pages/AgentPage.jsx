import { useCallback, useEffect, useMemo, useState } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link, useNavigate, useOutletContext, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { chatAgentsApi, chatConnectorsApi, chatTriggersApi } from '../utils/api'
import { bindingsOfAgent } from '../utils/chatConnectors'
import { agentInfo } from '../utils/agentInfo'
import { downloadAgentFile } from '../utils/agentFiles'
import { outcomeLine, recordStatus, runOfChat, STRIP_LENGTH, titleOf } from '../utils/agentRuns'
// eslint-disable-next-line no-unused-vars
import { StatusMark, RunStrip, RecordLine, Chip, FolderChip, LibraryChips, formatWhen, agentPath } from '../components/agents/AgentBits'
// eslint-disable-next-line no-unused-vars
import ActionMenu from '../components/ActionMenu'
import Icon from '../components/Icon'
// eslint-disable-next-line no-unused-vars
import StatusChip from '../components/ui/StatusChip'
// eslint-disable-next-line no-unused-vars
import { Button, Callout, EmptyState, LoadError, Page, PageHeader, SkeletonRows } from '../components/ui'
// eslint-disable-next-line no-unused-vars
import AgentSetupCard from '../components/agents/AgentSetupCard'
import { useAgentDelete } from '../components/agents/useAgentDelete'
import { firstLine } from '../utils/agentRuns'
import { filesOff, folderState } from '../utils/chatAgents'
import { agentShareEntry, canShareAgent, isSharedWithMe } from '../utils/agentShare'
// eslint-disable-next-line no-unused-vars
import { SharedByBadge } from '../components/share/ShareBadges'
// eslint-disable-next-line no-unused-vars
import { AgentNeedsList, AgentShareDialog } from '../components/agents/AgentShare'
import './agents.css'

// The chip tone of each state of a binding.
const BINDING_TONE = { ok: 'ok', off: 'idle', attention: 'warn' }

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
  // The bindings of messaging apps that answer as this agent; null while unknown
  // or when the server has connectors off, so the section is left out.
  const [bindings, setBindings] = useState(null)
  // What the server says about connectors, for the Finish setup card.
  const [connectorData, setConnectorData] = useState({ kinds: [], items: null })
  const [missing, setMissing] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [showAll, setShowAll] = useState(false)
  const [sharing, setSharing] = useState(false)

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
      // The triggers and connectors of an agent shared with you are the
      // owner's: none of yours can run it, so they are not read.
      const shared = isSharedWithMe(found)
      const [hist, trig, kinds, conns] = await Promise.all([
        chatAgentsApi.runs(found.id, 200).catch(() => ({ runs: [] })),
        shared ? { triggers: [] } : chatTriggersApi.list().catch(() => ({ triggers: [] })),
        shared ? null : chatConnectorsApi.kinds().catch(() => null),
        shared ? null : chatConnectorsApi.list().catch(() => null),
      ])
      setConnectorData({ kinds: kinds?.kinds || [], items: conns ? (conns.connectors || []) : null })
      setBindings(conns ? bindingsOfAgent(found.id, kinds?.kinds || [], conns.connectors || []) : null)
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
  const agentDelete = useAgentDelete({ addToast, t })
  const remove = (a) => agentDelete.ask(a, () => navigate('/app/agents'))

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
  // A person the agent is shared with reads it and chats with it; only the
  // owner edits, shares, exports or deletes it.
  const guest = isSharedWithMe(agent)
  const definition = !guest || agent.access?.role === 'viewer'
  const shareEntry = agentShareEntry(agent)

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
              {!guest && <Button icon="edit" to={agentPath(agent.id, '/edit')}>{t('actions.edit')}</Button>}
              {canShareAgent(agent) && <Button icon="share" onClick={() => setSharing(true)} data-testid="agent-share">{t('share.action')}</Button>}
              {!guest && (
                <ActionMenu
                  ariaLabel={t('actions.moreFor', { name: agent.name })}
                  items={[
                    { key: 'export', icon: 'download', label: t('actions.export'), onClick: () => downloadAgentFile(agent, addToast, t) },
                    { divider: true },
                    { key: 'delete', icon: 'trash', label: t('actions.delete'), danger: true, onClick: () => remove(agent) },
                  ]}
                />
              )}
            </div>
          )}
        />
        {shareEntry && (guest || shareEntry.sharedWith > 0) && (
          <div className="ag-shared-line" data-testid="agent-page-shared">
            <SharedByBadge entry={shareEntry} roleLabel={guest ? t(`share.roles.${shareEntry.role}`) : undefined} hideRole={!guest} testId="agent-page-shared-by" />
            {guest && <span className="ag-note">{t('shared.readOnly')}</span>}
          </div>
        )}

        <AgentSetupCard agent={agent} kinds={connectorData.kinds} bindings={connectorData.items} />
        {error && <LoadError title={t('load.agentStale')} error={error} onRetry={load} />}
        {!guest && !filesOff(agent) && (folderState(agent) === 'missing' || folderState(agent) === 'unset') && (
          <Callout
            tone="warn" testId="agent-folder-warning"
            actions={<Button size="sm" icon="folder" to={agentPath(agent.id, '/edit')}>{t('folder.chooseOther')}</Button>}
          >
            {t(folderState(agent) === 'missing' ? 'folder.missing' : 'folder.unset')}
          </Callout>
        )}

        <dl className="ag-facts" data-testid="agent-facts">
          <dt>{t('facts.model')}</dt>
          <dd className="ag-facts__model">{info.model || <span className="ag-muted">{t('facts.noModel')}</span>}</dd>
          {!guest && (
            <>
              <dt>{t('facts.folder')}</dt>
              <dd><FolderChip agent={agent} all /></dd>
            </>
          )}
          {/* With the role use the definition is not shared: what the agent
              uses is in What this agent needs below. */}
          {definition && (
          <>
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
          </>
          )}
          {info.instructions && (
            <>
              <dt>{t('facts.instructions')}</dt>
              <dd className="ag-facts__text">{firstLine(info.instructions, 220)}</dd>
            </>
          )}
          <dt>{t('facts.record')}</dt>
          <dd><span className="ag-record"><RunStrip runs={runs} /><RecordLine runs={runs} /></span></dd>
        </dl>

        {guest && <AgentNeedsList agentId={agent.id} />}

        {!guest && (
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
        )}

        {bindings && (
          <section className="ag-group" aria-labelledby="ag-bindings-h" data-testid="agent-bindings">
            <div className="ag-section-head">
              <h2 className="ag-h2" id="ag-bindings-h">{t('agent.bindings')}</h2>
              <Button variant="ghost" size="sm" icon="puzzle" to="/app/chats/connectors">{t('agent.bindingsAll')}</Button>
            </div>
            {bindings.length === 0 ? (
              <p className="ag-note" data-testid="agent-bindings-empty">
                {t('agent.noBindings')} <Link to="/app/addons">{t('agent.noBindingsLink')}</Link>
              </p>
            ) : (
              <ul className="ag-runs ag-bind-list">
                {bindings.map(b => (
                  <li key={b.id} className="ag-bind-row" data-binding={b.id} data-state={b.state}>
                    <span className="ag-bind-row__main">
                      <span className="ag-bind-row__title">{b.name}</span>
                      <span className="ag-bind-row__line">{b.kindTitle}{b.reason ? ` - ${b.reason}` : ''}</span>
                    </span>
                    <StatusChip className="ag-state" tone={BINDING_TONE[b.state]} label={t(`agent.bindingState.${b.state}`)} testId="agent-binding-state" />
                    <Button size="sm" iconEnd="arrow-right" to={b.href} aria-label={t('agent.openSettingsFor', { name: b.name })}>{t('agent.openSettings')}</Button>
                  </li>
                ))}
              </ul>
            )}
          </section>
        )}

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

      {agentDelete.dialog}
      {sharing && <AgentShareDialog agent={agent} close={() => setSharing(false)} onChanged={load} addToast={addToast} />}
    </Page>
  )
}
