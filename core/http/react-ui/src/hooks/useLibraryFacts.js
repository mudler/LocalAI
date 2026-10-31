import { useCallback, useEffect, useRef, useState } from 'react'
import { chatAgentsApi } from '../utils/api'

// Which saved agents exist and what each may use, from the agents of the chats.
// The Library pages use it to say who uses a collection, and to grant one.
//
// `status` is 'loading', 'ready' or 'failed'. A failed lookup says nothing
// about use, so the pages hide their "Used by" lines instead of claiming
// "Not used yet". `agents` is [{ name, id, config }] where config is the agent.
export function useLibraryFacts() {
  const [state, setState] = useState({ status: 'loading', agents: [] })
  const run = useRef(0)

  const refresh = useCallback(async () => {
    const id = ++run.current
    let next
    try {
      const res = await chatAgentsApi.list()
      next = { status: 'ready', agents: (res.agents || []).map(a => ({ name: a.name, id: a.id, config: a })) }
    } catch {
      next = { status: 'failed', agents: [] }
    }
    if (id !== run.current) return
    setState(next)
  }, [])

  useEffect(() => { refresh() }, [refresh])

  // Save a changed agent, then show it at once. The update replaces every field.
  const saveConfig = useCallback(async (name, config) => {
    const body = {
      name: config.name, description: config.description || '', instructions: config.instructions || '', model: config.model || '',
      mcp_servers: config.mcp_servers || [], addons: config.addons || [], collections: config.collections || [], skills: config.skills || [],
    }
    const saved = await chatAgentsApi.update(config.id, body)
    setState(prev => ({
      ...prev,
      agents: prev.agents.map(a => (a.name === name ? { ...a, config: { ...config, ...saved } } : a)),
    }))
  }, [])

  return { ...state, refresh, saveConfig }
}
