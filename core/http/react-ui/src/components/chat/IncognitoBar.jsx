import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Segmented, Switch } from '../ui'

// The incognito switch of the chat page, with its level and one line that
// says what the level does and does not do. While incognito is on, the bar is
// a band of its own colour over the thread, so the state cannot be missed.
//
//   levels     the levels the server offers ([] hides the bar)
//   level      the level that applies, '' when incognito is off
//   locked     the open chat is incognito: the level cannot change, and
//              turning the switch off discards the chat (onTurnOff)
//   onChange   (level) for a new chat; '' turns incognito off
//   onTurnOff  for an open incognito chat
export default function IncognitoBar({ levels, level, locked = false, onChange, onTurnOff }) {
  const { t } = useTranslation('chats')
  const labelId = useId()
  const descId = useId()
  if (!levels?.length && !level) return null
  const on = Boolean(level)
  const toggle = (next) => {
    if (next) onChange?.(levels[0])
    else if (locked) onTurnOff?.()
    else onChange?.('')
  }
  const options = (locked ? [level] : levels).map(l => ({ id: l, label: t(`incognito.levels.${l}`) }))
  return (
    <div className={`cx-incognito${on ? ' cx-incognito--on' : ''}`} data-testid="chat-incognito" data-level={level || undefined}>
      <div className="cx-incognito__row">
        <Icon name="user-secret" />
        <span id={labelId} className="cx-incognito__label">{t('incognito.label')}</span>
        <Switch checked={on} onChange={toggle} labelledBy={labelId} describedBy={on ? descId : undefined} testId="chat-incognito-switch" />
        {on && options.length > 1 && (
          <Segmented
            mode="radio"
            options={options}
            value={level}
            onChange={(l) => onChange?.(l)}
            label={t('incognito.levelLabel')}
            testId="chat-incognito-level"
            itemTestId="chat-incognito-level-"
          />
        )}
        {on && options.length === 1 && <span className="cx-incognito__level">{options[0].label}</span>}
      </div>
      {on && (
        <p id={descId} className="cx-incognito__desc" data-testid="chat-incognito-desc">
          {t(`incognito.describe.${level}`)}
        </p>
      )}
    </div>
  )
}
