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
  if (typeof n?.code === 'string' && n.code.startsWith('updates.')) return 'arrow-up'
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

// ---- Items that the UI words itself -----------------------------------------
//
// An item can carry a code and params (updates.digest, updates.result,
// updates.checkFailed). The words come from the chats namespace, key group
// inbox.codes, in the language of the person; the title and body the server
// stored are English and stay the fallback for a code this build does not
// know, or params it cannot read. `t` is the translator of the chats namespace.

const strings = (v) => (Array.isArray(v) ? v.filter(x => typeof x === 'string' && x) : [])
const count = (v) => (Number.isInteger(v) && v > 0 ? v : 0)

// "a, b and 2 more": the first names, then how many were left out.
function listWithMore(t, key, names, total) {
  const items = names.join(', ')
  const more = total - names.length
  return more > 0 ? t(`${key}.more`, { items, count: more }) : items
}

function resultText(t, p, n) {
  const family = p.family === 'addons' || p.family === 'backends' ? p.family : ''
  const updated = count(p.updated)
  const failedCount = count(p.failed)
  const held = count(p.held)
  if (!family || updated + failedCount + held === 0) return null
  const parts = []
  if (updated) parts.push(t('inbox.codes.result.updated', { count: updated }))
  if (failedCount) parts.push(t('inbox.codes.result.failed', { count: failedCount }))
  if (held) parts.push(t('inbox.codes.result.held', { count: held }))
  const body = []
  const add = (key, total, names) => {
    if (total && names.length) body.push(t(`inbox.codes.result.${key}`, { items: listWithMore(t, 'inbox.codes.result', names, total) }))
  }
  add('bodyUpdated', updated, strings(p.updatedItems))
  add('bodyFailed', failedCount, strings(p.failedItems))
  add('bodyHeld', held, strings(p.heldItems))
  return {
    title: t('inbox.codes.result.title', { family: t(`inbox.codes.result.family.${family}`), parts: parts.join(t('inbox.codes.result.join')) }),
    body: body.length ? body.join(' ') : n.body,
  }
}

// The title and body of a notification as the list shows them. Items without a
// code, and codes or params that are not understood, show what the server stored.
export function textOf(n, t) {
  const stored = { title: n?.title || '', body: n?.body || '' }
  const p = n?.params
  if (!n?.code || !p || typeof p !== 'object') return stored
  let out = null
  if (n.code === 'updates.digest' && p.family === 'backends' && count(p.count)) {
    const names = strings(p.names)
    out = {
      title: t('inbox.codes.digest.title', { count: p.count }),
      body: names.length ? listWithMore(t, 'inbox.codes.digest', names, p.count) : n.body,
    }
  } else if (n.code === 'updates.result') {
    out = resultText(t, p, n)
  } else if (n.code === 'updates.checkFailed') {
    const sources = strings(p.sources).filter(x => ['gallery', 'library', 'addons'].includes(x))
    if (sources.length) {
      out = {
        title: t('inbox.codes.checkFailed.title'),
        body: t('inbox.codes.checkFailed.body', { sources: sources.map(x => t(`inbox.codes.checkFailed.source.${x}`)).join(t('inbox.codes.result.join')) }),
      }
    }
  }
  return out || stored
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

// True when an unread item is a failure. The badge of the navbar entry turns
// red for it: something went wrong and nobody has looked yet.
export function hasUnreadFailure(items) {
  return (items || []).some(n => !n.read && failed(n))
}

// Whether the navbar entry is drawn. An administrator always sees it, because
// the digest and result items land there and an empty entry says where they
// go. Anyone else sees it once the inbox holds something, and keeps it for the
// rest of the session (everHad) and while the panel is open, so clearing the
// list does not take the button from under the person's hand. Nobody sees it
// when the inbox is off (chats off, no chats feature, or the server says
// there is none) or before the first answer.
export function entryVisible({ enabled, off, loaded, isAdmin, total, everHad, open }) {
  if (!enabled || off || !loaded) return false
  if (isAdmin) return true
  return total > 0 || everHad || open
}

// The answers that say "no inbox here": 404 and 409 (the server keeps none) and
// 403 (this person lacks the chats feature). The hook stops asking at once,
// where any other failure is retried.
export function inboxOff(status) {
  return status === 403 || status === 404 || status === 409
}

// Whether the notification stream should be open: the hook is on, the tab is
// showing and the server has not said there is no inbox. A hidden tab holds no
// connection, which matters on plain HTTP where a browser keeps few per host.
export function streamWanted({ enabled, visible, off }) {
  return Boolean(enabled && visible && !off)
}
