import test from 'node:test'
import assert from 'node:assert/strict'
import { promptCacheMessage, promptCacheOffLabel, promptCacheOffMessage, promptCacheState, promptCacheTotal } from '../../src/utils/promptCache.js'

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

test('prompt cache: the total shows only with several nodes, and counts the nodes that did not answer', () => {
  assert.equal(promptCacheTotal(null), null)
  assert.equal(promptCacheTotal({ nodes: [{}], total: { bytes: GIB, files: 2, nodes: 1 } }), null)
  const line = promptCacheTotal({ nodes: [{}, {}, {}], total: { bytes: GIB, files: 3, cap_bytes: 2 * GIB, nodes: 2, unreachable: 1 } })
  assert.match(line, /3 entries on 2 nodes/)
  assert.match(line, /at most/)
  assert.match(line, /1 node did not answer/)
})

test('prompt cache: a node that is off says why, and how to fix a bad directory', () => {
  assert.equal(promptCacheOffLabel(undefined), 'Off')
  assert.equal(promptCacheOffLabel('switched_off'), 'Off')
  assert.equal(promptCacheOffLabel('network_disk'), 'Off: network disk')
  assert.equal(promptCacheOffLabel('not_writable'), 'Off: not writable')
  assert.equal(promptCacheOffMessage('switched_off'), null)
  assert.equal(promptCacheOffMessage(undefined), null)
  assert.match(promptCacheOffMessage('network_disk'), /on a network disk/)
  assert.match(promptCacheOffMessage('network_disk'), /--prompt-cache-path/)
  assert.match(promptCacheOffMessage('not_writable'), /not writable/)
  assert.match(promptCacheOffMessage('no_path'), /--data-path/)
})
