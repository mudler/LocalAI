/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import SideSheet from '../SideSheet'
import { Button, Callout, Segmented, Slider, Switch } from '../ui'
import { REASONING_EFFORTS, SETTING_RANGES, settingsBody, settingsChanged, settingsErrors, settingsForm } from '../../utils/chatSettings'

// The chat in one sheet: how the page behaves, what the chat answers with (its
// system prompt, sampling values, reasoning effort and context size), its agent
// and, for admins, what the model is. The server keeps the chat, its model and
// its tools; the switches of the tools and skills are in the composer.
//
// The answer settings are one form: Save sends them all (PATCH /api/chats/{id})
// and they apply from the next turn. The reasoning effort is the exception: it
// applies at once, the same way the chip of the composer does, and both read it
// from the chat's settings, so they always show the same level.
const FORM_ID = 'chat-answer-settings'

function Field({ id, label, hint, error, children }) {
  const { t } = useTranslation('chat')
  return (
    <div className="dk-field cxs-field">
      <label className="dk-label" htmlFor={id}>{label}</label>
      {children}
      {error
        ? <p className="dk-field-error" id={`${id}-err`} role="alert">{t(`settings.invalid.${error}`)}</p>
        : hint && <p className="dk-hint" id={`${id}-hint`}>{hint}</p>}
    </div>
  )
}

// The form state of the answer settings, kept by the sheet so the Save button
// in the footer and the fields in the body share it.
function useAnswerForm(settings, onSave) {
  const { t } = useTranslation('chat')
  const [form, setForm] = useState(() => settingsForm(settings))
  const [state, setState] = useState({ kind: 'idle', message: '' })
  // The reasoning effort follows the chat at once; the rest of the form keeps
  // what was typed until it is saved or the chat's own values change.
  const others = JSON.stringify({ ...settingsForm(settings), reasoning_effort: '' })
  useEffect(() => { setForm(settingsForm(settings)) }, [others]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { setForm(f => ({ ...f, reasoning_effort: settingsForm(settings).reasoning_effort })) }, [settings])
  const errors = settingsErrors(form)
  const changed = settingsChanged(form, settings)
  const set = (key) => (value) => { setForm(f => ({ ...f, [key]: value })); setState({ kind: 'idle', message: '' }) }
  const save = async (e) => {
    e?.preventDefault()
    if (!onSave || Object.keys(errors).length > 0 || !changed) return
    setState({ kind: 'busy', message: '' })
    try {
      await onSave(settingsBody(form))
      setState({ kind: 'saved', message: '' })
    } catch (err) {
      setState({ kind: 'error', message: err.message || t('settings.saveFailed') })
    }
  }
  return { form, errors, changed, state, set, save }
}

function AnswerSettings({ answer, onEffort }) {
  const { t } = useTranslation('chat')
  const { form, errors, state, set, save } = answer
  const describedBy = (key) => (errors[key] ? `cx-set-${key}-err` : `cx-set-${key}-hint`)
  const slider = (key, label, hint) => (
    <Field id={`cx-set-${key}`} label={label} hint={hint} error={errors[key]}>
      <Slider
        id={`cx-set-${key}`}
        range={SETTING_RANGES[key]}
        value={form[key]}
        onChange={set(key)}
        label={t('settings.sliderOf', { name: label })}
        placeholder={t('settings.effort.default')}
        resetLabel={t('settings.reset', { name: label })}
        invalid={!!errors[key]}
        describedBy={describedBy(key)}
        testId={`chat-set-${key}`}
      />
    </Field>
  )
  const efforts = REASONING_EFFORTS.map(v => ({ id: v || 'default', label: t(`settings.effort.${v || 'default'}`) }))
  return (
    <form className="cxs-sec" id={FORM_ID} data-testid="chat-answer-settings" onSubmit={save}>
      <h3 className="cxs-sec__title">{t('settings.answers')}</h3>
      <p className="dk-hint cxs-sec__lead">{t('settings.answersHelp')}</p>
      <Field id="cx-set-system_prompt" label={t('settings.systemPrompt')}>
        <textarea
          id="cx-set-system_prompt"
          className="dk-textarea"
          value={form.system_prompt}
          onChange={(e) => set('system_prompt')(e.target.value)}
          placeholder={t('settings.systemPromptPlaceholder')}
        />
      </Field>
      {slider('temperature', t('settings.temperature'), t('settings.hint.temperature'))}
      {slider('top_p', t('settings.topP'), t('settings.hint.topP'))}
      {slider('top_k', t('settings.topK'), t('settings.hint.topK'))}
      <Field id="cx-set-context_size" label={t('settings.contextSize')} hint={t('settings.hint.contextSize')} error={errors.context_size}>
        <input
          id="cx-set-context_size"
          className="dk-input dk-input--mono"
          inputMode="numeric"
          value={form.context_size}
          onChange={(e) => set('context_size')(e.target.value)}
          placeholder={t('settings.modelDefault')}
          aria-invalid={errors.context_size ? true : undefined}
          aria-describedby={describedBy('context_size')}
          autoComplete="off"
        />
      </Field>
      {onEffort && (
        <div className="dk-field cxs-field" role="group" aria-labelledby="cx-set-effort-label">
          <span className="dk-label" id="cx-set-effort-label">{t('settings.reasoningEffort')}</span>
          <Segmented
            label={t('settings.reasoningEffort')}
            options={efforts}
            value={form.reasoning_effort || 'default'}
            onChange={(id) => onEffort(id === 'default' ? '' : id)}
            testId="chat-set-effort"
            itemTestId="chat-set-effort-"
          />
          <p className="dk-hint">{t('settings.hint.effort')}</p>
        </div>
      )}
      {state.kind === 'error' && <Callout tone="error" role="alert" testId="chat-settings-error">{state.message}</Callout>}
      {state.kind === 'saved' && <Callout tone="ok" role="status">{t('settings.saved')}</Callout>}
    </form>
  )
}

