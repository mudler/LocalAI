import { useAuth } from '../../context/AuthContext'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { newOperationId } from '../catalog/catalogState'
import { addonUpgrades } from '../../utils/addonUpgrades'
import { useAddonUpgrades } from '../../hooks/useAddonUpgrades'
import { useOperations } from '../../hooks/useOperations'
import { operationsApi } from '../../utils/api'
import { overlayLive } from '../../utils/addonUpgradeRows'

// The single-add-on upgrade and update check, shared by every view that shows
// the "update available" notice (the list, the details and Browse) and by the
// "upgrade all" run. One path means one operation identity per upgrade, the
// same digest pinning and the same confirmation everywhere.
//
// The upgrade itself runs in the upgrade store (utils/addonUpgrades), outside
// any page: it goes on when the view is left and shows in Activity.
//
// `onChanged` runs after a check or an upgrade ends, so the view can read the
// installed records again.
export function useAddonUpgrade(api, { onChanged } = {}) {

  const { user, isAdmin, sessionEpoch, captureSessionAuthority } = useAuth()
  const actor = JSON.stringify([user?.id, isAdmin, sessionEpoch])
  const authority = useRef(null)
  if (authority.current?.actor !== actor) {
    authority.current = { actor, controller: new AbortController(), sessionCurrent: captureSessionAuthority() }
  }
  const origin = authority.current
  const current = useCallback(() => authority.current === origin && origin.sessionCurrent() && !origin.controller.signal.aborted, [origin])
  const [storedConfirm, saveConfirm] = useState({ actor, value: null })
  const confirm = storedConfirm.actor === actor ? storedConfirm.value : null
  const setConfirm = useCallback(value => {
    if (!current()) return
    saveConfirm(old => ({ actor, value: typeof value === 'function' ? value(old.actor === actor ? old.value : null) : value }))
  }, [actor, current])
  const [storedChecks, saveChecks] = useState({ actor, value: {} })
  const checks = storedChecks.actor === actor ? storedChecks.value : {}
  const setChecks = useCallback(value => {
    if (!current()) return
    saveChecks(old => ({ actor, value: typeof value === 'function' ? value(old.actor === actor ? old.value : {}) : value }))
  }, [actor, current])
  const upgrades = useAddonUpgrades()
  // What the server says about each job (waiting, how far, cancellable) lies
  // over the entries of the store, so every view shows the real state.
  const { operations } = useOperations()
  const live = useMemo(() => overlayLive(upgrades.live, operations), [upgrades.live, operations])
  // The canceled operations whose note the person has closed.
  const [closedNotes, setClosedNotes] = useState(() => new Set())
  const changed = useRef(onChanged)
  changed.current = onChanged
  useEffect(() => {
    if (origin.controller.signal.aborted) origin.controller = new AbortController()
    return () => origin.controller.abort()
  }, [origin])

  // The explicit upgrade: the digest of the installed package the page showed
  // and the digest of the candidate the owner's check resolved. The owner
  // checks both again and refuses before it changes anything. It never throws
  // and resolves { ok, outcome, message }, where message is the key of the
  // words of a failure (see upgradeFailureWords).
  const upgrade = useCallback(async (addon) => {
    if (!current()) return null
    const out = await addonUpgrades.start(api, addon, { current: origin.sessionCurrent })
    if (!current()) return null
    if (changed.current) changed.current(addon)
    return out
  }, [api, current, origin])

  // Runs the upgrade of a held add-on the administrator approved in the host
  // access dialog, as the same operation of the store: `request` is
  // { key, fingerprint, version, send }, see ApproveUpgradeDialog. It never
  // throws and resolves like `upgrade`.
  const approve = useCallback(async (addon, request) => {
    if (!current()) return null
    const out = await addonUpgrades.startApproved(api, addon, { ...request, current: origin.sessionCurrent })
    if (!current()) return null
    if (changed.current) changed.current(addon)
    return out
  }, [api, current, origin])

  // Asks the owner to read the library again. It never applies anything.
  const check = useCallback(async (addon) => {
    if (!current()) return null
    const signal = origin.controller.signal
    setChecks(c => ({ ...c, [addon.id]: { phase: 'working' } }))
    try {
      const status = await api.updates.check(addon.id, { operationId: newOperationId() }, signal)
      if (!current()) return null
      // The answer is kept so the card can say what the check found and
      // when, even when the records read again show no notice any more.
      setChecks(c => ({ ...c, [addon.id]: { phase: 'done', throttled: Boolean(status && status.throttled), status: status && typeof status === 'object' ? status : null } }))
      if (changed.current) changed.current(addon)
    } catch (error) {
      if (!current() || (error && error.name === 'AbortError')) return
      setChecks(c => ({ ...c, [addon.id]: { phase: 'error', error } }))
    }
  }, [api, current, setChecks, origin])

  // The upgrade of one add-on the store follows, or null.
  const opOf = useCallback((id) => live.find(op => op.addonId === id) || null, [live])
  // Whether an upgrade of the add-on is running or being canceled.
  const working = useCallback((id) => {
    const op = opOf(id)
    return Boolean(op) && (op.state === 'running' || op.state === 'cancelling')
  }, [opOf])

  // Cancels the operation on the server by its job, through the cancel route
  // the Activity page uses; the store reads how the owner ended it.
  const cancel = useCallback((op) => addonUpgrades.cancel(op.jobID, (jobId) => operationsApi.cancel(jobId)), [])
  const dismiss = useCallback((op) => addonUpgrades.dismiss(op.jobID), [])

  // The note of the add-on's last operation when it was canceled and no other
  // operation of it runs, until the person closes it.
  const noteOf = useCallback((id) => {
    if (live.some(op => op.addonId === id)) return null
    const last = upgrades.records.find(r => r.addonId === id)
    return last && last.outcome === 'cancelled' && !closedNotes.has(last.jobID) ? last : null
  }, [live, upgrades.records, closedNotes])
  const closeNote = useCallback((note) => setClosedNotes(set => new Set(set).add(note.jobID)), [])

  return { confirm, ask: setConfirm, upgrade, approve, check, checks, opOf, working, cancel, dismiss, noteOf, closeNote, records: upgrades.records }
}
