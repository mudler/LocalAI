import { useCallback, useEffect, useMemo, useState } from 'react'
import { chatConnectorsApi, chatAgentsApi } from '../utils/api'

// The messaging connections of the person and the connector kinds of the
// running add-ons, read once and read again on reload(). Best effort: a host
// without chats, a feature the person lacks or a failed read leaves the lists
// empty and `ready` true, so a page never waits on it.
//   kinds       connector kinds of running add-ons ({ addon, kind, title, ... })
//   items       the connection records of the person
//   agents      the saved agents, for the name a connection answers as
//   addonIds    Set of the ids of add-ons that serve a connector kind
//   byAddon     { addonId: [connection, ...] }
export function useAddonConnections({ enabled = true } = {}) {
  const [state, setState] = useState({ ready: false, kinds: [], items: [], agents: [] })
  const [tick, setTick] = useState(0)
  const reload = useCallback(() => setTick(n => n + 1), [])

  useEffect(() => {
    if (!enabled) return undefined
    let live = true
    const kindsP = chatConnectorsApi.kinds().then(r => (Array.isArray(r?.kinds) ? r.kinds : [])).catch(() => [])
    const itemsP = chatConnectorsApi.list().then(r => (Array.isArray(r?.connectors) ? r.connectors : [])).catch(() => [])
    const agentsP = chatAgentsApi.list().then(r => (Array.isArray(r?.agents) ? r.agents : [])).catch(() => [])
    Promise.all([kindsP, itemsP, agentsP]).then(([kinds, items, agents]) => { if (live) setState({ ready: true, kinds, items, agents }) })
    return () => { live = false }
  }, [enabled, tick])

  const derived = useMemo(() => {
    const addonIds = new Set(state.kinds.map(k => k.addon))
    const byAddon = {}
    for (const c of state.items) {
      const id = c.addon || (state.kinds.find(k => k.kind === c.kind) || {}).addon
      if (id) (byAddon[id] = byAddon[id] || []).push(c)
    }
    return { addonIds, byAddon }
  }, [state.kinds, state.items])
  return { ...state, ...derived, reload }
}
