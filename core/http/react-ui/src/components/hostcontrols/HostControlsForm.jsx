/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Suspense, useMemo, useState } from 'react'
import { Callout, LoadError, SkeletonRows } from '../ui'
import PendingBar from '../settings/PendingBar'
import UnsavedChangesGuard from '../UnsavedChangesGuard'
import { pendingChanges, payloadFor, runChecks, visibleChanges } from '../../utils/settingsPending'
import { createHostSettingsClient } from '../../utils/hostSettingsClient'
import { createAddonsApi } from '../../utils/addonsApi'
import { apiUrl } from '../../utils/basePath'
import { useHostSettings } from '../../hooks/useHostSettings'
import { controlsOf } from '../../addons/hostControls'
import { HOST_CONTROLS } from '../../addons/hostControlComponents'
import '../../pages/settings.css'

// The host controls of one add-on on its Settings tab, in one form: the
// runtime settings are read once, every block edits the same draft, and one
// pending bar applies it with the checks and the history of the Settings
// page. The add-on supplies none of this; the apps list says which controls
// the host draws for it.
const ownerClient = createHostSettingsClient({ api: createAddonsApi({ base: apiUrl }) })

export default function HostControlsForm({ app, client = ownerClient }) {
  const ids = controlsOf(app)
  const owner = useHostSettings(client, app?.id)
  const { draft: settings, snapshot, error, busy: saving } = owner
  const initial = snapshot?.policy
  const [showDiff, setShowDiff] = useState(false)
  const changes = useMemo(() => pendingChanges(initial, settings), [initial, settings])
  const visible = useMemo(() => visibleChanges(changes), [changes])
  const checks = useMemo(() => settings ? runChecks(changes, initial, settings) : [], [changes, initial, settings])
  const pending = new Set(visible.map(c => c.field.key))
  const dirty = visible.length > 0
  if (!ids.length) return null
  if (!settings) return error ? <LoadError title="Owner settings unavailable" error={error} onRetry={owner.reload} /> : <SkeletonRows rows={3} />
  const blocked = error || saving
  return (
    <div className="ad-host-controls" data-testid="addon-host-controls">
      <UnsavedChangesGuard when={dirty} />
      <p className="st-note">Owner policy · revision {snapshot.revision}</p>
      {error && <Callout tone="error" testId="addon-host-controls-error">
        {error.kind === 'revision_conflict' ? 'Owner policy changed. Reload and rebase your edits, then review before applying.' : `Owner settings unavailable or write uncertain: ${error.message}. Save is disabled until reconciliation.`}
        <button type="button" className="dk-btn dk-btn--secondary" onClick={owner.reload} disabled={saving}>Reload and rebase edits</button>
        {owner.uncertain && <button type="button" className="dk-btn dk-btn--secondary" onClick={() => owner.save({})} disabled={saving}>Retry same operation</button>}
      </Callout>}
      <fieldset disabled={saving || owner.uncertain}>
        {ids.map(id => {
          const Block = HOST_CONTROLS[id]?.settings
          return Block ? <Suspense key={id} fallback={<SkeletonRows rows={2} />}><Block app={app} settings={settings} initial={initial} onChange={owner.setDraft} pending={pending} /></Suspense> : null
        })}
      </fieldset>
      {dirty && <PendingBar changes={visible} checks={blocked ? [...checks, { level: 'error', text: 'Reconcile with the owner before saving.' }] : checks}
        saving={saving} showDiff={showDiff} onToggleDiff={() => setShowDiff(v => !v)}
        onApply={() => {
          const patch = payloadFor(changes, settings)
          // The phrase stays in the patch: the owner checks it too, so a
          // direct admin API call cannot skip the confirmation.
          if (patch.chats_tool_providers) {
            const before = initial.chats_tool_providers, after = patch.chats_tool_providers
            patch.chats_tool_providers = Object.fromEntries([...new Set([...Object.keys(before), ...Object.keys(after)])].filter(k => before[k] !== after[k]).map(k => [k, after[k] ?? null]))
          }
          return owner.save(patch)
        }} onDiscard={owner.discard} />}
    </div>
  )
}
