// Field codecs: what a person types in a field of one column type, what the
// Tables add-on takes on the wire and what is drawn back. Pure functions, no
// React and no host: the node tests drive them directly.
//
// Wire values (see the Tables row documentation): text is a string, integer
// a JSON integer or a decimal string, real a finite number, boolean true or
// false, datetime an RFC 3339 string. Datetimes are in UTC.

export const TYPES = Object.freeze(['text', 'integer', 'real', 'boolean', 'datetime'])

const INT64_MIN = -(2n ** 63n)
const INT64_MAX = 2n ** 63n - 1n
const INTEGER = /^[+-]?\d{1,19}$/
const REAL = /^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$/
// A datetime-local value, with or without seconds, or a full RFC 3339 stamp.
const LOCAL = /^(\d{4})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2})(?::(\d{2})(\.\d{1,9})?)?$/
const RFC3339 = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$/
export const MAX_TEXT_BYTES = 1024 * 64

export function isType(type) {
  return TYPES.includes(type)
}

const pad = (n, w = 2) => String(n).padStart(w, '0')

function validDate(y, mo, d, h, mi, s) {
  if (mo < 1 || mo > 12 || d < 1 || h > 23 || mi > 59 || s > 59) return false
  const days = new Date(Date.UTC(y, mo, 0)).getUTCDate()
  return d <= days
}

// parseField turns the editor state of one field into a wire value.
//   state    { text, isNull }
//   column   { type, required }
// Returns { ok: true, value } or { ok: false, code } where code is one of
// required, integer, range, real, boolean, datetime, size.
export function parseField(column, state) {
  const type = column && column.type
  const required = Boolean(column && column.required)
  const raw = state && typeof state.text === 'string' ? state.text : ''
  if (state && state.isNull) {
    return required ? { ok: false, code: 'required' } : { ok: true, value: null }
  }
  switch (type) {
    case 'text': {
      if (raw === '' && required) return { ok: false, code: 'required' }
      if (new TextEncoder().encode(raw).length > MAX_TEXT_BYTES) return { ok: false, code: 'size' }
      return { ok: true, value: raw }
    }
    case 'integer': {
      const text = raw.trim()
      if (text === '') return required ? { ok: false, code: 'required' } : { ok: true, value: null }
      if (!INTEGER.test(text)) return { ok: false, code: 'integer' }
      const big = BigInt(text)
      if (big < INT64_MIN || big > INT64_MAX) return { ok: false, code: 'range' }
      const n = Number(big)
      return { ok: true, value: Number.isSafeInteger(n) ? n : big.toString() }
    }
    case 'real': {
      const text = raw.trim()
      if (text === '') return required ? { ok: false, code: 'required' } : { ok: true, value: null }
      if (!REAL.test(text)) return { ok: false, code: 'real' }
      const n = Number(text)
      return Number.isFinite(n) ? { ok: true, value: n } : { ok: false, code: 'real' }
    }
    case 'boolean': {
      if (raw === '') return required ? { ok: false, code: 'required' } : { ok: true, value: null }
      if (raw === 'true') return { ok: true, value: true }
      if (raw === 'false') return { ok: true, value: false }
      return { ok: false, code: 'boolean' }
    }
    case 'datetime': {
      const text = raw.trim()
      if (text === '') return required ? { ok: false, code: 'required' } : { ok: true, value: null }
      let m = LOCAL.exec(text)
      if (m) {
        const [y, mo, d, h, mi] = m.slice(1, 6).map(Number)
        const s = m[6] === undefined ? 0 : Number(m[6])
        if (!validDate(y, mo, d, h, mi, s)) return { ok: false, code: 'datetime' }
        return { ok: true, value: `${pad(y, 4)}-${pad(mo)}-${pad(d)}T${pad(h)}:${pad(mi)}:${pad(s)}${m[7] || ''}Z` }
      }
      m = RFC3339.exec(text)
      if (m) {
        const [y, mo, d, h, mi, s] = m.slice(1, 7).map(Number)
        if (!validDate(y, mo, d, h, mi, s) || Number.isNaN(Date.parse(text))) return { ok: false, code: 'datetime' }
        return { ok: true, value: text }
      }
      return { ok: false, code: 'datetime' }
    }
    default:
      return { ok: false, code: 'type' }
  }
}

