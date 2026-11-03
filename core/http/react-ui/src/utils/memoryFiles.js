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
