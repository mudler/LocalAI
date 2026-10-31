/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import SideSheet from '../SideSheet'
import { Button, Callout, Switch } from '../ui'
import { promptParams, taskErrors } from '../../utils/agentTasks'

// A labelled field of the task sheets: the label, the control, then either
// the error (after a save attempt) or the help line.
function Field({ id, label, help, error, optional, children }) {
  const { t } = useTranslation('agents')
  return (
    <div className="dk-field">
      <label className="dk-label" htmlFor={id}>
        {label}{optional && <span className="aj-optional"> {t('tasks.sheet.optional')}</span>}
      </label>
      {children}
      {error
        ? <p className="dk-field-error" id={`${id}-msg`} role="alert">{error}</p>
        : help && <p className="dk-hint" id={`${id}-msg`}>{help}</p>}
    </div>
  )
}

// Create or edit a task, in a side sheet with three groups: what it is, who
// runs it, and the prompt. Save checks the required fields and says what is
// missing next to each one instead of a dead button.
export function TaskSheet({ editing, setForm, agents, models, onSave, onClose, saving }) {
  const { t } = useTranslation('agents')
  const [tried, setTried] = useState(false)
  const { task, form } = editing
  const errors = tried ? taskErrors(form) : {}
  const params = promptParams(form.prompt)
  const submit = (e) => {
    e.preventDefault()
    setTried(true)
    if (Object.keys(taskErrors(form)).length > 0) {
      const first = ['name', 'model', 'agent', 'prompt'].find(k => taskErrors(form)[k === 'agent' ? 'agent_id' : k])
      document.getElementById(`at-${first}`)?.focus()
      return
    }
    onSave(e)
  }
  const err = (field) => (errors[field] ? t(`tasks.errors.${errors[field]}`) : undefined)
  const inv = (field) => ({ 'aria-invalid': errors[field] ? true : undefined, 'aria-describedby': `at-${field === 'agent_id' ? 'agent' : field}-msg` })
  const title = task ? t('tasks.editTitle') : t('tasks.newTitle')
  return (
    <SideSheet
      title={title}
      description={task ? t('tasks.sheet.editDesc') : t('tasks.sheet.newDesc')}
      onClose={onClose}
      closeLabel={t('tasks.cancel')}
      labelId="agent-task-sheet-title"
      footer={(
        <>
          <Button variant="ghost" onClick={onClose}>{t('tasks.cancel')}</Button>
          <Button type="submit" form="agent-task-form" variant="primary" busy={saving}>{t('tasks.save')}</Button>
        </>
      )}
    >
      <form id="agent-task-form" className="aj-sheet" onSubmit={submit} noValidate data-testid="agent-task-form" aria-label={title}>
        <fieldset className="aj-group">
          <legend className="aj-group__title">{t('tasks.sheet.about')}</legend>
          <Field id="at-name" label={t('tasks.name')} help={t('tasks.sheet.nameHelp')} error={err('name')}>
            <input id="at-name" className="dk-input" value={form.name} autoComplete="off" onChange={(e) => setForm({ name: e.target.value })} {...inv('name')} />
          </Field>
          <Field id="at-desc" label={t('tasks.description')} help={t('tasks.sheet.descHelp')} optional>
            <input id="at-desc" className="dk-input" value={form.description} onChange={(e) => setForm({ description: e.target.value })} aria-describedby="at-desc-msg" />
          </Field>
        </fieldset>

        <fieldset className="aj-group">
          <legend className="aj-group__title">{t('tasks.sheet.who')}</legend>
          <Field id="at-runner" label={t('tasks.runner')} help={form.runner === 'agent' ? t('tasks.sheet.agentHelp') : t('tasks.sheet.modelHelp')}>
            <span className="dk-select-wrap">
              <select id="at-runner" className="dk-select" value={form.runner} onChange={(e) => setForm({ runner: e.target.value })} aria-describedby="at-runner-msg">
                <option value="model">{t('tasks.byModel')}</option>
                <option value="agent" disabled={agents.length === 0}>{t('tasks.byAgent')}</option>
              </select>
            </span>
          </Field>
          {form.runner === 'model' ? (
            <Field id="at-model" label={t('tasks.model')} error={err('model')}>
              <span className="dk-select-wrap">
                <select id="at-model" className="dk-select" value={form.model} onChange={(e) => setForm({ model: e.target.value })} {...inv('model')}>
                  <option value="">{t('tasks.pick')}</option>
                  {models.map(m => <option key={m.id} value={m.id}>{m.id}</option>)}
                </select>
              </span>
            </Field>
          ) : (
            <Field id="at-agent" label={t('tasks.agent')} error={err('agent_id')}>
              <span className="dk-select-wrap">
                <select id="at-agent" className="dk-select" value={form.agent_id} onChange={(e) => setForm({ agent_id: e.target.value })} {...inv('agent_id')}>
                  <option value="">{t('tasks.pick')}</option>
                  {agents.map(a => <option key={a.id} value={a.id}>{a.name}</option>)}
                </select>
              </span>
            </Field>
          )}
        </fieldset>

        <fieldset className="aj-group">
          <legend className="aj-group__title">{t('tasks.sheet.what')}</legend>
          <Field id="at-prompt" label={t('tasks.prompt')} error={err('prompt')} help={t('tasks.promptHint', { example: '{{.name}}' })}>
            <textarea id="at-prompt" className="dk-input aj-prompt-input" rows={6} value={form.prompt} placeholder={t('tasks.sheet.promptPlaceholder', { example: '{{.ticket}}' })} onChange={(e) => setForm({ prompt: e.target.value })} {...inv('prompt')} />
          </Field>
          <p className="aj-inputs" aria-live="polite" data-testid="agent-task-inputs">
            {params.length > 0
              ? <>{t('tasks.sheet.inputs')} {params.map(p => <code key={p} className="aj-input-chip">{p}</code>)}</>
              : <span className="ag-muted">{t('tasks.sheet.noInputs')}</span>}
          </p>
        </fieldset>

        <div className="aj-switch-row">
          <span className="aj-switch-row__text">
            <span className="aj-switch-row__label" id="at-enabled-l">{t('tasks.sheet.on')}</span>
            <span className="dk-hint" id="at-enabled-h">{t('tasks.sheet.onHelp')}</span>
          </span>
          <Switch checked={form.enabled} onChange={(v) => setForm({ enabled: v })} labelledBy="at-enabled-l" describedBy="at-enabled-h" />
        </div>
        {task?.webhooks?.length > 0 && <Callout>{t('tasks.webhooksKept', { count: task.webhooks.length })}</Callout>}
      </form>
    </SideSheet>
  )
}

