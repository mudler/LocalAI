/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import Segmented from '../ui/Segmented'
import { SwitchHead, SwitchPopover } from './SwitchList'
import { REASONING_EFFORTS } from '../../utils/chatSettings'

// The reasoning effort chip of the composer, next to Tools and Skills. It
// shows the level of this chat and opens a kit Segmented to change it. The
// level is a setting the server keeps with the chat (the settings sheet shows
// the same value), so the page writes it through the chat's settings and this
// chip only reads and asks. "Default" leaves the model's own level.
//
//   effort     the level now ('' for the model's own)
//   onEffort   called with the level picked
//   openRef    a ref that gets { open() }: the /effort slash command opens the
//              menu with the focus on the level now
export default function ReasoningChips({ effort = '', onEffort, openRef, disabled = false }) {
  const { t } = useTranslation('chat')
  const current = effort || ''
  const nameOf = (v) => t(`settings.effort.${v || 'default'}`)
  const options = REASONING_EFFORTS.map((v) => ({ id: v || 'default', label: nameOf(v) }))
  const focusLevel = useCallback((menu) => {
    menu.querySelector('[aria-pressed="true"]')?.focus()
  }, [])
  if (disabled) return null
  return (
    <SwitchPopover
      icon="brain"
      label={`${t('reasoning.chip')}: ${nameOf(current)}`}
      title={t('reasoning.title')}
      count={0}
      testId="chat-effort"
      menuClassName="swl-menu--wide"
      openRef={openRef}
      onOpen={focusLevel}
    >
      {(close) => (
        <>
          <SwitchHead title={t('settings.reasoningEffort')}>
            <p className="swl-head__note">{t('reasoning.note')}</p>
          </SwitchHead>
          <div className="cx-effort">
            <Segmented
              label={t('settings.reasoningEffort')}
              options={options}
              value={current || 'default'}
              onChange={(id) => { onEffort(id === 'default' ? '' : id); close() }}
              testId="chat-effort-levels"
              itemTestId="chat-effort-"
            />
          </div>
        </>
      )}
    </SwitchPopover>
  )
}
