import assert from 'node:assert/strict'
import test from 'node:test'

import { backendState, opForBackend, sortInstalled, versionLabel } from './backendRows.js'

test('finds the operation by bare name or gallery id', () => {
  const ops = [{ name: 'vllm', id: 'localai@vllm' }, { name: 'whisper', id: 'whisper' }]
  assert.equal(opForBackend(ops, 'vllm').name, 'vllm')
  assert.equal(opForBackend(ops, 'x', 'localai@vllm').name, 'vllm')
  assert.equal(opForBackend(ops, 'piper'), null)
  assert.equal(opForBackend([], 'vllm'), null)
  assert.equal(opForBackend(ops), null)
})

test('what is happening now outranks everything else', () => {
  const upgrade = { installed_version: '1', available_version: '2' }
  assert.deepEqual(backendState({ installed: true, upgrade, op: { progress: 40.4, cancellable: true, jobID: 'j' } }), {
    kind: 'installing', progress: 40, cancellable: true, jobID: 'j',
  })
  assert.equal(backendState({ op: { isQueued: true } }).kind, 'queued')
  assert.equal(backendState({ installed: true, op: { isDeletion: true } }).kind, 'removing')
})

test('a failed attempt is shown before an update', () => {
  const state = backendState({ upgrade: { available_version: '2' }, op: { error: 'no space', jobID: 'j' } })
  assert.deepEqual(state, { kind: 'failed', error: 'no space', jobID: 'j' })
})

test('update, current and absent', () => {
  assert.deepEqual(backendState({ installed: true, upgrade: { installed_version: '1', available_version: '2' } }), {
    kind: 'update', from: '1', to: '2',
  })
  assert.equal(backendState({ installed: true }).kind, 'current')
  assert.equal(backendState({}).kind, 'absent')
})

test('puts backends with an update first, then by name', () => {
  const list = [{ Name: 'whisper' }, { Name: 'llama-cpp' }, { Name: 'bark' }]
  const sorted = sortInstalled(list, { whisper: {} })
  assert.deepEqual(sorted.map(b => b.Name), ['whisper', 'bark', 'llama-cpp'])
})

test('writes a version once with its v', () => {
  assert.equal(versionLabel('1.2.0'), 'v1.2.0')
  assert.equal(versionLabel('v1.2.0'), 'v1.2.0')
  assert.equal(versionLabel(''), '')
  assert.equal(versionLabel(null), '')
})
