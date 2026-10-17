import { useContext, useEffect, useMemo } from 'react'
import { RegistryContext, EMPTY_LIST } from './registryContext'
import { usable } from './registryState'
import { declaresSlot, slotContributions } from './slots'

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

// Every contribution of `slot`, with the holds that keep the instances it
// needs loaded. An instance withdrawn by a disable or a crash is loaded again
// once the add-on is usable, so a slot comes back with the add-on.
export function useSlotContributions(slot, enabled = true) {
  const registry = useContext(RegistryContext)
  const acquire = registry ? registry.acquire : null
  const hold = registry ? registry.hold : null
  const reload = registry ? registry.reload : null
  const allowed = registry ? registry.allowed && enabled : false
  const apps = registry ? registry.apps : EMPTY_LIST
  const instances = registry ? registry.instances : EMPTY_LIST
  const language = registry ? registry.language : 'en'

  useEffect(() => (acquire && allowed ? acquire() : undefined), [acquire, allowed])

  const wanted = useMemo(() => apps.filter(a => usable(a) && declaresSlot(a, slot)), [apps, slot])
  const key = wanted.map(a => `${a.id}@${a.generation}`).join(',')
  useEffect(() => {
    if (!hold || !allowed) return undefined
    const handles = wanted.map(app => hold(app))
    return () => handles.forEach(h => h.release())
  }, [key, allowed]) // eslint-disable-line react-hooks/exhaustive-deps

  // A withdrawn instance of an add-on that is usable again (re-enabled, or a
  // newer generation) is replaced by a fresh load.
  const stale = wanted.filter(app => {
    const inst = instances.find(i => i.id === app.id)
    return inst && inst.status === 'unavailable'
  }).map(app => `${app.id}@${app.generation}`).join(',')
  useEffect(() => {
    if (!reload || !stale) return
    for (const app of wanted) {
      const inst = instances.find(i => i.id === app.id)
      if (inst && inst.status === 'unavailable') reload(app)
    }
  }, [stale]) // eslint-disable-line react-hooks/exhaustive-deps

  return useMemo(
    () => slotContributions(slot, { apps, instances, language, allowed }),
    [slot, apps, instances, language, allowed],
  )
}

// Whether the signed-in person has add-ons that offer tools to a model: an
// enabled, running add-on whose manifest defines at least one. The server
// decides what a chat may call; this only decides whether to offer the switch.
export function useAddonToolsAvailable() {
  const registry = useContext(RegistryContext)
  const acquire = registry ? registry.acquire : null
  const allowed = registry ? registry.allowed : false
  useEffect(() => (acquire && allowed ? acquire() : undefined), [acquire, allowed])
  const apps = registry ? registry.apps : EMPTY_LIST
  return allowed && apps.some(a => a.enabled && a.state === 'running' && Array.isArray(a.toolDefinitions) && a.toolDefinitions.length > 0)
}
