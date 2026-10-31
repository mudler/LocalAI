import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { REASONING_EFFORTS, settingsBody, settingsChanged, settingsErrors, settingsForm } from '../../utils/chatSettings'

// eslint-disable-next-line no-unused-vars
function Switch({ checked, onChange, label }) {
  return (
    <button
      type="button"
      className="dk-switch"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      onClick={() => onChange(!checked)}
    />
  )
}

// The chat in one sheet: how the page behaves, what the chat runs with (its
// agent, the LocalAI tools) and, for admins, what the model is. The server keeps
// the chat, its model and its tools; the switches of the tools and skills are in
// the composer.
// The settings the server keeps with an open chat: a system prompt, the
// sampling values, the reasoning effort and the context size. Save sends them
// all (PATCH /api/chats/{id}); they apply from the next turn.
// eslint-disable-next-line no-unused-vars -- used as JSX below
function AnswerSettings({ settings, onSave }) {
  const { t } = useTranslation('chat')
  const [form, setForm] = useState(() => settingsForm(settings))
  const [state, setState] = useState({ kind: 'idle', message: '' })
  useEffect(() => { setForm(settingsForm(settings)) }, [settings])
  const errors = settingsErrors(form)
  const changed = settingsChanged(form, settings)
  const set = (key) => (e) => { setForm(f => ({ ...f, [key]: e.target.value })); setState({ kind: 'idle', message: '' }) }
  const save = async (e) => {
    e.preventDefault()
    if (Object.keys(errors).length > 0) return
    setState({ kind: 'busy', message: '' })
    try {
      await onSave(settingsBody(form))
      setState({ kind: 'saved', message: '' })
    } catch (err) {
      setState({ kind: 'error', message: err.message || t('settings.saveFailed') })
    }
  }
  const field = (key, label, extra = {}) => (
    <div className="cx-field">
      <label className="cx-field__label" htmlFor={`cx-set-${key}`}>{label}</label>
      <input
        id={`cx-set-${key}`}
        className="cx-in"
        inputMode="decimal"
        value={form[key]}
        onChange={set(key)}
        placeholder={t('settings.modelDefault')}
        aria-invalid={errors[key] ? true : undefined}
        aria-describedby={errors[key] ? `cx-set-${key}-err` : undefined}
        {...extra}
      />
      {errors[key] && <p className="cx-help cx-help--err" id={`cx-set-${key}-err`}>{t(`settings.invalid.${errors[key]}`)}</p>}
    </div>
  )
  return (
    <form className="cx-sec" data-testid="chat-answer-settings" onSubmit={save}>
      <h3>{t('settings.answers')}</h3>
      <p className="cx-help">{t('settings.answersHelp')}</p>
      <div className="cx-field">
        <label className="cx-field__label" htmlFor="cx-set-system_prompt">{t('settings.systemPrompt')}</label>
        <textarea id="cx-set-system_prompt" className="cx-ta" value={form.system_prompt} onChange={set('system_prompt')} placeholder={t('settings.systemPromptPlaceholder')} />
      </div>
      {field('temperature', t('settings.temperature'))}
      {field('top_p', t('settings.topP'))}
      {field('top_k', t('settings.topK'), { inputMode: 'numeric' })}
      {field('context_size', t('settings.contextSize'), { inputMode: 'numeric' })}
      <div className="cx-field" role="group" aria-label={t('settings.reasoningEffort')}>
        <span className="cx-field__label">{t('settings.reasoningEffort')}</span>
        <div className="cx-seg">
          {REASONING_EFFORTS.map(v => (
            <button key={v || 'default'} type="button" aria-pressed={form.reasoning_effort === v} onClick={() => set('reasoning_effort')({ target: { value: v } })}>
              {v ? t(`settings.effort.${v}`) : t('settings.effort.default')}
            </button>
          ))}
        </div>
      </div>
      {state.kind === 'error' && <p className="cx-notice cx-notice--error" role="alert" data-testid="chat-settings-error">{state.message}</p>}
      {state.kind === 'saved' && <p className="cx-help" role="status">{t('settings.saved')}</p>}
      <button type="submit" className="cx-btn cx-btn--primary" disabled={!changed || state.kind === 'busy' || Object.keys(errors).length > 0} data-testid="chat-settings-save">
        {state.kind === 'busy' ? t('settings.saving') : t('settings.save')}
      </button>
    </form>
  )
}

