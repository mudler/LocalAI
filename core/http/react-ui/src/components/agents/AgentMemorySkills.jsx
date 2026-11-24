/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import { SwitchRow } from '../chat/SwitchList'
import './agentuses.css'

// The skills the host grants with the notes, as real rows: locked, with a
// switch that shows the state and cannot be changed, and the provider slot that
// ships them as the source. The person does not grant them; they follow memory.
// rows: the skill rows with `provider` and `locked`; state: on | off | offUser.
export default function AgentMemorySkills({ rows, state }) {
  const { t } = useTranslation('agents')
  if (!rows.length) return null
  const slot = rows[0].provider
  return (
    <div className="au-mem" data-testid="agent-memory-skills" data-state={state}>
      <span className="au-mem__h">{t('create.uses.memorySkills.title')}</span>
      <div className="swl-list">
        {rows.map((s) => (
          <SwitchRow
            key={s.id || s.name}
            name={s.name}
            desc={t(`create.uses.memorySkills.${state}`)}
            meta={memorySkillFrom(t, slot)}
            metaKind="provider"
            checked={state === 'on'}
            disabled
            stacked
            onChange={() => {}}
            testId="agent-memory-skill"
            data={{ state }}
          />
        ))}
      </div>
    </div>
  )
}

// "From <provider slot>", the source label of a skill the host grants.
export const memorySkillFrom = (t, slot) => t('create.uses.memorySkills.from', { slot: t(`create.uses.slot.${slot}`, { defaultValue: slot }) })
