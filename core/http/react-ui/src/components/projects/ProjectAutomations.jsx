/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { BuiltinIcon } from '../addons/AddonBits'
import { Button, Callout, EmptyState, LoadError, SkeletonRows, StatusChip } from '../ui'
import { chatProjectsApi } from '../../utils/api'
import { loopState, nextRunLabel, triggerState } from '../../utils/projectLinks'
import { useProjectPoll } from '../../hooks/useProjectPoll'
import { useAuth } from '../../context/AuthContext'
import { firstProjectAgent } from '../../utils/triggerProjects'
import './projectlinks.css'

const CHIPS = {
  active: { tone: 'ok', icon: 'check-circle' },
  off: { tone: 'idle', icon: 'pause' },
  paused: { tone: 'warn', icon: 'pause' },
  ended: { tone: 'idle', icon: 'check-circle' },
}

// A refusal for lack of a permission (403) is an answer, not a failure: the
// list is left out and the tab says why. Any other error is the tab's error.
const denied = (err) => (err?.status === 403 ? null : Promise.reject(err))

const readAutomations = async (id) => {
  const [tr, lo, ag] = await Promise.all([
    chatProjectsApi.triggers(id).catch(denied),
    chatProjectsApi.loops(id),
    // Only to start a new trigger with an agent; without the agents permission the form is left to choose.
    chatProjectsApi.agents(id).catch(() => null),
  ])
  return { triggers: tr ? tr.triggers || [] : null, loops: lo.loops || [], agents: ag ? ag.agents || [] : null }
}

