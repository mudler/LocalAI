import test from 'node:test'
import assert from 'node:assert/strict'
import { promptCacheMessage, promptCacheState } from '../../src/utils/promptCache.js'

const GIB = 1024 ** 3

test('prompt cache: a state the page does not know reads as not read yet', () => {
  assert.equal(promptCacheState(undefined).tone, 'idle')
  assert.equal(promptCacheState({ state: 'bogus' }).tone, 'idle')
  assert.equal(promptCacheState({ state: 'on' }).tone, 'ok')
  assert.equal(promptCacheState({ state: 'paused_low_disk' }).tone, 'warn')
  assert.equal(promptCacheState({ state: 'disabled_disk_full' }).tone, 'error')
})

test('prompt cache: only a paused or disabled cache has a message, with the marks', () => {
  assert.equal(promptCacheMessage(null), null)
  assert.equal(promptCacheMessage({ state: 'on', free_bytes: GIB }), null)
  const paused = promptCacheMessage({ state: 'paused_low_disk', free_bytes: 5 * GIB, low_watermark_bytes: 10 * GIB })
  assert.match(paused, /low mark/)
  assert.match(paused, /oldest entries/)
  const off = promptCacheMessage({ state: 'disabled_disk_full', free_bytes: GIB, critical_watermark_bytes: 2 * GIB, low_watermark_bytes: 10 * GIB })
  assert.match(off, /critical mark/)
  assert.match(off, /is off until/)
})
