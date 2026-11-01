// Helpers of the switch lists of the composer (the Tools menu and the Skills
// picker). No React, so a node test drives them.
//
// A row: { id, on, usable, optIn }. A row that is not usable shows off and has
// no part in a bulk change: its stored state stays as it is, so it comes back
// as it was when it becomes usable. An opt-in row (saved agents) is switched on
// by hand only: "Select all" leaves it, "Disable all" switches it off.

// Whether the row shows on.
export const rowOn = (r) => r.usable !== false && !!r.on

// What is on among what can be used: the "3 of 5 on" of a header.
export function switchCounts(rows) {
  const usable = rows.filter((r) => r.usable !== false)
  return { on: usable.filter((r) => r.on).length, total: usable.length }
}

// The ids of the rows that "Select all" (on) or "Disable all" (off) changes.
export function bulkIds(rows, on) {
  return rows
    .filter((r) => r.usable !== false && (!on || !r.optIn) && rowOn(r) !== on)
    .map((r) => r.id)
}

// The rows a bulk change can ever touch, whatever their state: a group shows
// its actions only when there are two or more.
export function bulkable(rows) {
  return rows.filter((r) => r.usable !== false).length
}

// The items of a list with the ones whose key is in ids switched on or off.
export function withOn(items, keyOf, ids, on) {
  return items.map((x) => (ids.has(keyOf(x)) ? { ...x, off: !on } : x))
}

// The off list of the MCP switches after these keys change, in the order the
// switches were used.
export function offWith(off, keys, on) {
  const rest = (off || []).filter((x) => !keys.includes(x))
  return on ? rest : [...rest, ...keys]
}
