// The Memory page: the context files the host puts in the prompt, as the
// server lists them (GET /api/chats/context-files). Pure rules, so a node test
// drives them.

// How full the index of a group is, against the caps the server reports:
// { bytes, maxBytes, lines, maxLines, over }.
export function indexUse(group) {
  const index = (group?.files || []).find((f) => f.index)
  const bytes = index?.size || 0
  const lines = index?.lines || 0
  const maxBytes = group?.max_bytes || 0
  const maxLines = group?.max_lines || 0
  return {
    index, bytes, maxBytes, lines, maxLines,
    over: (maxBytes > 0 && bytes > maxBytes) || (maxLines > 0 && lines > maxLines),
  }
}

// Whether text fits the caps of a group's index: used before the owner saves
// an index. Lines count the non-blank ones, as the host does.
export function indexFits(group, text) {
  const bytes = new TextEncoder().encode(text).length
  const lines = text.split('\n').filter((l) => l.trim() !== '').length
  return {
    bytes, lines,
    fits: (!group.max_bytes || bytes <= group.max_bytes) && (!group.max_lines || lines <= group.max_lines),
  }
}

// The kind of heading a group gets: user, agent, channel or profile.
export function groupKind(group) {
  if (group?.source === 'profile') return 'profile'
  return group?.scope || 'user'
}

// A stable id for a group: source and key.
export const groupId = (g) => `${g.source}:${g.key || ''}`

// Every file of a list of groups, for Clear all.
export function allFiles(groups) {
  return (groups || []).flatMap((g) => g.files || [])
}

// The notes of one saved agent in a list of groups: the memory files in the
// agent's own folder (the host marks the groups of agents that were deleted).
export function agentNotes(groups, agentId) {
  return (groups || []).filter((g) => g.source === 'memory' && g.scope === 'agent' && g.key === agentId)
}

// How many notes a saved agent has of its own.
export function agentNoteCount(groups, agentId) {
  return allFiles(agentNotes(groups, agentId)).length
}

// The tabs of the notes list: yours, one for each saved agent that has notes,
// the notes of agents that were deleted, and the conversations. A tab is
// { id, kind, name, groups }; name is the agent's name for an agent tab. A tab
// with no group is left out, except Mine, which stays as the first.
export function noteTabs(groups) {
  const list = groups || []
  const tabs = [{ id: 'mine', kind: 'mine', groups: list.filter((g) => g.scope === 'user') }]
  const byAgent = new Map()
  const deleted = []
  const channels = []
  for (const g of list) {
    if (g.scope === 'channel') channels.push(g)
    else if (g.scope === 'agent') {
      if (g.deleted) deleted.push(g)
      else {
        if (!byAgent.has(g.key)) byAgent.set(g.key, { id: `agent:${g.key}`, kind: 'agent', name: g.label || g.key, groups: [] })
        byAgent.get(g.key).groups.push(g)
      }
    }
  }
  tabs.push(...[...byAgent.values()].sort((a, b) => a.name.localeCompare(b.name)))
  if (deleted.length) tabs.push({ id: 'deleted', kind: 'deleted', groups: deleted })
  if (channels.length) tabs.push({ id: 'channels', kind: 'channels', groups: channels })
  return tabs
}
