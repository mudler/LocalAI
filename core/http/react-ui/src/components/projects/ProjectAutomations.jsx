/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { BuiltinIcon } from '../addons/AddonBits'
import { Button, Callout, EmptyState, LoadError, SkeletonRows, StatusChip } from '../ui'
import { chatProjectsApi } from '../../utils/api'
import { loopState, nextRunLabel, triggerState } from '../../utils/projectLinks'
import { useProjectPoll } from '../../hooks/useProjectPoll'
import './projectlinks.css'

const CHIPS = {
  active: { tone: 'ok', icon: 'check-circle' },
  off: { tone: 'idle', icon: 'pause' },
  paused: { tone: 'warn', icon: 'pause' },
  ended: { tone: 'idle', icon: 'check-circle' },
}

const readAutomations = async (id) => {
  const [tr, lo] = await Promise.all([chatProjectsApi.triggers(id), chatProjectsApi.loops(id)])
  return { triggers: tr.triggers || [], loops: lo.loops || [] }
}

// The Automations tab: the triggers of the project's agents and the loops in
// its chats. Both are read only here; each opens on its own page. While the
// project is archived the server holds them: an active one reads Paused.
export default function ProjectAutomations({ project, archived, onRestore }) {
  const { t, i18n } = useTranslation('projects')
  const { data, error, reload: load } = useProjectPoll(project.id, readAutomations) // { triggers, loops }

  if (error && !data) return <LoadError title={t('load.automations')} error={error} onRetry={load} />
  if (!data) return <SkeletonRows count={3} />

  const chip = (state, testId) => {
    const c = CHIPS[state === 'paused-archived' ? 'paused' : state]
    return <StatusChip tone={c.tone} icon={c.icon} label={t(`state.${state === 'paused-archived' ? 'paused' : state}`)} testId={testId} />
  }

  const nextOf = (tr, state) => {
    if (state === 'paused') return t('automations.pausedWhy')
    if (state === 'off') return t('automations.nextNone')
    if (tr.kind === 'file') return t('automations.nextFile')
    const when = nextRunLabel(tr.next, i18n.language)
    return when ? t('automations.next', { when }) : t('automations.nextNone')
  }

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
        <h2 className="pj-h2">{t('automations.triggersTitle')}</h2>
        <p className="pj-note">{t('automations.triggersLead')}</p>
        {data.triggers.length === 0 ? (
          <div data-testid="project-triggers-empty"><EmptyState variant="no-results" title={t('automations.triggersNone')} body={t('automations.triggersNoneBody')} /></div>
        ) : (
          <ul className="pj-list" data-testid="project-trigger-list">
            {data.triggers.map((tr) => {
              const state = triggerState(tr, archived)
              const file = tr.kind === 'file'
              return (
                <li key={tr.id} className={`pjl-row${state === 'active' ? '' : ' pjl-row--paused'}`} data-testid="project-trigger" data-state={state}>
                  <BuiltinIcon name={file ? 'folder' : 'calendar'} size={40} />
                  <div className="pjl-main">
                    <span className="pjl-head">
                      <span className="pjl-name">{tr.name}</span>
                      <StatusChip tone="idle" icon={file ? 'folder' : 'clock'} label={t(file ? 'automations.kindFolder' : 'automations.kindSchedule')} testId="project-trigger-kind" />
                      {chip(state, 'project-trigger-state')}
                    </span>
                    <span className="pjl-meta">
                      <span data-testid="project-trigger-when">{file ? <span className="pjl-path">{tr.folder}</span> : tr.schedule}</span>
                      <span data-testid="project-trigger-agent">{t('automations.runs', { agent: tr.agent_name })}</span>
                      <span data-testid="project-trigger-next">{nextOf(tr, state)}</span>
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
