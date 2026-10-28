import { useContext, useEffect, useState } from 'react'
import { RegistryContext, EMPTY_LIST } from '../addons/registryContext'
import { addonsApi } from '../utils/addonsClient'
import { newOperationId } from '../components/catalog/catalogState'
import { SANDBOX_ADDON, sandboxState } from '../utils/sandboxStatus'

// The Sandbox add-on's state for the Tools picker, read once per chat while
// the chat lists the add-on. null when the chat does not offer it.
export function useSandboxStatus(listed, chatId) {
  const registry = useContext(RegistryContext)
  const acquire = registry ? registry.acquire : null
  const allowed = registry ? registry.allowed : false
  useEffect(() => (listed && acquire && allowed ? acquire() : undefined), [listed, acquire, allowed])
  const apps = registry ? registry.apps : EMPTY_LIST
  const app = apps.find(a => a.id === SANDBOX_ADDON)
  const generation = app?.generation || 0
  const [status, setStatus] = useState(null)
  useEffect(() => {
    if (!listed || !generation) { setStatus(null); return undefined }
    const ctl = new AbortController()
    addonsApi.dispatch({ operationId: newOperationId(), action: 'read', addonId: SANDBOX_ADDON, generation, args: { tool: 'status' } }, ctl.signal)
      .then(payload => setStatus(sandboxState(payload)))
      .catch(() => { if (!ctl.signal.aborted) setStatus(sandboxState(null)) })
    return () => ctl.abort()
  }, [listed, generation, chatId])
  return status
}
