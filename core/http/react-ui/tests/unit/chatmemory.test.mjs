// SPDX-License-Identifier: MIT
import test from 'node:test'
import assert from 'node:assert/strict'
import { memoryReason, memoryStatus, memoryRetryTime, createMemoryReader } from '../../src/utils/chatMemory.js'

test('memory status: codes are allowlisted and retry dates are Unix seconds', () => {
  assert.equal(memoryReason('policy_revoked'), 'policy_revoked')
  assert.equal(memoryReason('secret backend diagnostic'), 'unknown')
  assert.equal(memoryStatus('saved'), 'saved')
  assert.equal(memoryStatus('diagnostic'), 'unknown')
  assert.equal(memoryRetryTime(1700000000).toISOString(), '2023-11-14T22:13:20.000Z')
  assert.equal(memoryRetryTime(0), null)
})

test('memory reader: newer reads and disposed chat epochs reject stale responses', async () => {
  const applied = []
  const pending = []
  const read = createMemoryReader(() => new Promise(resolve => pending.push(resolve)), rows => applied.push(rows))
  const a = read.refresh(); const b = read.refresh()
  pending[1](['new']); await b
  pending[0](['old']); await a
  assert.deepEqual(applied, [['new']])
  const c = read.refresh(); read.dispose(); pending[2](['old chat']); await c
  assert.deepEqual(applied, [['new']])
})

test('memory reader: supersession and disposal abort fetch, stale failures do not surface', async () => {
  const signals = [], pending = [], errors = []
  const reader = createMemoryReader(signal => {
    signals.push(signal)
    return new Promise((resolve, reject) => pending.push({ resolve, reject }))
  }, () => {}, error => errors.push(error.message))
  const a = reader.refresh(), b = reader.refresh()
  assert.equal(signals[0].aborted, true)
  pending[0].reject(new Error('old error')); await a
  pending[1].reject(new Error('current error')); await b
  assert.deepEqual(errors, ['current error'])
  const c = reader.refresh()
  reader.dispose()
  assert.equal(signals[2].aborted, true)
  pending[2].reject(new Error('old chat')); await c
  await reader.refresh()
  assert.equal(signals.length, 3)
  assert.deepEqual(errors, ['current error'])
})

test('memory events invalidate only metadata, never messages, pending or live model state', async () => {
  const { readFileSync } = await import('node:fs')
  const hook = readFileSync(new URL('../../src/hooks/useServerChat.js', import.meta.url), 'utf8')
  assert.match(hook, /'memory_capture'/)
  const handler = hook.match(/memory_capture: \(\) => ([^\n]+)/)?.[1]
  assert.match(handler, /setTicks/)
  assert.doesNotMatch(handler, /setMessages|setLive|setDetail|setNotes|setError|refreshChat/)
  assert.match(hook, /source\.onopen[\s\S]*?setTicks\(t => \(\{ \.\.\.t, memory:/)
  const page = readFileSync(new URL('../../src/pages/Chat.jsx', import.meta.url), 'utf8')
  assert.match(page, /cx-dock__in[^]*?<ChatMemoryStatus/)
})
