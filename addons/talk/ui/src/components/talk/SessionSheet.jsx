import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import ClientMCPDropdown from '../ClientMCPDropdown'
import Icon from '../Icon'
// eslint-disable-next-line no-unused-vars
import Sheet from './Sheet'

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
  // A speech-to-speech model answers every role itself. Its only optional
  // part is the VAD; the four-slot view would show names that do not run.
  const anyToAny = Boolean(pipeline && (pipeline.any_to_any || pipeline.self_contained))
  const slots = pipeline && !anyToAny
    ? [
        ['vad', pipeline.vad],
        ['transcription', pipeline.transcription],
        ['llm', pipeline.llm],
        ['tts', pipeline.tts],
      ]
    : []

  return (
    <Sheet id="talk-settings" title={t('settings.title')} desc={t('settings.desc')} closeLabel={t('settings.close')} onClose={onClose} testId="talk-settings">
      <section className="talk-sec">
        <h3>{t('settings.voiceLanguage')}</h3>
        <div className="talk-field">
          <label htmlFor="talk-voice">{t('settings.voice')}</label>
          <input id="talk-voice" className="talk-in" value={voice} onChange={(e) => onVoice(e.target.value)} placeholder={t('settings.voicePlaceholder')} list={anyToAny ? 'talk-voice-options' : undefined} />
          {anyToAny && (
            <datalist id="talk-voice-options">
              <option value="female" />
              <option value="male" />
            </datalist>
          )}
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
          {anyToAny ? (
            <>
              <p className="talk-help">{t('settings.anyToAny')}</p>
              {pipeline.vad ? (
                <dl className="talk-slots">
                  <div><dt>{t('settings.slot.vad')}</dt><dd>{pipeline.vad}</dd></div>
                </dl>
              ) : (
                <p className="talk-help">{t('settings.noVad')}</p>
              )}
            </>
          ) : (
            <dl className="talk-slots">
              {slots.map(([key, value]) => (
                <div key={key}><dt>{t(`settings.slot.${key}`)}</dt><dd>{value || '—'}</dd></div>
              ))}
            </dl>
          )}
          {!anyToAny && !pipeline.vad && <p className="talk-help">{t('settings.noVad')}</p>}
          {!connected && (
            <button type="button" className="talk-btn" onClick={onEditPipeline}>
              <Icon name="edit" /> {anyToAny ? t('settings.editConfig') : t('settings.editPipeline')}
            </button>
          )}
        </section>
      )}
    </Sheet>
  )
}
