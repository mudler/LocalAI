import test from 'node:test'
import assert from 'node:assert/strict'
import { createUploadQueue, describeError, summarize, formatDuration } from '../../src/utils/uploadQueue.js'

// A sender the test drives by hand: each send waits until the test resolves,
// rejects or reports progress for it.
function fakeSender() {
  const calls = []
  const send = (item, { onProgress, signal }) => new Promise((resolve, reject) => {
    const call = { item, onProgress, resolve, reject, signal }
    signal.addEventListener('abort', () => reject(Object.assign(new Error('aborted'), { status: 0 })))
    calls.push(call)
  })
  return { calls, send }
}
const file = (name, size) => ({ name, size })
const tick = () => new Promise(r => setTimeout(r, 0))
const statuses = (q) => q.getSnapshot().items.map(i => `${i.name}:${i.status}`)

test('upload queue: sends two files at once, queues the rest, and starts the next when one ends', async () => {
  const s = fakeSender()
  const q = createUploadQueue({ send: s.send, concurrency: 2 })
  q.add(['a', 'b', 'c'].map(n => ({ file: file(n, 10), path: `/${n}`, dir: '/' })))
  await tick()
  assert.deepEqual(statuses(q), ['a:sending', 'b:sending', 'c:queued'])
  assert.equal(q.getSnapshot().active, 3)
  s.calls[0].resolve({ path: '/a' })
  await tick(); await tick()
  assert.deepEqual(statuses(q), ['a:done', 'b:sending', 'c:sending'])
  assert.deepEqual(q.getSnapshot().items[0].result, { path: '/a' })
})

test('upload queue: reports the bytes sent and a smoothed rate', async () => {
  let t = 0
  const s = fakeSender()
  const q = createUploadQueue({ send: s.send, now: () => t })
  q.add([{ file: file('big', 1000), path: '/big', dir: '/' }])
  await tick()
  t = 1000
  s.calls[0].onProgress(400)
  const item = q.getSnapshot().items[0]
  assert.equal(item.loaded, 400)
  assert.equal(item.rate, 400)
  t = 2000
  s.calls[0].onProgress(600)
  assert.equal(q.getSnapshot().items[0].rate, 400 * 0.7 + 200 * 0.3)
})

test('upload queue: cancel stops a waiting file at once and aborts a sending one', async () => {
  const s = fakeSender()
  const q = createUploadQueue({ send: s.send, concurrency: 1 })
  const [a, b] = q.add([{ file: file('a', 5), path: '/a', dir: '/' }, { file: file('b', 5), path: '/b', dir: '/' }])
  await tick()
  q.cancel(b)
  assert.deepEqual(statuses(q), ['a:sending', 'b:canceled'])
  q.cancel(a)
  assert.equal(s.calls[0].signal.aborted, true)
  await tick(); await tick()
  assert.deepEqual(statuses(q), ['a:canceled', 'b:canceled'])
  assert.equal(q.getSnapshot().active, 0)
})

test('upload queue: a failure keeps its reason, Try again sends it again, a file over the limit cannot be retried', async () => {
  const s = fakeSender()
  const settled = []
  const q = createUploadQueue({ send: s.send })
  q.onSettled(i => settled.push(i.status))
  const [a] = q.add([{ file: file('a', 5), path: '/a', dir: '/' }])
  const [big] = q.add([{ file: file('big', 50), path: '/big', dir: '/', error: { key: 'uploads.errors.tooBig', limit: 10, retry: false } }])
  await tick()
  assert.equal(s.calls.length, 1, 'the file over the limit is never sent')
  s.calls[0].reject(Object.assign(new Error('disk full'), { status: 500 }))
  await tick(); await tick()
  const failed = q.getSnapshot().items.find(i => i.id === a)
  assert.equal(failed.status, 'failed')
  assert.deepEqual(failed.error, { key: 'uploads.errors.server', reason: 'disk full', retry: true })
  q.retry(a)
  q.retry(big)
  await tick()
  assert.deepEqual(statuses(q), ['a:sending', 'big:failed'])
  s.calls[1].resolve({ path: '/a' })
  await tick(); await tick()
  assert.deepEqual(settled, ['failed', 'done'])
})

test('upload queue: dismiss removes finished items and keeps the ones still going', async () => {
  const s = fakeSender()
  const q = createUploadQueue({ send: s.send, concurrency: 1 })
  q.add([{ file: file('a', 1), path: '/a', dir: '/' }, { file: file('b', 1), path: '/b', dir: '/' }])
  await tick()
  s.calls[0].resolve({})
  await tick(); await tick()
  q.dismiss()
  assert.deepEqual(statuses(q), ['b:sending'])
})

test('upload queue: the errors say whether trying again can help', () => {
  assert.equal(describeError({ status: 413 }).retry, false)
  assert.equal(describeError({ status: 409, type: 'files_off' }).key, 'uploads.errors.off')
  assert.equal(describeError({ status: 0 }).key, 'uploads.errors.network')
  assert.equal(describeError({ status: 0 }).retry, true)
  assert.equal(describeError({ status: 502, message: 'bad gateway' }).reason, 'bad gateway')
})

test('upload queue: the summary counts bytes over every file and gives the time left only for a long upload', () => {
  const now = 10_000
  const items = [
    { status: 'done', size: 100, loaded: 100 },
    { status: 'sending', size: 1000, loaded: 200, rate: 100, startedAt: 0 },
    { status: 'queued', size: 900, loaded: 0 },
    { status: 'canceled', size: 5000, loaded: 0 },
    { status: 'failed', size: 10, loaded: 0 },
  ]
  const s = summarize(items, now)
  assert.equal(s.count, 4)
  assert.equal(s.pending, 2)
  assert.equal(s.failed, 1)
  assert.equal(s.total, 2010)
  assert.equal(s.loaded, 300)
  assert.equal(s.percent, 15)
  assert.equal(s.eta, 17)
  assert.equal(summarize([{ status: 'sending', size: 10, loaded: 5, rate: 100, startedAt: 9_900 }], now).eta, null, 'too short to say')
})

test('upload queue: the time left reads short, in the reader\'s language', () => {
  assert.equal(formatDuration(12, 'en'), '12s')
  assert.equal(formatDuration(200, 'en'), '3m')
  assert.equal(formatDuration(3900, 'en'), '1h 5m')
  assert.equal(formatDuration(-1, 'en'), '')
})
