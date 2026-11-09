import test, { mock } from 'node:test'
import assert from 'node:assert/strict'
import { startVad, silenceHoldMs, maxUtteranceMs } from '../../src/lib/liveVad.js'
import { openMic, MicPermissionError } from '../../src/lib/liveMic.js'
import { createFakeMic } from './fakemic.mjs'

const SPEECH = 0.2
const SILENCE = 0.001
const pollSlack = 100

// Sets up fake timers and a VAD over a fake mic; `play` advances the poll loop
// by `ms` at the given loudness.
function setup() {
  // Node 20+ takes { apis }, Node 18 takes the array itself.
  if (Number(process.versions.node.split('.')[0]) >= 20) mock.timers.enable({ apis: ['setInterval'] })
  else mock.timers.enable(['setInterval'])
  const fake = createFakeMic()
  const utterances = []
  const states = []
  const vad = startVad({
    mic: fake.mic,
    onUtterance: (b) => utterances.push(b),
    onState: (s) => states.push(s),
  })
  const play = (ms, at) => {
    fake.setLevel(at)
    // One poll at a time: Node 18 fires an interval once per tick call.
    for (let left = ms; left > 0; left -= 50) mock.timers.tick(50)
  }
  return { ...fake, vad, utterances, states, play }
}
const done = () => mock.timers.reset()

test('vad: emits an utterance once speech is followed by a held silence', () => {
  const t = setup()
  t.play(600, SPEECH)
  assert.ok(t.states.includes('hearing'))
  assert.equal(t.utterances.length, 0)
  t.play(silenceHoldMs - 200, SILENCE)
  assert.equal(t.utterances.length, 0)
  t.play(300, SILENCE)
  assert.equal(t.utterances.length, 1)
  assert.equal(t.states.at(-1), 'listening')
  t.vad.stop()
  done()
})

test('vad: keeps listening after an utterance and emits the next one too', () => {
  const t = setup()
  t.play(500, SPEECH)
  t.play(silenceHoldMs + 100, SILENCE)
  assert.equal(t.utterances.length, 1)
  t.play(500, SPEECH)
  t.play(silenceHoldMs + 100, SILENCE)
  assert.equal(t.utterances.length, 2)
  t.vad.stop()
  done()
})

test('vad: a room that is quiet-noisy and never speaks stays silent', () => {
  const t = setup()
  t.play(10_000, 0.004)
  assert.equal(t.utterances.length, 0)
  assert.deepEqual(t.states, [])
  t.vad.stop()
  done()
})

test('vad: adapts to a noisy room instead of sending its hum', () => {
  const t = setup()
  t.play(10_000, 0.03)
  assert.deepEqual(t.states, [])
  assert.equal(t.utterances.length, 0)
  t.play(500, SPEECH)
  t.play(silenceHoldMs + 200, 0.03)
  assert.equal(t.utterances.length, 1)
  t.vad.stop()
  done()
})

test('vad: a blip too short to be speech is dropped', () => {
  const t = setup()
  t.play(100, SPEECH)
  t.play(silenceHoldMs + 200, SILENCE)
  assert.equal(t.utterances.length, 0)
  t.vad.stop()
  done()
})

test('vad: cuts an utterance loose at the duration cap', () => {
  const t = setup()
  t.play(maxUtteranceMs + pollSlack, SPEECH)
  assert.equal(t.utterances.length, 1)
  t.vad.stop()
  done()
})

test('vad: a long silence recycles the segment without emitting', () => {
  const t = setup()
  t.play(maxUtteranceMs + pollSlack, SILENCE)
  assert.equal(t.utterances.length, 0)
  assert.ok(t.state.begins > 1)
  t.vad.stop()
  done()
})

test('vad: the emitted clip carries its audio', () => {
  const t = setup()
  t.play(500, SPEECH)
  t.play(silenceHoldMs + 100, SILENCE)
  assert.equal(t.utterances.length, 1)
  assert.ok(t.utterances[0].size > 0)
  assert.equal(t.utterances[0].type, 'audio/wav')
  t.vad.stop()
  done()
})

