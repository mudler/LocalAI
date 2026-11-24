// The command palette's data: what can be searched, and how a query ranks it.
// No React and no network here, so tests/unit can load it.
import { canViewAs } from '../../utils/access.js'

export function isApplePlatform() {
  if (typeof navigator === 'undefined') return false
  return /mac|iphone|ipad/i.test(navigator.platform || navigator.userAgent || '')
}

// The order of the result groups, and how many of each a query shows. With no
// query only the pages show: the others need a word to narrow them down.
export const KINDS = ['page', 'setting', 'worker', 'viewas', 'person']
const LIMIT = { page: 30, setting: 6, worker: 6, viewas: 6, person: 6 }

const norm = (text) => String(text || '').toLowerCase()

// One entry per Administration page the viewer can open, in the sidebar's
// order. `groups` is resolveGroups(...).groups and `t` the nav translator.
export function pageEntries(groups, t) {
  const root = t('sections.operate')
  const out = []
  for (const { group, items } of groups) {
    for (const item of items) {
      const title = t(item.labelKey)
      const where = group.labelKey ? `${root} / ${t(group.labelKey)}` : root
      out.push({
        id: `page:${item.path}?${item.query || ''}`,
        kind: 'page',
        title,
        where,
        icon: item.icon,
        to: item.query ? `${item.path}?${item.query}` : item.path,
        hay: norm(`${title} ${where}`),
      })
    }
  }
  return out
}

// A person, from the users list the administrator may already read.
export function personEntries(users, where) {
  return (Array.isArray(users) ? users : []).filter(u => u && u.id).map(u => {
    const title = u.name || u.email
    return {
      id: `person:${u.id}`,
      kind: 'person',
      title,
      where: u.name && u.email ? `${u.email} · ${where}` : where,
      icon: 'user',
      to: `/app/users?q=${encodeURIComponent(u.email || u.name)}`,
      hay: norm(`${u.name} ${u.email}`),
    }
  })
}

// "View as <name>" for each person an administrator may view as: active, not
// the viewer and not an administrator (the same rule as the Users page, see
// canViewAs in utils/access.js). `words` are the translated texts:
// title(name), where(email) and kind, the words of the group, which the query
// can match too ("view as" finds every one of them).
export function viewAsEntries(users, selfId, words) {
  return (Array.isArray(users) ? users : []).filter(u => canViewAs(u, selfId)).map(u => {
    const name = u.name || u.email
    return {
      id: `viewas:${u.id}`,
      kind: 'viewas',
      title: words.title(name),
      where: words.where(u.email || ''),
      icon: 'eye',
      viewAs: { id: u.id, name },
      hay: norm(`${words.title(name)} ${u.name} ${u.email} ${words.kind}`),
    }
  })
}

// A worker, from the node list of a cluster.
export function workerEntries(nodes, where) {
  return (Array.isArray(nodes) ? nodes : []).filter(n => n && (n.id || n.name)).map(n => {
    const title = n.name || n.id
    return {
      id: `worker:${n.id || n.name}`,
      kind: 'worker',
      title,
      where,
      icon: 'server',
      to: `/app/nodes/${encodeURIComponent(n.id || n.name)}`,
      hay: norm(`${n.name} ${n.id} ${n.address || ''}`),
    }
  })
}

// A server setting, from the settings schema the Settings page draws.
export function settingEntries(fields, groups, where) {
  const label = Object.fromEntries(groups.map(g => [g.id, g.label]))
  return fields.filter(f => f && f.label && !f.hidden).map(f => ({
    id: `setting:${f.key}`,
    kind: 'setting',
    title: f.label,
    where: `${where} / ${label[f.group] || f.group}`,
    icon: 'settings',
    to: `/app/settings?group=${encodeURIComponent(f.group)}`,
    hay: norm(`${f.label} ${f.key} ${String(f.key).replace(/_/g, ' ')} ${f.desc || ''} ${label[f.group] || ''}`),
  }))
}

// Every word of the query must appear. A title that starts with the query
// comes first, then one that contains it, then the rest in their own order.
export function rank(entries, query) {
  const words = norm(query).split(/\s+/).filter(Boolean)
  if (words.length === 0) return entries.filter(e => e.kind === 'page').slice(0, LIMIT.page)
  const scored = []
  for (const entry of entries) {
    if (!words.every(w => entry.hay.includes(w))) continue
    const title = norm(entry.title)
    const score = title.startsWith(words[0]) ? 0 : title.includes(words[0]) ? 1 : 2
    scored.push({ entry, score })
  }
  scored.sort((a, b) => a.score - b.score)
  const taken = {}
  const out = []
  for (const kind of KINDS) {
    for (const { entry } of scored) {
      if (entry.kind !== kind) continue
      taken[kind] = (taken[kind] || 0) + 1
      if (taken[kind] <= LIMIT[kind]) out.push(entry)
    }
  }
  return out
}
