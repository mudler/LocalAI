import assert from 'node:assert/strict'
import test from 'node:test'

import {
  chipLoops, findLoop, intervalParts, looksLikeInterval, parseLoopCommand, pendingLoops, shortId,
} from '../../src/utils/chatLoops.js'

test('an interval is digits with s, m or h', () => {
  for (const w of ['30s', '5m', '2h', '1h30m']) assert.equal(looksLikeInterval(w), true, w)
  for (const w of ['5', 'check', '5ms', '2d', 'm5', '', undefined]) assert.equal(looksLikeInterval(w), false, String(w))
})

test('/loop with an interval repeats at it, without one it is self-paced', () => {
  assert.deepEqual(parseLoopCommand('5m Check the build.'), { kind: 'start', interval: '5m', prompt: 'Check the build.' })
  assert.deepEqual(parseLoopCommand('  Watch the deploy  '), { kind: 'start', interval: '', prompt: 'Watch the deploy' })
  assert.deepEqual(parseLoopCommand('2h /summary today'), { kind: 'start', interval: '2h', prompt: '/summary today' })
})

test('/loop list and /loop stop, with or without an id', () => {
  assert.deepEqual(parseLoopCommand('list'), { kind: 'list' })
  assert.deepEqual(parseLoopCommand('stop'), { kind: 'stop', id: '' })
  assert.deepEqual(parseLoopCommand('stop 9b2e'), { kind: 'stop', id: '9b2e' })
  // A prompt that only starts with the word is a prompt.
  assert.deepEqual(parseLoopCommand('stop the build when it fails'), { kind: 'start', interval: '', prompt: 'stop the build when it fails' })
  assert.deepEqual(parseLoopCommand('list the open issues'), { kind: 'start', interval: '', prompt: 'list the open issues' })
})

test('/loop alone or with an interval only shows how to use it', () => {
  assert.deepEqual(parseLoopCommand(''), { kind: 'usage' })
  assert.deepEqual(parseLoopCommand('5m'), { kind: 'usage' })
})

test('a loop is found by its id or by a unique start of it', () => {
  const loops = [{ id: '9b2e01' }, { id: '9b3f02' }, { id: 'aa0000' }]
  assert.equal(findLoop(loops, 'aa0000').id, 'aa0000')
  assert.equal(findLoop(loops, '9b2').id, '9b2e01')
  assert.equal(findLoop(loops, '9b'), null, 'two loops start with 9b')
  assert.equal(findLoop(loops, ''), null)
  assert.equal(shortId('9b2e01ffee'), '9b2e01')
})

test('an interval is shown in its largest whole unit', () => {
  assert.deepEqual(intervalParts(300), { unit: 'minutes', count: 5 })
  assert.deepEqual(intervalParts(7200), { unit: 'hours', count: 2 })
  assert.deepEqual(intervalParts(5400), { unit: 'minutes', count: 90 })
  assert.deepEqual(intervalParts(45), { unit: 'seconds', count: 45 })
})

test('the chips show running loops first, a pending loop is a card', () => {
  const loops = [
    { id: 'e', state: 'ended' }, { id: 'p', state: 'pending' }, { id: 'a', state: 'active' }, { id: 'z', state: 'paused' },
  ]
  assert.deepEqual(chipLoops(loops).map(l => l.id), ['a', 'z', 'e'])
  assert.deepEqual(pendingLoops(loops).map(l => l.id), ['p'])
  assert.deepEqual(chipLoops(undefined), [])
})
