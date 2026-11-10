// Pure rules of nav.json, the order of the sidebar: reading the file, turning
// it into the sections the sidebar draws, the rows the Navigation editor
// edits, and the merge of an edit with a newer file. No React and no network,
// so the node unit tests load it.
//
// An item of the file is exactly one of:
//   {id}                  a built-in entry (home, files, ...), with label and hidden
//   {group}               a section title; the items after it belong to it
//   {type, page|chat|url} a link of the user's own
// New chat, Chats, the settings entries and the search are not items: the file
// has no way to move or hide them.

import { navBuiltins } from './navigationModel.js'
import { pageLinkTarget } from './pageLinks.js'

export const NAV_BUILTIN_IDS = Object.freeze(navBuiltins.map(b => b.id))
// Home is where a chat starts; it can be moved and renamed but not hidden.
export const NAV_ALWAYS_SHOWN = Object.freeze(['home'])
export const NAV_LIMITS = Object.freeze({ items: 60, groups: 12, links: 30, label: 40 })
export const LINK_TYPES = Object.freeze(['page', 'chat', 'url'])

const PAGE_NAME = /^[a-z0-9][a-z0-9-]{0,63}$/
const CHAT_ID = /^[A-Za-z0-9_.-]{1,80}$/
const ICON = /^[a-z0-9-]{1,40}$/

const isObject = (v) => v !== null && typeof v === 'object' && !Array.isArray(v)
const same = (a, b) => JSON.stringify(a ?? null) === JSON.stringify(b ?? null)

// navUrlOk says whether u may be the address of a link: http or https, a host,
// no user name or password. The server checks the same rule (NavURLOK in Go).
export function navUrlOk(u) {
  if (typeof u !== 'string' || /[\s\\]/.test(u)) return false
  return !pageLinkTarget(u).error
}

// itemKind says what an item is: 'builtin', 'group', 'link', or '' for a value
// that is none of them.
export function itemKind(it) {
  if (!isObject(it)) return ''
  const has = ['id', 'group', 'type'].filter(k => it[k] !== undefined)
  if (has.length !== 1) return ''
  if (has[0] === 'id') return typeof it.id === 'string' && it.id ? 'builtin' : ''
  if (has[0] === 'group') return typeof it.group === 'string' && it.group.trim() ? 'group' : ''
  if (!LINK_TYPES.includes(it.type)) return ''
  const target = it[it.type]
  if (typeof target !== 'string') return ''
  if (it.type === 'page') return PAGE_NAME.test(target) ? 'link' : ''
  if (it.type === 'chat') return CHAT_ID.test(target) ? 'link' : ''
  return navUrlOk(target) ? 'link' : ''
}

// rowKind is itemKind for a row of the editor, where a group whose title was
// just cleared is still a group (it is dropped when the rows are saved).
export const rowKind = (r) => (isObject(r) && typeof r.group === 'string' && r.id === undefined && r.type === undefined ? 'group' : itemKind(r))

// itemKey names an item the way the merge matches them: a built-in by its id,
// a group by its title, a link by what it opens.
export function itemKey(it) {
  switch (itemKind(it)) {
    case 'builtin': return `id:${it.id}`
    case 'group': return `group:${it.group}`
    case 'link': return `link:${it.type}:${it[it.type]}`
    default: return ''
  }
}

// isKnown says whether this version of LocalAI acts on the item. A built-in id
// the server does not list is kept in the file and ignored.
export const isKnown = (it) => itemKind(it) !== 'builtin' || NAV_BUILTIN_IDS.includes(it.id)

// readItems keeps the valid items of a file's list in order, once each, so a
// half-valid file (written by the assistant, or by a newer version) still
// shows what it can. Keys the editor does not know stay on the item.
export function readItems(data) {
  const list = Array.isArray(data?.items) ? data.items : []
  const seen = new Set()
  const out = []
  for (const raw of list) {
    const key = itemKey(raw)
    if (!key || seen.has(key)) continue
    seen.add(key)
    const it = { ...raw }
    if (typeof it.label !== 'string' || !it.label.trim() || it.label.length > NAV_LIMITS.label) delete it.label
    else it.label = it.label.trim()
    if (typeof it.icon !== 'string' || !ICON.test(it.icon)) delete it.icon
    if (typeof it.hidden !== 'boolean') delete it.hidden
    out.push(it)
    if (out.length === NAV_LIMITS.items) break
  }
  return out
}

