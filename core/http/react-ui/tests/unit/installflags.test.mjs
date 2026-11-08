import test from 'node:test'
import assert from 'node:assert/strict'
import { addFlag, removeFlag, FALLBACK_MS } from '../../src/utils/installFlags.js'

test('install flags: happy path sets the flag and clears it', () => {
  const set = addFlag(new Map(), 'm', 100)
  assert.equal(set.get('m'), 100)
  assert.equal(removeFlag(set, 'm').has('m'), false)
})

test('install flags: a rejected request clears the flag it set', () => {
  const set = addFlag(new Map(), 'm', 100)
  assert.equal(removeFlag(set, 'm', 100).size, 0)
})

test('install flags: a repeated failure leaves nothing behind', () => {
  let flags = new Map()
  for (const now of [1, 2, 3]) {
    flags = addFlag(flags, 'm', now)
    flags = removeFlag(flags, 'm', now)
  }
  assert.equal(flags.size, 0)
})

test('install flags: a double click keeps the first flag and the same map', () => {
  const once = addFlag(new Map(), 'm', 100)
  assert.equal(addFlag(once, 'm', 200), once)
  assert.equal(once.get('m'), 100)
})

test('install flags: a stale timer does not clear a newer attempt', () => {
  const second = addFlag(removeFlag(addFlag(new Map(), 'm', 100), 'm', 100), 'm', 7000)
  assert.equal(removeFlag(second, 'm', 100), second)
  assert.equal(removeFlag(second, 'm', 7000).size, 0)
})

test('install flags: events after the terminal state change nothing', () => {
  const empty = new Map()
  assert.equal(removeFlag(empty, 'm'), empty)
  assert.equal(removeFlag(empty, 'm', 5), empty)
})

test('install flags: the fallback is five seconds', () => {
  assert.equal(FALLBACK_MS, 5000)
})
