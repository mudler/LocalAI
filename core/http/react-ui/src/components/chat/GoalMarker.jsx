import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { goalMarker } from '../../utils/chatGoal'

// A goal line of the transcript: the goal was set, replaced, paused, resumed,
// reached, blocked or cleared, or the chat went on with it by itself.
export default function GoalMarker({ msg }) {
  const { t } = useTranslation('chat')
  const m = goalMarker(msg)
  const text = m.textKey ? t(m.textKey) : m.text
  return (
    <div className="cx-goal-line" data-testid="goal-marker" data-kind={m.kind} data-tone={m.tone}>
      <Icon name={m.icon} className="cx-goal-line__icon" />
      <span className="cx-goal-line__label">{t(m.labelKey, m.params)}</span>
      {text && <span className="cx-goal-line__text">{text}</span>}
    </div>
  )
}
