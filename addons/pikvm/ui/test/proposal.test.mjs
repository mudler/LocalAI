import test from 'node:test'
import assert from 'node:assert/strict'
import { connectionsFor, proposalView } from '../src/proposal.js'

test('pikvm proposal: a power action shows the power state read in the preview and the effect', () => {
  const v = proposalView('power-action', { device: 'https://a.example#sha256=ab', command: 'off' },
    { action: 'off', hard: false, powerNow: 'on', effect: 'Press the power button briefly', device: 'https://a.example' })
  assert.equal(v.kind, 'power')
  assert.equal(v.action, 'off')
  assert.equal(v.powerNow, 'on')
  assert.equal(v.hard, false)
  assert.equal(v.effect, 'Press the power button briefly')
})

test('pikvm proposal: a hard action is marked', () => {
  assert.equal(proposalView('power-action', {}, { action: 'off_hard', hard: true, powerNow: 'on' }).hard, true)
})

test('pikvm proposal: typed text is shown exactly, from the preview or the stored arguments', () => {
  const v = proposalView('type-text', { device: 'x', text: 'ls -l\nwhoami' }, { text: 'ls -l\nwhoami', chars: 12, lines: 2 })
  assert.equal(v.kind, 'text')
  assert.equal(v.text, 'ls -l\nwhoami')
  assert.equal(v.chars, 12)
  assert.equal(v.lines, 2)
  const w = proposalView('type-text', { text: 'héllo' }, null)
  assert.equal(w.text, 'héllo')
  assert.equal(w.chars, 5)
})

test('pikvm proposal: keys are listed in order', () => {
  const v = proposalView('send-key', { keys: ['escape', 'f2'] }, { keys: ['escape', 'f2'] })
  assert.equal(v.kind, 'keys')
  assert.deepEqual(v.keys, ['escape', 'f2'])
  assert.deepEqual(proposalView('send-key', { keys: ['a', 3] }, null).keys, ['a'])
})

test('pikvm proposal: nothing but strings is drawn from a hostile preview', () => {
  const v = proposalView(5, { device: 3 }, { action: {}, powerNow: 'maybe', text: 4, keys: 'x', effect: [] })
  assert.equal(v.tool, '')
  assert.equal(v.kind, '')
  assert.equal(v.action, '')
  assert.equal(v.powerNow, '')
  assert.equal(v.text, '')
  assert.deepEqual(v.keys, [])
})

test('pikvm proposal: the connections that list a device are found by name, ignoring the pin', () => {
  const list = [
    { kind: 'pikvm', name: 'Lab', resources: ['https://A.example#sha256=ab'] },
    { kind: 'pikvm', name: 'Other', resources: ['https://b.example'] },
    { kind: 'github', name: 'Nope', resources: ['https://a.example'] },
  ]
  assert.deepEqual(connectionsFor(list, 'https://a.example'), ['Lab'])
  assert.deepEqual(connectionsFor(list, 'https://a.example#sha256=ab'), ['Lab'])
  assert.deepEqual(connectionsFor(list, ''), [])
  assert.deepEqual(connectionsFor(null, 'x'), [])
})
