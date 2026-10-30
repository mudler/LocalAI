/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import Button from '../ui/Button'
import ConfirmDialog from '../ConfirmDialog'
import { UpgradeReason } from '../upgrade/UpgradeBits'
import { shortDigest } from './addonState'
import { updateView, upgradeAvailability } from './updateState'

// What the confirmation shows before an upgrade: which library and channel,
// which version and digest goes to which, and that the data is backed up.
export function UpgradeSummary({ addon, t }) {
  const view = updateView(addon.update)
  return (
    <div data-testid="upgrade-confirm">
      <dl className="dk-kv ad-kv">
        <dt>{t('updates.confirm.library')}</dt><dd>{addon.sourceId || '—'}</dd>
        <dt>{t('updates.confirm.channel')}</dt><dd>{addon.channel || addon.selection || '—'}</dd>
        <dt>{t('updates.confirm.from')}</dt><dd>{addon.version} <span className="dk-mono">{shortDigest(addon.digest)}</span></dd>
        <dt>{t('updates.confirm.to')}</dt><dd>{view.version} <span className="dk-mono">{shortDigest(view.digest)}</span></dd>
      </dl>
      <p>{t('updates.confirm.body')}</p>
      <p>{t('updates.confirm.data')}</p>
    </div>
  )
}

// The words for why an upgrade cannot start.
export function reasonText(availability, t) {
  if (!availability.reason) return ''
  const base = t(`updates.why.${availability.reason}`)
  if (availability.reason === 'incompatible' && availability.reasons.length > 0) {
    return `${base} ${availability.reasons.map(r => t(`updates.reason.${r}`)).join('; ')}`
  }
  return base
}

// The Upgrade action of one installed add-on where its notice is seen, or the
// reason it cannot start with a way to check again. `upgrade` is the
// useAddonUpgrade of the view; the confirmation is <UpgradeConfirm>.
export function AddonUpgradeAction({ addon, pending, isAdmin, upgrade, working, variant = 'primary' }) {
  const { t } = useTranslation('addons')
  if (!addon && !pending) return null
  const availability = upgradeAvailability(addon, { isAdmin, pending })
  if (availability.startable) {
    return (
      <Button variant={variant} size="sm" icon="arrow-up" busy={working} disabled={working} onClick={() => upgrade.ask(addon)} data-testid="update-upgrade-card" aria-label={t('updates.upgradeNamed', { name: addon.title })}>
        {t('updates.upgrade')}
      </Button>
    )
  }
  if (!availability.reason) return null
  const checking = Boolean(addon) && upgrade.checks[addon.id]?.phase === 'working'
  return (
    <UpgradeReason
      text={reasonText(availability, t)}
      onCheck={availability.canCheck && addon ? () => upgrade.check(addon) : undefined}
      checking={checking}
    />
  )
}

// The confirmation before an upgrade, the same in every view.
export function UpgradeConfirm({ upgrade }) {
  const { t } = useTranslation('addons')
  const addon = upgrade.confirm
  return (
    <ConfirmDialog
      open={Boolean(addon)}
      title={addon ? t('updates.confirm.title', { name: addon.title, version: updateView(addon.update).version }) : ''}
      message={addon ? <UpgradeSummary addon={addon} t={t} /> : null}
      confirmLabel={t('updates.confirm.action')}
      onConfirm={() => { const a = addon; upgrade.ask(null); upgrade.upgrade(a) }}
      onCancel={() => upgrade.ask(null)}
    />
  )
}
