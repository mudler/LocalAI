import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  QUIET_IN_FRONT, SOUND_DEFAULTS, SOUND_EVENTS, SOUND_NAMES, TONES, inQuietHours, isRemoteModel, parseTime, readAloudText, soundDecision, soundOf, voiceOf,
} from '../../src/utils/sound.js'
import { ttsModel } from '../../src/utils/ttsModel.js'

const here = dirname(fileURLToPath(import.meta.url))
const ui = JSON.parse(readFileSync(join(here, '../../../../services/studio/builtinskills/tree/localai-workspace/schemas/ui.schema.json'), 'utf8'))

test('sound: the events and the sounds are the ones of the schema', () => {
  const events = ui.properties.sound.properties.events.properties
  assert.deepEqual(Object.keys(events), [...SOUND_EVENTS])
  for (const e of SOUND_EVENTS) assert.deepEqual(events[e].enum, [...SOUND_NAMES], e)
  for (const n of SOUND_NAMES) if (n !== 'none') assert.ok(TONES[n]?.length > 0, `${n} has notes`)
})

test('sound: with no key, sound is off, and the defaults are filled in', () => {
  const s = soundOf(undefined)
  assert.equal(s.enabled, false)
  assert.equal(s.volume, 0.5)
  assert.equal(s.quiet, null)
  assert.deepEqual(s.events, { ...SOUND_DEFAULTS })
  assert.equal(soundDecision(s, 'replyDone', { gesture: true, front: false }).why, 'off')
})

test('sound: a value outside its range falls back to its default', () => {
  const s = soundOf({ sound: { enabled: true, volume: 9, quietHours: ['25:00', '07:00'], events: { replyDone: 'ring', error: 'pop', other: 'bell' } } })
  assert.equal(s.volume, 0.5)
  assert.equal(s.quiet, null)
  assert.equal(s.events.replyDone, SOUND_DEFAULTS.replyDone)
  assert.equal(s.events.error, 'pop')
  assert.equal(soundOf({ sound: { quietHours: ['22:00', '22:00'] } }).quiet, null)
})

test('sound: times are read as minutes after midnight', () => {
  assert.equal(parseTime('00:00'), 0)
  assert.equal(parseTime('07:30'), 450)
  assert.equal(parseTime('23:59'), 1439)
  for (const bad of ['24:00', '7:30', '07:60', '', null, 'noon']) assert.equal(parseTime(bad), null, String(bad))
})

test('sound: quiet hours that cross midnight cover the night and not the day', () => {
  const q = soundOf({ sound: { quietHours: ['22:00', '07:00'] } }).quiet
  const at = (h, m) => new Date(2026, 10, 10, h, m)
  assert.equal(inQuietHours(q, at(22, 0)), true)
  assert.equal(inQuietHours(q, at(23, 59)), true)
  assert.equal(inQuietHours(q, at(0, 0)), true)
  assert.equal(inQuietHours(q, at(6, 59)), true)
  assert.equal(inQuietHours(q, at(7, 0)), false, 'the end is not inside')
  assert.equal(inQuietHours(q, at(12, 0)), false)
  assert.equal(inQuietHours(q, at(21, 59)), false)
})

test('sound: quiet hours inside one day', () => {
  const q = soundOf({ sound: { quietHours: ['13:00', '14:00'] } }).quiet
  assert.equal(inQuietHours(q, new Date(2026, 10, 10, 13, 30)), true)
  assert.equal(inQuietHours(q, new Date(2026, 10, 10, 14, 0)), false)
  assert.equal(inQuietHours(null, new Date()), false)
})

