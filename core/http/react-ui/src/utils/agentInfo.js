// What a saved agent is, read from its definition: the pieces the agent page
// and the list show as chips. Only fields the definition really holds.

import { splitAgentMCP } from './chatMCP.js'

export function agentInfo(agent) {
  const a = agent || {}
  const list = (v) => (Array.isArray(v) ? v.filter(Boolean) : [])
  const { servers, connectors } = splitAgentMCP(list(a.mcp_servers))
  return {
    model: a.model || '',
    description: (a.description || '').trim(),
    tools: list(a.addons),
    mcp: servers,
    connectors,
    memory: list(a.collections),
    skills: list(a.skills),
    instructions: (a.instructions || '').replace(/\s+/g, ' ').trim(),
  }
}
