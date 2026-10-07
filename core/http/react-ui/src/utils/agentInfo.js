// What an agent is, read from its saved configuration: the pieces the agent
// page and the launcher show as chips. Only fields the config really holds.

function parseStdioNames(value) {
  if (!value) return []
  if (Array.isArray(value)) return value.map(s => s?.name).filter(Boolean)
  if (typeof value === 'string') {
    try {
      const parsed = JSON.parse(value)
      if (parsed?.mcpServers) return Object.keys(parsed.mcpServers)
      if (Array.isArray(parsed)) return parseStdioNames(parsed)
    } catch { /* not JSON */ }
  }
  return []
}

function hostOf(url) {
  try { return new URL(url).hostname } catch { return url }
}

export function agentInfo(config) {
  const c = config || {}
  const actions = (Array.isArray(c.actions) ? c.actions : []).map(a => a?.name).filter(Boolean)
  const mcp = [
    ...parseStdioNames(c.mcp_stdio_servers),
    ...(Array.isArray(c.mcp_servers) ? c.mcp_servers.map(s => hostOf(s?.url)).filter(Boolean) : []),
  ]
  // Memory is the knowledge base and long-term memory the agent switches on.
  // The shared Library holds the collections; which one an agent searches is
  // not in its config, so the chip opens the Library, not one collection.
  const memory = []
  if (c.enable_kb) memory.push('knowledge')
  if (c.long_term_memory) memory.push('long-term')
  const selected = Array.isArray(c.selected_skills) ? c.selected_skills : []
  const skills = c.enable_skills ? (selected.length ? selected : ['all']) : []
  const prompt = (c.system_prompt || '').replace(/\s+/g, ' ').trim()
  return {
    model: c.model || '',
    description: (c.description || '').trim(),
    tools: actions,
    mcp,
    memory,
    skills,
    skillsAll: !!c.enable_skills && selected.length === 0,
    instructions: prompt,
    schedule: (c.periodic_runs || '').trim(),
    goal: (c.permanent_goal || '').trim(),
  }
}

// What an agent is doing right now, from its observables: an entry with no
// completion is an action still in flight. The newest one names the work.
export function workingLine(history) {
  const open = (Array.isArray(history) ? history : []).filter(o => o && !o.completion)
  if (open.length === 0) return null
  const o = open[open.length - 1]
  const msg = o.creation?.chat_completion_message?.content
  const req = o.creation?.chat_completion_request?.messages
  const last = Array.isArray(req) && req.length ? req[req.length - 1]?.content : ''
  const text = [typeof msg === 'string' ? msg : '', typeof last === 'string' ? last : '', o.creation?.function_definition?.name || '', o.name || '']
    .find(s => s && s.trim()) || ''
  return text.replace(/\s+/g, ' ').trim().slice(0, 140)
}
