// The structured filter of the row grid. A person builds a list of
// conditions; this module checks them against the column types and builds the
// filter tree rows-query takes. There is no free text and no expression: the
// add-on rejects anything else, and so does this builder.
import { parseField } from './codecs.js'

export const ID_COLUMN = '_id'
export const MAX_NODES = 64
export const MAX_IN_VALUES = 100
export const MAX_VALUE_BYTES = 1024

const ORDER = ['eq', 'ne', 'lt', 'lte', 'gt', 'gte']
const TEXT_ONLY = ['contains', 'starts-with', 'ends-with']
const NULLS = ['is-null', 'is-not-null']

// operators lists the operators a column type supports, in menu order.
export function operators(type) {
  switch (type) {
    case 'text': return [...ORDER, ...TEXT_ONLY, 'in', ...NULLS]
    case 'boolean': return ['eq', 'ne', ...NULLS]
    case 'integer':
    case 'real':
    case 'datetime': return [...ORDER, 'in', ...NULLS]
    default: return []
  }
}

export function needsValue(op) {
  return !NULLS.includes(op)
}

// The row identifier is filterable as an integer under its own label.
export function columnType(columns, label) {
  if (label === ID_COLUMN) return 'integer'
  const c = (columns || []).find(x => x.name === label)
  return c ? c.type : ''
}

export function blankCondition(columns) {
  const first = (columns || [])[0]
  return { column: first ? first.name : ID_COLUMN, op: 'eq', value: '' }
}

const bytes = (s) => new TextEncoder().encode(s).length

// buildFilter returns { filter, errors }. `errors` maps a condition index to
// a code; an empty map means the filter may be sent. A condition with no
// value for an operator that needs one is an error, never silently dropped.
export function buildFilter(conditions, columns, mode = 'and') {
  const errors = {}
  const nodes = []
  const list = Array.isArray(conditions) ? conditions : []
  list.forEach((c, i) => {
    const type = columnType(columns, c.column)
    if (!type) { errors[i] = 'column'; return }
    if (!operators(type).includes(c.op) && !(c.column === ID_COLUMN && operators('integer').includes(c.op))) { errors[i] = 'operator'; return }
    if (!needsValue(c.op)) { nodes.push({ column: c.column, op: c.op }); return }
    const col = { type, required: false }
    const one = (text) => {
      if (type === 'text') {
        if (bytes(text) > MAX_VALUE_BYTES) return { ok: false, code: 'size' }
        return { ok: true, value: text }
      }
      return parseField(col, { text, isNull: false })
    }
    if (c.op === 'in') {
      const parts = String(c.value ?? '').split(',').map(s => s.trim()).filter(s => s !== '')
      if (parts.length === 0) { errors[i] = 'value'; return }
      if (parts.length > MAX_IN_VALUES) { errors[i] = 'too-many'; return }
      const values = []
      for (const p of parts) {
        const r = one(p)
        if (!r.ok || r.value === null) { errors[i] = r.code || 'value'; return }
        values.push(r.value)
      }
      nodes.push({ column: c.column, op: 'in', values })
      return
    }
    const text = String(c.value ?? '')
    if (text === '' && type !== 'text') { errors[i] = 'value'; return }
    const r = one(text)
    if (!r.ok || r.value === null) { errors[i] = r.code || 'value'; return }
    nodes.push({ column: c.column, op: c.op, value: r.value })
  })
  if (Object.keys(errors).length > 0) return { filter: null, errors }
  if (nodes.length > MAX_NODES) return { filter: null, errors: { 0: 'too-many' } }
  if (nodes.length === 0) return { filter: null, errors: {} }
  if (nodes.length === 1) return { filter: nodes[0], errors: {} }
  return { filter: { [mode === 'or' ? 'or' : 'and']: nodes }, errors: {} }
}
