import { useContext, useEffect, useMemo } from 'react'
import { RegistryContext, EMPTY_LIST } from './registryContext'
import { usable } from './registryState'

export function useAddonRegistry() {
  const value = useContext(RegistryContext)
  if (!value) throw new Error('useAddonRegistry must be used inside AddonRegistryProvider')
  return value
}

// The navigation entries add-ons contribute, from the usable ones only.
export function useAddonNavigation(enabled = true) {
  const registry = useContext(RegistryContext)
  const acquire = registry ? registry.acquire : null
  const allowed = registry ? registry.allowed : false
  useEffect(() => (enabled && acquire && allowed ? acquire() : undefined), [enabled, acquire, allowed])
  return enabled && registry ? registry.navigationItems : EMPTY_LIST
}


// The chat actions add-ons contribute. The chat page draws them in its own
// overflow menu and owns the panel or dialog an action opens. Only ready
// instances of the listed generation contribute, and an action carries no
// conversation history: `open` receives { conversationId, resourceRef? }.
export function useAddonChatActions() {
  const registry = useContext(RegistryContext)
  const apps = registry ? registry.apps : EMPTY_LIST
  const wanted = useMemo(() => apps.filter(a => usable(a) && (a.ui.chatActions || []).length > 0), [apps])
  const hold = registry ? registry.hold : null
  const acquire = registry ? registry.acquire : null
  const allowed = registry ? registry.allowed : false
  useEffect(() => (acquire && allowed ? acquire() : undefined), [acquire, allowed])
  const key = wanted.map(a => `${a.id}@${a.generation}`).join(',')
  useEffect(() => {
    if (!hold) return undefined
    const handles = wanted.map(app => hold(app))
    return () => handles.forEach(h => h.release())
  }, [key]) // eslint-disable-line react-hooks/exhaustive-deps
  return registry ? registry.chatActions : []
}
