import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createAssetLoader } from '../src/assets.js'

test('four candidates are loaded with only one host transfer in flight', async () => {
  let active = 0, peak = 0
  const load = createAssetLoader({
    async read({ arguments: args }) { peak = Math.max(peak, ++active); return { files: [{ id: args.artifact }] } },
    async blob(id) { await new Promise(resolve => setTimeout(resolve, 1)); active--; return id },
  })
  assert.deepEqual(await Promise.all(['a', 'b', 'c', 'd'].map(id => load('r', id))), ['a', 'b', 'c', 'd'])
  assert.equal(peak, 1)
})

test('temporary failures retry with a fresh handle and bounded backoff', async () => {
  let reads = 0
  const delays = []
  const load = createAssetLoader({
    async read() { if (++reads < 3) throw new Error('Addon owner unavailable'); return { files: [{ id: 'ok' }] } },
    async blob(id) { return id },
  }, async ms => delays.push(ms))
  assert.equal(await load('r', 'a'), 'ok')
  assert.deepEqual(delays, [400, 800])
})

test('permanent failures do not retry or block subsequent images', async () => {
  let reads = 0
  const load = createAssetLoader({
    async read() { reads++; if (reads === 1) throw new Error('Forbidden'); return { files: [{ id: 'ok' }] } },
    async blob(id) { return id },
  })
  await assert.rejects(load('r', 'a'), /Forbidden/)
  assert.equal(await load('r', 'b'), 'ok')
  assert.equal(reads, 2)
})

test('cancelling one consumer does not cancel another consumer of the same image', async () => {
  const cancelled = new AbortController()
  cancelled.abort()
  let reads = 0
  const load = createAssetLoader({ async read() { reads++; return { files: [{ id: 'ok' }] } }, async blob(id) { return id } })
  await assert.rejects(load('r', 'a', cancelled.signal), { name: 'AbortError' })
  assert.equal(await load('r', 'a'), 'ok')
  assert.equal(reads, 1)
})
