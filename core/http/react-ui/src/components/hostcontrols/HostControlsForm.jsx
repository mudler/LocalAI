/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Callout, LoadError, SkeletonRows } from '../ui'
import PendingBar from '../settings/PendingBar'
import UnsavedChangesGuard from '../UnsavedChangesGuard'
import { settingsApi } from '../../utils/api'
import { normalizeLoaded } from '../../utils/settingsSchema'
import { appendHistory, historyEntries, payloadFor, pendingChanges, runChecks, visibleChanges } from '../../utils/settingsPending'
import { controlsOf } from '../../addons/hostControls'
import { HOST_CONTROLS } from '../../addons/hostControlComponents'
import '../../pages/settings.css'

// The host controls of one add-on on its Settings tab, in one form: the
// runtime settings are read once, every block edits the same draft, and one
// pending bar applies it with the checks and the history of the Settings
// page. The add-on supplies none of this; the apps list says which controls
// the host draws for it.
export default function HostControlsForm({ app }) {
  const { t } = useTranslation('addons')
  const ids = controlsOf(app)
  const [settings, setSettings] = useState(null)
  const [initial, setInitial] = useState(null)
  const [error, setError] = useState(null)
  const [saveError, setSaveError] = useState('')
  const [saving, setSaving] = useState(false)
  const [showDiff, setShowDiff] = useState(false)
  const live = useRef(true)

  const load = useCallback(() => {
    setError(null)
    settingsApi.get()
      .then(data => { if (live.current) { const loaded = normalizeLoaded(data); setSettings(loaded); setInitial(structuredClone(loaded)) } })
      .catch(e => { if (live.current) setError(e) })
  }, [])
  useEffect(() => {
    live.current = true
    if (ids.length > 0) load()
    return () => { live.current = false }
  }, [load, ids.length])

  const changes = useMemo(() => pendingChanges(initial, settings), [initial, settings])
  const visible = useMemo(() => visibleChanges(changes), [changes])
  const checks = useMemo(() => (settings ? runChecks(changes, initial, settings) : []), [changes, initial, settings])
  const pending = useMemo(() => new Set(visible.map(c => c.field.key)), [visible])
  const dirty = visible.length > 0

  if (ids.length === 0) return null
  if (error) return <LoadError title={t('settingsTab.controls.loadFailed')} error={error} onRetry={load} />
  if (!settings) return <SkeletonRows rows={3} />

  const apply = async () => {
    if (!dirty || saving) return
    setSaving(true)
    setSaveError('')
    try {
      await settingsApi.save(payloadFor(changes, settings))
      setInitial(structuredClone(settings))
      appendHistory(historyEntries(changes))
      setShowDiff(false)
    } catch (e) {
      setSaveError(e.message || 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="ad-host-controls" data-testid="addon-host-controls">
      <UnsavedChangesGuard when={dirty} />
      {ids.map(id => {
        const Block = HOST_CONTROLS[id]?.settings
        if (!Block) return null
        return (
          <Suspense key={id} fallback={<SkeletonRows rows={2} />}>
            <Block app={app} settings={settings} initial={initial} onChange={setSettings} pending={pending} />
          </Suspense>
        )
      })}
      {saveError && <Callout tone="error" testId="addon-host-controls-error">{t('settingsTab.controls.saveFailed', { message: saveError })}</Callout>}
      {dirty && (
        <PendingBar
          changes={visible} checks={checks} saving={saving} showDiff={showDiff}
          onToggleDiff={() => setShowDiff(v => !v)} onApply={apply}
          onDiscard={() => { setSettings(structuredClone(initial)); setShowDiff(false) }}
        />
      )}
    </div>
  )
}
