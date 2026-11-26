/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Segmented } from '../ui'
import { SwitchPopover, SwitchRow } from './SwitchList'

// Incognito in the composer, on Home and on the chat page. Two parts:
//
//   IncognitoButton  a button of the composer's bottom bar, after Live, with
//                    the menu of the composer chips: the switch, the level
//                    and what the level keeps. Off, it is an icon like the
//                    attach button; on, it is a small accent pill that names
//                    the level.
//   IncognitoNote    the line under the composer while incognito is on: the
//                    level and what it does and does not keep, so the state
//                    is visible without opening the menu.
//
// Both take the same props:
//
//   levels     the levels the server offers ([] hides the chip)
//   level      the level that applies, '' when incognito is off
//   locked     the open chat is incognito: the level cannot change, and
//              turning the switch off discards the chat (onTurnOff)
//   onChange   (level) for a new chat; '' turns incognito off
//   onTurnOff  for an open incognito chat

export function IncognitoButton({ levels, level, locked = false, onChange, onTurnOff }) {
  const { t } = useTranslation('chats')
  const levelsId = useId()
  if (!levels?.length && !level) return null
  const on = Boolean(level)
  const toggle = (next) => {
    if (next) onChange?.(levels[0])
    else if (locked) onTurnOff?.()
    else onChange?.('')
  }
  const name = t('incognito.label')
  const options = (locked ? [level] : levels).map(l => ({ id: l, label: t(`incognito.levels.${l}`) }))
  const levelName = on ? t(`incognito.levels.${level}`) : ''
  const state = on ? t('incognito.stateOn', { level: levelName }) : t('incognito.stateOff')
  return (
    <SwitchPopover
      foot
      accent={on}
      icon="user-secret"
      label={levelName}
      title={state}
      ariaLabel={state}
      data={{ level: level || undefined }}
      testId="chat-incognito"
      className="cx-incognito-ctl"
      menuClassName="swl-menu--wide"
      onOpen={(menu) => menu.querySelector('[role="switch"]')?.focus()}
    >
      <SwitchRow
        name={name}
        blurb={t(locked ? 'incognito.aboutLocked' : 'incognito.about')}
        checked={on}
        onChange={toggle}
        switchTestId="chat-incognito-switch"
      />
      {/* Always drawn, so the description is a live region from the start and
          a screen reader reads it when the switch turns incognito on. */}
      <div className={`cx-incognito${on ? '' : ' cx-incognito--off'}`}>
        {on && (
          <>
            <span id={levelsId} className="cx-incognito__heading">{t('incognito.levelLabel')}</span>
            {options.length > 1 ? (
              <Segmented
                mode="radio"
                options={options}
                value={level}
                onChange={(l) => onChange?.(l)}
                labelledBy={levelsId}
                testId="chat-incognito-level"
                itemTestId="chat-incognito-level-"
              />
            ) : (
              // One level only (an open chat, or a server that offers one):
              // its name, with nothing to pick.
              <span className="cx-incognito__level" data-testid="chat-incognito-level-name">{options[0].label}</span>
            )}
          </>
        )}
        <p className="cx-incognito__desc" data-testid={on ? 'chat-incognito-desc' : undefined} aria-live="polite">
          {on ? t(`incognito.describe.${level}`) : ''}
        </p>
      </div>
    </SwitchPopover>
  )
}

export function IncognitoNote({ level }) {
  const { t } = useTranslation('chats')
  if (!level) return null
  return (
    <p className="cx-incognito-note" data-testid="chat-incognito-note" data-level={level}>
      <Icon name="user-secret" />
      <span>
        <span className="cx-incognito-note__title">{t('incognito.label')} · {t(`incognito.levels.${level}`)}.</span>
        {' '}{t(`incognito.describe.${level}`)}
      </span>
    </p>
  )
}
