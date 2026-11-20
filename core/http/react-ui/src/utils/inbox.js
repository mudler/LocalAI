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
  if (n?.code === 'models.upgradeDone') return 'check-circle'
  if (n?.code === 'models.upgradeFailed') return 'alert-circle'
  if (n?.code === 'models.removedOld') return 'trash'
  if (n?.code === 'models.updateAvailable') return 'arrow-up'
  if (n?.code === 'updates.opDone' || n?.code === 'updates.opFailed') return failed(n) ? 'alert-circle' : n.params?.outcome === 'cancelled' ? 'info' : 'check-circle'
  if (typeof n?.code === 'string' && n.code.startsWith('updates.')) return 'arrow-up'
  const source = sourceOf(n)
  if (source === 'connector' && failed(n)) return 'plug-off'
  return SOURCE_ICON[source]
}

// Whether a path is a page of the chats: a chat, an agent, the connectors.
// With the chats off these pages are not there.
const CHATS_PATH = /^\/app\/chats(?:[/?#]|$)/

// Where a click on the item goes: its own link, or for a connector with none
// the page of the connectors, where it can be switched on again. Only paths
// inside the app are followed. With the chats off a link into the chats goes
// nowhere (chatsOn false): the row stays in the list, read-only.
export function targetOf(n, chatsOn = true) {
  const link = n?.link || ''
  const target = link.startsWith('/app/') ? link : sourceOf(n) === 'connector' ? '/app/chats/connectors' : ''
  if (!chatsOn && CHATS_PATH.test(target)) return ''
  return target
}

// True when the item points into the chats and the chats are off, so the row
// says why it opens nothing.
export function linkDisabled(n, chatsOn = true) {
  if (chatsOn) return false
  return targetOf(n, true) !== '' && targetOf(n, false) === ''
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

// The end of an install, removal or upgrade of a model or backend. The reason
// of a failure stays the server's own words (the body).
const OP_KINDS = ['model', 'backend', 'addon']
const OP_ACTIONS = ['install', 'delete', 'upgrade']

function opText(t, n, p) {
  const name = typeof p.name === 'string' ? p.name : ''
  if (!name || !OP_KINDS.includes(p.kind) || !OP_ACTIONS.includes(p.action)) return null
  const state = n.code === 'updates.opDone' ? 'done' : p.outcome === 'cancelled' ? 'cancelled' : 'failed'
  // An add-on whose package asked for more host access than was reviewed did
  // not fail in a way a retry fixes: it waits for a new review.
  if (state === 'failed' && p.kind === 'addon' && p.action === 'install' && p.needsApproval === true) {
    return { title: t('inbox.codes.op.needsReview.install', { name }), body: n.body }
  }
  return { title: t(`inbox.codes.op.${state}.${p.action}`, { kind: t(`inbox.codes.op.kind.${p.kind}`), name }), body: n.body }
}

// A digest of a model entry as the list shows it: "sha256:0123456789ab".
function shortRef(v) {
  const s = typeof v === 'string' ? v.trim() : ''
  if (!s) return ''
  const [algo, hex] = s.includes(':') ? s.split(':') : ['', s]
  return hex ? `${algo ? `${algo}:` : ''}${hex.slice(0, 12)}` : ''
}

// The model update codes: the digest of the models with an update, the end of
// one upgrade (done or failed, and whether it was rolled back) and the removal
// of the old version an upgrade kept. A failure keeps the server's reason.
function modelText(t, n, p) {
  const name = typeof p.name === 'string' ? p.name : ''
  switch (n.code) {
    case 'models.updateAvailable': {
      if (p.family !== 'models' || !count(p.count)) return null
      const names = strings(p.names)
      return {
        title: t('inbox.codes.models.available.title', { count: p.count }),
        body: names.length ? listWithMore(t, 'inbox.codes.digest', names, p.count) : n.body,
      }
    }
    case 'models.upgradeDone': {
      if (!name) return null
      const from = shortRef(p.from)
      const to = shortRef(p.to)
      return { title: t('inbox.codes.models.done.title', { name }), body: from && to ? t('inbox.codes.models.done.body', { from, to }) : n.body }
    }
    case 'models.upgradeFailed': {
      if (!name) return null
      const reason = typeof p.error === 'string' && p.error ? p.error : (n.body || '')
      const key = p.rolledBack === true ? 'rolledBack' : 'kept'
      return {
        title: t('inbox.codes.models.failed.title', { name }),
        body: reason ? t(`inbox.codes.models.failed.${key}`, { error: reason }) : t(`inbox.codes.models.failed.${key}Plain`),
      }
    }
    case 'models.removedOld': {
      if (!name) return null
      const files = Number.isInteger(p.files) && p.files >= 0 ? p.files : -1
      return { title: t('inbox.codes.models.removed.title', { name }), body: files >= 0 ? t('inbox.codes.models.removed.body', { count: files }) : n.body }
    }
    default:
      return null
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
  } else if (n.code === 'updates.opDone' || n.code === 'updates.opFailed') {
    out = opText(t, n, p)
  } else if (typeof n.code === 'string' && n.code.startsWith('models.')) {
    out = modelText(t, n, p)
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
// when the inbox is off (the server says there is none, or the notification
// center did not start) or before the first answer. The chats do not matter.
export function entryVisible({ enabled, off, loaded, isAdmin, total, everHad, open }) {
  if (!enabled || off || !loaded) return false
  if (isAdmin) return true
  return total > 0 || everHad || open
}

// The answers that say "no inbox here": 404 and 409 (the server keeps none) and
// 403 (the server refuses this person). The hook stops asking at once, where
// any other failure is retried.
export function inboxOff(status) {
  return status === 403 || status === 404 || status === 409
}

// Whether the notification stream should be open: the hook is on, the tab is
// showing and the server has not said there is no inbox. A hidden tab holds no
// connection, which matters on plain HTTP where a browser keeps few per host.
export function streamWanted({ enabled, visible, off }) {
  return Boolean(enabled && visible && !off)
}
