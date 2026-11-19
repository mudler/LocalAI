/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, StatusChip, Switch } from '../ui'
import { canAdopt, canHold } from '../../utils/modelUpdates'
import { modelChip, useModelDetail } from './modelWords'
import './model-updates.css'

// The update state of one installed gallery model in the Models inspector:
// the chip and the words of the Updates page, the next step (Upgrade or Retry,
// which open the same confirmation), and the "Hold this version" switch. The
// switch applies at once: a held model is offered no update until it is
// released. "Pin" is a different thing here (it keeps a model in memory).
//
// `model` is a useModelUpgradeActions result; its dialogs are drawn by the
// page (ModelActionDialogs).
export default function ModelUpdateCard({ row, model }) {
  const { t } = useTranslation('updates')
  const detail = useModelDetail(row)
  const holdId = useId()
  if (!row) return null
  const chip = modelChip(row, t)
  const pending = model.actions.pending(row)
  const holdable = canHold(row)
  return (
    <section className="mu-model" aria-labelledby={`${holdId}-title`} data-testid="model-update-card" data-state={row.state}>
      <div className="mu-model__head">
        <span className="detail-pane__label" id={`${holdId}-title`}>{t('model.card.title')}</span>
        {chip ? <StatusChip {...chip} testId="model-update-chip" /> : <StatusChip tone="ok" label={t('model.card.current')} testId="model-update-chip" />}
      </div>
      {detail && <p className="mu-model__line">{detail.text}</p>}
      {(row.state === 'ready' || row.state === 'failed' || row.state === 'cleanup') && (
        <div className="badge-row">
          {row.state === 'ready' && (
            <Button size="sm" icon="arrow-up" disabled={pending} onClick={() => model.actions.upgrade(row)} data-testid="model-card-upgrade">{t('model.action.upgrade')}</Button>
          )}
          {row.state === 'failed' && (
            <Button size="sm" icon="refresh" busy={pending} disabled={pending} onClick={() => model.actions.retry(row)} data-testid="model-card-retry">{t('row.retry')}</Button>
          )}
          {(row.state === 'cleanup' || (row.removableOld && row.state !== 'updating')) && (
            <Button size="sm" variant="ghost" icon="trash" disabled={pending} onClick={() => model.actions.removeOld(row)} data-testid="model-card-remove-old">{t('model.action.removeOld')}</Button>
          )}
          <Button size="sm" variant="ghost" to="/app/updates">{t('model.card.open')}</Button>
        </div>
      )}
      {canAdopt(row) && (
        <div className="badge-row">
          <Button size="sm" icon="link" busy={pending} disabled={pending} onClick={() => model.actions.adopt(row)} aria-label={t('model.action.adoptNamed', { name: row.name })} data-testid="model-card-adopt">{t('model.action.adopt')}</Button>
        </div>
      )}
      {holdable && (
        <div className="mu-model__hold">
          <span className="mu-model__hold-text">
            <span id={`${holdId}-hold`}>{t('model.action.hold')}</span>
            <span className="mu-model__hint" id={`${holdId}-hint`}>{t('model.card.holdHint')}</span>
          </span>
          <Switch
            checked={row.held}
            disabled={pending}
            labelledBy={`${holdId}-hold`}
            describedBy={`${holdId}-hint`}
            onChange={(on) => model.actions.setHold(row, on)}
            testId="model-hold-switch"
          />
        </div>
      )}
    </section>
  )
}
