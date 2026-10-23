import { useCallback, useEffect, useRef, useState } from 'react'
import { agentJobsApi, agentsApi, failoverApi, modelsApi } from '../utils/api'
import { agentModels, collectReferences } from '../utils/cleanupPlan'

const AGENT_CONFIG_CONCURRENCY = 4

// A feature that is not built into this server answers 404 or 501. That is an
// answer ("no agents exist"), not a failed lookup.
const absent = err => err?.status === 404 || err?.status === 501

async function pool(items, limit, fn) {
  const results = new Array(items.length)
  let cursor = 0
  const run = async () => {
    while (cursor < items.length) {
      const i = cursor++
      results[i] = await fn(items[i])
    }
  }
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, run))
  return results
}

// Who names which model, read from the same endpoints the Agents, Jobs and
// Failover pages read.
//
// Returns { references, verified, loading, refresh }. `verified` is false when
// any lookup failed for a reason other than "this feature is not installed";
// the cleanup plan then refuses to call anything safe, because something it
// could not see might use the model.
export function useCleanupFacts(enabled) {
  const [state, setState] = useState({ references: new Map(), verified: true, loading: false, loaded: false })
  const run = useRef(0)

  const refresh = useCallback(async () => {
    const id = ++run.current
    setState(prev => ({ ...prev, loading: true }))
    let verified = true
    const settle = async (promise, fallback) => {
      try {
        return await promise
      } catch (err) {
        if (!absent(err)) verified = false
        return fallback
      }
    }

    const [agentList, taskList, chainList, aliasList] = await Promise.all([
      settle(agentsApi.list(true), null),
      settle(agentJobsApi.listTasks(true), []),
      settle(failoverApi.list(), { chains: [] }),
      settle(modelsApi.listAliases(), []),
    ])

    // Agent names, with the user each belongs to so an admin's other users are
    // covered. The list gives names only; the model lives in each config.
    const named = []
    for (const name of Array.isArray(agentList?.agents) ? agentList.agents : []) {
      named.push({ name: typeof name === 'string' ? name : name?.name, userId: undefined })
    }
    for (const [userId, group] of Object.entries(agentList?.user_groups || {})) {
      for (const name of group?.agents || []) {
        named.push({ name: typeof name === 'string' ? name : name?.name, userId })
      }
    }
    const agents = (await pool(named.filter(a => a.name), AGENT_CONFIG_CONCURRENCY, async agent => {
      try {
        const config = await agentsApi.getConfig(agent.name, agent.userId)
        return { name: agent.name, models: agentModels(config) }
      } catch (err) {
        if (!absent(err)) verified = false
        return { name: agent.name, models: [] }
      }
    }))

    // The admin listing groups other users' tasks under user_groups the same
    // way; the flat list is the caller's own.
    const ownTasks = Array.isArray(taskList) ? taskList : (taskList?.tasks || [])
    const tasks = [
      ...ownTasks,
      ...Object.values(taskList?.user_groups || {}).flatMap(group => group?.tasks || []),
    ]

    const aliases = {}
    for (const alias of Array.isArray(aliasList) ? aliasList : []) aliases[alias.name] = alias.target

    if (id !== run.current) return null
    const references = collectReferences({
      agents,
      tasks: tasks.filter(task => task && task.model),
      chains: chainList?.chains || [],
      aliases,
    })
    const next = { references, verified, loading: false, loaded: true }
    setState(next)
    return next
  }, [])

  useEffect(() => {
    if (enabled) refresh()
  }, [enabled, refresh])

  return { ...state, refresh }
}
