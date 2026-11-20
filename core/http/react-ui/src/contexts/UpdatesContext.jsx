import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { backendsApi, modelsApi } from '../utils/api'
import { addonsApi, hostGrantsApi } from '../utils/addonsClient'
import { createHostSettingsClient } from '../utils/hostSettingsClient'
import { normalizeOverview } from '../components/addons/hostGrantsState'
import { localizeRecord } from '../components/addons/addonState'
import { newOperationId } from '../components/catalog/catalogState'
import { usePolling } from '../hooks/usePolling'
import { useOperations } from '../hooks/useOperations'
import { liveWithServer } from '../utils/addonUpgradeRows'
import { useAddonUpgrades } from '../hooks/useAddonUpgrades'
import { useAuth } from '../context/AuthContext'
import { FEATURE_ADDONS } from '../utils/capabilities'
import { addonRows, addonSource, backendRows, badgeOf, sourceViews } from '../utils/updates'
import { modelRows, modelSource } from '../utils/modelUpdates'

// What is out of date on this server, read once for the whole shell: the tab of
// Updates, the badge of the Operate item and the page itself read this and
// nothing else, so no page keeps a poll of its own for the same fact.
//
// One poll a minute while the tab is visible (usePolling pauses it when the
// tab is hidden and catches up when it is shown again). Each tick reads
//   - GET /api/backends/updates, the cached read model (no registry call),
//   - the installed add-ons and the host-access overview of the add-on owner,
//   - the owner's automatic-update setting for add-ons,
//   - GET /api/models/updates, the model read model (the last check's
//     result plus the hold flags and the upgrade journals, read from disk),
// the add-on ones only when this server has add-ons for this person. An
// operation that leaves /api/operations is the one moment the answer can have
// changed, so the model is read again then.
//
// Administrators only: every route it reads is an administrator's.
const UpdatesContext = createContext(null)

const POLL_INTERVAL_MS = 60_000
// A check within this time of the last one is answered from it by the server;
// the button waits for the same time.
export const CHECK_COOLDOWN_MS = 15_000

const settingsClient = createHostSettingsClient({ api: addonsApi })

