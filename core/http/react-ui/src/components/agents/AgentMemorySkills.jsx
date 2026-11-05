/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import './agentuses.css'

// The skills the host grants with the notes: no switches, shown once, with
// the provider slot that ships them. Nothing renders while there are none.
// rows: the skill rows with `provider` and `locked`; state: on | off | offUser.
export default function AgentMemorySkills({ rows, state }) {
  const { t } = useTranslation('agents')
  if (!rows.length) return null
  const slot = rows[0].provider
  return (
    <div className="au-mem" data-testid="agent-memory-skills" data-state={state}>
      <span className="au-mem__h">
        {t('create.uses.memorySkills.title')}
        <span className="au-mem__from">{t('create.uses.memorySkills.from', { slot: t(`create.uses.slot.${slot}`, { defaultValue: slot }) })}</span>
      </span>
      <ul className="au-mem__list">
        {rows.map((s) => <li key={s.id || s.name}><code>{s.name}</code></li>)}
      </ul>
      <p className="au-mem__state">{t(`create.uses.memorySkills.${state}`)}</p>
    </div>
  )
}
