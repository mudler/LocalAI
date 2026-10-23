import { useEffect, useRef } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import ClientMCPDropdown from '../ClientMCPDropdown'
import Icon from '../Icon'

// Everything about the call that is not the call itself: what the model is told,
// the voice and language, tools, Manage mode and the pipeline's parts. The
// pipeline and Manage mode are fixed once a session starts (the server reads
// them when the call is made), so they lock while connected.
export default function SessionSheet({
  connected, isAdmin, instructions, onInstructions, voice, onVoice, language, onLanguage,
  manageMode, onManageMode, pipeline, onEditPipeline,
  activeServerIds, onToggleServer, onServerAdded, onServerRemoved, connectionStatuses, getConnectedTools,
  onClose,
}) {
  const { t } = useTranslation('talk')
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

  const slots = pipeline && !pipeline.self_contained
    ? [
        ['vad', pipeline.vad],
        ['transcription', pipeline.transcription],
        ['llm', pipeline.llm],
        ['tts', pipeline.tts],
      ]
    : []

  return createPortal(
    <div className="dk-sheet-veil talk-sheet-veil" data-state="open" onMouseDown={onClose}>
      <div
        ref={sheetRef}
        className="dk-sheet talk-sheet"
        role="dialog"
        aria-modal="true"
        aria-labelledby="talk-settings-title"
        data-state="open"
        data-testid="talk-settings"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <span className="dk-sheet-grip" aria-hidden="true" />
        <div className="dk-sheet-head">
          <div>
            <h2 className="dk-sheet-title" id="talk-settings-title">{t('settings.title')}</h2>
            <p className="dk-sheet-desc">{t('settings.desc')}</p>
          </div>
          <button ref={closeRef} type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" aria-label={t('settings.close')} onClick={onClose}>
            <Icon name="close" />
          </button>
        </div>
        <div className="dk-sheet-body talk-sheet__body">
          <section className="talk-sec">
            <h3>{t('settings.voiceLanguage')}</h3>
            <div className="talk-field">
              <label htmlFor="talk-voice">{t('settings.voice')}</label>
              <input id="talk-voice" className="talk-in" value={voice} onChange={(e) => onVoice(e.target.value)} placeholder={t('settings.voicePlaceholder')} />
            </div>
            <div className="talk-field">
              <label htmlFor="talk-language">{t('settings.language')}</label>
              <input id="talk-language" className="talk-in" value={language} onChange={(e) => onLanguage(e.target.value)} placeholder={t('settings.languagePlaceholder')} />
            </div>
          </section>

          <section className="talk-sec">
            <h3>{t('settings.instructions')}</h3>
            <textarea
              className="talk-ta"
              rows={5}
              value={instructions}
              onChange={(e) => onInstructions(e.target.value)}
              placeholder={t('settings.instructionsPlaceholder')}
              aria-label={t('settings.instructions')}
            />
            <p className="talk-help">{t('settings.instructionsHelp')}</p>
          </section>

          <section className="talk-sec">
            <h3>{t('settings.tools')}</h3>
            <ClientMCPDropdown
              activeServerIds={activeServerIds}
              onToggleServer={onToggleServer}
              onServerAdded={onServerAdded}
              onServerRemoved={onServerRemoved}
              connectionStatuses={connectionStatuses}
              getConnectedTools={getConnectedTools}
            />
            {isAdmin && (
              <div className="talk-tog">
                <span>
                  <b><Icon name="user-shield" /> {t('settings.manage')}</b>
                  <small>{connected ? t('settings.manageLocked') : t('settings.manageDesc')}</small>
                </span>
                <button
                  type="button"
                  className="dk-switch"
                  role="switch"
                  aria-checked={manageMode}
                  aria-label={t('settings.manage')}
                  disabled={connected}
                  onClick={() => onManageMode(!manageMode)}
                />
              </div>
            )}
          </section>

          {pipeline && (
            <section className="talk-sec" data-testid="talk-pipeline">
              <h3>{t('settings.pipeline', { name: pipeline.name })}</h3>
              {pipeline.self_contained ? (
                <p className="talk-help">{t('settings.selfContained')}</p>
              ) : (
                <dl className="talk-slots">
                  {slots.map(([key, value]) => (
                    <div key={key}><dt>{t(`settings.slot.${key}`)}</dt><dd>{value || '—'}</dd></div>
                  ))}
                </dl>
              )}
              {!connected && (
                <button type="button" className="talk-btn" onClick={onEditPipeline}>
                  <Icon name="edit" /> {pipeline.self_contained ? t('settings.editConfig') : t('settings.editPipeline')}
                </button>
              )}
            </section>
          )}
        </div>
      </div>
    </div>,
    document.body,
  )
}
