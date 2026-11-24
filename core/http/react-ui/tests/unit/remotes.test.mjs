import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  PRESETS, ageParts, canTest, chosenModels, remoteIndex, createErrorKind, defaultName,
  failStep, filterModels, hostOf, idsText, initialTicked, isMissingRoute, mergeAdopt, missingModels,
  needsAttention, noUsableModels, remoteSubline, resultParts, skipReasonKey, statusOf, testBody, usecasesOf,
} from '../../src/utils/remotes.js'
import { isRemoteModel } from '../../src/utils/sound.js'

const models = [
  { id: 'alpha', usecases: ['chat'] },
  { id: 'beta-embed', usecases: ['embeddings'] },
  { id: 'gamma', usecases: ['chat', 'tts'] },
]

test('remotes: a LocalAI remote starts with every model ticked, the cloud kinds with none', () => {
  assert.deepEqual(initialTicked('localai', models), ['alpha', 'beta-embed', 'gamma'])
  assert.deepEqual(initialTicked('openai', models), [])
  assert.deepEqual(initialTicked('anthropic', models), [])
})

test('remotes: the chosen models keep the remote order and their usecases', () => {
  assert.deepEqual(chosenModels(models, ['gamma', 'alpha']), [
    { id: 'alpha', usecases: ['chat'] },
    { id: 'gamma', usecases: ['chat', 'tts'] },
  ])
  assert.deepEqual(chosenModels([{ id: 'x' }], ['x']), [{ id: 'x', usecases: [] }])
})

test('remotes: what the test needs depends on the kind', () => {
  assert.equal(canTest({ kind: 'localai', url: 'https://a.example.com', key: '' }), true)
  assert.equal(canTest({ kind: 'localai', url: ' ', key: 'k' }), false)
  assert.equal(canTest({ kind: 'openai', url: PRESETS[0].url, key: '' }), false)
  assert.equal(canTest({ kind: 'openai', url: PRESETS[0].url, key: 'k' }), true)
  assert.equal(canTest({ kind: 'anthropic', url: '', key: 'k' }), true)
  assert.equal(canTest({ kind: 'anthropic', url: '', key: '  ' }), false)
})

test('remotes: the test body trims, and Anthropic sends no address', () => {
  assert.deepEqual(testBody({ kind: 'openai', url: ' https://a.example.com/v1 ', key: ' k ' }), { kind: 'openai', url: 'https://a.example.com/v1', api_key: 'k' })
  assert.deepEqual(testBody({ kind: 'anthropic', url: 'ignored', key: 'k' }), { kind: 'anthropic', api_key: 'k' })
})

test('remotes: a host never shows credentials, a path or a query', () => {
  assert.equal(hostOf('https://user:secret@api.example.com:8443/v1?token=abc'), 'api.example.com:8443')
  assert.equal(hostOf('api.example.com/v1'), 'api.example.com')
  assert.equal(hostOf('user:secret@api.example.com/v1'), 'api.example.com')
  assert.equal(hostOf(''), '')
})

test('remotes: default names', () => {
  assert.equal(defaultName('anthropic', 'openai', ''), 'Anthropic')
  assert.equal(defaultName('openai', 'groq', ''), 'Groq')
  assert.equal(defaultName('openai', 'custom', 'https://llm.example.com/v1'), 'llm.example.com')
  assert.equal(defaultName('localai', 'openai', 'http://box.example.com:8080'), 'box.example.com:8080')
})

test('remotes: a failed test names a known step, anything else is general', () => {
  for (const s of ['invalid_url', 'unreachable', 'auth', 'no_models', 'unsupported']) assert.equal(failStep(s), s)
  assert.equal(failStep('boom'), 'other')
  assert.equal(failStep(undefined), 'other')
})

test('remotes: status and attention', () => {
  assert.equal(statusOf({ status: 'ok' }), 'ok')
  assert.equal(statusOf({ status: 'weird' }), 'never')
  assert.equal(needsAttention({ status: 'auth_failed' }), true)
  assert.equal(needsAttention({ status: 'unreachable' }), true)
  assert.equal(needsAttention({ status: 'ok' }), false)
  assert.equal(needsAttention({ status: 'never' }), false)
})

test('remotes: a remote row names its remote, or the backend alone', () => {
  const index = remoteIndex([{ id: 'c1', name: 'Office box' }])
  assert.equal(remoteSubline({ backend: 'proxy', remote_id: 'c1' }, index, 'auto'), 'proxy · Office box')
  assert.equal(remoteSubline({ backend: 'proxy', remote_id: 'gone' }, index, 'auto'), 'proxy')
  assert.equal(remoteSubline({ remote_id: 'gone' }, index, 'auto'), 'auto')
  assert.equal(remoteSubline({ backend: 'proxy' }, undefined, 'auto'), 'proxy')
})

test('remotes: the age of a refresh', () => {
  const now = Date.parse('2026-11-14T12:00:00Z')
  assert.equal(ageParts('2026-11-14T11:59:30Z', now), null)
  assert.deepEqual(ageParts('2026-11-14T11:15:00Z', now), { value: 45, unit: 'minute' })
  assert.deepEqual(ageParts('2026-11-14T09:00:00Z', now), { value: 3, unit: 'hour' })
  assert.deepEqual(ageParts('2026-11-12T12:00:00Z', now), { value: 2, unit: 'day' })
  assert.equal(ageParts('', now), undefined)
  assert.equal(ageParts('nonsense', now), undefined)
})

