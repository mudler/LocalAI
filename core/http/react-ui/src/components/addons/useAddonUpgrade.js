import { useCallback, useEffect, useRef, useState } from 'react'
import { AddonsError } from '../../utils/addonsApi'
import { newOperationId } from '../catalog/catalogState'
import { upgradeBody } from './updateState'

// The single-add-on upgrade and update check, shared by every view that shows
// the "update available" notice (the list, the details and Browse) and by the
// "upgrade all" run. One path means one operation identity per upgrade, the
// same digest pinning and the same confirmation everywhere.
//
// `actions` is the useAddonActions book of the view. `onChecked` runs after a
// check so the view can read the installed records again.
export function useAddonUpgrade(api, actions, { onChecked } = {}) {
  const [confirm, setConfirm] = useState(null)
  const [checks, setChecks] = useState({})
  const aborter = useRef(null)
  const checked = useRef(onChecked)
  checked.current = onChecked
  useEffect(() => {
    aborter.current = new AbortController()
    return () => aborter.current.abort()
  }, [])

  // The explicit upgrade: the digest of the installed package the page showed
  // and the digest of the candidate the owner's check resolved. The owner
  // checks both again and refuses before it changes anything. It resolves the
  // result of the action.
  const upgrade = useCallback((addon) => actions.run(`upgrade:${addon.id}:${addon.generation}`, {
    addonId: addon.id,
    submit: (operationId, signal) => {
      const body = upgradeBody(addon, operationId)
      if (!body) return Promise.reject(new AddonsError({ kind: 'invalid' }))
      return api.updates.upgrade(addon.id, body, signal)
    },
    verify: async (signal) => {
      const now = (await api.installed(signal)).find(a => a.id === addon.id)
      return Boolean(now) && now.generation > addon.generation
    },
  }), [actions, api])

  // Asks the owner to read the library again. It never applies anything.
  const check = useCallback(async (addon) => {
    const signal = aborter.current.signal
    setChecks(c => ({ ...c, [addon.id]: { phase: 'working' } }))
    try {
      const status = await api.updates.check(addon.id, { operationId: newOperationId() }, signal)
      setChecks(c => ({ ...c, [addon.id]: { phase: 'done', throttled: Boolean(status && status.throttled) } }))
      if (checked.current) checked.current(addon)
    } catch (error) {
      if (error && error.name === 'AbortError') return
      setChecks(c => ({ ...c, [addon.id]: { phase: 'error', error } }))
    }
  }, [api])

  return { confirm, ask: setConfirm, upgrade, check, checks }
}
