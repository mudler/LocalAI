import test from 'node:test'
import assert from 'node:assert/strict'
import { parseField, editorState, emptyState, display, sameValue, planUpdate, instant, browserDatetime } from '../src/lib/codecs.js'

const col = (type, over = {}) => ({ name: 'C', type, required: false, ...over })

test('codecs: text keeps what was typed, bounds it, and null is explicit', () => {
  assert.deepEqual(parseField(col('text'), { text: ' a b ', isNull: false }), { ok: true, value: ' a b ' })
  assert.deepEqual(parseField(col('text'), { text: '', isNull: false }), { ok: true, value: '' })
  assert.deepEqual(parseField(col('text'), { text: '', isNull: true }), { ok: true, value: null })
  assert.equal(parseField(col('text', { required: true }), { text: '', isNull: false }).code, 'required')
  assert.equal(parseField(col('text', { required: true }), { text: 'x', isNull: true }).code, 'required')
  assert.equal(parseField(col('text'), { text: 'x'.repeat(70000), isNull: false }).code, 'size')
})

test('codecs: integers are exact up to 64 bits and never silently rounded', () => {
  assert.deepEqual(parseField(col('integer'), { text: ' 42 ', isNull: false }), { ok: true, value: 42 })
  assert.deepEqual(parseField(col('integer'), { text: '-7', isNull: false }), { ok: true, value: -7 })
  assert.deepEqual(parseField(col('integer'), { text: '9007199254740993', isNull: false }), { ok: true, value: '9007199254740993' })
  assert.deepEqual(parseField(col('integer'), { text: '9223372036854775807', isNull: false }), { ok: true, value: '9223372036854775807' })
  assert.equal(parseField(col('integer'), { text: '9223372036854775808', isNull: false }).code, 'range')
  assert.equal(parseField(col('integer'), { text: '1.5', isNull: false }).code, 'integer')
  assert.equal(parseField(col('integer'), { text: '1e3', isNull: false }).code, 'integer')
  assert.equal(parseField(col('integer'), { text: '0x10', isNull: false }).code, 'integer')
  assert.deepEqual(parseField(col('integer'), { text: '', isNull: false }), { ok: true, value: null })
  assert.equal(parseField(col('integer', { required: true }), { text: '', isNull: false }).code, 'required')
})

test('codecs: reals are finite numbers', () => {
  assert.deepEqual(parseField(col('real'), { text: '3.25', isNull: false }), { ok: true, value: 3.25 })
  assert.deepEqual(parseField(col('real'), { text: '-1e-3', isNull: false }), { ok: true, value: -0.001 })
  assert.equal(parseField(col('real'), { text: 'NaN', isNull: false }).code, 'real')
  assert.equal(parseField(col('real'), { text: 'Infinity', isNull: false }).code, 'real')
  assert.equal(parseField(col('real'), { text: '1e999', isNull: false }).code, 'real')
  assert.equal(parseField(col('real'), { text: '1,5', isNull: false }).code, 'real')
})

test('codecs: booleans are true, false or empty for an optional column', () => {
  assert.deepEqual(parseField(col('boolean'), { text: 'true', isNull: false }), { ok: true, value: true })
  assert.deepEqual(parseField(col('boolean'), { text: 'false', isNull: false }), { ok: true, value: false })
  assert.deepEqual(parseField(col('boolean'), { text: '', isNull: false }), { ok: true, value: null })
  assert.equal(parseField(col('boolean', { required: true }), { text: '', isNull: false }).code, 'required')
  assert.equal(parseField(col('boolean'), { text: 'yes', isNull: false }).code, 'boolean')
})

test('codecs: datetimes are read as UTC and sent as RFC 3339', () => {
  assert.deepEqual(parseField(col('datetime'), { text: '2026-10-14T09:30', isNull: false }), { ok: true, value: '2026-10-14T09:30:00Z' })
  assert.deepEqual(parseField(col('datetime'), { text: '2026-10-14T09:30:15', isNull: false }), { ok: true, value: '2026-10-14T09:30:15Z' })
  assert.deepEqual(parseField(col('datetime'), { text: '2026-10-14T09:30:15.123456789Z', isNull: false }), { ok: true, value: '2026-10-14T09:30:15.123456789Z' })
  assert.deepEqual(parseField(col('datetime'), { text: '2026-10-14T11:30:00+02:00', isNull: false }), { ok: true, value: '2026-10-14T11:30:00+02:00' })
  assert.equal(parseField(col('datetime'), { text: '2026-02-30T00:00', isNull: false }).code, 'datetime')
  assert.equal(parseField(col('datetime'), { text: '2026-13-01T00:00', isNull: false }).code, 'datetime')
  assert.equal(parseField(col('datetime'), { text: 'tomorrow', isNull: false }).code, 'datetime')
  assert.equal(parseField(col('datetime'), { text: '2026-10-14', isNull: false }).code, 'datetime')
  assert.equal(parseField(col('datetime'), { text: '2026-10-14T24:00', isNull: false }).code, 'datetime')
})

