import { addonPageAddress } from '../addons/addresses.js'

// Chats that a person did not start: a trigger, a connector binding, an agent
// job or a page sent the first message. They stay out of the lists of the
// person's own chats and have an area of their own. No React, so a node test
// drives these helpers.
//
// Nothing here knows a platform. A chat has an origin kind (trigger,
// connector, job, page, or a kind a later release adds) and the id of the
// thing that started it. The title of that thing comes from the list the
// kind belongs to; a kind that has no list falls back to the id, and a kind
// this file never heard of falls under a generic group.

// The order of the groups. A kind that is not here comes after, by name.
const KIND_ORDER = ['trigger', 'connector', 'job', 'page']

// The origin kinds that have words of their own; any other falls under "other".
const KNOWN = ['trigger', 'connector', 'job', 'page']
export const knownKind = (kind) => (KNOWN.includes(kind) ? kind : 'other')

export const isAutomated = (chat) => Boolean(chat?.origin)

// The chat summary, split: the chats a person started, then the others.
export function splitByOrigin(chats) {
  const person = []
  const automated = []
  for (const c of chats || []) (isAutomated(c) ? automated : person).push(c)
  return { person, automated }
}

// Where the thing that started a chat has its page, or '' when it has none.
function originHref(kind, id) {
  if (kind === 'trigger') return id ? `/app/triggers/${encodeURIComponent(id)}` : '/app/triggers'
  if (kind === 'connector') return '/app/chats/connectors'
  if (kind === 'job') return '/app/triggers'
  if (kind === 'page') return id ? `/app/pages/${encodeURIComponent(id)}` : '/app/pages'
  return ''
}

// The name a list gives the origin, or '' when the list does not know it.
// ctx holds the lists the page read: connectors (bindings), connectorKinds
// and triggers. A connector without a name of its own takes the title of its
// kind.
function originName(kind, id, ctx) {
  if (kind === 'connector') {
    const b = (ctx?.connectors || []).find((x) => x.id === id)
    if (!b) return ''
    if (b.name) return b.name
    return (ctx?.connectorKinds || []).find((k) => k.kind === b.kind)?.title || b.kind || ''
  }
  if (kind === 'trigger') return (ctx?.triggers || []).find((x) => x.id === id)?.name || ''
  // A job or a page has no list of its own here: its id is its name.
  if (kind === 'job' || kind === 'page') return id || ''
  return ''
}

// What started a chat: { kind, id, key, title, href }. title is '' when
// nothing names it; the view then shows the label of the kind.
export function resolveOrigin(chat, ctx) {
  const kind = chat?.origin || ''
  const id = chat?.originId || ''
  let href = originHref(kind, id)
  if (kind === 'connector') {
    // The add-on that serves the binding's kind has a page of its own for it.
    const b = (ctx?.connectors || []).find((x) => x.id === id)
    const k = b && (ctx?.connectorKinds || []).find((x) => x.kind === b.kind && (!b.addon || x.addon === b.addon))
    if (k?.page) href = addonPageAddress({ addon: k.addon, page: k.page })
  }
  return { kind, id, key: `${kind}/${id}`, title: originName(kind, id, ctx), href }
}

// The status of a chat of this kind: running while a turn runs, waiting when
// it needs the person (its turn paused at the step limit, or an unread
// notification asks for them; needsYou is the Set of chat ids with one), idle
// otherwise.
export function chatStatus(chat, needsYou) {
  if (chat?.lastTurn === 'paused' || needsYou?.has(chat?.id)) return 'waiting'
  if (chat?.running) return 'running'
  return 'idle'
}

// The ids of the chats that unread inbox items ask the person to look at: a
// run that failed, or something that needs them. An item links to its chat.
const CHAT_LINK = /^\/app\/chats\/([^/?#]+)/
export function needsYouIds(notifications) {
  const ids = new Set()
  for (const n of notifications || []) {
    if (n.read || (n.kind !== 'needs_attention' && n.kind !== 'run_failed')) continue
    const m = CHAT_LINK.exec(n.link || '')
    if (m) ids.add(decodeURIComponent(m[1]))
  }
  return ids
}

// How many of these chats need the person, and whether any turn runs.
export function attention(chats, needsYou) {
  let waiting = 0
  let running = 0
  for (const c of chats || []) {
    if (!isAutomated(c)) continue
    const s = chatStatus(c, needsYou)
    if (s === 'waiting') waiting++
    else if (s === 'running') running++
  }
  return { waiting, running }
}

// The chats that were not started by a person, in groups by origin kind and,
// inside a kind, by origin (one trigger, one connector binding...). Kinds
// keep a fixed order, origins and chats go newest first. Every group has the
// count, the time of its latest chat and how many chats run or wait.
export function groupByOrigin(chats, ctx, needsYou) {
  const kinds = new Map()
  for (const c of chats || []) {
    if (!isAutomated(c)) continue
    const o = resolveOrigin(c, ctx)
    if (!kinds.has(o.kind)) kinds.set(o.kind, new Map())
    const origins = kinds.get(o.kind)
    if (!origins.has(o.key)) origins.set(o.key, { ...o, chats: [], count: 0, lastActive: 0, running: 0, waiting: 0 })
    const g = origins.get(o.key)
    g.chats.push(c)
    g.count++
    g.lastActive = Math.max(g.lastActive, c.updatedAt || 0)
    const s = chatStatus(c, needsYou)
    if (s === 'running') g.running++
    if (s === 'waiting') g.waiting++
  }
  const rank = (k) => { const i = KIND_ORDER.indexOf(k); return i < 0 ? KIND_ORDER.length : i }
  return [...kinds.entries()]
    .sort(([a], [b]) => rank(a) - rank(b) || a.localeCompare(b))
    .map(([kind, origins]) => {
      const groups = [...origins.values()].sort((a, b) => b.lastActive - a.lastActive)
      for (const g of groups) g.chats.sort((a, b) => (b.updatedAt || 0) - (a.updatedAt || 0))
      return {
        kind,
        count: groups.reduce((n, g) => n + g.count, 0),
        waiting: groups.reduce((n, g) => n + g.waiting, 0),
        running: groups.reduce((n, g) => n + g.running, 0),
        groups,
      }
    })
}

// The tab of All chats the browser remembers.
export const TAB_KEY = 'localai_all_chats_tab'
export const TABS = ['mine', 'automated']

export function readTab(storage) {
  try {
    const v = (storage || window.localStorage).getItem(TAB_KEY)
    return TABS.includes(v) ? v : 'mine'
  } catch {
    return 'mine'
  }
}

export function writeTab(tab, storage) {
  try { (storage || window.localStorage).setItem(TAB_KEY, tab) } catch { /* private mode: the tab is not remembered */ }
}
