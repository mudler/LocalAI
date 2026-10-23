import test from 'node:test'
import assert from 'node:assert/strict'
import { retryRead, DELAYS } from '../src/lib/retry.js'

const failing = (kind, times, value = 'ok') => {
  let n = 0
  const fn = async () => { n++; if (n <= times) throw Object.assign(new Error(kind), { kind }); return value }
  fn.calls = () => n
  return fn
}
const instant = async () => {}

test('retry: a read that met an unavailable add-on is tried again and then succeeds', async () => {
  const waits = []
  const fn = failing('unavailable', 2)
  const out = await retryRead(fn, { sleep: async (ms) => { waits.push(ms) } })
  assert.equal(out, 'ok')
  assert.equal(fn.calls(), 3)
  assert.deepEqual(waits, DELAYS.slice(0, 2))
})

test('retry: it gives the last error back after the bound and never loops', async () => {
  const fn = failing('network', 99)
  await assert.rejects(retryRead(fn, { sleep: instant }), (e) => e.kind === 'network')
  assert.equal(fn.calls(), DELAYS.length + 1)
})

test('retry: a refusal, a stale answer or an unknown outcome is never repeated', async () => {
  for (const kind of ['denied', 'stale', 'invalid', 'unknown_outcome', 'auth', 'not_selected']) {
    const fn = failing(kind, 99)
    await assert.rejects(retryRead(fn, { sleep: instant }), (e) => e.kind === kind)
    assert.equal(fn.calls(), 1, kind)
  }
})

test('retry: a withdrawn add-on stops the waiting and the reads', async () => {
  const controller = new AbortController()
  const fn = failing('unavailable', 99)
  const sleep = async () => { controller.abort('withdrawn') }
  await assert.rejects(retryRead(fn, { sleep, signal: controller.signal }))
  assert.equal(fn.calls(), 1)
})
