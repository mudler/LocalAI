/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import { SwitchGroup, SwitchRow } from '../chat/SwitchList'
import { groupOps, tickable } from './hostGrantsState'

// The words under one operation: why its switch cannot move, or what leaving
// it off means. The owner decides every status; this only says it.
export function opNote(op, t) {
  if (op.status === 'blocked') return { reason: t('hostAccess.op.blocked') }
  if (op.status === 'unsupported') return { reason: t('hostAccess.op.unsupported') }
  if (op.status === 'allowed') return { reason: t(op.source === 'deployment' ? 'hostAccess.op.byDeployment' : 'hostAccess.op.alreadyAllowed') }
  return { desc: t(op.required ? 'hostAccess.op.required' : 'hostAccess.op.optional') }
}

// The operations an add-on asks for, in groups, one switch each. Required ones
// are on and cannot be turned off; optional ones start off. A blocked one is
// greyed with the reason. `added` highlights what a new version asks for on
// top of what the old one held.
export default function HostAccessList({ ops, ticked, onTick, added = [], testId = 'hostaccess', locked = false }) {
  const { t } = useTranslation('addons')
  return (
    <div className="ad-hostlist" data-testid={`${testId}-list`}>
      {groupOps(ops).map(g => (
        <SwitchGroup key={g.family} title={t(`hostAccess.family.${g.family}`, { defaultValue: g.family })} testId={`${testId}-group-${g.family}`}>
          {g.ops.map(op => {
            const on = tickable(op) ? ticked.includes(op.name) : op.status === 'allowed'
            const note = opNote(op, t)
            const isNew = added.includes(op.name)
            return (
              <SwitchRow
                key={op.name}
                name={op.title}
                blurb={op.text}
                desc={note.desc}
                reason={note.reason}
                meta={[op.risk ? t(`hostAccess.risk.${op.risk}`) : '', isNew ? t('hostAccess.op.new') : ''].filter(Boolean).join(' · ') || undefined}
                checked={on}
                disabled={locked || !tickable(op) || op.required}
                onChange={(next) => onTick(op.name, next)}
                stacked
                testId={`${testId}-op`}
                data={{ op: op.name, required: op.required ? 'true' : 'false', status: op.status, added: isNew ? 'true' : undefined }}
              />
            )
          })}
        </SwitchGroup>
      ))}
    </div>
  )
}
