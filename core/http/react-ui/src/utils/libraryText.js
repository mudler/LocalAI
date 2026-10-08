// Words and links the Library screens share.

// "Used by research-assistant +2", or the quiet "Not used yet". It is
// information, never a requirement. When the lookup of agents failed it says
// nothing about use at all.
export function usedByLine(t, users, status) {
  if (status === 'failed') return t('library:usedBy.unreadShort')
  if (status !== 'ready') return ''
  if (users.length === 0) return t('library:usedBy.none')
  if (users.length === 1) return t('library:usedBy.line', { name: users[0].name })
  return t('library:usedBy.lineMore', { name: users[0].name, count: users.length - 1 })
}

export function agentHref(name) {
  return `/app/agents/${encodeURIComponent(name)}`
}