export default function ChatSettingsSheet({
  chat, agent, isAdmin, focusMode, onFocusMode, modelInfo, onEditConfig, onEditAgent, onDelete, onClose, onSaveSettings,
}) {
  const { t } = useTranslation('chat')
  const sheetRef = useRef(null)
  const closeRef = useRef(null)
  const openerRef = useRef(null)
  // The page passes a fresh onClose on every render. Read it through a ref so
  // the effect below runs once per open: re-running it would move focus to
  // the opener and back to Close, pulling it out of the control being edited.
  const onCloseRef = useRef(onClose)
  useEffect(() => { onCloseRef.current = onClose })

  useEffect(() => {
    openerRef.current = document.activeElement
    closeRef.current?.focus()
    const onKey = (e) => {
      if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onCloseRef.current(); return }
      if (e.key !== 'Tab' || !sheetRef.current) return
      const focusable = Array.from(sheetRef.current.querySelectorAll('button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])'))
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus() }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
    }
    window.addEventListener('keydown', onKey, true)
    return () => {
      window.removeEventListener('keydown', onKey, true)
      const el = openerRef.current
      if (el && document.contains(el)) el.focus?.()
    }
  }, [])

  return createPortal(
    <div className="dk-sheet-veil cx-sheet-veil" data-state="open" onMouseDown={onClose}>
      <div
        ref={sheetRef}
        className="dk-sheet cx-sheet"
        role="dialog"
        aria-modal="true"
        aria-labelledby="chat-settings-title"
        data-state="open"
        data-testid="chat-settings"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <span className="dk-sheet-grip" aria-hidden="true" />
        <div className="dk-sheet-head">
          <div>
            <h2 className="dk-sheet-title" id="chat-settings-title">{t('settings.title')}</h2>
            <p className="dk-sheet-desc">{t('settings.serverKept')}</p>
          </div>
          <button
            ref={closeRef}
            type="button"
            className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm"
            aria-label={t('header.close')}
            onClick={onClose}
          >
            <Icon name="close" />
          </button>
        </div>

        <div className="dk-sheet-body cx-sheet__body">
          <section className="cx-sec">
            <h3>{t('settings.behaviour')}</h3>
            {chat.assistant && (
              <div className="cx-tog" data-testid="chat-manage-mode">
                <span>
                  <b><Icon name="user-shield" /> {t('settings.manageMode')}</b>
                  <small>{t('settings.manageModeOn')}</small>
                </span>
              </div>
            )}
            <div className="cx-tog">
              <span>
                <b><Icon name="minimize" /> {t('settings.focusMode')}</b>
                <small>{t('settings.focusModeDesc')}</small>
              </span>
              <Switch checked={focusMode} onChange={onFocusMode} label={t('settings.focusMode')} />
            </div>
          </section>

          {onSaveSettings && <AnswerSettings settings={chat.settings} onSave={onSaveSettings} />}

          {agent && (
            <section className="cx-sec" data-testid="chat-agent-info">
              <h3>{t('settings.agent')}</h3>
              <p className="cx-help">{t('settings.agentHelp', { name: agent.name })}</p>
              <button type="button" className="cx-btn" onClick={onEditAgent}>
                <Icon name="edit" /> {t('settings.editAgent')}
              </button>
            </section>
          )}

          {isAdmin && chat.model && modelInfo && (
            <section className="cx-sec" data-testid="chat-model-info">
              <h3>{t('header.modelInfoTitle', { model: chat.model })}</h3>
              <dl className="cx-facts">
                {modelInfo.backend && <div><dt>{t('modelInfo.backend')}</dt><dd>{modelInfo.backend}</dd></div>}
                {modelInfo.parameters?.model && <div><dt>{t('modelInfo.modelFile')}</dt><dd>{modelInfo.parameters.model}</dd></div>}
                {modelInfo.context_size > 0 && <div><dt>{t('modelInfo.contextSize')}</dt><dd>{modelInfo.context_size}</dd></div>}
                {modelInfo.threads > 0 && <div><dt>{t('modelInfo.threads')}</dt><dd>{modelInfo.threads}</dd></div>}
                {(modelInfo.mcp?.remote || modelInfo.mcp?.stdio) && <div><dt>{t('modelInfo.mcp')}</dt><dd>{t('modelInfo.configured')}</dd></div>}
                {modelInfo.template?.chat_message && <div><dt>{t('modelInfo.chatTemplate')}</dt><dd>{t('modelInfo.yes')}</dd></div>}
                {modelInfo.gpu_layers > 0 && <div><dt>{t('modelInfo.gpuLayers')}</dt><dd>{modelInfo.gpu_layers}</dd></div>}
              </dl>
              <button type="button" className="cx-btn" onClick={onEditConfig}>
                <Icon name="edit" /> {t('header.editConfig')}
              </button>
            </section>
          )}

          {onDelete && <section className="cx-sec">
            <h3>{t('settings.conversation')}</h3>
            <div className="cx-danger">
              <span>{t('settings.deleteHelp')}</span>
              <button type="button" className="cx-btn cx-btn--danger" onClick={onDelete} data-testid="chat-settings-delete">
                <Icon name="trash" /> {t('menu.deleteChat')}
              </button>
            </div>
          </section>}
        </div>
      </div>
    </div>,
    document.body,
  )
}