test('vad: audio spoken while paused is dropped, and capture resumes', () => {
  const t = setup()
  t.play(400, SPEECH)
  t.vad.pause()
  t.play(5_000, SPEECH)
  t.play(silenceHoldMs + 200, SILENCE)
  assert.equal(t.utterances.length, 0)
  t.vad.resume()
  t.play(500, SPEECH)
  t.play(silenceHoldMs + 200, SILENCE)
  assert.equal(t.utterances.length, 1)
  t.vad.stop()
  done()
})

test('vad: stop releases the mic, stays stopped and is safe twice', () => {
  const t = setup()
  t.vad.stop()
  assert.equal(t.state.closes, 1)
  t.vad.stop()
  assert.equal(t.state.closes, 1)
  t.play(5_000, SPEECH)
  assert.equal(t.utterances.length, 0)
  done()
})

test('vad: noise floor does not creep past the ceiling during 10 s of continuous speech', () => {
  const t = setup()
  t.play(500, 0.005) // room tone seeds a low floor
  t.play(10_000, 0.12) // sustained speech
  t.play(2_000, 0.09) // quieter passage, still above the 0.08 ceiling
  assert.equal(t.utterances.length, 0, 'utterance must not be cut inside speech')
  t.play(silenceHoldMs + 100, SILENCE)
  assert.equal(t.utterances.length, 1)
  t.vad.stop()
  done()
})

test('openMic: a refused microphone is a MicPermissionError', async () => {
  const had = Object.getOwnPropertyDescriptor(globalThis, 'navigator')
  for (const name of ['NotAllowedError', 'SecurityError', 'PermissionDeniedError']) {
    Object.defineProperty(globalThis, 'navigator', {
      configurable: true,
      value: { mediaDevices: { getUserMedia: async () => { throw Object.assign(new Error('x'), { name }) } } },
    })
    await assert.rejects(openMic(), (e) => e instanceof MicPermissionError)
  }
  Object.defineProperty(globalThis, 'navigator', { configurable: true, value: {} })
  await assert.rejects(openMic(), /no microphone api/)
  if (had) Object.defineProperty(globalThis, 'navigator', had)
  else delete globalThis.navigator
})

test('vad: quiet speech over a quiet room is not split by a rising noise floor', () => {
  const t = setup()
  t.play(500, 0.005)
  t.play(6_000, 0.05)
  assert.equal(t.utterances.length, 0, 'no cut while the person is still talking')
  t.play(silenceHoldMs + 100, SILENCE)
  assert.equal(t.utterances.length, 1)
  t.vad.stop()
  done()
})

test('vad: a room that jumps to a constant hum recovers after the first cap cut', () => {
  const t = setup()
  t.play(1_000, 0.005)
  t.play(2 * maxUtteranceMs + 10_000, 0.03)
  assert.ok(t.utterances.length <= 1, `got ${t.utterances.length} clips`)
  assert.equal(t.states.at(-1), 'listening')
  t.vad.stop()
  done()
})

test('openMic: a failed audio setup stops the tracks and closes the context', async () => {
  let stopped = 0
  let closed = 0
  const stream = { getTracks: () => [{ stop: () => { stopped++ } }] }
  Object.defineProperty(globalThis, 'navigator', {
    configurable: true,
    value: { mediaDevices: { getUserMedia: async () => stream } },
  })
  globalThis.AudioContext = class {
    constructor() { this.state = 'suspended'; this.audioWorklet = { addModule: async () => { throw new Error('blocked') } } }
    async resume() { this.state = 'running' }
    async close() { closed++ }
  }
  try {
    await assert.rejects(openMic(), /blocked/)
    assert.equal(stopped, 1)
    assert.equal(closed, 1)
  } finally {
    delete globalThis.AudioContext
    delete globalThis.navigator
  }
})

test('vad: the first reading after a pause does not cover the pause', () => {
  const mock0 = createFakeMic()
  let reads = 0
  const mic = { ...mock0.mic, level() { reads++; return mock0.mic.level() } }
  const vad = startVad({ mic, onUtterance: () => {}, onState: () => {} })
  vad.pause()
  const before = reads
  vad.resume()
  // resume() empties the level window, so the next poll reads only new audio.
  assert.equal(reads, before + 1)
  vad.stop()
}
)
