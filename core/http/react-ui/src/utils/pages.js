// Pure rules of the pages of the workspace: names, the paths of the change
// feed, the trust decision and the pins kept in ui.json. No React, no network.

export const PAGE_NAME = /^[a-z0-9][a-z0-9-]{0,63}$/
export const MAX_PINS = 20
export const UI_FILE = '/ui.json'

export const isPageName = (name) => typeof name === 'string' && PAGE_NAME.test(name)

// The folder of a page inside the home, and its index.html.
export const pageFolder = (name) => `/pages/${name}`
export const pageIndexPath = (name) => `pages/${name}/index.html`

// The changed paths of a call of the change feed, or null for a resync: the
// feed calls with {paths: [], resync: true} when it may have missed changes,
// and an empty list there means "anything", not "nothing".
export function feedPaths(ev) {
  if (!ev || ev.resync || !Array.isArray(ev.paths)) return null
  return ev.paths
}

// What a change of the feed means for one page. paths are inside the home,
// with or without a leading slash. rel are the changed paths relative to the
// page's folder; index says the page itself changed and must load again.
// Without paths (a resync) everything may have changed.
export function pageChanges(paths, name) {
  if (!Array.isArray(paths)) return { index: true, meta: true, rel: [], all: true }
  const prefix = `pages/${name}/`
  const rel = []
  let index = false
  let meta = false
  for (const raw of paths) {
    if (typeof raw !== 'string') continue
    const p = raw.replace(/^\/+/, '')
    if (p === `pages/${name}`) { index = true; meta = true; continue }
    if (!p.startsWith(prefix)) continue
    const r = p.slice(prefix.length)
    if (r === 'index.html') index = true
    else if (r === 'page.json') meta = true
    else if (r) rel.push(r)
  }
  return { index, meta, rel, all: false }
}

// Whether a change of the feed touches any page: the list must be read again.
export function touchesPages(paths) {
  if (!Array.isArray(paths)) return true
  return paths.some(p => typeof p === 'string' && /^\/?pages(\/|$)/.test(p))
}

// Whether a page may run without asking. A page in the user's own home is
// trusted. Pages of shared folders will ask first (the trust sheet); until
// that exists they do not run.
export function trusted(page) {
  return page?.source === 'own'
}

// The pinned page names of a ui.json value: a list of page names, without
// duplicates or names that cannot be pages.
export function pinsOf(ui) {
  const list = ui && typeof ui === 'object' && Array.isArray(ui.pinnedPages) ? ui.pinnedPages : []
  const out = []
  for (const n of list) {
    if (isPageName(n) && !out.includes(n)) out.push(n)
    if (out.length === MAX_PINS) break
  }
  return out
}

// ui.json with the pin of name set or cleared. Every other key, including the
// ones this UI does not know, stays as it was.
export function withPin(ui, name, pinned) {
  const base = ui && typeof ui === 'object' && !Array.isArray(ui) ? ui : {}
  const current = pinsOf(base).filter(n => n !== name)
  const next = pinned ? [...current, name].slice(-MAX_PINS) : current
  return { ...base, pinnedPages: next }
}

// The pinned pages that exist, in pin order.
export function pinnedPages(pages, pins) {
  const byName = new Map((pages || []).map(p => [p.name, p]))
  return pins.map(n => byName.get(n)).filter(Boolean)
}

const UNITS = [['year', 31536000], ['month', 2592000], ['week', 604800], ['day', 86400], ['hour', 3600], ['minute', 60]]

// How long ago ts was, as {value, unit} for Intl.RelativeTimeFormat (value is
// negative), or null under a minute, which the UI says as "just now".
export function ageParts(ts, now = Date.now()) {
  const t = typeof ts === 'string' ? Date.parse(ts) : ts
  if (!Number.isFinite(t)) return null
  const seconds = Math.max(0, Math.floor((now - t) / 1000))
  for (const [unit, size] of UNITS) {
    if (seconds >= size) return { value: -Math.floor(seconds / size), unit }
  }
  return null
}
