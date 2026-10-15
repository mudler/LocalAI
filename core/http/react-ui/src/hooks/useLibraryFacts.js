import { useCallback, useEffect, useRef, useState } from 'react'
import { agentsApi } from '../utils/api'

const CONFIG_CONCURRENCY = 4

// A server without the agent pool answers 404 or 501. That means "no agents",
// which is an answer; anything else means the lookup failed.
const absent = err => err?.status === 404 || err?.status === 501

async function pool(items, limit, fn) {
  const out = new Array(items.length)
  let cursor = 0
  const run = async () => {
    while (cursor < items.length) {
      const i = cursor++
      out[i] = await fn(items[i])
    }
  }
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, run))
  return out
}

// Which agents exist and what each one has switched on, from the same two
// endpoints the Agents page reads: the list, then each agent's saved config.
// The Library pages use it to say who uses a skill or a collection.
//
// `status` is 'loading', 'ready' or 'failed'. A failed lookup says nothing
// about use, so the pages hide their "Used by" lines instead of claiming
// "Not used yet".
export function useLibraryFacts() {
  const [state, setState] = useState({ status: 'loading', agents: [] })
  const run = useRef(0)

  const refresh = useCallback(async () => {
    const id = ++run.current
    let failed = false
    let list = null
    try {
      list = await agentsApi.list(false)
    } catch (err) {
      if (!absent(err)) failed = true
    }
    const names = (Array.isArray(list?.agents) ? list.agents : [])
      .map(a => (typeof a === 'string' ? a : a?.name))
      .filter(Boolean)
    const agents = await pool(names, CONFIG_CONCURRENCY, async name => {
      try {
        return { name, config: await agentsApi.getConfig(name) }
      } catch (err) {
        if (!absent(err)) failed = true
        return { name, config: null }
      }
    })
    if (id !== run.current) return
    setState({ status: failed ? 'failed' : 'ready', agents })
  }, [])

  useEffect(() => { refresh() }, [refresh])

  // Save a changed config for one agent, then show it at once.
  const saveConfig = useCallback(async (name, config) => {
    await agentsApi.update(name, config)
    setState(prev => ({
      ...prev,
      agents: prev.agents.map(a => (a.name === name ? { ...a, config } : a)),
    }))
  }, [])

  return { ...state, refresh, saveConfig }
}
