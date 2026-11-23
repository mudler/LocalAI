import test from 'node:test'
import assert from 'node:assert/strict'
import { createTokenBuffer } from '../../src/utils/tokenBuffer.js'

test('token buffer: pieces of one frame flush once, in order', () => {
  const calls = []
  const rafCalls = []
  const timerCalls = []

  const { push } = createTokenBuffer({
    onFlush: calls.push.bind(calls),
    raf: (cb) => rafCalls.push(cb),
    cancelRaf: () => {},
    setTimer: (cb) => timerCalls.push(cb),
    clearTimer: () => {},
  })

  push('text', 'a')
  push('text', 'b')

  // Fire the rAF callback manually.
  rafCalls[0]()

  assert.equal(calls.length, 1)
  assert.deepEqual(calls[0], { text: 'ab' })
})

test('token buffer: the interval flushes when animation frames are throttled', () => {
  const calls = []
  const rafCalls = []
  const timerCalls = []

  const { push } = createTokenBuffer({
    onFlush: calls.push.bind(calls),
    raf: (cb) => rafCalls.push(cb),
    cancelRaf: () => {},
    setTimer: (cb) => timerCalls.push(cb),
    clearTimer: () => {},
  })

  push('text', 'hello')

  // Fire the timer callback instead of the frame.
  timerCalls[0]()

  assert.equal(calls.length, 1)
  assert.deepEqual(calls[0], { text: 'hello' })
})

test('token buffer: an explicit flush cancels the pending frame and the timer', () => {
  const calls = []
  const rafCalls = []
  const timerCalls = []

  const { push, flush } = createTokenBuffer({
    onFlush: calls.push.bind(calls),
    raf: (cb) => rafCalls.push(cb),
    cancelRaf: () => {},
    setTimer: (cb) => timerCalls.push(cb),
    clearTimer: () => {},
  })

  push('text', 'x')

  // Explicit flush.
  flush()
  assert.equal(calls.length, 1)
  assert.deepEqual(calls[0], { text: 'x' })

  // Now fire the saved callbacks — should produce no further calls.
  rafCalls[0]()
  timerCalls[0]()

  assert.equal(calls.length, 1, 'no further calls after callbacks fire')
})

test('token buffer: cancel drops the pieces', () => {
  const calls = []
  const rafCalls = []
  const timerCalls = []

  const { push, cancel, isEmpty } = createTokenBuffer({
    onFlush: calls.push.bind(calls),
    raf: (cb) => rafCalls.push(cb),
    cancelRaf: () => {},
    setTimer: (cb) => timerCalls.push(cb),
    clearTimer: () => {},
  })

  push('text', 'y')
  cancel()

  // Fire both callbacks — should not call onFlush.
  rafCalls[0]()
  timerCalls[0]()

  assert.equal(calls.length, 0)
  assert.ok(isEmpty())
})

test('token buffer: two fields keep their own text', () => {
  const calls = []
  const rafCalls = []
  const timerCalls = []

  const { push, flush } = createTokenBuffer({
    onFlush: calls.push.bind(calls),
    raf: (cb) => rafCalls.push(cb),
    cancelRaf: () => {},
    setTimer: (cb) => timerCalls.push(cb),
    clearTimer: () => {},
  })

  push('text', 'a')
  push('reasoning', 'b')

  flush()
  assert.equal(calls.length, 1)
  assert.deepEqual(calls[0], { text: 'a', reasoning: 'b' })

  // Fire callbacks again — should produce zero calls.
  rafCalls[0]()
  timerCalls[0]()

  assert.equal(calls.length, 1, 'second flush produces no calls')
})

test('token buffer: a frame without a cancel handle still flushes again', () => {
  const calls = []
  const rafCalls = []
  const timerCalls = []

  const { push } = createTokenBuffer({
    onFlush: calls.push.bind(calls),
    raf: (cb) => rafCalls.push(cb),
    setTimer: (cb) => timerCalls.push(cb),
    clearTimer: () => {},
  })

  // First push + frame callback.
  push('text', 'a')
  rafCalls[0]()

  // Second push + second frame callback.
  push('text', 'b')
  rafCalls[1]()

  assert.equal(calls.length, 2)
  assert.deepEqual(calls[0], { text: 'a' })
  assert.deepEqual(calls[1], { text: 'b' })
})