export default function ChatSettingsSheet({
  chat, agent, isAdmin, focusMode, onFocusMode, modelInfo, onEditConfig, onEditAgent, onDelete, onClose, onSaveSettings, onEffort,
}) {
  const { t } = useTranslation('chat')
  const answer = useAnswerForm(chat.settings, onSaveSettings)
  const footer = onSaveSettings && (
    <Button
      variant="primary"
      type="submit"
      form={FORM_ID}
      busy={answer.state.kind === 'busy'}
      disabled={!answer.changed || Object.keys(answer.errors).length > 0}
      data-testid="chat-settings-save"
    >
      {t('settings.save')}
    </Button>
  )
  return (
    <SideSheet
      title={t('settings.title')}
      description={t('settings.serverKept')}
      onClose={onClose}
      closeLabel={t('header.close')}
      testId="chat-settings"
      labelId="chat-settings-title"
      footer={footer}
    >
      <section className="cxs-sec">
        <h3 className="cxs-sec__title">{t('settings.behaviour')}</h3>
        {chat.assistant && (
          <Callout tone="info" icon="user-shield" title={t('settings.manageMode')} testId="chat-manage-mode">{t('settings.manageModeOn')}</Callout>
        )}
        <div className="cxs-row">
          <span className="cxs-row__text">
            <span className="dk-label" id="cx-focus-label">{t('settings.focusMode')}</span>
            <span className="dk-hint" id="cx-focus-desc">{t('settings.focusModeDesc')}</span>
          </span>
          <Switch checked={focusMode} onChange={onFocusMode} labelledBy="cx-focus-label" describedBy="cx-focus-desc" testId="chat-focus-switch" />
        </div>
      </section>

      {onSaveSettings && <AnswerSettings answer={answer} onEffort={onEffort} />}

      {agent && (
        <section className="cxs-sec" data-testid="chat-agent-info">
          <h3 className="cxs-sec__title">{t('settings.agent')}</h3>
          <p className="dk-hint cxs-sec__lead">{t('settings.agentHelp', { name: agent.name })}</p>
          <Button size="sm" icon="edit" onClick={onEditAgent}>{t('settings.editAgent')}</Button>
        </section>
      )}

      {isAdmin && chat.model && modelInfo && (
        <section className="cxs-sec" data-testid="chat-model-info">
          <h3 className="cxs-sec__title">{t('header.modelInfoTitle', { model: chat.model })}</h3>
          <dl className="cxs-facts">
            {modelInfo.backend && <div><dt>{t('modelInfo.backend')}</dt><dd>{modelInfo.backend}</dd></div>}
            {modelInfo.parameters?.model && <div><dt>{t('modelInfo.modelFile')}</dt><dd>{modelInfo.parameters.model}</dd></div>}
            {modelInfo.context_size > 0 && <div><dt>{t('modelInfo.contextSize')}</dt><dd>{modelInfo.context_size}</dd></div>}
            {modelInfo.threads > 0 && <div><dt>{t('modelInfo.threads')}</dt><dd>{modelInfo.threads}</dd></div>}
            {(modelInfo.mcp?.remote || modelInfo.mcp?.stdio) && <div><dt>{t('modelInfo.mcp')}</dt><dd>{t('modelInfo.configured')}</dd></div>}
            {modelInfo.template?.chat_message && <div><dt>{t('modelInfo.chatTemplate')}</dt><dd>{t('modelInfo.yes')}</dd></div>}
            {modelInfo.gpu_layers > 0 && <div><dt>{t('modelInfo.gpuLayers')}</dt><dd>{modelInfo.gpu_layers}</dd></div>}
          </dl>
          <Button size="sm" icon="edit" onClick={onEditConfig}>{t('header.editConfig')}</Button>
        </section>
      )}

      {onDelete && (
        <section className="cxs-sec">
          <h3 className="cxs-sec__title">{t('settings.conversation')}</h3>
          <p className="dk-hint cxs-sec__lead">{t('settings.deleteHelp')}</p>
          <Button size="sm" variant="danger-ghost" icon="trash" onClick={onDelete} data-testid="chat-settings-delete">{t('menu.deleteChat')}</Button>
        </section>
      )}
    </SideSheet>
  )
}
