/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { Notice } from '../ui'
import { CAPABILITY_PRESETS } from '../../utils/agentPresets'
import './agentparts.css'

// What an applied level or template says about itself, with the way back.
export function AppliedNotice({ applied, onUndo, onKeep, testId }) {
  const { t } = useTranslation('agents')
  if (!applied) return null
  return (
    <Notice
      severity="info"
      icon="check-circle"
      testId={testId}
      title={applied.kind === 'template' ? t('create.tpl.applied', { name: applied.name }) : applied.kind === 'addon' ? t('create.uses.addonOn.title', { name: applied.name }) : applied.kind === 'level' ? t('create.memoryLevelApplied.title', { name: applied.name }) : t('create.preset.applied', { name: applied.name })}
      reason={applied.kind === 'template' ? t('create.tpl.appliedBody') : applied.kind === 'addon' ? t('create.uses.addonOn.body', { count: applied.count, names: applied.names }) : applied.kind === 'level' ? t(`create.memoryLevelApplied.${applied.id}`) : t('create.preset.appliedBody')}
      fix={{ label: t('create.undo'), onClick: onUndo, testId: `${testId}-undo` }}
      secondary={{ label: t('create.keep'), onClick: onKeep, testId: `${testId}-keep` }}
    />
  )
}

// The capability levels: one choice that sets the tool switches and the
// grants in the form. Nothing is saved until the person saves. When the
// switches match none of the levels the group says it is a custom mix.
export default function AgentPresets({ active, ready, onPick }) {
  const { t } = useTranslation('agents')
  const labelId = useId()
  return (
    <div className="aed-preset" data-testid="agent-presets">
      <p className="aed-preset__label" id={labelId}>{t('create.preset.label')}</p>
      <div className="aed-preset__cards" role="radiogroup" aria-labelledby={labelId}>
        {CAPABILITY_PRESETS.map((p) => (
          <label key={p.id} className="aed-preset__card" data-on={active === p.id || undefined} data-testid={`agent-preset-${p.id}`}>
            <input
              type="radio" name="agent-preset" value={p.id} checked={active === p.id} disabled={!ready}
              onChange={() => onPick(p.id)}
            />
            <span className="aed-preset__text">
              <span className="aed-preset__title">{t(`create.preset.${p.id}.title`)}</span>
              <span className="aed-preset__body">{t(`create.preset.${p.id}.body`)}</span>
            </span>
          </label>
        ))}
      </div>
      <p className="aed-preset__state" data-testid="agent-preset-state" role="status">
        {ready ? (active ? t('create.preset.current', { name: t(`create.preset.${active}.title`) }) : t('create.preset.custom')) : t('create.preset.loading')}
      </p>
    </div>
  )
}
