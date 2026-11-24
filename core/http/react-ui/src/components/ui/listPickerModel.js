// The pure rules of ListPicker: which rows a query keeps, where the active row
// moves on a key, which row is active when the menu opens, where a group
// heading goes. No React here, so tests/unit/listpicker.test.mjs runs them in
// node.

// How many rows before the menu gets a filter box, when `filter` is left to
// decide.
export const FILTER_FROM = 8

// The id of the row that empties the value (the `noneLabel` row).
export const NONE_KEY = '__none'

export function rowKey(row) {
  return row.key ?? String(row.id)
}

function text(v) {
  return typeof v === 'string' || typeof v === 'number' ? String(v).toLowerCase() : ''
}

// A row matches when its label, secondary text, description, id or one of its
// keywords contains the query. Case does not matter.
export function rowMatches(row, query) {
  const q = (query || '').trim().toLowerCase()
  if (!q) return true
  const fields = [row.label, row.secondary, row.description, row.id, ...(row.keywords || [])]
  return fields.some(f => text(f).includes(q))
}

export function filterRows(items, query) {
  return items.filter(row => rowMatches(row, query))
}

export function showFilterBox(filter, count, from = FILTER_FROM) {
  return filter ?? count >= from
}

export function isRowSelected(row, value, multiple) {
  if (row.none) return multiple ? !(Array.isArray(value) && value.length) : (value === '' || value == null)
  if (multiple) return Array.isArray(value) && value.includes(row.id)
  return row.id === value
}

// The row that is active when the menu opens: the chosen one in a single
// choice, the first one otherwise.
export function initialActive(rows, value, multiple) {
  if (multiple) return 0
  const i = rows.findIndex(r => (r.selected ?? isRowSelected(r, value, false)))
  return Math.max(0, i)
}

// The next active index for a key in the open menu, or null when the key does
// not move it. The arrows wrap; Home and End only move when the filter is
// empty, so they keep moving the caret in the filter box.
export function moveActive(key, active, count, hasQuery = false) {
  if (key === 'ArrowDown') return count ? (active + 1) % count : active
  if (key === 'ArrowUp') return count ? (active + count - 1) % count : active
  if (key === 'Home' && !hasQuery) return 0
  if (key === 'End' && !hasQuery) return Math.max(0, count - 1)
  return null
}

// The heading drawn before row i: a row with a group starts a new block when
// the row before it has another group (or none).
export function groupHeading(rows, i, groups) {
  const g = rows[i]?.group
  if (g == null) return null
  if (i > 0 && rows[i - 1]?.group === g) return null
  const label = groups && typeof groups === 'object' ? groups[g] : undefined
  return label ?? g
}

export function toggleValue(values, id) {
  const list = Array.isArray(values) ? values : []
  return list.includes(id) ? list.filter(v => v !== id) : [...list, id]
}

// Where a menu sits, in px from the left edge of its trigger, and how wide it
// may be. The menu hangs from the trigger's left edge. When it does not fit
// there it hangs from the trigger's right edge, and when it still does not fit
// it is pushed back inside `bounds`: the window cut down to the nearest
// ancestor that clips (a side sheet's body scrolls, so it clips). A menu wider
// than `bounds` is narrowed to fit. `width` is null when the menu keeps its
// own width.
export function placeMenu({ rootLeft, rootRight, menuWidth, boundsLeft, boundsRight, margin = 8 }) {
  const room = Math.max(0, boundsRight - boundsLeft - 2 * margin)
  const width = Math.min(menuWidth, room)
  const last = boundsRight - margin
  let x = rootLeft
  if (x + width > last) x = rootRight - width
  if (x + width > last) x = last - width
  if (x < boundsLeft + margin) x = boundsLeft + margin
  return { left: x - rootLeft, width: width < menuWidth ? width : null }
}
