import test from 'node:test'
import assert from 'node:assert/strict'
import { createWorkspaceFeed, globToRegExp } from '../../src/utils/workspaceFeed.js'

// A fake clock: timers run when the test advances the time.
function clock() {
  let t = 0
  let next = 1
  const timers = new Map()
  return {
    now: () => t,
    setTimer: (fn, ms) => { const id = next++; timers.set(id, { fn, at: t + ms }); return id },
    clearTimer: (id) => { timers.delete(id) },
    advance(ms) {
      const end = t + ms
      for (;;) {
        let first = null
        for (const [id, x] of timers) if (x.at <= end && (!first || x.at < first[1].at)) first = [id, x]
        if (!first) break
        timers.delete(first[0])
        t = first[1].at
        first[1].fn()
      }
      t = end
    },
    pending: () => timers.size,
  }
}

// A fake EventSource that records every instance.
function fakeES() {
  const made = []
  class FakeEventSource {
    constructor(url) {
      this.url = url
      this.listeners = {}
      this.closed = false
      made.push(this)
    }
    addEventListener(name, fn) { (this.listeners[name] ||= []).push(fn) }
    close() { this.closed = true }
    emit(name, data) { for (const fn of this.listeners[name] || []) fn({ data: JSON.stringify(data) }) }
  }
  return { FakeEventSource, made }
}

function setup(extra = {}) {
  const c = clock()
  const { FakeEventSource, made } = fakeES()
  const target = new EventTarget()
  const doc = new EventTarget()
  doc.visibilityState = 'visible'
  const feed = createWorkspaceFeed({
    url: '/api/workspace/events', EventSourceImpl: FakeEventSource, target, doc,
    setTimer: c.setTimer, clearTimer: c.clearTimer, now: c.now, ...extra,
  })
  return { c, made, feed, target, doc }
}

test('workspace feed: globs match inside a segment, across segments and exactly', () => {
  assert.ok(globToRegExp('home.json').test('home.json'))
  assert.ok(!globToRegExp('home.json').test('homexjson'))
  assert.ok(!globToRegExp('home.json').test('a/home.json'))
  const pages = globToRegExp('pages/*/**')
  assert.ok(pages.test('pages/x/index.html'))
  assert.ok(pages.test('pages/x/data/a.json'))
  assert.ok(pages.test('pages/x'))
  assert.ok(!pages.test('pages'))
  assert.ok(!pages.test('other/x/index.html'))
  assert.ok(globToRegExp('*.json').test('ui.json'))
  assert.ok(!globToRegExp('*.json').test('pages/ui.json'))
  assert.ok(globToRegExp('**/page.json').test('page.json'))
  assert.ok(globToRegExp('**/page.json').test('pages/a/page.json'))
  assert.ok(globToRegExp('/home.json').test('home.json'), 'a leading slash is ignored')
})

test('workspace feed: one stream per tab, opened by the first subscriber and closed after the last', () => {
  const { made, feed } = setup()
  assert.equal(made.length, 0)
  const a = feed.subscribe(['home.json'], () => {})
  const b = feed.subscribe(['pages/*/**'], () => {})
  assert.equal(made.length, 1)
  assert.equal(made[0].url, '/api/workspace/events')
  a()
  assert.equal(made[0].closed, false)
  b()
  assert.equal(made[0].closed, true)
  feed.subscribe(['x'], () => {})
  assert.equal(made.length, 2)
})

test('workspace feed: a burst of matching changes becomes one debounced call', () => {
  const { c, made, feed } = setup()
  const calls = []
  feed.subscribe(['pages/*/**'], (ev) => calls.push(ev))
  const es = made[0]
  es.emit('change', { revision: 3, paths: ['pages/a/index.html'] })
  c.advance(100)
  es.emit('change', { revision: 4, paths: ['pages/a/data.json', 'home.json'] })
  c.advance(100)
  assert.equal(calls.length, 0, 'the trailing wait restarts with each change')
  es.emit('change', { revision: 5, paths: ['pages/b/index.html'] })
  c.advance(149)
  assert.equal(calls.length, 0)
  c.advance(1)
  assert.deepEqual(calls, [{ paths: ['pages/a/data.json', 'pages/a/index.html', 'pages/b/index.html'], revision: 5 }])
})

test('workspace feed: a subscriber hears only the paths it asked for', () => {
  const { c, made, feed } = setup()
  const home = []
  const pages = []
  feed.subscribe(['home.json'], (ev) => home.push(ev))
  feed.subscribe(['pages/*/**'], (ev) => pages.push(ev))
  made[0].emit('change', { revision: 7, paths: ['notes/x.md', 'home.json'] })
  c.advance(200)
  assert.deepEqual(home, [{ paths: ['home.json'], revision: 7 }])
  assert.deepEqual(pages, [])
})