// Run a task: one field for each value its prompt asks for, and a preview of
// the prompt with those values. A task that asks for nothing runs as written.
export function RunSheet({ running, setParams, onRun, onClose }) {
  const { t } = useTranslation('agents')
  const { task, params } = running
  const names = promptParams(task.prompt)
  const preview = String(task.prompt || '').replace(/\{\{-?\s*\.([A-Za-z_][A-Za-z0-9_]*)\s*-?\}\}/g, (m, k) => params[k] || `‹${k}›`)
  return (
    <SideSheet
      title={t('tasks.runTitle', { name: task.name })}
      description={names.length ? t('tasks.sheet.runDesc') : t('tasks.sheet.runNone')}
      onClose={onClose}
      closeLabel={t('tasks.cancel')}
      labelId="agent-task-run-title"
      footer={(
        <>
          <Button variant="ghost" onClick={onClose}>{t('tasks.cancel')}</Button>
          <Button type="submit" form="agent-task-run" variant="primary" icon="play">{t('tasks.runNow')}</Button>
        </>
      )}
    >
      <form id="agent-task-run" className="aj-sheet" onSubmit={onRun} data-testid="agent-task-run" aria-label={t('tasks.runTitle', { name: task.name })}>
        {names.length > 0 && (
          <fieldset className="aj-group">
            <legend className="aj-group__title">{t('tasks.output.values')}</legend>
            {names.map(p => (
              <div className="dk-field" key={p}>
                <label className="dk-label aj-mono-label" htmlFor={`at-p-${p}`}>{p}</label>
                <input id={`at-p-${p}`} className="dk-input" value={params[p] || ''} onChange={(e) => setParams({ [p]: e.target.value })} />
              </div>
            ))}
          </fieldset>
        )}
        <div className="aj-group">
          <span className="aj-group__title">{t('tasks.sheet.preview')}</span>
          <p className="aj-preview" data-testid="agent-task-preview">{preview}</p>
        </div>
      </form>
    </SideSheet>
  )
}
