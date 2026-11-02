import { useAuth } from '../../context/AuthContext'
import { useCallback, useEffect, useRef, useState } from 'react'
import { newOperationId } from '../catalog/catalogState'
import { addonUpgrades } from '../../utils/addonUpgrades'
import { useAddonUpgrades } from '../../hooks/useAddonUpgrades'

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
  const opOf = useCallback((id) => upgrades.live.find(op => op.addonId === id) || null, [upgrades.live])
  // Whether an upgrade of the add-on is running or being canceled.
  const working = useCallback((id) => {
    const op = opOf(id)
    return Boolean(op) && (op.state === 'running' || op.state === 'cancelling')
  }, [opOf])

  return { confirm, ask: setConfirm, upgrade, check, checks, opOf, working, records: upgrades.records }
}