test('workspace feed: a resync calls every subscriber once', () => {
  const { c, made, feed } = setup()
  const a = []
  const b = []
  feed.subscribe(['home.json'], (ev) => a.push(ev))
  feed.subscribe(['pages/*/**'], (ev) => b.push(ev))
  made[0].emit('resync', { revision: 9 })
  made[0].emit('resync', { revision: 10 })
  c.advance(200)
  assert.deepEqual(a, [{ paths: [], revision: 10, resync: true }])
  assert.deepEqual(b, [{ paths: [], revision: 10, resync: true }])
})

test('workspace feed: an own save does not refresh twice', () => {
  const { c, made, feed } = setup()
  const calls = []
  feed.subscribe(['home.json', 'ui.json'], (ev) => calls.push(ev))
  feed.invalidate('/home.json', 12)
  made[0].emit('change', { revision: 12, paths: ['home.json'] })
  c.advance(200)
  assert.deepEqual(calls, [], 'the echo of the save at revision 12 is dropped')
  made[0].emit('change', { revision: 13, paths: ['home.json'] })
  c.advance(200)
  assert.deepEqual(calls, [{ paths: ['home.json'], revision: 13 }], 'a later change of the same file is a change')

  feed.invalidate('ui.json')
  made[0].emit('change', { revision: 14, paths: ['ui.json'] })
  c.advance(200)
  assert.equal(calls.length, 1, 'without a revision the next echo is dropped')
  made[0].emit('change', { revision: 15, paths: ['ui.json'] })
  c.advance(200)
  assert.equal(calls.length, 2, 'and only that one')
})

test('workspace feed: a dropped stream reconnects with backoff and resumes after the last revision', () => {
  const { c, made, feed } = setup()
  const calls = []
  feed.subscribe(['home.json'], (ev) => calls.push(ev))
  made[0].emit('change', { revision: 20, paths: ['home.json'] })
  c.advance(200)
  made[0].onerror()
  assert.equal(made[0].closed, true)
  c.advance(999)
  assert.equal(made.length, 1)
  c.advance(1)
  assert.equal(made.length, 2)
  assert.equal(made[1].url, '/api/workspace/events?after=20')
  made[1].onerror()
  c.advance(1999)
  assert.equal(made.length, 2, 'the second wait is twice as long')
  c.advance(1)
  assert.equal(made.length, 3)
  made[2].onopen()
  c.advance(200)
  assert.equal(calls.length, 1, 'a resumed stream needs no resync: the server sends what was missed')
})

test('workspace feed: a reconnect that cannot resume calls every subscriber', () => {
  const { c, made, feed } = setup()
  const calls = []
  feed.subscribe(['home.json'], (ev) => calls.push(ev))
  made[0].onopen()
  made[0].onerror()
  c.advance(1000)
  assert.equal(made[1].url, '/api/workspace/events')
  made[1].onopen()
  c.advance(200)
  assert.deepEqual(calls, [{ paths: [], revision: null, resync: true }])
})

test('workspace feed: focus, a visible tab and the end of a turn call every subscriber once', () => {
  const { c, feed, target, doc } = setup()
  const calls = []
  feed.subscribe(['home.json'], (ev) => calls.push(ev))
  c.advance(5000)
  target.dispatchEvent(new Event('focus'))
  doc.dispatchEvent(new Event('visibilitychange'))
  c.advance(200)
  assert.equal(calls.length, 1, 'focus and visibility together are one call')
  feed.turnEnded()
  c.advance(200)
  assert.equal(calls.length, 2)
  assert.equal(calls[1].resync, true)
})

test('workspace feed: nothing is called and nothing listens after the last unsubscribe', () => {
  const { c, made, feed, target } = setup()
  const calls = []
  const off = feed.subscribe(['home.json'], (ev) => calls.push(ev))
  made[0].emit('change', { revision: 2, paths: ['home.json'] })
  off()
  c.advance(5000)
  target.dispatchEvent(new Event('focus'))
  c.advance(200)
  assert.deepEqual(calls, [])
  assert.equal(c.pending(), 0)
  assert.equal(feed.size, 0)
})

test('workspace feed: without EventSource the fallbacks still work', () => {
  const c = clock()
  const target = new EventTarget()
  const feed = createWorkspaceFeed({ EventSourceImpl: null, target, doc: null, setTimer: c.setTimer, clearTimer: c.clearTimer, now: c.now })
  const calls = []
  feed.subscribe(['home.json'], (ev) => calls.push(ev))
  c.advance(5000)
  target.dispatchEvent(new Event('focus'))
  c.advance(200)
  assert.equal(calls.length, 1)
  assert.equal(feed.connected, false)
})

test('workspace feed: a steady stream of changes still calls within a second', () => {
  const { c, made, feed } = setup()
  const calls = []
  feed.subscribe(['log.txt'], (ev) => calls.push(ev))
  for (let i = 1; i <= 12; i++) {
    made[0].emit('change', { revision: i, paths: ['log.txt'] })
    c.advance(100)
  }
  assert.equal(calls.length, 1)
  assert.equal(calls[0].revision, 10)
})
