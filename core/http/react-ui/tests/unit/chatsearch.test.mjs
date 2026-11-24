import test from 'node:test'
import assert from 'node:assert/strict'
import { highlightParts, searchParams, chatAddress, seqOfSearch, hitRows, filtersActive, noFilters, filtersKey } from '../../src/utils/chatSearch.js'

test('chat search: highlight splits the text at the ranges and keeps all of it', () => {
  const parts = highlightParts('What is the Qdrant port?', [{ start: 12, end: 18 }, { start: 19, end: 23 }])
  assert.deepEqual(parts, [
    { text: 'What is the ', mark: false },
    { text: 'Qdrant', mark: true },
    { text: ' ', mark: false },
    { text: 'port', mark: true },
    { text: '?', mark: false },
  ])
  assert.equal(parts.map((p) => p.text).join(''), 'What is the Qdrant port?')
})

test('chat search: highlight never turns message text into markup', () => {
  const text = '<img src=x onerror=alert(1)> <b>bold</b>'
  const parts = highlightParts(text, [{ start: 29, end: 36 }])
  assert.equal(parts.map((p) => p.text).join(''), text)
  assert.ok(parts.every((p) => typeof p.text === 'string'))
})

test('chat search: highlight drops ranges that are bad and never repeats text', () => {
  const text = 'abcdef'
  assert.deepEqual(highlightParts(text, null), [{ text, mark: false }])
  assert.deepEqual(highlightParts(text, [{ start: 4, end: 2 }, { start: 'a', end: 3 }, null]), [{ text, mark: false }])
  assert.deepEqual(highlightParts(text, [{ start: 2, end: 99 }]).map((p) => p.text), ['ab', 'cdef'])
  const overlap = highlightParts(text, [{ start: 0, end: 3 }, { start: 2, end: 5 }])
  assert.equal(overlap.map((p) => p.text).join(''), text)
  assert.deepEqual(overlap.filter((p) => p.mark).map((p) => p.text), ['abc', 'de'])
  assert.deepEqual(highlightParts('', [{ start: 0, end: 1 }]), [])
})

test('chat search: offsets count UTF-16 units, as the server does', () => {
  const text = '😀 needle'
  assert.deepEqual(highlightParts(text, [{ start: 3, end: 9 }]).filter((p) => p.mark).map((p) => p.text), ['needle'])
})

test('chat search: filters become query parameters', () => {
  const now = new Date('2026-11-04T12:00:00Z')
  assert.deepEqual(searchParams(noFilters, now), {})
  assert.deepEqual(searchParams({ agent: 'helper', origin: 'trigger', range: 'week' }, now), {
    agent: 'helper', origin: 'trigger', since: '2026-10-28T12:00:00.000Z',
  })
  assert.deepEqual(searchParams({ range: 'custom', from: '2026-01-02', to: '2026-01-09' }, now), { since: '2026-01-02', until: '2026-01-09' })
  assert.deepEqual(searchParams({ range: 'custom', from: 'nope', to: '' }, now), {})
  assert.equal(filtersActive(noFilters), false)
  assert.equal(filtersActive({ ...noFilters, origin: 'person' }), true)
  assert.notEqual(filtersKey({ ...noFilters, agent: 'a' }), filtersKey(noFilters))
})

test('chat search: the address of a chat names the message', () => {
  assert.equal(chatAddress('c 1'), '/app/chats/c%201')
  assert.equal(chatAddress('c1', { seq: 14 }), '/app/chats/c1?seq=14')
  assert.equal(chatAddress('c1', { seq: 0 }), '/app/chats/c1')
  assert.equal(chatAddress('c1', { seq: 'x' }), '/app/chats/c1')
  assert.equal(seqOfSearch('?seq=14'), 14)
  assert.equal(seqOfSearch('?seq=-1'), 0)
  assert.equal(seqOfSearch('?seq=1.5'), 0)
  assert.equal(seqOfSearch(''), 0)
})

test('chat search: an answer becomes rows', () => {
  const rows = hitRows({ hits: [
    { chat: { id: 'a', title: 'A' }, seq: 3, role: 'user', at: '2026-11-04T10:00:00Z', snippet: 'hi', highlights: [{ start: 0, end: 2 }], matches: 3 },
    { chat: { id: 'b', title: 'B' }, in_title: true },
    { chat: {} },
  ] }, (c) => ({ id: c.id, name: c.title }))
  assert.equal(rows.length, 2)
  assert.deepEqual(rows[0], { key: 'a:3', chat: { id: 'a', name: 'A' }, seq: 3, role: 'user', at: Date.parse('2026-11-04T10:00:00Z'), snippet: 'hi', highlights: [{ start: 0, end: 2 }], inTitle: false, more: 2 })
  assert.equal(rows[1].inTitle, true)
  assert.equal(rows[1].seq, 0)
  assert.deepEqual(hitRows(null, (c) => c), [])
})
