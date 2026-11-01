// The "Jump back in" list of the Home page: chats the server keeps, grouped by
// day. The list comes from GET /api/chats.

const DAY_MS = 24 * 60 * 60 * 1000

// The rows for a list of chats from the server: the chats a person started,
// newest first. A sub-agent run belongs to its parent and is left out.
export function resumeItems(conversations) {
  return (conversations || [])
    .filter(c => c && c.id && c.kind !== 'subagent')
    .map(c => ({
      id: c.id,
      title: (c.title || '').trim() || 'New chat',
      model: c.model || '',
      updatedAt: Date.parse(c.updated || c.created || '') || 0,
    }))
    .sort((a, b) => b.updatedAt - a.updatedAt)
}

function startOfDay(ts) {
  const d = new Date(ts)
  d.setHours(0, 0, 0, 0)
  return d.getTime()
}

// Buckets: today, yesterday, earlier this week (2 to 6 days ago), older.
export function dayBucket(ts, now = Date.now()) {
  const days = Math.round((startOfDay(now) - startOfDay(ts)) / DAY_MS)
  if (days <= 0) return 'today'
  if (days === 1) return 'yesterday'
  if (days < 7) return 'week'
  return 'older'
}

// Groups keep the input order, so a sorted list gives sorted groups.
export function groupConversations(items, now = Date.now()) {
  const groups = []
  for (const item of items) {
    const key = dayBucket(item.updatedAt, now)
    let group = groups[groups.length - 1]
    if (!group || group.key !== key) {
      group = { key, items: [] }
      groups.push(group)
    }
    group.items.push(item)
  }
  return groups
}
