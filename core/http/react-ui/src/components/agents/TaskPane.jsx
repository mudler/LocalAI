/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useRef } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Button, SidePane, StatusChip, Switch } from '../ui'
import { OutputBlock, RelTime } from './RunBits'
import { JobRow } from './TaskJobs'
import { apiExample, promptParams } from '../../utils/agentTasks'

const TABS = ['overview', 'runs', 'settings']

// The details of one agent task beside the list, in three tabs: Overview
// (what it does, who runs it, how to run it over the API), Runs (its jobs)
// and Settings (on or off, webhooks, delete). The footer keeps Run now and
// Edit in reach on every tab.
export function TaskPane({ task, owner, runner, jobs, tab, onTab, onClose, onRun, onEdit, onToggle, onDelete, jobProps }) {
  const { t } = useTranslation('agents')
  const refs = useRef({})
  const open = Boolean(task)
  const mine = !owner
  const params = task ? promptParams(task.prompt) : []
  const onKeyDown = (e) => {
    const i = TABS.indexOf(tab)
    let n = i
    if (e.key === 'ArrowRight') n = (i + 1) % TABS.length
    else if (e.key === 'ArrowLeft') n = (i - 1 + TABS.length) % TABS.length
    else if (e.key === 'Home') n = 0
    else if (e.key === 'End') n = TABS.length - 1
    else return
    e.preventDefault()
    onTab(TABS[n])
    refs.current[TABS[n]]?.focus()
  }
  return (
    <SidePane
      open={open}
      wide
      title={task?.name}
      testId="agent-task-pane"
      onClose={onClose}
      meta={task && (
        <span className="aj-pane-meta">
          {task.enabled === false ? <StatusChip tone="idle" label={t('tasks.off')} /> : <StatusChip tone="ok" label={t('tasks.on')} />}
          {owner && <span className="ag-muted">{owner}</span>}
        </span>
      )}
      footer={task && mine && (
        <div className="aj-pane-foot">
          <Button variant="ghost" size="sm" icon="edit" onClick={onEdit}>{t('tasks.edit')}</Button>
          <Button variant="primary" size="sm" icon="play" onClick={onRun} disabled={task.enabled === false}>{t('tasks.run')}</Button>
        </div>
      )}
    >
      {task && (
        <>
          <div className="ag-views aj-pane-tabs" role="tablist" aria-label={t('tasks.pane.label')} onKeyDown={onKeyDown}>
            {TABS.map(id => (
              <button
                key={id}
                ref={el => { refs.current[id] = el }}
                type="button"
                role="tab"
                id={`at-tab-${id}`}
                className="ag-view"
                aria-selected={tab === id}
                aria-controls={`at-panel-${id}`}
                tabIndex={tab === id ? 0 : -1}
                onClick={() => onTab(id)}
              >
                {t(`tasks.pane.${id}`)}
                {id === 'runs' && jobs.length > 0 && <span className="aj-tab-count">{jobs.length}</span>}
              </button>
            ))}
          </div>
          <div role="tabpanel" id={`at-panel-${tab}`} aria-labelledby={`at-tab-${tab}`} className="aj-pane-panel">
            {tab === 'overview' && (
              <>
                {task.description && <p className="aj-pane-lede">{task.description}</p>}
                <dl className="ag-facts aj-pane-facts">
                  <dt>{t('tasks.runner')}</dt>
                  <dd>{task.agent_id ? <Link className="ag-link" to={`/app/agents/${encodeURIComponent(task.agent_id)}`}>{runner}</Link> : <span className="ag-mono">{runner}</span>}</dd>
                  <dt>{t('tasks.pane.inputs')}</dt>
                  <dd>{params.length ? params.map(p => <code key={p} className="aj-input-chip">{p}</code>) : <span className="ag-muted">{t('tasks.pane.noInputs')}</span>}</dd>
                  {task.updated_at && <><dt>{t('tasks.pane.changed')}</dt><dd><RelTime iso={task.updated_at} /></dd></>}
                </dl>
                <section className="ag-sec">
                  <h3 className="ag-eyebrow">{t('tasks.prompt')}</h3>
                  <p className="aj-preview">{task.prompt}</p>
                </section>
                <section className="ag-sec">
                  <OutputBlock label={t('tasks.pane.api')} text={apiExample(task, window.location.origin)} />
                  <p className="ag-note">{t('tasks.pane.apiHint')}</p>
                </section>
              </>
            )}
            {tab === 'runs' && (
              jobs.length === 0
                ? <p className="ag-note">{t('tasks.pane.noRuns')}</p>
                : <ul className="aj-runs aj-runs--pane">{jobs.slice(0, 30).map(job => <JobRow key={job.id} job={job} taskName={task.name} owner={owner} showTask={false} {...jobProps(job, 'pane:')} />)}</ul>
            )}
            {tab === 'settings' && (
              <div className="aj-settings">
                <div className="aj-switch-row">
                  <span className="aj-switch-row__text">
                    <span className="aj-switch-row__label" id="at-pane-on">{t('tasks.sheet.on')}</span>
                    <span className="dk-hint" id="at-pane-on-h">{t('tasks.sheet.onHelp')}</span>
                  </span>
                  <Switch checked={task.enabled !== false} onChange={onToggle} labelledBy="at-pane-on" describedBy="at-pane-on-h" disabled={!mine} />
                </div>
                <div className="aj-switch-row">
                  <span className="aj-switch-row__text">
                    <span className="aj-switch-row__label">{t('tasks.pane.webhooks')}</span>
                    <span className="dk-hint">{task.webhooks?.length ? t('tasks.webhooksKept', { count: task.webhooks.length }) : t('tasks.pane.noWebhooks')}</span>
                  </span>
                </div>
                {mine && (
                  <div className="aj-switch-row">
                    <span className="aj-switch-row__text">
                      <span className="aj-switch-row__label">{t('tasks.pane.deleteTitle')}</span>
                      <span className="dk-hint">{t('tasks.pane.deleteHelp')}</span>
                    </span>
                    <Button variant="danger-ghost" size="sm" icon="trash" onClick={onDelete}>{t('tasks.delete')}</Button>
                  </div>
                )}
              </div>
            )}
          </div>
        </>
      )}
    </SidePane>
  )
}