test('sound: nothing plays in quiet hours, before a gesture, for none, or at volume 0', () => {
  const on = { sound: { enabled: true, volume: 0.6, quietHours: ['22:00', '07:00'], events: { error: 'none' } } }
  const s = soundOf(on)
  const night = new Date(2026, 10, 10, 23, 0)
  const noon = new Date(2026, 10, 10, 12, 0)
  const state = { gesture: true, front: false }
  assert.equal(soundDecision(s, 'taskDone', state, night).why, 'quiet')
  assert.deepEqual(soundDecision(s, 'taskDone', state, noon), { play: true, name: 'bell', volume: 0.6 })
  assert.equal(soundDecision(s, 'taskDone', { gesture: false, front: false }, noon).why, 'gesture')
  assert.equal(soundDecision(s, 'error', state, noon).why, 'none')
  assert.equal(soundDecision(soundOf({ sound: { enabled: true, volume: 0 } }), 'replyDone', state, noon).why, 'volume')
  assert.equal(soundDecision(s, 'keypress', state, noon).why, 'event')
})

test('sound: a reply, an approval and an error stay quiet while the tab is in front; a finished task does not', () => {
  const s = soundOf({ sound: { enabled: true } })
  const noon = new Date(2026, 10, 10, 12, 0)
  for (const e of QUIET_IN_FRONT) assert.equal(soundDecision(s, e, { gesture: true, front: true }, noon).why, 'front', e)
  assert.equal(soundDecision(s, 'taskDone', { gesture: true, front: true }, noon).play, true)
  for (const e of QUIET_IN_FRONT) assert.equal(soundDecision(s, e, { gesture: true, front: false }, noon).play, true, e)
})

test('voice: defaults, ranges and the remote check', () => {
  assert.deepEqual(voiceOf(undefined), { readAloud: 'off', voice: '', speed: 1, skipCode: true })
  assert.deepEqual(voiceOf({ voice: { readAloud: 'auto', voice: ' aria ', speed: 1.5, skipCode: false } }), { readAloud: 'auto', voice: 'aria', speed: 1.5, skipCode: false })
  assert.equal(voiceOf({ voice: { readAloud: 'always', speed: 5 } }).readAloud, 'off')
  assert.equal(voiceOf({ voice: { speed: 5 } }).speed, 1)
  assert.equal(isRemoteModel({ backend: 'cloud-proxy' }), true)
  assert.equal(isRemoteModel({ backend: 'localai-proxy' }), true)
  assert.equal(isRemoteModel({ backend: 'kokoro' }), false)
  assert.equal(isRemoteModel(undefined), false)
})

test('voice: the default text-to-speech model, and whether it is local', () => {
  const models = [
    { id: 'kokoro-82m', backend: 'kokoro', capabilities: ['FLAG_TTS'] },
    { id: 'cloud-voice', backend: 'cloud-proxy', capabilities: ['FLAG_TTS'] },
    { id: 'qwen', backend: 'llama-cpp', capabilities: ['FLAG_CHAT'] },
  ]
  assert.deepEqual(ttsModel({ tts: 'kokoro-82m' }, {}, models), { state: 'model', model: 'kokoro-82m', remote: false })
  assert.deepEqual(ttsModel({ tts: 'cloud-voice' }, {}, models), { state: 'model', model: 'cloud-voice', remote: true })
  assert.deepEqual(ttsModel({}, {}, models), { state: 'automatic', model: 'cloud-voice', remote: true }, 'no choice: the first installed model, by name')
  assert.equal(ttsModel({ tts: 'ask' }, {}, models).state, 'ask')
  assert.deepEqual(ttsModel({ tts: 'gone' }, {}, models), { state: 'missing', model: 'gone', remote: false })
  assert.equal(ttsModel({}, {}, [models[2]]).state, 'none')
})

test('read aloud: code blocks and tables become a short phrase, and marks that read badly go', () => {
  const md = '# Title\n\nSome **bold** and `code` and [a link](https://x.test).\n\n```js\nconst a = 1\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n- one\n- two'
  const out = readAloudText(md, { skipCode: true, omitted: (k) => `${k} omitted.` })
  assert.equal(out, 'Title\n\nSome bold and code and a link.\n\ncode omitted.\n\ntable omitted.\n\none\ntwo')
  const kept = readAloudText(md, { skipCode: false })
  assert.ok(kept.includes('const a = 1'))
  assert.ok(!kept.includes('```'))
  assert.equal(readAloudText('', {}), '')
})
