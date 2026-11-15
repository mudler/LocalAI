/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import Icon from '../Icon'
import { Callout } from '../ui'
import { MEMORY_LEVELS, MEMORY_SAVE_RULES, MEMORY_WHOSE } from '../../utils/chatAgents'
import './agentmemory.css'

// The Memory section of the agent form. One control says how much the agent
// does with notes (Off, Reads notes, Reads and saves notes); a second one,
// shown while memory is not Off, says whose notes. The host enforces both.
// An agent saved before the choice of whose notes has none: it shows "As
// before" as a note, with the cards to pick a clearer choice. `userOff` is the
// person's own switch (read only here): when it is off the agent's level has
// no effect, and the form says so. `level` is off | reads | saves.
export default function AgentMemoryChoice({ level, onLevel, value, onChange, userOff, saveRule = '', onSaveRule, userLevel, notice, children }) {
  const { t } = useTranslation('chats')
  const levelId = useId()
  const groupId = useId()
  const ruleId = useId()
  const off = level === 'off'
  const saves = level === 'saves'
  return (
    <div className="am-wrap">
      <div className="am" data-testid="agent-memory-choice">
        <p className="am__lede">{t('agentMemory.lede')}</p>
        {userOff && (
          <Callout tone="info" icon="eye-off" testId="agent-memory-user-off">
            {t('agentMemory.offUser')}{' '}<Link className="cs-link" to="/app/account?tab=memory">{t('agentMemory.offUserFix')}</Link>
          </Callout>
        )}
        <p className="am__rule-title" id={levelId}>{t('agentMemory.level.title')}</p>
        <div className="am__cards am__cards--levels" role="radiogroup" aria-labelledby={levelId} data-testid="agent-memory-level">
          {MEMORY_LEVELS.map((l) => (
            <label key={l} className={`am__card${level === l ? ' am__card--on' : ''}`} data-testid={`agent-memory-level-${l}`}>
              <input type="radio" name="agent-memory-level" value={l} checked={level === l} onChange={() => onLevel(l)} />
              <span className="am__text">
                <span className="am__title">{t(`agentMemory.level.${l}.title`)}</span>
                <span className="am__body">{t(`agentMemory.level.${l}.body`)}</span>
              </span>
            </label>
          ))}
        </div>
        {notice}
        {off ? (
          <p className="am__note" role="note" data-testid="agent-memory-whose-off"><Icon name="info" />{' '}{t('agentMemory.whoseOff')}</p>
        ) : (
          <>
            {!value && (
              <div className="am__before" data-testid="agent-memory-before" role="note">
                <Icon name="info" />
                <span>
                  <b>{t('agentMemory.asBefore.title')}</b>{' '}{t('agentMemory.asBefore.body')}
                </span>
              </div>
            )}
            <p className="am__rule-title" id={groupId}>{t('agentMemory.whose')}</p>
            <div className="am__cards" role="radiogroup" aria-labelledby={groupId} data-testid="agent-memory-whose">
              {MEMORY_WHOSE.map((c) => {
                const checked = value === c
                return (
                  <label key={c} className={`am__card${checked ? ' am__card--on' : ''}`} data-testid={`agent-memory-${c}`}>
                    <input type="radio" name="agent-memory" value={c} checked={checked} onChange={() => onChange(c)} />
                    <span className="am__text">
                      <span className="am__title">
                        {t(`agentMemory.${c}.title`)}
                        {c === 'mine_and_own' && <span className="am__tag">{t('agentMemory.recommended')}</span>}
                      </span>
                      <span className="am__body">{t(`agentMemory.${c}.body`)}</span>
                    </span>
                  </label>
                )
              })}
            </div>
            {value === 'mine' && saves && (
              <p className="am__warn" role="note" data-testid="agent-memory-mine-warn"><Icon name="warning" />{' '}{t('agentMemory.mineWarn')}</p>
            )}
          </>
        )}
      </div>
      {saves && onSaveRule && (
        <div className="am am__rule" data-testid="agent-memory-rule">
          <p className="am__rule-title" id={ruleId}>{t('agentMemory.rule.title')}</p>
          <div className="am__rule-cards" role="radiogroup" aria-labelledby={ruleId}>
            {['', ...MEMORY_SAVE_RULES].map((r) => {
              const checked = saveRule === r || (r === '' && saveRule === 'use_mine')
              const key = r || 'use_mine'
              return (
                <label key={key} className={`am__card am__card--small${checked ? ' am__card--on' : ''}`} data-testid={`agent-memory-rule-${key}`}>
                  <input type="radio" name="agent-memory-rule" value={key} checked={checked} onChange={() => onSaveRule(r)} />
                  <span className="am__text">
                    <span className="am__title">{t(`agentMemory.rule.${key}.title`)}</span>
                    <span className="am__body">{r ? t(`agentMemory.rule.${key}.body`) : (
                      <Trans t={t} i18nKey="agentMemory.rule.use_mine.body" values={{ level: t(`memorySettings.save.${userLevel || 'ask'}.title`) }}
                        components={{ b: <b data-testid="agent-memory-user-level" />, settings: <Link className="cs-link" to="/app/account?tab=memory" data-testid="agent-memory-settings-link" /> }} />
                    )}</span>
                  </span>
                </label>
              )
            })}
          </div>
          <p className="am__note" data-testid="agent-memory-rule-hint"><Icon name="info" />{' '}{t('agentMemory.rule.hint')}</p>
        </div>
      )}
      {!saves && (
        <p className="am__note" role="note" data-testid="agent-memory-rule-off"><Icon name="info" />{' '}{t(off ? 'agentMemory.rule.offReason' : 'agentMemory.rule.readsReason')}</p>
      )}
      <p className="am__note"><Icon name="info" />{' '}{t('agentMemory.copyNote')}</p>
      {children}
    </div>
  )
}
