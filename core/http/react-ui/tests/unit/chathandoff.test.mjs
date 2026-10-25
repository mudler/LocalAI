import test from 'node:test'
import assert from 'node:assert/strict'
import { handOffToChat, takeChatHandoff, LEGACY_HANDOFF_KEY } from '../../src/utils/chatHandoff.js'

// A localStorage stand-in. `full` makes every write throw the way a browser
// does at its quota; `broken` makes every access throw (storage disabled).
function fakeStorage({ full = false, broken = false } = {}) {
  const items = new Map()
  const guard = () => { if (broken) throw new DOMException('denied', 'SecurityError') }
  return {
    items,
    getItem: (k) => { guard(); return items.has(k) ? items.get(k) : null },
    setItem: (k, v) => {
      guard()
      if (full) throw new DOMException('The quota has been exceeded.', 'QuotaExceededError')
      items.set(k, String(v))
    },
    removeItem: (k) => { guard(); items.delete(k) },
  }
}

const PHOTO = { name: 'IMG_9456.HEIC', type: 'image/heic', base64: 'A'.repeat(1_680_000) }

test('chat hand-off: a message with a large picture reaches Chat although the storage is full', () => {
  globalThis.localStorage = fakeStorage({ full: true })
  const data = { message: 'Reifendruck?', model: 'm', files: [PHOTO], newChat: true }
  assert.doesNotThrow(() => handOffToChat(data))
  assert.deepEqual(takeChatHandoff(), data)
  // Nothing went into the storage.
  assert.equal(globalThis.localStorage.items.size, 0)
})

test('chat hand-off: it is delivered once', () => {
  globalThis.localStorage = fakeStorage()
  handOffToChat({ message: 'once', newChat: true })
  assert.equal(takeChatHandoff().message, 'once')
  assert.equal(takeChatHandoff(), null)
})

test('chat hand-off: an entry an older page left in localStorage still opens the chat, once', () => {
  globalThis.localStorage = fakeStorage()
  globalThis.localStorage.setItem(LEGACY_HANDOFF_KEY, JSON.stringify({ message: 'legacy', newChat: true }))
  assert.equal(takeChatHandoff().message, 'legacy')
  assert.equal(globalThis.localStorage.getItem(LEGACY_HANDOFF_KEY), null)
  assert.equal(takeChatHandoff(), null)
})

test('chat hand-off: the in-memory one wins and a stale localStorage entry is dropped', () => {
  globalThis.localStorage = fakeStorage()
  globalThis.localStorage.setItem(LEGACY_HANDOFF_KEY, JSON.stringify({ message: 'stale' }))
  handOffToChat({ message: 'fresh' })
  assert.equal(takeChatHandoff().message, 'fresh')
  assert.equal(globalThis.localStorage.getItem(LEGACY_HANDOFF_KEY), null)
  assert.equal(takeChatHandoff(), null)
})

test('chat hand-off: an unreadable storage or a corrupt entry neither throws nor blocks the in-memory one', () => {
  globalThis.localStorage = fakeStorage({ broken: true })
  handOffToChat({ message: 'works' })
  assert.equal(takeChatHandoff().message, 'works')
  assert.equal(takeChatHandoff(), null)

  globalThis.localStorage = fakeStorage()
  globalThis.localStorage.setItem(LEGACY_HANDOFF_KEY, '{not json')
  assert.equal(takeChatHandoff(), null)
  assert.equal(globalThis.localStorage.getItem(LEGACY_HANDOFF_KEY), null)
})
