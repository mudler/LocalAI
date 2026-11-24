import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { pickLiveModels, RECOMMENDED } from '../../src/utils/liveModels.js'

test('pickLiveModels takes the first of each capability', () => {
  assert.deepEqual(
    pickLiveModels({ stt: [{ id: 'whisper-base' }, { id: 'other' }], tts: [{ id: 'kokoro' }, { id: 'x' }] }),
    { stt: 'whisper-base', tts: 'kokoro' },
  )
})

test('pickLiveModels gives an empty string for a missing capability', () => {
  assert.deepEqual(pickLiveModels({ stt: [], tts: [{ id: 'k' }] }), { stt: '', tts: 'k' })
  assert.deepEqual(pickLiveModels({}), { stt: '', tts: '' })
  assert.deepEqual(pickLiveModels(undefined), { stt: '', tts: '' })
})

test('every recommended id exists in gallery/index.yaml', () => {
  const file = fileURLToPath(new URL('../../../../../gallery/index.yaml', import.meta.url))
  const names = new Set([...readFileSync(file, 'utf8').matchAll(/^ {2}name: (\S+)\s*$/gm)].map((m) => m[1]))
  assert.ok(names.size > 100, 'the gallery was read')
  for (const kind of ['stt', 'tts']) {
    assert.ok(RECOMMENDED[kind].length > 0, `${kind} has a recommendation`)
    for (const id of RECOMMENDED[kind]) assert.ok(names.has(id), `${id} is in the gallery`)
  }
})
