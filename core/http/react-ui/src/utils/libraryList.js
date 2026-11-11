// The rules behind the lists of the Knowledge and Skills pages that do not need
// React: matching a search, ordering, grouping and how many rows to draw. They
// are kept apart so a list of hundreds of items behaves the same everywhere and
// can be tested without a browser.

// Names such as report-2 and report-10 order the way a person counts them.
const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })
export const byName = (a, b) => collator.compare(a, b)

// Every word of the query must appear in one of the fields, in any order. An
// empty query matches everything.
export function matchesQuery(query, ...fields) {
  const words = String(query || '').toLowerCase().split(/\s+/).filter(Boolean)
  if (words.length === 0) return true
  const hay = fields.map(f => String(f || '').toLowerCase()).join('\n')
  return words.every(w => hay.includes(w))
}

// Orders items by one of the modes. `get` gives each item's name, how many
// agents use it and its group; the rest of the item is left alone. The sort is
// stable and always ends on the name, so equal rows never jump around.
//   name       A to Z
//   name-desc  Z to A
//   used       most used first
//   unused     not used first
//   source     by group, in the order `groups` lists them
export function sortItems(items, mode, get, groups = []) {
  const rank = (item) => { const i = groups.indexOf(get(item).group); return i < 0 ? groups.length : i }
  const cmp = {
    'name-desc': (a, b) => byName(get(b).name, get(a).name),
    used: (a, b) => (get(b).used - get(a).used) || byName(get(a).name, get(b).name),
    unused: (a, b) => (get(a).used - get(b).used) || byName(get(a).name, get(b).name),
    source: (a, b) => (rank(a) - rank(b)) || byName(get(a).name, get(b).name),
  }[mode] || ((a, b) => byName(get(a).name, get(b).name))
  return [...items].sort(cmp)
}

// Splits an ordered list into runs of one group, for the headings of the
// "source" order. Returns [{ group, items }].
export function runsOf(items, groupOf) {
  const runs = []
  for (const item of items) {
    const group = groupOf(item)
    const last = runs[runs.length - 1]
    if (last && last.group === group) last.items.push(item)
    else runs.push({ group, items: [item] })
  }
  return runs
}

// How many rows to draw: `step` at first, `step` more per press of "Show
// more", and always enough to include the open item so it is never cut off.
export function rowLimit(shown, total, mustShowIndex = -1) {
  const base = Math.min(Math.max(shown, 0), total)
  return mustShowIndex >= 0 ? Math.min(total, Math.max(base, mustShowIndex + 1)) : base
}