// The Automations tab: the triggers of the project's agents and the loops in
// its chats. Both are read only here; each opens on its own page, and a
// person without the triggers permission sees the rows without the link (the
// page would refuse them). New trigger opens the form with a project agent
// and its folder chosen. While the project is archived the server holds the
// triggers and loops: an active one reads Paused.
export default function ProjectAutomations({ project, archived, onRestore }) {
  const { t, i18n } = useTranslation('projects')
  const { hasFeature } = useAuth()
  const canTriggers = hasFeature('triggers')
  const { data, error, reload: load } = useProjectPoll(project.id, readAutomations) // { triggers, loops, agents }

  if (error && !data) return <LoadError title={t('load.automations')} error={error} onRetry={load} />
  if (!data) return <SkeletonRows count={3} />

  const chip = (state, testId) => {
    const c = CHIPS[state === 'paused-archived' ? 'paused' : state]
    return <StatusChip tone={c.tone} icon={c.icon} label={t(`state.${state === 'paused-archived' ? 'paused' : state}`)} testId={testId} />
  }

  const nextOf = (tr, state) => {
    if (state === 'paused') return t('automations.pausedWhy')
    // An event trigger has no next time: it runs when an event arrives.
    if (tr.kind === 'event') return null
    if (state === 'off') return t('automations.nextNone')
    if (tr.kind === 'file') return t('automations.nextFile')
    const when = nextRunLabel(tr.next, i18n.language)
    return when ? t('automations.next', { when }) : t('automations.nextNone')
  }

  const starter = firstProjectAgent(data.agents)
  // The project has agents when the list says so; with no list (no permission) the form chooses.
  const noAgents = data.agents !== null && data.agents.length === 0
  const newTrigger = (
    <Button
      variant="primary"
      icon="plus"
      to={`/app/triggers/new?project=${encodeURIComponent(project.id)}`}
      disabled={archived || noAgents || (data.agents !== null && !starter)}
      data-testid="project-trigger-new"
    >
      {t('automations.newTrigger')}
    </Button>
  )
  const newHint = archived ? t('automations.newArchived') : noAgents ? null : (data.agents !== null && !starter) ? t('automations.newUnavailable') : null

  return (
    <div className="pj-stack" data-testid="project-automations">
      {archived && (
        <Callout
          tone="info"
          title={t('automations.archivedTitle')}
          testId="project-automations-archived"
          actions={<Button size="sm" onClick={onRestore} data-testid="project-automations-restore">{t('automations.restore')}</Button>}
        >
          {t('automations.archivedBody')}
        </Callout>
      )}
      {error && <LoadError title={t('load.automationsStale')} error={error} onRetry={load} />}
      <section className="pj-section" data-testid="project-triggers">
        <div className="pj-head">
          <h2 className="pj-h2">{t('automations.triggersTitle')}</h2>
          {canTriggers && data.triggers !== null && data.triggers.length > 0 && newTrigger}
        </div>
        <p className="pj-note">{t('automations.triggersLead')}</p>
        {newHint && canTriggers && <p className="pj-note" data-testid="project-trigger-new-hint">{newHint}</p>}
        {data.triggers === null || !canTriggers ? (
          <div data-testid="project-triggers-denied"><EmptyState variant="unavailable" title={t('automations.triggersDenied')} body={t('automations.triggersDeniedBody')} /></div>
        ) : data.triggers.length === 0 ? (
          <div data-testid="project-triggers-empty">
            <EmptyState
              variant="no-results"
              title={t('automations.triggersNone')}
              body={noAgents ? (
                <>{t('automations.triggersNoneNoAgents')} <Link to={`/app/projects/${encodeURIComponent(project.id)}?tab=agents`}>{t('automations.goAgents')}</Link></>
              ) : t('automations.triggersNoneBody')}
              actions={noAgents ? undefined : newTrigger}
            />
          </div>
        ) : (
          <ul className="pj-list" data-testid="project-trigger-list">
            {data.triggers.map((tr) => {
              const state = triggerState(tr, archived)
              const file = tr.kind === 'file'
              const event = tr.kind === 'event'
              const next = nextOf(tr, state)
              return (
                <li key={tr.id} className={`pjl-row${state === 'active' ? '' : ' pjl-row--paused'}`} data-testid="project-trigger" data-state={state}>
                  <BuiltinIcon name={file ? 'folder' : event ? 'bolt' : 'calendar'} size={40} />
                  <div className="pjl-main">
                    <span className="pjl-head">
                      <span className="pjl-name">{tr.name}</span>
                      <StatusChip tone="idle" icon={file ? 'folder' : event ? 'bolt' : 'clock'} label={t(file ? 'automations.kindFolder' : event ? 'automations.kindEvent' : 'automations.kindSchedule')} testId="project-trigger-kind" />
                      {chip(state, 'project-trigger-state')}
                    </span>
                    <span className="pjl-meta">
                      <span data-testid="project-trigger-when">{file ? <span className="pjl-path">{tr.folder}</span> : event ? t('automations.whenEvent') : tr.schedule}</span>
                      <span data-testid="project-trigger-agent">{t('automations.runs', { agent: tr.agent_name })}</span>
                      {next && <span data-testid="project-trigger-next">{next}</span>}
                    </span>
                  </div>
                  <div className="pjl-end">
                    <Button size="sm" iconEnd="arrow-right" to={`/app/triggers/${encodeURIComponent(tr.id)}`} data-testid="project-trigger-open">{t('connections.open')}</Button>
                  </div>
                </li>
              )
            })}
          </ul>
        )}
      </section>
      <section className="pj-section" data-testid="project-loops">
        <h2 className="pj-h2">{t('automations.loopsTitle')}</h2>
        <p className="pj-note">{t('automations.loopsLead')}</p>
        {data.loops.length === 0 ? (
          <div data-testid="project-loops-empty"><EmptyState variant="no-results" title={t('automations.loopsNone')} body={t('automations.loopsNoneBody')} /></div>
        ) : (
          <ul className="pj-list" data-testid="project-loop-list">
            {data.loops.map((lo) => {
              const state = loopState(lo, archived)
              const href = `/app/chats/${encodeURIComponent(lo.chat_id)}`
              return (
                <li key={lo.id} className={`pjl-row${state === 'active' ? '' : ' pjl-row--paused'}`} data-testid="project-loop" data-state={state}>
                  <BuiltinIcon name="repeat" size={40} />
                  <div className="pjl-main">
                    <span className="pjl-head">
                      <Link className="pjl-name" to={href}>{lo.chat_title || lo.chat_id}</Link>
                      {chip(state, 'project-loop-state')}
                    </span>
                    {lo.prompt && <p className="pjl-prompt" data-testid="project-loop-prompt">{lo.prompt}</p>}
                    {state === 'paused-archived' && <span className="pjl-meta"><span data-testid="project-loop-why">{t('automations.pausedWhy')}</span></span>}
                  </div>
                  <div className="pjl-end">
                    <Button size="sm" iconEnd="arrow-right" to={href} data-testid="project-loop-open">{t('automations.openChat')}</Button>
                  </div>
                </li>
              )
            })}
          </ul>
        )}
      </section>
    </div>
  )
}
