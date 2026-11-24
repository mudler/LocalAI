import { useEffect, useMemo, useState } from 'react'
import { chatAgentsApi } from '../utils/api'
import { agentGrants } from '../utils/agentTools'

// The builtin grants the server declares, read once for the page. The agent
// form and the Tools menu of a chat both show them as one group of the Tools
// list (useToolGroups), so a grant has one name and one description in both.
let cached = null
const load = () => {
  if (!cached) cached = chatAgentsApi.builtinGrants().then((res) => res.grants || []).catch((e) => { cached = null; throw e })
  return cached
}

export function useBuiltinGrants() {
  const [catalog, setCatalog] = useState([])
  useEffect(() => {
    let live = true
    load().then((l) => { if (live) setCatalog(l) }).catch(() => { if (live) setCatalog([]) })
    return () => { live = false }
  }, [])
  return catalog
}

// The grants group of a chat: the grants its saved agent holds, read only (the
// agent form changes them). Null for a chat without an agent, or when the
// agent holds none.
export function useChatGrants(agent) {
  const catalog = useBuiltinGrants()
  const held = agent?.builtins
  return useMemo(() => {
    const list = agentGrants(catalog, held, { heldOnly: true })
    return list.length > 0 ? { list, editable: false } : null
  }, [catalog, held])
}
