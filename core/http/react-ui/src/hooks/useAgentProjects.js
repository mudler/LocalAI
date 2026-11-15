import { useEffect, useState } from 'react'
import { chatAgentsApi, chatProjectsApi } from '../utils/api'

// The agents and the projects of the person, for a trigger page that says which
// project an agent belongs to. Both reads are best effort: a person without the
// agents permission sees no agent names, and one whose server has no projects
// sees no project chips. The page works without either.
export function useAgentProjects() {
  const [state, setState] = useState({ agents: [], projects: [], ready: false })
  useEffect(() => {
    let alive = true
    Promise.all([
      chatAgentsApi.list().then((r) => r.agents || []).catch(() => []),
      chatProjectsApi.list().then((r) => r.projects || []).catch(() => []),
    ]).then(([agents, projects]) => { if (alive) setState({ agents, projects, ready: true }) })
    return () => { alive = false }
  }, [])
  return state
}
