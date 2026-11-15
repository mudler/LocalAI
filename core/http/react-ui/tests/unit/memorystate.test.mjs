import test from 'node:test'
import assert from 'node:assert/strict'
import { memoryFix, memoryOff } from '../../src/utils/memoryState.js'

test('memory state: memory that works is not off', () => {
  assert.equal(memoryOff({ on: true }, { group: 'memory', off: false }), null)
  assert.equal(memoryOff(null, undefined), null)
})

test('memory state: a chat that switched the group off is a private chat, whatever else says', () => {
  assert.deepEqual(memoryOff({ on: true }, { group: 'memory', off: true }), { kind: 'chat', by: 'chat' })
  assert.deepEqual(memoryOff({ on: false, code: 'memory_off_user', blocked_by: 'user' }, { group: 'memory', off: true }), { kind: 'chat', by: 'chat' })
})

test('memory state: the level the server names is the one shown', () => {
  assert.equal(memoryOff({ on: false, code: 'memory_off_user', blocked_by: 'user' }, undefined).kind, 'user')
  assert.equal(memoryOff({ on: false, code: 'memory_off_agent', blocked_by: 'agent' }, undefined).kind, 'agent')
  assert.equal(memoryOff({ on: false, code: 'memory_off_files', blocked_by: 'server' }, undefined).kind, 'files')
  assert.equal(memoryOff({ on: false, code: 'memory_off_chat', blocked_by: 'chat' }, undefined).kind, 'chat')
  assert.equal(memoryOff({ on: false, code: 'something_new' }, undefined).kind, 'user', 'an unknown code reads as the safe, widest level')
})

test('memory state: only the level above the chat has a link', () => {
  assert.deepEqual(memoryFix({ kind: 'user' }), { to: '/app/account?tab=memory', key: 'fixUser' })
  assert.equal(memoryFix({ kind: 'chat' }), null)
  assert.equal(memoryFix(null), null)
})

test('memory state: a provider that cannot serve is its own level and links to the settings', () => {
  const off = memoryOff({ on: false, code: 'memory_provider_unavailable', blocked_by: 'provider' }, undefined)
  assert.deepEqual(off, { kind: 'provider', by: 'provider' })
  assert.deepEqual(memoryFix(off), { to: '/app/account?tab=memory', key: 'fixUser' })
})
