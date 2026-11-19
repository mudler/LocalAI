/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import { Button } from '../ui'
import ActionMenu from '../ActionMenu'
import { canAdopt, canHold } from '../../utils/modelUpdates'

// The actions of a model row: one button for the next step (Upgrade, Retry,
// Cancel, Remove old version, Release hold or Adopt) and a menu with the rest. Every
// action is a click: nothing upgrades a model by itself.
//
// actions: { upgrade, retry, cancel, removeOld, setHold, adopt, pending(row) }
export default function ModelRowActions({ row, actions, locked = false }) {
  const { t } = useTranslation('updates')
  if (!actions) return <div className="uc-row__acts" />
  const pending = actions.pending ? actions.pending(row) : false
  const off = locked || pending
  let main = null
  if (row.state === 'ready') {
    main = <Button size="sm" disabled={off} onClick={() => actions.upgrade(row)} aria-label={t('model.action.upgradeNamed', { name: row.name })} data-testid="model-upgrade">{t('model.action.upgrade')}</Button>
  } else if (row.state === 'failed') {
    main = <Button size="sm" icon="refresh" busy={pending} disabled={off} onClick={() => actions.retry(row)} aria-label={t('row.retryNamed', { name: row.name })} data-testid="model-retry">{t('row.retry')}</Button>
  } else if (row.state === 'updating' && row.cancellable) {
    main = <Button size="sm" variant="ghost" onClick={() => actions.cancel(row)} aria-label={t('row.cancelNamed', { name: row.name })} data-testid="model-cancel">{t('row.cancel')}</Button>
  } else if (row.state === 'cleanup') {
    main = <Button size="sm" icon="trash" disabled={off} onClick={() => actions.removeOld(row)} aria-label={t('model.action.removeOldNamed', { name: row.name })} data-testid="model-remove-old">{t('model.action.removeOld')}</Button>
  } else if (row.state === 'on-hold') {
    main = <Button size="sm" icon="lock" busy={pending} disabled={off} onClick={() => actions.setHold(row, false)} aria-label={t('model.action.releaseNamed', { name: row.name })} data-testid="model-release">{t('model.action.release')}</Button>
  } else if (canAdopt(row)) {
    main = <Button size="sm" icon="link" busy={pending} disabled={off} onClick={() => actions.adopt(row)} aria-label={t('model.action.adoptNamed', { name: row.name })} data-testid="model-adopt">{t('model.action.adopt')}</Button>
  }
  const items = [
    {
      key: 'adopt',
      icon: 'link',
      label: t('model.action.adopt'),
      hidden: !canAdopt(row),
      disabled: off,
      onClick: () => actions.adopt(row),
    },
    {
      key: 'hold',
      icon: 'lock',
      label: t('model.action.hold'),
      hidden: row.held || !canHold(row),
      disabled: off,
      onClick: () => actions.setHold(row, true),
    },
    {
      key: 'remove-old',
      icon: 'trash',
      label: t('model.action.removeOld'),
      hidden: !row.removableOld || row.state === 'cleanup' || row.state === 'updating',
      disabled: off,
      onClick: () => actions.removeOld(row),
    },
  ].filter(i => !i.hidden)
  return (
    <div className="uc-row__acts">
      {main}
      {items.length > 0 && (
        <ActionMenu
          items={items}
          ariaLabel={t('model.action.more', { name: row.name })}
          triggerLabel={t('model.action.more', { name: row.name })}
        />
      )}
    </div>
  )
}
