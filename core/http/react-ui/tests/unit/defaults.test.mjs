import test from 'node:test'
import assert from 'node:assert/strict'
import { ASK, DEFAULT_KINDS, defaultChoice, chatStartModel, modelsFor, rowState, startPick, valueFor } from '../../src/utils/defaults.js'
import { WELL_KNOWN_FILES } from '../../src/utils/wellKnown.js'

test('defaults: the file is a well-known file and the kinds are the keys of its schema', () => {
  assert.equal(WELL_KNOWN_FILES.defaults, 'LocalAI/defaults.json')
  assert.deepEqual(DEFAULT_KINDS.map(k => k.key).sort(), ['chat', 'embeddings', 'image', 'rerank', 'stt', 'tts', 'video', 'vision'])
})

test('defaults: a model, ask, null and a missing key', () => {
  assert.deepEqual(defaultChoice({ image: 'flux' }, null, 'image'), { mode: 'model', model: 'flux', from: 'defaults' })
  assert.deepEqual(defaultChoice({ tts: ' Ask ' }, null, 'tts'), { mode: 'ask', model: '', from: 'defaults' })
  assert.equal(defaultChoice({ video: null }, null, 'video').mode, 'unset')
  assert.equal(defaultChoice({}, null, 'stt').mode, 'unset')
  assert.equal(defaultChoice(undefined, undefined, 'rerank').mode, 'unset')
  assert.equal(defaultChoice({ image: '   ' }, null, 'image').mode, 'unset')
})

test('defaults: the old defaultModel of ui.json is the chat fallback and nothing else', () => {
  const ui = { defaultModel: 'old-chat' }
  assert.deepEqual(defaultChoice({}, ui, 'chat'), { mode: 'model', model: 'old-chat', from: 'ui' })
  assert.deepEqual(defaultChoice({ chat: null }, ui, 'chat'), { mode: 'model', model: 'old-chat', from: 'ui' })
  assert.equal(defaultChoice({ chat: 'new-chat' }, ui, 'chat').model, 'new-chat')
  assert.equal(defaultChoice({ chat: ASK }, ui, 'chat').mode, 'ask')
  assert.equal(defaultChoice({}, ui, 'image').mode, 'unset')
})

test('defaults: a new chat starts with the chat default, else the old one, else nothing', () => {
  assert.equal(chatStartModel({ chat: 'a' }, { defaultModel: 'b' }), 'a')
  assert.equal(chatStartModel({}, { defaultModel: 'b' }), 'b')
  assert.equal(chatStartModel({ chat: ASK }, { defaultModel: 'b' }), '')
  assert.equal(chatStartModel({}, {}), '')
})

test('defaults: models are filtered by capability, without disabled ones, by name', () => {
  const models = [
    { id: 'zeta', capabilities: ['FLAG_IMAGE'], backend: 'stablediffusion' },
    { id: 'alpha', capabilities: ['FLAG_IMAGE', 'FLAG_CHAT'] },
    { id: 'off', capabilities: ['FLAG_IMAGE'], disabled: true },
    { id: 'voice', capabilities: ['tts'] },
    { id: 'plain' },
  ]
  assert.deepEqual(modelsFor(models, 'FLAG_IMAGE'), [{ id: 'alpha', backend: '' }, { id: 'zeta', backend: 'stablediffusion' }])
  assert.deepEqual(modelsFor(models, 'FLAG_TTS'), [{ id: 'voice', backend: '' }])
  assert.deepEqual(modelsFor(models, 'FLAG_VIDEO'), [])
  assert.deepEqual(modelsFor(undefined, 'FLAG_VIDEO'), [])
})

test('defaults: the state of a row', () => {
  const installed = [{ id: 'flux', backend: '' }]
  assert.equal(rowState({ mode: 'model', model: 'flux' }, installed), 'model')
  assert.equal(rowState({ mode: 'model', model: 'gone' }, installed), 'missing')
  assert.equal(rowState({ mode: 'ask' }, installed), 'ask')
  assert.equal(rowState({ mode: 'ask' }, []), 'ask')
  assert.equal(rowState({ mode: 'unset' }, installed), 'automatic')
  assert.equal(rowState({ mode: 'unset' }, []), 'none')
})

test('defaults: a pick saves a name, ask, or null that removes the key', () => {
  assert.equal(valueFor('flux'), 'flux')
  assert.equal(valueFor(' flux '), 'flux')
  assert.equal(valueFor(ASK), 'ask')
  assert.equal(valueFor(''), null)
  assert.equal(valueFor(undefined), null)
})

test('defaults: the picker of a new chat never guesses over ask or a default that is gone', () => {
  const usable = ['a', 'b', 'c']
  const ask = defaultChoice({ chat: ASK }, null, 'chat')
  assert.deepEqual(startPick(ask, usable, 'b'), { model: '', note: 'ask', missing: '' }, 'ask: nothing chosen, the person chooses')
  const gone = defaultChoice({ chat: 'removed' }, null, 'chat')
  assert.deepEqual(startPick(gone, usable, 'b'), { model: '', note: 'missing', missing: 'removed' }, 'a removed default is not replaced by the last model')
  assert.deepEqual(startPick(defaultChoice({ chat: 'c' }, null, 'chat'), usable, 'b'), { model: 'c', note: '', missing: '' })
  // No chat key and no defaultModel: as before, the last model used, else the first.
  const unset = defaultChoice({}, {}, 'chat')
  assert.deepEqual(startPick(unset, usable, 'b'), { model: 'b', note: '', missing: '' })
  assert.deepEqual(startPick(unset, usable, 'gone'), { model: 'a', note: '', missing: '' })
  assert.deepEqual(startPick(null, usable, null), { model: 'a', note: '', missing: '' }, 'before the files are read')
  // The older defaultModel of ui.json keeps its old fallback when it is gone.
  assert.deepEqual(startPick(defaultChoice({}, { defaultModel: 'old' }, 'chat'), usable, 'b'), { model: 'b', note: '', missing: '' })
  assert.deepEqual(startPick(defaultChoice({}, { defaultModel: 'c' }, 'chat'), usable, 'b'), { model: 'c', note: '', missing: '' })
})
