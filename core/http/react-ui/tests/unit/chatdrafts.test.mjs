import test from 'node:test'
import assert from 'node:assert/strict'
import {
  draftsKey, readDrafts, writeDraft, clearDraft, MAX_DRAFTS, MAX_DRAFT_CHARS,
} from '../../src/utils/chatDrafts.js'

// A Storage with the methods the helpers use, kept in a Map.
function memoryStorage(seed = {}) {
  const m = new Map(Object.entries(seed))
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => { m.set(k, String(v)) },
    removeItem: (k) => { m.delete(k) },
    dump: () => Object.fromEntries(m),
  }
}

const throwing = {
  getItem() { throw new Error('denied') },
  setItem() { throw new Error('denied') },
  removeItem() { throw new Error('denied') },
}

test('chat drafts: the storage key names the account, so another account does not see the drafts', () => {
  assert.equal(draftsKey(''), 'localai_chat_drafts')
  assert.equal(draftsKey('u1'), 'localai_chat_drafts:u1')
  assert.notEqual(draftsKey('u1'), draftsKey('u2'))
})

test('chat drafts: a draft is written under its chat and read back', () => {
  const st = memoryStorage()
  const key = draftsKey('u1')
  writeDraft(st, key, 'c1', 'half a thought', 1000)
  writeDraft(st, key, 'c2', 'another one', 2000)
  assert.deepEqual(readDrafts(st, key), {
    c1: { text: 'half a thought', at: 1000 },
    c2: { text: 'another one', at: 2000 },
  })
})

test('chat drafts: an empty draft removes the entry, and the last one removes the key', () => {
  const st = memoryStorage()
  const key = draftsKey('')
  writeDraft(st, key, 'c1', 'x', 1)
  writeDraft(st, key, 'c2', 'y', 2)
  writeDraft(st, key, 'c1', '   ', 3)
  assert.deepEqual(Object.keys(readDrafts(st, key)), ['c2'])
  clearDraft(st, key, 'c2')
  assert.equal(st.getItem(key), null)
})

test('chat drafts: a chat without an id (the new chat) is never written', () => {
  const st = memoryStorage()
  writeDraft(st, draftsKey(''), '', 'not for storage', 1)
  writeDraft(st, draftsKey(''), null, 'not for storage', 1)
  assert.deepEqual(st.dump(), {})
})

test('chat drafts: a long draft is cut to the size cap', () => {
  const st = memoryStorage()
  const key = draftsKey('')
  writeDraft(st, key, 'c1', 'a'.repeat(MAX_DRAFT_CHARS + 50), 1)
  assert.equal(readDrafts(st, key).c1.text.length, MAX_DRAFT_CHARS)
})

test('chat drafts: only the newest drafts are kept when there are too many', () => {
  const st = memoryStorage()
  const key = draftsKey('')
  for (let i = 0; i < MAX_DRAFTS + 5; i++) writeDraft(st, key, `c${i}`, `draft ${i}`, i)
  const kept = readDrafts(st, key)
  assert.equal(Object.keys(kept).length, MAX_DRAFTS)
  assert.equal(kept.c0, undefined)
  assert.equal(kept.c4, undefined)
  assert.equal(kept[`c${MAX_DRAFTS + 4}`].text, `draft ${MAX_DRAFTS + 4}`)
})

test('chat drafts: a write keeps the drafts another tab wrote meanwhile', () => {
  const st = memoryStorage()
  const key = draftsKey('')
  writeDraft(st, key, 'c1', 'from this tab', 1)
  // Another tab writes c2 straight to the storage.
  st.setItem(key, JSON.stringify({ ...JSON.parse(st.getItem(key)), c2: { text: 'from the other tab', at: 2 } }))
  writeDraft(st, key, 'c1', 'from this tab, longer', 3)
  assert.deepEqual(Object.keys(readDrafts(st, key)).sort(), ['c1', 'c2'])
})

test('chat drafts: broken, foreign or missing storage reads as no drafts and writes do not throw', () => {
  const key = draftsKey('')
  assert.deepEqual(readDrafts(memoryStorage({ [key]: '{not json' }), key), {})
  assert.deepEqual(readDrafts(memoryStorage({ [key]: '[1,2]' }), key), {})
  assert.deepEqual(readDrafts(memoryStorage({ [key]: JSON.stringify({ c1: { text: 5 }, c2: 'x', c3: { text: 'ok', at: 1 } }) }), key), { c3: { text: 'ok', at: 1 } })
  assert.deepEqual(readDrafts(null, key), {})
  assert.deepEqual(readDrafts(throwing, key), {})
  assert.doesNotThrow(() => writeDraft(throwing, key, 'c1', 'x', 1))
  assert.doesNotThrow(() => clearDraft(throwing, key, 'c1'))
  assert.doesNotThrow(() => writeDraft(null, key, 'c1', 'x', 1))
})