// sections turns the items of the file into what the sidebar draws:
// [{key, group, entries}], where group is the title (null before the first
// group) and an entry is {key, kind: 'builtin', id, label} or
// {key, kind: 'link', item}. A built-in entry the file does not list goes
// after the last item, in the default order, so a short list never loses one.
//
//   available   the built-in ids this viewer may see (features, role)
//   linkStatus  (item) => 'ok' | 'missing' | 'unknown' for a page or chat link
//
// Hidden entries (except Home), entries the viewer cannot use, links to
// something that is gone or not known yet, and groups left empty are skipped.
export function sections(items, { available, linkStatus = () => 'ok' }) {
  const ordered = items.filter(isKnown)
  const listed = new Set(ordered.filter(i => itemKind(i) === 'builtin').map(i => i.id))
  for (const id of NAV_BUILTIN_IDS) if (!listed.has(id)) ordered.push({ id })

  const out = []
  let cur = { key: 'top', group: null, entries: [] }
  for (const it of ordered) {
    const kind = itemKind(it)
    if (kind === 'group') {
      out.push(cur)
      cur = { key: itemKey(it), group: it.group, entries: [] }
    } else if (kind === 'builtin') {
      if (!available.has(it.id)) continue
      if (it.hidden && !NAV_ALWAYS_SHOWN.includes(it.id)) continue
      cur.entries.push({ key: itemKey(it), kind: 'builtin', id: it.id, label: it.label })
    } else if (kind === 'link') {
      if (it.type !== 'url' && linkStatus(it) !== 'ok') continue
      cur.entries.push({ key: itemKey(it), kind: 'link', item: it })
    }
  }
  out.push(cur)
  return out.filter(s => s.entries.length > 0)
}

// ---- The editor ----

// editorRows are the rows of the Navigation editor: every known item of the
// file in order, then the built-in entries it does not list, so each one can
// be moved and hidden. extras are the items this version does not know; they
// are kept, after the rows, when the rows are saved.
export function editorRows(items) {
  const rows = items.filter(isKnown)
  const extras = items.filter(i => !isKnown(i))
  const listed = new Set(rows.filter(i => itemKind(i) === 'builtin').map(i => i.id))
  for (const id of NAV_BUILTIN_IDS) if (!listed.has(id)) rows.push({ id })
  return { rows, extras }
}

// defaultRows is the sidebar without a nav.json: the built-in entries in
// their default order.
export const defaultRows = () => NAV_BUILTIN_IDS.map(id => ({ id }))

// cleanRows shapes the rows for the file: keys that hold nothing are left out.
// Rows that are the default order with nothing changed save as an empty list,
// which is the sidebar's default and keeps new built-in entries in place.
export function cleanRows(rows, extras = [], collapse = true) {
  const out = []
  const seen = new Set()
  for (const raw of [...rows, ...extras]) {
    const { _uid, ...clean } = raw // _uid is the editor's own row id
    const it = typeof clean.group === 'string' ? { ...clean, group: clean.group.trim() } : { ...clean }
    const key = itemKey(it)
    if (!key || seen.has(key)) continue
    seen.add(key)
    if (typeof it.label === 'string') it.label = it.label.trim()
    if (!it.label) delete it.label
    if (!it.hidden || NAV_ALWAYS_SHOWN.includes(it.id)) delete it.hidden
    if (!it.icon) delete it.icon
    out.push(it)
  }
  return collapse ? collapseDefault(out, extras) : out
}

// collapseDefault turns the default order, untouched, into an empty list.
export function collapseDefault(list, extras = []) {
  return extras.length === 0 && same(list, defaultRows()) ? [] : list
}

// counts says how many groups and links a list has, for the limits.
export function counts(rows) {
  let groups = 0
  let links = 0
  for (const r of rows) {
    const k = itemKind(r)
    if (k === 'group') groups++
    else if (k === 'link') links++
  }
  return { groups, links, items: rows.length }
}

// canAdd says whether one more item of kind fits.
export function canAdd(rows, kind) {
  const c = counts(rows)
  if (c.items >= NAV_LIMITS.items) return false
  if (kind === 'group') return c.groups < NAV_LIMITS.groups
  if (kind === 'link') return c.links < NAV_LIMITS.links
  return true
}

// withGroup adds a group at the end; title is made unique in the list.
export function withGroup(rows, title) {
  const taken = new Set(rows.filter(r => itemKind(r) === 'group').map(r => r.group))
  let name = title
  for (let n = 2; taken.has(name); n++) name = `${title} ${n}`
  return [...rows, { group: name }]
}

// withLink adds a link at the end unless the list already has it.
export function withLink(rows, link) {
  if (rows.some(r => itemKey(r) === itemKey(link))) return rows
  return [...rows, link]
}

