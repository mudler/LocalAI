/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Button, EmptyState, LoadError, SkeletonRows, StatusChip } from '../ui'
import { chatAgentsApi, chatProjectsApi } from '../../utils/api'
import { agentPayload } from '../../utils/chatAgents'
import { unavailableReason } from '../../utils/projectAgents'
import './projectagents.css'

const agentLink = (id, tail = '') => `/app/agents/${encodeURIComponent(id)}${tail}`

// The Agents tab: the agents created from the project, each with whether it
// can work now, how it uses the files and whether it may write to the memory.
// New agent opens the editor with the project chosen. An agent whose project
// folders were replaced has a Reconnect button: it saves the agent again with
// no folder list, which makes the server link the two project folders again.
export default function ProjectAgents({ project, archived, addToast, onChanged }) {
  const { t } = useTranslation('projects')
  const navigate = useNavigate()
  const [rows, setRows] = useState(null)
  const [error, setError] = useState(null)
  const [busy, setBusy] = useState('')

  const load = useCallback(async () => {
    try {
      const res = await chatProjectsApi.agents(project.id)
      setRows(res.agents || [])
      setError(null)
    } catch (err) {
      setError(err)
    }
  }, [project.id])

  useEffect(() => { load() }, [load])

  const reconnect = async (row) => {
    setBusy(row.id)
    try {
      // The agent as saved, written back with no `folders` and no `project_id`:
      // the server keeps the project and links its two folders again.
      const agent = await chatAgentsApi.get(row.id)
      await chatAgentsApi.update(row.id, agentPayload(agent))
      addToast(t('agents.reconnected', { name: row.name }), 'success')
      await load()
      onChanged?.()
    } catch (err) {
      addToast(t('agents.reconnectFailed', { message: err.message }), 'error')
    } finally {
      setBusy('')
    }
  }

  const newAgent = (
    <Button variant="primary" icon="plus" disabled={archived} onClick={() => navigate(`/app/agents/new?project=${encodeURIComponent(project.id)}`)} data-testid="project-agent-new">
      {t('agents.new')}
    </Button>
  )

  if (error?.status === 403 && !rows) return <div data-testid="project-agents-denied"><EmptyState variant="unavailable" title={t('access.agents')} body={t('access.agentsBody')} /></div>
  if (error && !rows) return <LoadError title={t('load.agents')} error={error} onRetry={load} />
  if (!rows) return <SkeletonRows count={3} />

  return (
    <section className="pj-section" data-testid="project-agents">
      <div className="pj-head">
        <h2 className="pj-h2">{t('agents.title')}</h2>
        {rows.length > 0 && newAgent}
      </div>
      <p className="pj-note">{t('agents.lead')}</p>
      {archived && <p className="pj-note" data-testid="project-agents-archived">{t('agents.archivedHint')}</p>}
      {error && <LoadError title={t('load.agentsStale')} error={error} onRetry={load} />}
      {rows.length === 0 ? (
        <div data-testid="project-agents-empty">
          <EmptyState variant="no-results" icon="robot" title={t('agents.empty')} body={t('agents.emptyBody')} actions={newAgent} />
        </div>
      ) : (
        <ul className="pj-list" data-testid="project-agent-list">
          {rows.map(row => {
            const why = row.unavailable ? unavailableReason(project, true) : ''
            return (
              <li key={row.id} className="pj-line pjag-row" data-testid="project-agent-row" data-agent={row.name} data-unavailable={why || undefined}>
                <span className="pj-tile" aria-hidden="true"><Icon name="robot" /></span>
                <div className="pjag-main">
                  <Link className="pj-line__name" to={agentLink(row.id)}>{row.name}</Link>
                  {row.description && <span className="pjag-desc">{row.description}</span>}
                  {row.model && <span className="pj-mono pj-muted">{row.model}</span>}
                  <span className="pjag-chips">
                    {why
                      ? <StatusChip tone="warn" icon="alert-circle" label={t('agents.unavailable')} testId="project-agent-state" />
                      : <StatusChip tone="ok" label={t('agents.ready')} testId="project-agent-state" />}
                    <span className="pjag-chip" data-testid="project-agent-files"><Icon name={row.files_mode === 'read' ? 'eye' : 'edit'} />{t(row.files_mode === 'read' ? 'agents.filesRead' : 'agents.filesWrite')}</span>
                    {row.memory_write && <span className="pjag-chip" data-testid="project-agent-memory"><Icon name="database" />{t('agents.remembers')}</span>}
                  </span>
                  {why && <span className="pjag-why" data-testid="project-agent-reason"><Icon name="warning" />{t(`agents.reason.${why}`)}</span>}
                </div>
                <div className="pjag-end">
                  {why === 'changed'
                    ? <Button size="sm" icon="refresh" busy={busy === row.id} onClick={() => reconnect(row)} data-testid="project-agent-reconnect">{t('agents.reconnect')}</Button>
                    : <Button size="sm" icon="chat" disabled={Boolean(why)} onClick={() => navigate('/app/chats', { state: { agentId: row.id } })} data-testid="project-agent-chat">{t('agents.chat')}</Button>}
                </div>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}
