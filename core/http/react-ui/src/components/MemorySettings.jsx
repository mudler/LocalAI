/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { useMemorySettings } from '../hooks/useMemorySettings'
import { chatMemoryApi } from '../utils/api'
import Icon from './Icon'
import ProviderPicker from './ProviderPicker'
import { Callout, LoadError, SkeletonRows, Switch } from './ui'
import './memorysettings.css'

// The ways a note is saved, in the order they are listed. The value is what the
// server stores; the default is the one a person who never chose gets.
export const SAVE_LEVELS = [
  { value: 'auto' },
  { value: 'ask', isDefault: true },
  { value: 'always' },
  { value: 'never' },
]

// The rows for a note proposed in a messaging app. The row can never be looser
// than the level above when that is Always confirm or Never.
export const MESSAGING_LEVELS = [
  { value: 'ask', isDefault: true },
  { value: 'paired_private' },
  { value: 'never' },
]

// Settings > Memory, the controls: the On or Off switch first, then how a note
// is saved. The notes themselves (MemoryTab) sit below. The page is laid out in
// sections that share one card, so the provider choice, the rule for messaging
// apps and the scope of an agent can take their place later without a new
// page.
export default function MemorySettings({ addToast, children }) {
  const { t } = useTranslation('chats')
  const { settings, error, saving, reload, change } = useMemorySettings()
  const titleId = useId()
  const ledeId = useId()
  const saveId = useId()
  const messagingId = useId()
  const notesId = useId()

  const notes = (
    <section className="ms-notes" aria-labelledby={notesId} data-testid="memory-notes-section">
      <div className="ms-notes-head">
        <h3 className="ms-h" id={notesId}>{t('memorySettings.notesTitle')}</h3>
        <p className="ms-p">{t('memorySettings.notesLede')}</p>
      </div>
      {children}
    </section>
  )
  if (error && !settings) return <div className="ms-page" data-testid="memory-settings"><LoadError title={t('memorySettings.loadError')} error={error} onRetry={reload} testId="memory-settings-error" />{notes}</div>
  if (!settings) return <div className="ms-page" data-testid="memory-settings"><SkeletonRows count={2} />{notes}</div>

  const on = settings.enabled
  // Always confirm and Never hold in messaging apps too: the row cannot loosen them.
  const lockedBy = settings.save_mode === 'always' || settings.save_mode === 'never' ? settings.save_mode : ''
  const store = async (body) => {
    if (!(await change(body))) addToast?.(t('memorySettings.saveFailed'), 'error')
  }

  return (
    <div className="ms-page" data-testid="memory-settings">
      <section className={`ms-card ms-switch${on ? '' : ' ms-switch--off'}`} aria-labelledby={titleId} data-testid="memory-switch-card" data-state={on ? 'on' : 'off'}>
        <div className="ms-switch__text">
          <h3 className="ms-h" id={titleId}>{on ? t('memorySettings.titleOn') : t('memorySettings.titleOff')}</h3>
          <p className="ms-p" id={ledeId}>{on ? t('memorySettings.ledeOn') : t('memorySettings.ledeOff')}</p>
          {!settings.available && <p className="ms-p ms-p--warn" data-testid="memory-unavailable">{t('memorySettings.unavailable')}</p>}
        </div>
        <div className="ms-switch__ctl">
          <span className="ms-switch__word" aria-hidden="true">{t('memorySettings.off')}</span>
          <Switch checked={on} disabled={saving} labelledBy={titleId} describedBy={ledeId} testId="memory-switch" onChange={(next) => store({ enabled: next })} />
          <span className="ms-switch__word" aria-hidden="true">{t('memorySettings.on')}</span>
        </div>
      </section>
      {error && <Callout tone="error" role="alert" testId="memory-settings-save-error">{error.message}</Callout>}

      {/* The providers come from the slot list: the built-in one, Off and every
          add-on that says it can serve memory. The switch above and the Off
          choice here are one answer, so each reads the other again. */}
      <ProviderPicker slot="memory" version={`${settings.provider}:${on}`} onChange={reload} addToast={addToast} forget={() => chatMemoryApi.forget()} />

      <section className="ms-card" aria-labelledby={saveId} data-testid="memory-save-card">
        <h3 className="ms-h" id={saveId}>{t('memorySettings.saveTitle')}</h3>
        <p className="ms-p">{t('memorySettings.saveLede')}</p>
        <div className="ms-levels" role="radiogroup" aria-labelledby={saveId}>
          {SAVE_LEVELS.map((lv) => {
            const checked = settings.save_mode === lv.value
            return (
              <label key={lv.value} className={`ms-level${checked ? ' ms-level--on' : ''}`} data-testid={`memory-save-${lv.value}`}>
                <input type="radio" name="memory-save-mode" value={lv.value} checked={checked} disabled={saving} onChange={() => store({ save_mode: lv.value })} />
                <span className="ms-level__text">
                  <span className="ms-level__title">{t(`memorySettings.save.${lv.value}.title`)}{lv.isDefault && <span className="ms-tag">{t('memorySettings.default')}</span>}</span>
                  <span className="ms-level__body">{t(`memorySettings.save.${lv.value}.body`)}</span>
                </span>
              </label>
            )
          })}
        </div>

        <h4 className="ms-sub" id={messagingId}>{t('memorySettings.messaging.title')}</h4>
        <p className="ms-p">{t('memorySettings.messaging.lede')}</p>
        <div className="ms-levels" role="radiogroup" aria-labelledby={messagingId} data-testid="memory-messaging">
          {MESSAGING_LEVELS.map((lv) => {
            const checked = (settings.messaging_save || 'ask') === lv.value
            return (
              <label key={lv.value} className={`ms-level${checked ? ' ms-level--on' : ''}`} data-testid={`memory-messaging-${lv.value}`}>
                <input type="radio" name="memory-messaging-save" value={lv.value} checked={checked} disabled={saving} onChange={() => store({ messaging_save: lv.value })} />
                <span className="ms-level__text">
                  <span className="ms-level__title">{t(`memorySettings.messaging.${lv.value}.title`)}{lv.isDefault && <span className="ms-tag">{t('memorySettings.default')}</span>}</span>
                  <span className="ms-level__body">{t(`memorySettings.messaging.${lv.value}.body`)}</span>
                  {lv.value === 'paired_private' && <span className="ms-level__fine">{t('memorySettings.messaging.limit')}</span>}
                </span>
              </label>
            )
          })}
        </div>
        {lockedBy && (
          <p className="ms-p ms-p--note" data-testid="memory-messaging-locked" role="note"><Icon name="info" />{' '}{t(lockedBy === 'always' ? 'memorySettings.messaging.lockedAlways' : 'memorySettings.messaging.lockedNever')}</p>
        )}
        <p className="ms-p ms-p--note"><Icon name="info" />{' '}{t('memorySettings.saveNote')}</p>
      </section>
      {notes}
    </div>
  )
}
