import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import Sheet from '../../../../talk/ui/src/components/talk/Sheet'
import { MAX_NFE, MIN_NFE } from '../lib/turn'
import Icon from './Icon'

// The models of each part of a turn, what the chat model is told, and the
// settings of the video. The page keeps them in this browser.
export default function HeadSettings({ settings, choice, groups, pipelines, onChange, onPipeline, onClose, onSetup }) {
  const { t } = useTranslation('head')
  const set = (key) => (e) => onChange({ [key]: e.target.value })

  const select = (role, id) => {
    // A pipeline may name a model that has no capability flag of its part:
    // keep it in the list so the choice shows as it is.
    const list = groups[role] || []
    const options = choice[role] && !list.includes(choice[role]) ? [choice[role], ...list] : list
    return (
      <div className="talk-field">
        <label htmlFor={id}>{t(`settings.role.${role}`)}</label>
        <select id={id} className="talk-in" value={choice[role] || ''} onChange={(e) => onChange({ [role]: e.target.value, pipeline: role === 'avatar' ? settings.pipeline : '' })}>
          {options.length === 0 && <option value="">{t('settings.noneInstalled')}</option>}
          {options.map((m) => <option key={m} value={m}>{m}</option>)}
        </select>
      </div>
    )
  }

  return (
    <Sheet id="head-settings" title={t('settings.title')} desc={t('settings.desc')} closeLabel={t('settings.close')} onClose={onClose} testId="head-settings">
      <section className="talk-sec">
        <h3>{t('settings.models')}</h3>
        {pipelines.length > 0 && (
          <div className="talk-field">
            <label htmlFor="head-pipeline">{t('settings.pipeline')}</label>
            <select id="head-pipeline" className="talk-in" value={settings.pipeline || ''} onChange={(e) => onPipeline(e.target.value)}>
              <option value="">{t('settings.pipelineNone')}</option>
              {pipelines.map((p) => <option key={p.name} value={p.name}>{p.name}</option>)}
            </select>
            <p className="talk-help">{t('settings.pipelineHelp')}</p>
          </div>
        )}
        {select('chat', 'head-chat')}
        {select('stt', 'head-stt')}
        {select('tts', 'head-tts')}
        <div className="talk-field">
          <label htmlFor="head-voice">{t('settings.voice')}</label>
          <input id="head-voice" className="talk-in" value={settings.voice || ''} onChange={set('voice')} placeholder={t('settings.voicePlaceholder')} />
        </div>
        {select('avatar', 'head-avatar')}
        <button type="button" className="talk-btn" onClick={onSetup} data-testid="head-settings-setup">
          <Icon name="download" /> {t('setup.button')}
        </button>
        <p className="talk-help">{t('setup.settingsHelp')}</p>
      </section>

      <section className="talk-sec">
        <h3>{t('settings.instructions')}</h3>
        <textarea className="talk-ta" rows={4} value={settings.instructions} onChange={set('instructions')} aria-label={t('settings.instructions')} />
        <p className="talk-help">{t('settings.instructionsHelp')}</p>
      </section>

      <section className="talk-sec">
        <h3>{t('settings.video')}</h3>
        <div className="talk-field">
          <label htmlFor="head-nfe">{t('settings.nfe')}</label>
          <input id="head-nfe" className="talk-in" type="number" min={MIN_NFE} max={MAX_NFE} step="1" value={settings.nfe} onChange={set('nfe')} />
          <p className="talk-help">{t('settings.nfeHelp')}</p>
        </div>
        <div className="talk-field">
          <label htmlFor="head-seed">{t('settings.seed')}</label>
          <input id="head-seed" className="talk-in" type="number" min="0" step="1" value={settings.seed} onChange={set('seed')} placeholder={t('settings.seedPlaceholder')} />
        </div>
        <p className="talk-help">{t('settings.cpuNote')}</p>
        <p className="talk-help">{t('settings.licence')}</p>
      </section>
    </Sheet>
  )
}
