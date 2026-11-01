// What the inbox panel shows for each notification, as pure functions so the
// unit tests can check them without a browser.

// The badge on the inbox button: the unread count, capped so it stays one
// small pill.
export function badgeText(unread) {
  if (!unread || unread < 1) return ''
  return unread > 99 ? '99+' : String(unread)
}

// What sent a notification: trigger, connector or system. Items stored before
// the server said so have no source; a run can only come from a trigger.
export function sourceOf(n) {
  if (n?.source === 'trigger' || n?.source === 'connector' || n?.source === 'system') return n.source
  if (n?.kind === 'run_ended' || n?.kind === 'run_failed') return 'trigger'
  return 'system'
}

export function failed(n) {
  return n?.status === 'failed'
}

// The tone of the leading tile: error for a failure, ok for a run that
// finished, accent for news that needs a look.
export function toneOf(n) {
  if (failed(n)) return 'error'
  if (n?.kind === 'run_ended') return 'ok'
  return 'accent'
}

const SOURCE_ICON = { trigger: 'bolt', connector: 'plug', system: 'info' }

export function iconOf(n) {
  const source = sourceOf(n)
  if (source === 'connector' && failed(n)) return 'plug-off'
  return SOURCE_ICON[source]
}

// Where a click on the item goes: its own link, or for a connector with none
// the page of the connectors, where it can be switched on again. Only paths
// inside the app are followed.
export function targetOf(n) {
  const link = n?.link || ''
  if (link.startsWith('/app/')) return link
  if (sourceOf(n) === 'connector') return '/app/chats/connectors'
  return ''
}

// The body of a failure is the reason as the server wrote it ("took longer
// than 15m"); it starts with a capital in the list.
export function reasonText(body) {
  const s = (body || '').trim()
  return s ? s.charAt(0).toUpperCase() + s.slice(1) : ''
}

function dayStart(ms) {
  const d = new Date(ms)
  d.setHours(0, 0, 0, 0)
  return d.getTime()
}

// The day group of a time: today, yesterday or earlier, in the local zone.
export function dayOf(iso, now = Date.now()) {
  const at = Date.parse(iso || '')
  if (!at) return 'earlier'
  const today = dayStart(now)
  if (at >= today) return 'today'
  // The start of yesterday, also on a day that is 23 or 25 hours long.
  const yesterday = dayStart(today - 12 * 3600 * 1000)
  return at >= yesterday ? 'yesterday' : 'earlier'
}

// Groups the items (newest first) into runs of the same day, in order.
export function groupByDay(items, now = Date.now()) {
  const groups = []
  for (const n of items || []) {
    const day = dayOf(n.created, now)
    const last = groups[groups.length - 1]
    if (last && last.day === day) last.items.push(n)
    else groups.push({ day, items: [n] })
  }
  return groups
}

// The items one filter shows. The server already sends only the unread ones
// for "unread"; "failed" is picked here from everything it sent.
export function filterItems(items, filter) {
  const list = items || []
  if (filter === 'failed') return list.filter(failed)
  if (filter === 'unread') return list.filter(n => !n.read)
  return list
}
