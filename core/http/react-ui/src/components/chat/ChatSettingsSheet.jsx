import { useEffect, useRef } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'

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
export default function ChatSettingsSheet({
  chat, agent, isAdmin, focusMode, onFocusMode, modelInfo, onEditConfig, onEditAgent, onDelete, onClose,
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

          <section className="cx-sec">
            <h3>{t('settings.conversation')}</h3>
            <div className="cx-danger">
              <span>{t('settings.deleteHelp')}</span>
              <button type="button" className="cx-btn cx-btn--danger" onClick={onDelete} data-testid="chat-settings-delete">
                <Icon name="trash" /> {t('menu.deleteChat')}
              </button>
            </div>
          </section>
        </div>
      </div>
    </div>,
    document.body,
  )
}
