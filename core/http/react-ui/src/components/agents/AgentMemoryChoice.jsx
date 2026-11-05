/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import Icon from '../Icon'
import { Callout } from '../ui'
import { MEMORY_CHOICES, MEMORY_SAVE_RULES, memorySaves } from '../../utils/chatAgents'
import './agentmemory.css'

// The Memory section of the agent form: which notes the agent reads and where
// it saves. The host enforces the choice. An agent saved before the choice has
// none: it shows "As before" as a note, with the cards below to pick a clearer
// choice. `userOff` is the person's own switch (read only here): when it is
// off the choice has no effect, and the form says so.
export default function AgentMemoryChoice({ value, onChange, saves, onSaves, userOff, saveRule = '', onSaveRule, userLevel }) {
  const { t } = useTranslation('chats')
  const groupId = useId()
  const grantId = useId()
  const ruleId = useId()
  return (
    <div className="am-wrap">
      <div className="am" data-testid="agent-memory-choice">
        <p className="am__lede" id={groupId}>{t('agentMemory.lede')}</p>
        {userOff && (
          <Callout tone="info" icon="eye-off" testId="agent-memory-user-off">
            {t('agentMemory.offUser')}{' '}<Link className="cs-link" to="/app/account?tab=memory">{t('agentMemory.offUserFix')}</Link>
          </Callout>
        )}
        {!value && (
          <div className="am__before" data-testid="agent-memory-before" role="note">
            <Icon name="info" />
            <span>
              <b>{t('agentMemory.asBefore.title')}</b>{' '}{t('agentMemory.asBefore.body')}
            </span>
          </div>
        )}
        <div className="am__cards" role="radiogroup" aria-labelledby={groupId}>
          {MEMORY_CHOICES.map((c) => {
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
        {value === 'mine' && (
          <p className="am__warn" role="note" data-testid="agent-memory-mine-warn"><Icon name="warning" />{' '}{t('agentMemory.mineWarn')}</p>
        )}
        {memorySaves(value) && (
          <label className="am__grant" htmlFor={grantId}>
            <input id={grantId} type="checkbox" checked={Boolean(saves)} onChange={(e) => onSaves(e.target.checked)} data-testid="agent-memory-saves" />
            <span className="am__text">
              <span className="am__title">{t('agentMemory.saveGrant')}</span>
              <span className="am__body">{t('agentMemory.saveGrantHelp')}</span>
            </span>
          </label>
        )}
      </div>
      {value !== 'none' && onSaveRule && (
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
                    <span className="am__body">{r ? t(`agentMemory.rule.${key}.body`) : t('agentMemory.rule.use_mine.body', { level: t(`memorySettings.save.${userLevel || 'ask'}.title`) })}</span>
                  </span>
                </label>
              )
            })}
          </div>
          <p className="am__note" data-testid="agent-memory-rule-hint"><Icon name="info" />{' '}{t('agentMemory.rule.hint')}</p>
        </div>
      )}
      <p className="am__note"><Icon name="info" />{' '}{t('agentMemory.copyNote')}</p>
    </div>
  )
}
