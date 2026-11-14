import test from 'node:test'
import assert from 'node:assert/strict'
import {
  ANTHROPIC_URL, PRESETS, ageParts, canTest, chosenModels, connectionIndex, createErrorKind, defaultName,
  failStep, filterModels, hostOf, initialTicked, needsAttention, presetFor, remoteSubline, statusOf, testBody,
} from '../../src/utils/connections.js'
import { isRemoteModel } from '../../src/utils/sound.js'

const models = [
  { id: 'alpha', usecases: ['chat'] },
  { id: 'beta-embed', usecases: ['embeddings'] },
  { id: 'gamma', usecases: ['chat', 'tts'] },
]

test('connections: a LocalAI remote starts with every model ticked, the cloud kinds with none', () => {
  assert.deepEqual(initialTicked('localai', models), ['alpha', 'beta-embed', 'gamma'])
  assert.deepEqual(initialTicked('openai', models), [])
  assert.deepEqual(initialTicked('anthropic', models), [])
})

test('connections: the chosen models keep the remote order and their usecases', () => {
  assert.deepEqual(chosenModels(models, ['gamma', 'alpha']), [
    { id: 'alpha', usecases: ['chat'] },
    { id: 'gamma', usecases: ['chat', 'tts'] },
  ])
  assert.deepEqual(chosenModels([{ id: 'x' }], ['x']), [{ id: 'x', usecases: [] }])
})

test('connections: what the test needs depends on the kind', () => {
  assert.equal(canTest({ kind: 'localai', url: 'https://a.example.com', key: '' }), true)
  assert.equal(canTest({ kind: 'localai', url: ' ', key: 'k' }), false)
  assert.equal(canTest({ kind: 'openai', url: PRESETS[0].url, key: '' }), false)
  assert.equal(canTest({ kind: 'openai', url: PRESETS[0].url, key: 'k' }), true)
  assert.equal(canTest({ kind: 'anthropic', url: '', key: 'k' }), true)
  assert.equal(canTest({ kind: 'anthropic', url: '', key: '  ' }), false)
})

test('connections: the test body trims, and Anthropic has a fixed address', () => {
  assert.deepEqual(testBody({ kind: 'openai', url: ' https://a.example.com/v1 ', key: ' k ' }), { kind: 'openai', url: 'https://a.example.com/v1', api_key: 'k' })
  assert.deepEqual(testBody({ kind: 'anthropic', url: 'ignored', key: 'k' }), { kind: 'anthropic', url: ANTHROPIC_URL, api_key: 'k' })
})

test('connections: a host never shows credentials, a path or a query', () => {
  assert.equal(hostOf('https://user:secret@api.example.com:8443/v1?token=abc'), 'api.example.com:8443')
  assert.equal(hostOf('api.example.com/v1'), 'api.example.com')
  assert.equal(hostOf('user:secret@api.example.com/v1'), 'api.example.com')
  assert.equal(hostOf(''), '')
})

test('connections: presets and default names', () => {
  assert.equal(presetFor('https://openrouter.ai/api/v1/'), 'openrouter')
  assert.equal(presetFor('https://other.example.com'), 'custom')
  assert.equal(defaultName('anthropic', 'openai', ''), 'Anthropic')
  assert.equal(defaultName('openai', 'groq', ''), 'Groq')
  assert.equal(defaultName('openai', 'custom', 'https://llm.example.com/v1'), 'llm.example.com')
  assert.equal(defaultName('localai', 'openai', 'http://box.example.com:8080'), 'box.example.com:8080')
})

test('connections: a failed test names a known step, anything else is general', () => {
  for (const s of ['invalid_url', 'unreachable', 'auth', 'no_models']) assert.equal(failStep(s), s)
  assert.equal(failStep('boom'), 'other')
  assert.equal(failStep(undefined), 'other')
})

test('connections: status and attention', () => {
  assert.equal(statusOf({ status: 'ok' }), 'ok')
  assert.equal(statusOf({ status: 'weird' }), 'never')
  assert.equal(needsAttention({ status: 'auth_failed' }), true)
  assert.equal(needsAttention({ status: 'unreachable' }), true)
  assert.equal(needsAttention({ status: 'ok' }), false)
  assert.equal(needsAttention({ status: 'never' }), false)
})

test('connections: a remote row names its connection, or the backend alone', () => {
  const index = connectionIndex([{ id: 'c1', name: 'Office box' }])
  assert.equal(remoteSubline({ backend: 'proxy', connection: 'c1' }, index, 'auto'), 'proxy · Office box')
  assert.equal(remoteSubline({ backend: 'proxy', connection: 'gone' }, index, 'auto'), 'proxy')
  assert.equal(remoteSubline({ connection: 'gone' }, index, 'auto'), 'auto')
  assert.equal(remoteSubline({ backend: 'proxy' }, undefined, 'auto'), 'proxy')
})

test('connections: the age of a refresh', () => {
  const now = Date.parse('2026-11-14T12:00:00Z')
  assert.equal(ageParts('2026-11-14T11:59:30Z', now), null)
  assert.deepEqual(ageParts('2026-11-14T11:15:00Z', now), { value: 45, unit: 'minute' })
  assert.deepEqual(ageParts('2026-11-14T09:00:00Z', now), { value: 3, unit: 'hour' })
  assert.deepEqual(ageParts('2026-11-12T12:00:00Z', now), { value: 2, unit: 'day' })
  assert.equal(ageParts('', now), undefined)
  assert.equal(ageParts('nonsense', now), undefined)
})

test('connections: filter and error kinds', () => {
  assert.deepEqual(filterModels(models, 'EMBED').map(m => m.id), ['beta-embed'])
  assert.equal(filterModels(models, '  ').length, 3)
  assert.equal(createErrorKind({ status: 409 }), 'nameTaken')
  assert.equal(createErrorKind({ status: 400 }), 'invalid')
  assert.equal(createErrorKind({ status: 500 }), 'other')
})

test('sound: a model says whether it is remote, the backend list is the fallback', () => {
  assert.equal(isRemoteModel({ backend: 'llama-cpp', remote: true }), true)
  assert.equal(isRemoteModel({ backend: 'cloud-proxy', remote: false }), false)
  assert.equal(isRemoteModel({ backend: 'cloud-proxy' }), true)
  assert.equal(isRemoteModel({ backend: 'kokoro' }), false)
})
