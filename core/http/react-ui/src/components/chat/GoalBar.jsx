import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { goalAnnouncement, goalBar } from '../../utils/chatGoal'

// The goal of the chat, above the composer: its text, its state and what the
// person can do with it. Pause stops the running turn, which pauses the goal;
// Resume goes on with new counts; Clear removes it. A screen reader hears when
// the goal changes, also when it changed in another tab.
//
//   goal      the chat's goal from the server, or null
//   replaced  the text of the goal that the last /goal replaced, shown once
//   onPause, onResume, onClear
export default function GoalBar({ goal, replaced, onPause, onResume, onClear }) {
  const { t } = useTranslation('chat')
  const view = goalBar(goal)
  const [said, setSaid] = useState('')
  const prev = useRef(goal)
  useEffect(() => {
    const line = goalAnnouncement(prev.current, goal, t)
    prev.current = goal
    if (line) setSaid(line)
  }, [goal, t])

  return (
    <>
      <p className="visually-hidden" role="status" aria-live="polite" data-testid="goal-announce">{said}</p>
      {view && (
        <section className="cx-goal" data-testid="goal-bar" data-state={view.state} data-tone={view.tone} aria-label={t('goal.label')}>
          <div className="cx-goal__main">
            <Icon name={view.state === 'done' ? 'check-circle' : 'target'} className="cx-goal__icon" />
            <span className="cx-goal__label">{t('goal.label')}</span>
            <span className="cx-goal__text" title={view.text} data-testid="goal-text">{view.text}</span>
            <span className="cx-goal__state" data-testid="goal-state">{t(`goal.states.${view.state}`)}</span>
            <span className="cx-goal__acts">
              {view.action === 'pause' && (
                <button type="button" className="cx-goal__btn" data-testid="goal-pause" title={t('goal.pauseTitle')} onClick={onPause}>
                  <Icon name="pause" /><span>{t('goal.pause')}</span>
                </button>
              )}
              {view.action === 'resume' && (
                <button type="button" className="cx-goal__btn" data-testid="goal-resume" title={t('goal.resumeTitle')} onClick={onResume}>
                  <Icon name="play" /><span>{t('goal.resume')}</span>
                </button>
              )}
              <button type="button" className="cx-goal__btn cx-goal__btn--quiet" data-testid="goal-clear" title={t('goal.clearTitle')} onClick={onClear}>
                <Icon name="close" /><span>{t('goal.clear')}</span>
              </button>
            </span>
          </div>
          {(view.detailKey || replaced) && (
            <p className="cx-goal__detail" data-testid="goal-detail">
              {view.detailKey && <span>{t(view.detailKey, view.detail)}</span>}
              {replaced && <span className="cx-goal__replaced">{t('goal.replaced', { text: replaced })}</span>}
            </p>
          )}
        </section>
      )}
    </>
  )
}
