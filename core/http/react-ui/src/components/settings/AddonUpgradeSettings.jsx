import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { createHostSettingsClient } from '../../utils/hostSettingsClient'
import { createAddonsApi } from '../../utils/addonsApi'
import { apiUrl } from '../../utils/basePath'
import { useHostSettings } from '../../hooks/useHostSettings'

// The owner's switch for updating add-ons by themselves. The words are
// translated (admin.json, settings.addonUpdates); the setting key stays
// auto_upgrade_addons.
export default function AddonUpgradeSettings() {
  const { t } = useTranslation('admin')
  const client = useMemo(() => createHostSettingsClient({ api: createAddonsApi({ base: apiUrl }) }), [])
  const owner = useHostSettings(client, 'automatic-upgrades')
  return <section aria-label={t('settings.addonUpdates.label')} data-testid="owner-upgrade-settings">
    <label><input type="checkbox" checked={owner.draft?.auto_upgrade_addons ?? false}
      disabled={!owner.snapshot || owner.busy || !!owner.error}
      onChange={e => owner.setDraft({ ...owner.draft, auto_upgrade_addons: e.target.checked })} /> {t('settings.autoUpgradeAddons')}</label>
    <p>{t('settings.addonUpdates.note')}</p>
    {owner.error && <div role="alert">{t('settings.addonUpdates.unavailable', { message: owner.error.message })}
      <button type="button" disabled={owner.busy} onClick={owner.reload}>{t('settings.addonUpdates.reload')}</button>
      {owner.uncertain && <button type="button" disabled={owner.busy} onClick={() => owner.save({})}>{t('settings.addonUpdates.retry')}</button>}
    </div>}
    {owner.snapshot && <p>{t('settings.addonUpdates.policy', { revision: owner.snapshot.revision })}</p>}
    <button type="button" disabled={!owner.snapshot || owner.busy || !!owner.error || owner.draft.auto_upgrade_addons === owner.snapshot.policy.auto_upgrade_addons}
      onClick={() => owner.save({ auto_upgrade_addons: owner.draft.auto_upgrade_addons })}>{t('settings.addonUpdates.apply')}</button>
  </section>
}
