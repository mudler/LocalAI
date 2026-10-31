import { useCallback, useEffect, useRef, useState } from 'react'
import { useAuth } from '../context/AuthContext'
import { chatAgentsApi, chatTriggersApi, failoverApi, modelsApi } from '../utils/api'
import { agentModels, collectReferences } from '../utils/cleanupPlan'

// A feature that is not built into this server answers 404 or 501. That is an
// answer ("no agents exist"), not a failed lookup.
const absent = err => err?.status === 404 || err?.status === 501

// Who names which model, read from the same endpoints the Agents, Triggers and
// Failover pages read. Saved agents and triggers belong to their users, so with
// accounts only the viewer's own can be seen: the lookup is then not verified.
//
// Returns { references, verified, loading, refresh }. `verified` is false when
// any lookup failed for a reason other than "this feature is not installed";
// the cleanup plan then refuses to call anything safe, because something it
// could not see might use the model.
export function useCleanupFacts(enabled) {
  const { authEnabled } = useAuth()
  const [state, setState] = useState({ references: new Map(), verified: true, loading: false, loaded: false })
  const run = useRef(0)

  const refresh = useCallback(async () => {
    const id = ++run.current
    setState(prev => ({ ...prev, loading: true }))
    // Other users' agents and triggers cannot be listed; something unseen might use the model.
    let verified = !authEnabled
    const settle = async (promise, fallback) => {
      try {
        return await promise
      } catch (err) {
        if (!absent(err)) verified = false
        return fallback
      }
    }

    const [agentList, triggerList, chainList, aliasList] = await Promise.all([
      settle(chatAgentsApi.list(), { agents: [] }),
      settle(chatTriggersApi.list(), { triggers: [] }),
      settle(failoverApi.list(), { chains: [] }),
      settle(modelsApi.listAliases(), []),
    ])

    const agents = (Array.isArray(agentList?.agents) ? agentList.agents : [])
      .map(agent => ({ name: agent.name, models: agentModels(agent) }))
    const tasks = Array.isArray(triggerList?.triggers) ? triggerList.triggers : []

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
  }, [authEnabled])

  useEffect(() => {
    if (enabled) refresh()
  }, [enabled, refresh])

  return { ...state, refresh }
}