test('remotes: filter and error kinds', () => {
  assert.deepEqual(filterModels(models, 'EMBED').map(m => m.id), ['beta-embed'])
  assert.equal(filterModels(models, '  ').length, 3)
  assert.equal(createErrorKind({ status: 409 }), 'nameTaken')
  assert.equal(createErrorKind({ status: 400 }), 'invalid')
  assert.equal(createErrorKind({ status: 500 }), 'other')
  assert.equal(createErrorKind({ status: 404 }), 'unsupported')
  assert.equal(isMissingRoute({ status: 404 }), true)
  assert.equal(isMissingRoute({ status: 500 }), false)
  assert.equal(isMissingRoute(null), false)
})

test('remotes: the cloud kinds list ids only, so a model without uses is a chat model', () => {
  assert.deepEqual(usecasesOf('openai', { id: 'a', usecases: [] }), ['chat'])
  assert.deepEqual(usecasesOf('anthropic', { id: 'a' }), ['chat'])
  assert.deepEqual(usecasesOf('localai', { id: 'a', usecases: [] }), [])
  assert.deepEqual(usecasesOf('openai', { id: 'a', usecases: ['embeddings'] }), ['embeddings'])
  assert.deepEqual(chosenModels([{ id: 'a', usecases: [] }], ['a'], 'openai'), [{ id: 'a', usecases: ['chat'] }])
})

test('remotes: every use a remote model can carry has a label in English', () => {
  // The uses of the server's usecase table that a person can pick, with the
  // reserved ones left out (see core/services/remotes/usecases.go).
  const used = [
    'chat', 'completion', 'edit', 'vision', 'embeddings', 'tokenize', 'image', 'upscale', 'video', '3d', '3d_animation',
    'motion', 'transcript', 'tts', 'sound_generation', 'rerank', 'detection', 'depth', 'forecast', 'vad', 'audio_transform',
    'diarization', 'sound_classification', 'realtime_audio', 'face_recognition', 'speaker_recognition',
  ]
  const here = dirname(fileURLToPath(import.meta.url))
  const en = JSON.parse(readFileSync(join(here, '../../public/locales/en/remotes.json'), 'utf8'))
  for (const u of used) assert.ok(en.usecase[u], `no label for the use "${u}"`)
})

test('remotes: a result lists what was added, what was skipped and the warning', () => {
  const result = { remote: { id: 'r1' }, created: ['a'], skipped: [{ id: 'b', reason: 'listed twice' }, null, { reason: 'x' }], warning: 'partial' }
  assert.deepEqual(resultParts(result), { created: ['a'], skipped: [{ id: 'b', reason: 'listed twice' }], warning: 'partial' })
  assert.deepEqual(resultParts(undefined), { created: [], skipped: [], warning: '' })
  assert.equal(skipReasonKey('Listed twice'), 'listedTwice')
  assert.equal(skipReasonKey('a model with that name exists'), 'nameExists')
  assert.equal(skipReasonKey('something new'), '')
  assert.equal(idsText([{ id: 'a' }, { id: 'b' }]), 'a, b')
  assert.equal(idsText([{ id: 'a' }, { id: 'b' }, { id: 'c' }, { id: 'd' }, { id: 'e' }]), 'a, b, c +2')
})

test('remotes: adopting after a partial create asks only for the models that are missing', () => {
  const chosen = [{ id: 'a', usecases: ['chat'] }, { id: 'b', usecases: ['chat'] }, { id: 'c', usecases: ['chat'] }]
  const first = { remote: { id: 'r1' }, created: ['a'], skipped: [{ id: 'b', reason: 'could not write the entry' }], warning: 'partial' }
  assert.deepEqual(missingModels(chosen, first).map(m => m.id), ['b', 'c'])
  const merged = mergeAdopt(first, { remote: { id: 'r1', entries: ['a', 'c'] }, created: ['c'], skipped: [{ id: 'b', reason: 'could not write the entry' }] })
  assert.deepEqual(merged.created, ['a', 'c'])
  assert.deepEqual(merged.skipped, [{ id: 'b', reason: 'could not write the entry' }])
  assert.equal(merged.warning, undefined)
  assert.deepEqual(missingModels(chosen, merged).map(m => m.id), ['b'])
})

test('remotes: a remote that answered but gives nothing to use shows a warning', () => {
  const base = { status: 'ok', entries: ['a'], unavailable: [], new: [] }
  assert.equal(noUsableModels(base), false)
  assert.equal(noUsableModels({ ...base, last_error: 'the list was empty' }), true)
  assert.equal(noUsableModels({ ...base, entries: [] }), true)
  assert.equal(noUsableModels({ ...base, unavailable: ['a'] }), true)
  assert.equal(noUsableModels({ ...base, unavailable: ['a'], new: [{ id: 'z' }] }), false)
  assert.equal(noUsableModels({ ...base, entries: [], status: 'auth_failed' }), false)
  assert.equal(noUsableModels({ ...base, entries: [], status: 'never' }), false)
})

test('sound: a model says whether it is remote, the backend list is the fallback', () => {
  assert.equal(isRemoteModel({ backend: 'llama-cpp', remote: true }), true)
  assert.equal(isRemoteModel({ backend: 'cloud-proxy', remote: false }), false)
  assert.equal(isRemoteModel({ backend: 'cloud-proxy' }), true)
  assert.equal(isRemoteModel({ backend: 'kokoro' }), false)
})
