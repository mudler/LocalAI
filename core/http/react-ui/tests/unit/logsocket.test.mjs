import test from 'node:test'
import assert from 'node:assert/strict'
import { shouldReconnect } from '../../src/utils/logSocket.js'

const ws = {}

test('log socket: a drop while mounted reconnects', () => {
  assert.equal(shouldReconnect({ closed: false, current: ws, ws }), true)
})

test('log socket: a repeated drop keeps reconnecting while mounted', () => {
  const second = {}
  assert.equal(shouldReconnect({ closed: false, current: second, ws: second }), true)
})

test('log socket: the close caused by unmount does not reconnect', () => {
  assert.equal(shouldReconnect({ closed: true, current: null, ws }), false)
})

test('log socket: a superseded socket closing late does not reconnect', () => {
  assert.equal(shouldReconnect({ closed: false, current: {}, ws }), false)
})

test('log socket: a close after cleanup stays closed even if the same socket is held', () => {
  assert.equal(shouldReconnect({ closed: true, current: ws, ws }), false)
})
