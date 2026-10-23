import { useEffect, useRef } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import { cssVars } from '../../utils/modelLedger'
import Icon from '../Icon'

const CONTEXT_STEPS = [4096, 8192, 16384, 32768]

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

// One sampling setting. null means "the model's own default": the slider sits
// at a typical value, the label says so, and Reset puts the chat back to it.
// eslint-disable-next-line no-unused-vars
function Slider({ id, label, value, fallback, min, max, step, onChange, onReset, parse }) {
  const { t } = useTranslation('chat')
  const shown = value ?? fallback
  const pct = ((shown - min) / (max - min)) * 100
  return (
    <div className="cx-field">
      <div className="cx-field__label">
        <label htmlFor={id}>{label}</label>
        <span className="cx-field__value" data-default={value === null || value === undefined || undefined}>
          {value === null || value === undefined ? t('settings.modelDefault') : value}
        </span>
        {value !== null && value !== undefined && (
          <button type="button" className="cx-link" onClick={onReset}>{t('settings.reset')}</button>
        )}
      </div>
      <input
        id={id}
        className="dk-range"
        type="range"
        min={min}
        max={max}
        step={step}
        value={shown}
        style={cssVars({ '--dk-fill': `${pct}%` })}
        onChange={(e) => onChange(parse(e.target.value))}
      />
      <div className="cx-field__ends"><span>{min}</span><span>{max}</span></div>
    </div>
  )
}

// Everything that shapes the next reply, in one sheet: the system prompt,
// sampling, the context size, the behaviour switches and, for admins, what the
// model is. Settings apply from the next message.
export default function ChatSettingsSheet({
  chat, isAdmin, onUpdate, focusMode, onFocusMode, modelInfo, onEditConfig, onClear, onClose,
}) {
  const { t } = useTranslation('chat')
  const sheetRef = useRef(null)
  const closeRef = useRef(null)
  const openerRef = useRef(null)

  useEffect(() => {
    openerRef.current = document.activeElement
    closeRef.current?.focus()
    const onKey = (e) => {
      if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onClose(); return }
      if (e.key !== 'Tab' || !sheetRef.current) return
      const focusable = Array.from(sheetRef.current.querySelectorAll('button:not([disabled]), input:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'))
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
  }, [onClose])

  const set = (patch) => onUpdate(patch)
  const used = chat.tokenUsage?.total || 0
  const percent = chat.contextSize ? Math.min(100, Math.round((used / chat.contextSize) * 100)) : null

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
            <p className="dk-sheet-desc">{t('settings.appliesNext')}</p>
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
            <h3>{t('settings.systemPrompt')}</h3>
            <textarea
              className="cx-ta"
              data-testid="chat-system-prompt"
              value={chat.systemPrompt || ''}
              onChange={(e) => set({ systemPrompt: e.target.value })}
              rows={5}
              placeholder={t('settings.systemPromptPlaceholder')}
              aria-label={t('settings.systemPrompt')}
            />
            <p className="cx-help">{t('settings.systemPromptHelp')}</p>
          </section>

          <section className="cx-sec">
            <h3>{t('settings.sampling')}</h3>
            <Slider
              id="chat-temperature" label={t('settings.temperature')} value={chat.temperature} fallback={0.7}
              min={0} max={2} step={0.1} parse={parseFloat}
              onChange={(v) => set({ temperature: v })} onReset={() => set({ temperature: null })}
            />
            <Slider
              id="chat-top-p" label={t('settings.topP')} value={chat.topP} fallback={0.9}
              min={0} max={1} step={0.05} parse={parseFloat}
              onChange={(v) => set({ topP: v })} onReset={() => set({ topP: null })}
            />
            <Slider
              id="chat-top-k" label={t('settings.topK')} value={chat.topK} fallback={40}
              min={1} max={100} step={1} parse={(v) => parseInt(v, 10)}
              onChange={(v) => set({ topK: v })} onReset={() => set({ topK: null })}
            />
          </section>

          <section className="cx-sec">
            <h3>{t('settings.contextWindow')}</h3>
            <div className="cx-field">
              <div className="cx-field__label">
                <label htmlFor="chat-context-size">{t('settings.contextSize')}</label>
              </div>
              <div className="cx-seg" role="group" aria-label={t('settings.contextSize')}>
                {CONTEXT_STEPS.map(n => (
                  <button
                    key={n}
                    type="button"
                    aria-pressed={chat.contextSize === n}
                    onClick={() => set({ contextSize: n })}
                  >
                    {n / 1024}k
                  </button>
                ))}
              </div>
              <input
                id="chat-context-size"
                type="number"
                className="cx-in"
                value={chat.contextSize || ''}
                onChange={(e) => set({ contextSize: parseInt(e.target.value, 10) || null })}
                placeholder={t('settings.contextSizePlaceholder')}
              />
              <p className="cx-help">
                {percent !== null
                  ? t('settings.contextUsing', { used, size: chat.contextSize, percent })
                  : t('settings.contextHelp')}
              </p>
            </div>
          </section>

          <section className="cx-sec">
            <h3>{t('settings.behaviour')}</h3>
            {isAdmin && (
              <div className="cx-tog">
                <span>
                  <b><Icon name="user-shield" /> {t('settings.manageMode')}</b>
                  <small>{t('settings.manageModeDesc')}</small>
                </span>
                <Switch
                  checked={!!chat.localaiAssistant}
                  onChange={(next) => set({ localaiAssistant: next })}
                  label={t('settings.manageMode')}
                />
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

          <section className="cx-sec">
            <h3>{t('settings.conversation')}</h3>
            <div className="cx-danger">
              <span>{t('settings.clearHelp')}</span>
              <button type="button" className="cx-btn cx-btn--danger" onClick={onClear} title={t('settings.clearHistory')}>
                <Icon name="eraser" /> {t('settings.clearHistory')}
              </button>
            </div>
          </section>
        </div>
      </div>
    </div>,
    document.body,
  )
}