export function UpdatesProvider({ children, features, pollInterval = POLL_INTERVAL_MS }) {
  const { isAdmin, hasFeature } = useAuth()
  const { i18n } = useTranslation()
  const { operations } = useOperations()
  const upgrades = useAddonUpgrades()
  // The upgrades this browser follows, with what the server says of the same
  // jobs, and the ones only the server lists (started in another tab).
  const live = useMemo(() => liveWithServer(upgrades.live, operations), [upgrades.live, operations])
  const addonsOn = Boolean(isAdmin && features && features.addons !== false && hasFeature(FEATURE_ADDONS))
  const featuresKnown = features != null

  const [backend, setBackend] = useState({ status: 'loading', snapshot: null, error: null })
  const [addons, setAddons] = useState({ status: 'loading', installed: [], grants: null, auto: false, error: '' })
  const [models, setModels] = useState({ status: 'loading', snapshot: null, error: null })
  const [checking, setChecking] = useState(false)
  const [lastCheck, setLastCheck] = useState(null)
  const alive = useRef(true)
  useEffect(() => { alive.current = true; return () => { alive.current = false } }, [])

  const readBackend = useCallback(async () => {
    try {
      const snapshot = await backendsApi.updates()
      if (alive.current) setBackend({ status: 'ready', snapshot, error: null })
    } catch (error) {
      if (alive.current) setBackend(prev => ({ status: prev.snapshot ? 'ready' : 'error', snapshot: prev.snapshot, error, stale: Boolean(prev.snapshot) }))
    }
  }, [])

  // A failed read keeps the last answer: a model that had an update still has
  // one.
  const readModels = useCallback(async () => {
    try {
      const snapshot = await modelsApi.updates()
      if (alive.current) setModels({ status: 'ready', snapshot, error: null })
    } catch (error) {
      if (alive.current) setModels(prev => ({ status: prev.snapshot ? 'ready' : 'error', snapshot: prev.snapshot, error }))
    }
  }, [])

  const readAddons = useCallback(async () => {
    if (!addonsOn) return
    const [inst, grants, owner] = await Promise.allSettled([
      addonsApi.installed(),
      hostGrantsApi.overview().then(normalizeOverview),
      settingsClient.read(),
    ])
    if (!alive.current) return
    setAddons(prev => {
      const next = { ...prev }
      if (inst.status === 'fulfilled') { next.status = 'ready'; next.installed = inst.value; next.error = '' } else { next.status = 'error'; next.error = inst.reason && inst.reason.kind ? inst.reason.kind : 'unavailable' }
      // A held upgrade the overview cannot show is not "none": keep what was read.
      if (grants.status === 'fulfilled') next.grants = grants.value
      if (owner.status === 'fulfilled') next.auto = owner.value.policy.auto_upgrade_addons === true
      return next
    })
  }, [addonsOn])

  const tick = useCallback(async () => { await Promise.all([readBackend(), readAddons(), readModels()]) }, [readBackend, readAddons, readModels])
  // The add-on gate depends on /api/features, so the first tick waits for it.
  const { refetch } = usePolling(tick, pollInterval, { enabled: Boolean(isAdmin) && featuresKnown })

  // An operation that left the list, or an add-on upgrade that ended: read
  // again now, and once more when the server's own check after the operation
  // has had time to run.
  const liveIds = useRef(new Set())
  useEffect(() => {
    const ids = new Set(operations.map(op => op.jobID || op.id))
    let departed = false
    for (const id of liveIds.current) if (!ids.has(id)) departed = true
    liveIds.current = ids
    if (!departed || !isAdmin) return undefined
    refetch()
    const timer = setTimeout(() => { if (alive.current) refetch() }, 2500)
    return () => clearTimeout(timer)
  }, [operations, refetch, isAdmin])
  const endedRecords = upgrades.records.length
  const firstEnded = useRef(endedRecords)
  useEffect(() => {
    if (endedRecords === firstEnded.current) return
    firstEnded.current = endedRecords
    if (isAdmin) refetch()
  }, [endedRecords, refetch, isAdmin])

  const installed = useMemo(() => addons.installed.map(rec => localizeRecord(rec, i18n.language)), [addons.installed, i18n.language])
  const autoBackends = Boolean(backend.snapshot && backend.snapshot.autoUpdate && backend.snapshot.autoUpdate.backends)

  const rows = useMemo(() => [
    ...backendRows(backend.snapshot, { operations, auto: autoBackends }),
    ...addonRows({ installed, grants: addons.grants, live, auto: addons.auto, isAdmin }),
    ...modelRows(models.snapshot, { operations }),
  ], [backend.snapshot, operations, autoBackends, installed, addons.grants, addons.auto, live, isAdmin, models.snapshot])

  const addonSourceView = useMemo(
    () => addonSource({ status: addonsOn ? addons.status : 'off', installed, error: addons.error }),
    [addonsOn, addons.status, installed, addons.error],
  )
  const modelSourceView = useMemo(() => modelSource(models.snapshot, models.status), [models.snapshot, models.status])
  const sources = useMemo(() => sourceViews(backend.snapshot, addonSourceView, modelSourceView), [backend.snapshot, addonSourceView, modelSourceView])
  const badge = useMemo(() => badgeOf(rows), [rows])

  // A real check of the backend sources only, used to confirm that an update
  // took: the answer is the new read model. It rejects when the call fails.
  const recheck = useCallback(async () => {
    const snapshot = await backendsApi.checkUpdatesNow()
    if (alive.current) setBackend({ status: 'ready', snapshot, error: null })
    return snapshot
  }, [])

  // A real check of every source, waiting for the answer. The backend check is
  // the server's (at most 45 s, shared by concurrent callers). Each add-on is
  // checked by the administrator's own browser session, one after another, as
  // the Apps page does. One source that fails does not stop the others.
  const checkNow = useCallback(async () => {
    if (checking) return null
    setChecking(true)
    let throttled = false
    let addonFailed = 0
    try {
      // The backend and the model checks are the server's own and run at
      // the same time.
      await Promise.all([
        (async () => {
          try {
            const snapshot = await backendsApi.checkUpdatesNow()
            throttled = throttled || Boolean(snapshot && snapshot.throttled)
            if (alive.current) setBackend({ status: 'ready', snapshot, error: null })
          } catch (error) {
            if (alive.current) setBackend(prev => ({ status: prev.snapshot ? 'ready' : 'error', snapshot: prev.snapshot, error, stale: Boolean(prev.snapshot) }))
          }
        })(),
        (async () => {
          try {
            const snapshot = await modelsApi.checkUpdatesNow()
            throttled = throttled || Boolean(snapshot && snapshot.throttled)
            if (alive.current) setModels({ status: 'ready', snapshot, error: null })
          } catch (error) {
            if (alive.current) setModels(prev => ({ status: prev.snapshot ? 'ready' : 'error', snapshot: prev.snapshot, error }))
          }
        })(),
      ])
      if (addonsOn) {
        for (const rec of addons.installed) {
          try {
            await addonsApi.updates.check(rec.id, { operationId: newOperationId() })
          } catch { addonFailed += 1 }
        }
        await readAddons()
      }
    } finally {
      if (alive.current) {
        setChecking(false)
        setLastCheck({ at: Date.now(), throttled, addonFailed })
      }
    }
    return { throttled, addonFailed }
  }, [checking, addonsOn, addons.installed, readAddons])

  const value = useMemo(() => ({
    enabled: true,
    backend,
    addons: { ...addons, installed, on: addonsOn },
    models,
    snapshot: backend.snapshot,
    loading: backend.status === 'loading' || (addonsOn && addons.status === 'loading'),
    loadError: backend.status === 'error',
    rows,
    sources,
    badge,
    checking,
    lastCheck,
    autoUpdate: { backends: autoBackends, addons: addons.auto },
    refresh: refetch,
    readBackend,
    readAddons,
    readModels,
    recheck,
    checkNow,
  }), [backend, addons, installed, addonsOn, models, rows, sources, badge, checking, lastCheck, autoBackends, refetch, readBackend, readAddons, readModels, recheck, checkNow])

  if (!isAdmin) {
    return <UpdatesContext.Provider value={DISABLED}>{children}</UpdatesContext.Provider>
  }
  return <UpdatesContext.Provider value={value}>{children}</UpdatesContext.Provider>
}

const DISABLED = { enabled: false, rows: [], sources: [], badge: { count: 0, failed: false } }

// Null outside the provider (a render test of a single component); a value
// with enabled false for anyone who is not an administrator.
export function useUpdates() {
  return useContext(UpdatesContext)
}