// rename sets or clears the label of a built-in entry or a link, or the title
// of a group. A group title must stay unique: a clash gets a number.
export function rename(rows, index, value) {
  const text = String(value ?? '').slice(0, NAV_LIMITS.label)
  return rows.map((r, i) => {
    if (i !== index) return r
    if (rowKind(r) === 'group') {
      const taken = new Set(rows.filter((x, j) => j !== index && rowKind(x) === 'group').map(x => x.group))
      let name = text
      for (let n = 2; text.trim() && taken.has(name); n++) name = `${text} ${n}`
      return { ...r, group: name }
    }
    const next = { ...r }
    if (text.trim()) next.label = text
    else delete next.label
    return next
  })
}

// setHidden hides or shows a built-in entry. Home is never hidden.
export function setHidden(rows, index, hidden) {
  return rows.map((r, i) => {
    if (i !== index || itemKind(r) !== 'builtin' || NAV_ALWAYS_SHOWN.includes(r.id)) return r
    const next = { ...r }
    if (hidden) next.hidden = true
    else delete next.hidden
    return next
  })
}

// ---- The merge ----

// mergeItem keeps, field by field, what the person changed (mine against
// base) on top of the newer item (theirs).
function mergeItem(base, mine, theirs) {
  const out = { ...theirs }
  for (const f of new Set([...Object.keys(base || {}), ...Object.keys(mine || {})])) {
    if (same(base?.[f], mine?.[f])) continue
    if (mine?.[f] === undefined) delete out[f]
    else out[f] = mine[f]
  }
  return out
}

// mergeItems puts an edit on top of a newer file without losing either side.
// base is the list the edit started from, mine the edited list and theirs the
// list now in the file (written by the assistant or another tab). Items are
// matched by itemKey:
//   - an item the person changed keeps their change, field by field;
//   - an item only the other writer changed keeps theirs;
//   - an item the person removed stays removed; one the other writer removed
//     and the person did not touch stays removed;
//   - an item either side added is kept;
//   - the order is the person's when they moved something, else the other
//     writer's, and an added item goes after the one that came before it.
export function mergeItems(base, mine, theirs) {
  const keyed = (list) => new Map(list.map(i => [itemKey(i), i]).filter(([k]) => k))
  const B = keyed(base)
  const M = keyed(mine)
  const T = keyed(theirs)
  const baseKeys = [...B.keys()]
  const mineKeys = [...M.keys()]
  const theirKeys = [...T.keys()]

  const commonMine = mineKeys.filter(k => B.has(k))
  const commonBase = baseKeys.filter(k => M.has(k))
  const moved = !same(commonMine, commonBase)

  const resolve = (k) => {
    const b = B.get(k)
    const m = M.get(k)
    const t = T.get(k)
    if (m && !b) return t ? mergeItem({}, m, t) : m // added by the person
    if (!m) return b ? null : t // removed by the person, or added by the other writer
    if (!t) return same(b, m) ? null : m // removed by the other writer, unless the person edited it
    return mergeItem(b, m, t)
  }

  const skeleton = moved ? mineKeys : theirKeys
  const result = []
  const placed = new Set()
  for (const k of skeleton) {
    const it = resolve(k)
    if (it) { result.push({ key: k, it }); placed.add(k) }
  }
  // The items the skeleton lacks: the other side's additions after a move,
  // the person's additions (or edits of an item the other writer removed)
  // otherwise.
  const extraOrder = moved ? theirKeys : mineKeys
  extraOrder.forEach((k, i) => {
    if (placed.has(k)) return
    const it = resolve(k)
    if (!it) return
    let at = 0
    for (let j = i - 1; j >= 0; j--) {
      const idx = result.findIndex(r => r.key === extraOrder[j])
      if (idx >= 0) { at = idx + 1; break }
    }
    result.splice(at, 0, { key: k, it })
    placed.add(k)
  })
  return result.map(r => r.it)
}

// mergeNav is mergeItems for the editor: base and theirs are the data of the
// file (as the editor loaded it, and as it is now), mine the rows and extras
// being saved. All three are written in full (every built-in entry listed), so
// an entry the file leaves out does not look like a removal.
export function mergeNav(baseData, mineRows, mineExtras, theirData) {
  const full = (items) => {
    const { rows, extras } = editorRows(items)
    return cleanRows(rows, extras, false)
  }
  const merged = mergeItems(full(readItems(baseData)), cleanRows(mineRows, mineExtras, false), full(readItems(theirData)))
  return collapseDefault(merged, merged.filter(i => !isKnown(i)))
}
