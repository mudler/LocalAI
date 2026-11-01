import { useContext, useEffect, useState } from 'react'
import { RegistryContext, EMPTY_LIST } from '../addons/registryContext'
import { addonsApi } from '../utils/addonsClient'
import { newOperationId } from '../components/catalog/catalogState'
import { unavailableWait } from '../components/addons/lifecycleFlow'
import { SANDBOX_ADDON, sandboxState, startingState } from '../utils/sandboxStatus'

// When the add-on service does not answer (it starts, or restarts after an
// upgrade), the next read waits longer each time. The wait is kept for the
// page, not for one chat: opening another chat, or the same one again, does
// not ask before it is over. A new generation of the add-on starts afresh.
const backoff = { generation: 0, down: 0, nextAt: 0 }

const isUnavailable = (e) => e && (e.kind === 'unavailable' || e.kind === 'network')

// The Sandbox add-on's state for the Tools picker, read once per chat while
// the chat lists the add-on. null when the chat does not offer it. While the
// add-on service starts the state is 'starting', and the hook reads again by
// itself, backing off.
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
    let timer = null
    if (backoff.generation !== generation) Object.assign(backoff, { generation, down: 0, nextAt: 0 })
    const read = () => {
      const wait = backoff.nextAt - Date.now()
      if (wait > 0) {
        setStatus(startingState(wait))
        timer = setTimeout(read, wait)
        return
      }
      addonsApi.dispatch({ operationId: newOperationId(), action: 'read', addonId: SANDBOX_ADDON, generation, args: { tool: 'status' } }, ctl.signal)
        .then(payload => {
          Object.assign(backoff, { down: 0, nextAt: 0 })
          setStatus(sandboxState(payload))
        })
        .catch(e => {
          if (ctl.signal.aborted) return
          if (!isUnavailable(e)) { setStatus(sandboxState(null)); return }
          backoff.down += 1
          const next = unavailableWait(backoff.down)
          backoff.nextAt = Date.now() + next
          setStatus(startingState(next))
          timer = setTimeout(read, next)
        })
    }
    read()
    return () => { ctl.abort(); clearTimeout(timer) }
  }, [listed, generation, chatId])
  return status
}
