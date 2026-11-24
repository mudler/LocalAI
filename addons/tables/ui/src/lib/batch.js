// Bounded batches. The add-on takes at most 100 rows in one insert and at
// most 1000 row identifiers in one update or delete, and each operation
// commits whole or not at all. A person's larger selection is split into
// steps the person sees, each with its own operation identifier, so one
// failed step never hides what the earlier steps did.
import { parseField } from './codecs.js'

export const MAX_INSERT_ROWS = 100
export const MAX_ROW_IDS = 1000

export function chunk(list, size) {
  const out = []
  for (let i = 0; i < list.length; i += size) out.push(list.slice(i, i + size))
  return out
}

// planInsert parses the rows a person typed. `rows` is a list of
// { [column]: {text, isNull} }; `columns` the table's columns. A column the
// person left blank and that is optional is left out of the row, so the
// column default applies. Returns { rows, errors } with errors keyed by
// "rowIndex:column".
export function planInsert(rows, columns) {
  const out = []
  const errors = {}
  rows.forEach((row, i) => {
    const values = {}
    for (const c of columns) {
      const state = row[c.name] || { text: '', isNull: false }
      const untouched = !state.isNull && state.text === '' && c.type !== 'text'
      if (untouched && !c.required) continue
      if (!state.isNull && state.text === '' && c.type === 'text' && !c.required) continue
      const r = parseField(c, state)
      if (!r.ok) { errors[`${i}:${c.name}`] = r.code; continue }
      values[c.name] = r.value
    }
    out.push(values)
  })
  return { rows: out, errors }
}

export function insertSteps(rows) {
  return chunk(rows, MAX_INSERT_ROWS)
}

export function idSteps(ids) {
  return chunk(ids, MAX_ROW_IDS)
}

// The selection of grid rows, by row identifier. It survives paging; a row
// that is no longer in the table is dropped when the page is read again.
export function toggle(selection, id) {
  const next = new Set(selection)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  return next
}

export function setAll(selection, ids, on) {
  const next = new Set(selection)
  for (const id of ids) { if (on) next.add(id); else next.delete(id) }
  return next
}

export function allSelected(selection, ids) {
  return ids.length > 0 && ids.every(id => selection.has(id))
}
