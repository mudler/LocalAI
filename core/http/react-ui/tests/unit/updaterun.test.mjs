import test from 'node:test'
import assert from 'node:assert/strict'
import { followJob } from '../../src/utils/updateRun.js'

// followJob waits for one operation; the node dialog uses it. A clock that only moves when the code sleeps, so the tests are instant.
function clock() {
  let t = 0
  return { now: () => t, sleep: async (ms) => { t += ms } }
}
test('follow: an operation that is never listed ends after the grace time, one that never ends times out', async () => {
  const c = clock()
  const out = await followJob({ jobID: 'x', getOperations: () => [], graceMs: 2000, pollMs: 500, ...c })
  assert.equal(out.verdict, 'ended')
  assert.ok(c.now() >= 2000)
  const c2 = clock()
  const out2 = await followJob({ jobID: 'x', getOperations: () => [{ jobID: 'x' }], pollMs: 1000, timeoutMs: 5000, ...c2 })
  assert.deepEqual(out2, { verdict: 'failed', error: 'timeout' })
})

test('follow: an abort stops the wait', async () => {
  const ctl = new AbortController()
  const sleep = async () => { ctl.abort(); throw new DOMException('Aborted', 'AbortError') }
  await assert.rejects(followJob({ jobID: 'x', getOperations: () => [{ jobID: 'x' }], sleep, signal: ctl.signal }), { name: 'AbortError' })
})