// editorState is the inverse: the state of the editor for a stored value.
export function editorState(column, value) {
  if (value === null || value === undefined) return { text: '', isNull: true }
  switch (column && column.type) {
    case 'boolean':
      return { text: value === true ? 'true' : 'false', isNull: false }
    case 'datetime': {
      const m = RFC3339.exec(String(value))
      if (!m) return { text: String(value), isNull: false }
      // Stored datetimes are UTC; the editor shows the same instant in UTC.
      // The fraction of a second is kept as stored (trailing zeros trimmed):
      // a whole offset never changes it.
      const t = Date.parse(String(value).replace(/\.\d+/, ''))
      if (Number.isNaN(t)) return { text: String(value), isNull: false }
      const d = new Date(t)
      const frac = (m[7] || '').replace(/0+$/, '')
      return { text: `${pad(d.getUTCFullYear(), 4)}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}T${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}:${pad(d.getUTCSeconds())}${frac === '.' ? '' : frac}`, isNull: false }
    }
    default:
      return { text: String(value), isNull: false }
  }
}

// emptyState is the editor state of a new row's field: the default of the
// column when it has one, otherwise blank (null for an optional column).
export function emptyState(column) {
  if (column && column.default !== undefined && column.default !== null) return editorState(column, column.default)
  if (column && column.type === 'boolean') return { text: column.required ? 'false' : '', isNull: false }
  return { text: '', isNull: false }
}

// display is the text of a stored value in a grid cell.
export function display(column, value) {
  if (value === null || value === undefined) return ''
  switch (column && column.type) {
    case 'boolean':
      return value === true ? 'true' : value === false ? 'false' : String(value)
    case 'datetime': {
      const t = Date.parse(String(value))
      if (Number.isNaN(t)) return String(value)
      const d = new Date(t)
      return `${pad(d.getUTCFullYear(), 4)}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())} ${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}:${pad(d.getUTCSeconds())} UTC`
    }
    default:
      return String(value)
  }
}

export function isNumeric(type) {
  return type === 'integer' || type === 'real'
}

// sameValue compares two stored values the way the editor does, so an
// unchanged field is not sent as a change.
export function sameValue(a, b, type) {
  if (a === null || a === undefined) return b === null || b === undefined
  if (b === null || b === undefined) return false
  if (type === 'datetime') {
    const x = instant(a)
    const y = instant(b)
    if (x !== null && y !== null) return x === y
  }
  return String(a) === String(b)
}

// instant is the nanoseconds since the epoch of an RFC 3339 stamp, as a
// BigInt, or null when the text is not one. Two spellings of one instant
// (another offset, trailing zeros) have the same value.
export function instant(value) {
  const text = String(value)
  const m = RFC3339.exec(text)
  if (!m) return null
  const whole = Date.parse(text.replace(/\.\d+/, ''))
  if (Number.isNaN(whole)) return null
  return BigInt(whole / 1000) * 1000000000n + BigInt((m[7] || '').slice(1).padEnd(9, '0'))
}

// planUpdate decides what an edited row sends. A field whose editor text is
// the text the stored value produced is left alone and never re-sent, so
// a value the editor cannot show exactly is not rewritten by editing another
// field. The other fields are parsed, and sent when they hold another value
// (datetimes are compared as instants).
//   columns   [{ name, type, required }]
//   original  { [name]: stored value }
//   state     { [name]: { text, isNull } }
// Returns { set, errors }.
export function planUpdate(columns, original, state) {
  const set = {}
  const errors = {}
  for (const c of columns) {
    const initial = editorState(c, original[c.name])
    const now = state[c.name]
    if (now && now.text === initial.text && Boolean(now.isNull) === initial.isNull) continue
    const r = parseField(c, now)
    if (!r.ok) { errors[c.name] = r.code; continue }
    if (!sameValue(original[c.name], r.value, c.type)) set[c.name] = r.value
  }
  return { set, errors }
}

// browserDatetime is the value to give a datetime-local control: it holds at
// most three digits of a second. The editor state keeps the full text.
export function browserDatetime(text) {
  return String(text).replace(/(\.\d{3})\d+$/, '$1')
}
