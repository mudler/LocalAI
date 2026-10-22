// Row query state: the arguments of rows-query for the grid, the page stack of
// the cursors the add-on returned, and the rules that keep a person's view
// consistent when the database moved.
import { buildFilter } from './filters.js'

export const PAGE_SIZES = Object.freeze([25, 50, 100, 200])
export const MAX_SORT = 4

// buildQuery returns { arguments, errors }. `arguments` is what rows-query
// takes; the tool name is added by the caller that sends it.
export function buildQuery({ table, conditions = [], mode = 'and', sort = [], limit = 50, cursor = '' }, columns) {
  const { filter, errors } = buildFilter(conditions, columns, mode)
  const out = { table }
  if (filter) out.filter = filter
  const keys = []
  for (const s of sort) {
    if (keys.length >= MAX_SORT) break
    if (s && typeof s.column === 'string' && s.column !== '' && !keys.some(k => k.column === s.column)) keys.push({ column: s.column, desc: s.desc === true })
  }
  if (keys.length > 0) out.sort = keys
  out.limit = PAGE_SIZES.includes(limit) ? limit : 50
  if (cursor) out.cursor = cursor
  return { arguments: out, errors }
}

// A pager remembers the cursor of each page it showed, so Previous needs no
// cursor of the add-on's. A cursor is valid for one query and one revision:
// any change of either starts again at the first page.
export function newPager() {
  return { cursors: [''], at: 0 }
}

export function pagerNext(pager, nextCursor) {
  if (!nextCursor) return pager
  const cursors = pager.cursors.slice(0, pager.at + 1)
  cursors.push(nextCursor)
  return { cursors, at: pager.at + 1 }
}

export function pagerPrev(pager) {
  return pager.at > 0 ? { cursors: pager.cursors, at: pager.at - 1 } : pager
}

export function pagerCursor(pager) {
  return pager.cursors[pager.at] || ''
}

// flipSort cycles one column: ascending, descending, none.
export function flipSort(sort, column) {
  const at = sort.findIndex(s => s.column === column)
  if (at < 0) return [{ column, desc: false }]
  if (!sort[at].desc) return [{ column, desc: true }]
  return []
}
