import { useMemo } from 'react'
import { createHostSettingsClient } from '../../utils/hostSettingsClient'
import { createAddonsApi } from '../../utils/addonsApi'
import { apiUrl } from '../../utils/basePath'
import { useHostSettings } from '../../hooks/useHostSettings'

export default function AddonUpgradeSettings() {
  const client = useMemo(() => createHostSettingsClient({ api: createAddonsApi({ base: apiUrl }) }), [])
  const owner = useHostSettings(client, 'automatic-upgrades')
  return <section aria-label="Add-on upgrades" data-testid="owner-upgrade-settings">
    <label><input type="checkbox" checked={owner.draft?.auto_upgrade_addons ?? false}
      disabled={!owner.snapshot || owner.busy || !!owner.error}
      onChange={e => owner.setDraft({ ...owner.draft, auto_upgrade_addons: e.target.checked })} /> Upgrade add-ons automatically</label>
    <p>The add-on owner applies this setting. New generations can run executable code.</p>
    {owner.error && <div role="alert">Owner settings unavailable or changed: {owner.error.message}
      <button type="button" disabled={owner.busy} onClick={owner.reload}>Reload and rebase edits</button>
      {owner.uncertain && <button type="button" disabled={owner.busy} onClick={() => owner.save({})}>Retry same operation</button>}
    </div>}
    {owner.snapshot && <p>Owner policy · revision {owner.snapshot.revision}</p>}
    <button type="button" disabled={!owner.snapshot || owner.busy || !!owner.error || owner.draft.auto_upgrade_addons === owner.snapshot.policy.auto_upgrade_addons}
      onClick={() => owner.save({ auto_upgrade_addons: owner.draft.auto_upgrade_addons })}>Apply upgrade setting</button>
  </section>
}