test('codecs: an unknown column type is refused', () => {
  assert.equal(parseField({ type: 'blob', required: false }, { text: 'x', isNull: false }).code, 'type')
})

test('codecs: the editor round-trips stored values', () => {
  const dt = col('datetime')
  assert.deepEqual(editorState(dt, '2026-10-14T09:30:15.000000000Z'), { text: '2026-10-14T09:30:15', isNull: false })
  assert.deepEqual(editorState(col('boolean'), true), { text: 'true', isNull: false })
  assert.deepEqual(editorState(col('text'), null), { text: '', isNull: true })
  assert.deepEqual(editorState(col('integer'), '9007199254740993'), { text: '9007199254740993', isNull: false })
  for (const [c, v] of [[col('integer'), 17], [col('real'), 2.5], [col('boolean'), false], [col('text'), 'x y']]) {
    assert.deepEqual(parseField(c, editorState(c, v)), { ok: true, value: v })
  }
  assert.equal(emptyState(col('text', { default: 'hi' })).text, 'hi')
  assert.deepEqual(emptyState(col('boolean', { required: true })), { text: 'false', isNull: false })
})

test('codecs: grid text and the unchanged-field test', () => {
  assert.equal(display(col('datetime'), '2026-10-14T11:30:00+02:00'), '2026-10-14 09:30:00 UTC')
  assert.equal(display(col('boolean'), true), 'true')
  assert.equal(display(col('text'), null), '')
  assert.equal(sameValue(1, '1'), true)
  assert.equal(sameValue(null, undefined), true)
  assert.equal(sameValue(null, ''), false)
  assert.equal(sameValue('a', 'b'), false)
})

test('codecs: a datetime with a fraction of a second is not rewritten by editing another field', () => {
  const columns = [col('text', { name: 'Name' }), col('datetime', { name: 'Due' })]
  const stored = '2026-01-01T00:00:00.5Z'
  assert.deepEqual(editorState(columns[1], stored), { text: '2026-01-01T00:00:00.5', isNull: false })
  assert.deepEqual(editorState(columns[1], '2026-01-01T00:00:00.123456789Z'), { text: '2026-01-01T00:00:00.123456789', isNull: false })
  assert.deepEqual(editorState(columns[1], '2026-01-01T00:00:00.000000000Z'), { text: '2026-01-01T00:00:00', isNull: false })
  const original = { Name: 'a', Due: stored }
  const state = { Name: { text: 'b', isNull: false }, Due: editorState(columns[1], stored) }
  assert.deepEqual(planUpdate(columns, original, state), { set: { Name: 'b' }, errors: {} })
  // Nothing edited is nothing to send.
  assert.deepEqual(planUpdate(columns, original, { Name: editorState(columns[0], 'a'), Due: editorState(columns[1], stored) }), { set: {}, errors: {} })
  // The edited datetime is sent, with its fraction.
  const edited = { ...state, Due: { text: '2027-02-03T04:05:06.25', isNull: false } }
  assert.deepEqual(planUpdate(columns, original, edited).set, { Name: 'b', Due: '2027-02-03T04:05:06.25Z' })
  // The browser control holds three digits at most; the state keeps all.
  assert.equal(browserDatetime('2026-01-01T00:00:00.123456789'), '2026-01-01T00:00:00.123')
  assert.equal(browserDatetime('2026-01-01T00:00:00'), '2026-01-01T00:00:00')
})

test('codecs: datetimes are compared as instants and offsets are normalised', () => {
  assert.equal(sameValue('2026-01-01T00:00:00.5Z', '2026-01-01T01:00:00.500+01:00', 'datetime'), true)
  assert.equal(sameValue('2026-01-01T00:00:00Z', '2026-01-01T00:00:00.000000001Z', 'datetime'), false)
  assert.equal(sameValue('2026-01-01T00:00:00.5Z', '2026-01-01T00:00:00Z', 'datetime'), false)
  assert.equal(instant('2026-01-01T02:00:00.25+02:00'), instant('2026-01-01T00:00:00.250000000Z'))
  assert.equal(instant('nope'), null)
  const columns = [col('datetime', { name: 'Due' })]
  // The same instant typed with an offset is no change; another instant is.
  const original = { Due: '2026-01-01T00:00:00.5Z' }
  assert.deepEqual(planUpdate(columns, original, { Due: { text: '2026-01-01T01:00:00.5+01:00', isNull: false } }).set, {})
  assert.deepEqual(planUpdate(columns, original, { Due: { text: '2026-01-01T01:00:01+01:00', isNull: false } }).set, { Due: '2026-01-01T01:00:01+01:00' })
  // Clearing an optional datetime is a change to null.
  assert.deepEqual(planUpdate(columns, original, { Due: { text: '', isNull: true } }).set, { Due: null })
})
