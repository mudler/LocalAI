import test, { mock } from 'node:test'
import assert from 'node:assert/strict'
import { createLiveMachine, speechHoldMs, queuedWatchdogMs } from '../../src/lib/liveMachine.js'
import { startVad, silenceHoldMs } from '../../src/lib/liveVad.js'
import { MicPermissionError } from '../../src/lib/liveMic.js'
import { createFakeMic } from './fakemic.mjs'

const SPEECH = 0.2
const SILENCE = 0.001
const flush = () => new Promise(r => setImmediate(r))

// A fake speaker: say() stays pending until the test fires an event.
function fakeSpeaker() {
  const handlers = { start: new Set(), end: new Set(), blocked: new Set(), error: new Set() }
  const s = {
    said: [], stops: 0, resumes: 0, unlocks: 0,
    unlock() { s.unlocks++ },
    say(chunks) { s.said.push(chunks); return new Promise(() => {}) },
    stop() { s.stops++; s.fire('end') },
    resume() { s.resumes++ },
    on(name, fn) { handlers[name].add(fn); return () => handlers[name].delete(fn) },
    fire(name, arg) { for (const fn of [...handlers[name]]) fn(arg) },
    listeners: () => Object.values(handlers).reduce((n, h) => n + h.size, 0),
  }
  return s
}

function fakeClock() {
  let now = 0
  let nextId = 1
  const timers = new Map()
  return {
    set(fn, ms) { const id = nextId++; timers.set(id, { at: now + ms, fn }); return id },
    clear(id) { timers.delete(id) },
    advance(ms) {
      const end = now + ms
      for (;;) {
        const due = [...timers.entries()].filter(([, t]) => t.at <= end).sort((a, b) => a[1].at - b[1].at)[0]
        if (!due) break
        timers.delete(due[0])
        now = due[1].at
        due[1].fn()
      }
      now = end
    },
  }
}

// Machine over the real VAD and a fake mic, with mocked timers, so the tests
// can assert what the microphone would accept.
function setup(over = {}) {
  mock.timers.reset()
  // The VAD poll loop runs on mocked intervals; the machine's own timeouts go
  // through a small fake clock (Node 18's mocked setTimeout is unreliable).
  if (Number(process.versions.node.split('.')[0]) >= 20) mock.timers.enable({ apis: ['setInterval'] })
  else mock.timers.enable(['setInterval'])
  const clock = fakeClock()
  const fake = createFakeMic()
  const speaker = fakeSpeaker()
  const calls = { transcribe: 0, send: [], vads: 0, vadPauses: 0 }
  let messages = []
  const deps = {
    openMic: async () => fake.mic,
    startVad: (o) => {
      calls.vads++
      const v = startVad(o)
      return { ...v, pause() { calls.vadPauses++; v.pause() } }
    },
    speaker,
    transcribe: async () => { calls.transcribe++; return { text: 'hello there' } },
    send: async (text) => { calls.send.push(text) },
    getMessages: async () => messages,
    setTimeout: clock.set,
    clearTimeout: clock.clear,
    ...over,
  }
  const machine = createLiveMachine(deps)
  const play = (ms, at) => {
    fake.setLevel(at)
    for (let left = ms; left > 0; left -= 50) { mock.timers.tick(50); clock.advance(50) }
  }
  const say = async (at = 600) => { play(at, SPEECH); play(silenceHoldMs + 100, SILENCE); await flush() }
  return { machine, speaker, calls, play, advance: clock.advance, say, ...fake, setMessages: (m) => { messages = m }, name: () => machine.state.name }
}
const done = () => mock.timers.reset()

// Drives one full turn up to the point where the reply is being spoken.
async function toSpeaking(t, reply = 'Sure thing.') {
  await t.machine.dispatch('start')
  await t.say()
  assert.equal(t.name(), 'thinking')
  t.setMessages([{ seq: 2, role: 'user', text: 'hello there' }, { seq: 3, role: 'assistant', text: reply }])
  t.machine.dispatch('turnStarted')
  t.machine.dispatch('turnEnded', { failed: false })
  await flush()
  assert.equal(t.name(), 'speaking')
}

test('machine: an utterance pauses the VAD until the voice ended plus 400 ms', async () => {
  const t = setup()
  await toSpeaking(t)
  assert.equal(t.calls.transcribe, 1)
  assert.deepEqual(t.calls.send, ['hello there'])
  assert.deepEqual(t.speaker.said, [['Sure thing.']])
  // Loud sound while the voice plays is never accepted.
  await t.say(2000)
  assert.equal(t.calls.transcribe, 1)
  t.speaker.fire('end')
  t.play(speechHoldMs - 100, SPEECH)
  assert.equal(t.name(), 'speaking')
  t.play(200, SPEECH) // now past the hold: back to listening, VAD resumed
  assert.equal(t.name(), 'listening')
  await t.say()
  assert.equal(t.calls.transcribe, 2)
  t.machine.dispatch('end')
  done()
})

test('machine: the mic stays closed while the turn is running even when the voice has not started', async () => {
  const t = setup()
  await t.machine.dispatch('start')
  await t.say()
  t.machine.dispatch('turnStarted')
  await t.say(1500)
  await t.say(1500)
  assert.equal(t.calls.transcribe, 1)
  assert.equal(t.name(), 'thinking')
  t.machine.dispatch('end')
  done()
})

test('machine: an empty transcript returns to listening without sending', async () => {
  const t = setup({ transcribe: async () => ({ text: '   ' }) })
  await t.machine.dispatch('start')
  await t.say()
  assert.equal(t.name(), 'listening')
  assert.equal(t.calls.send.length, 0)
  t.machine.dispatch('end')
  done()
})

test('machine: a failed turn returns to listening and speaks nothing', async () => {
  const t = setup()
  await t.machine.dispatch('start')
  await t.say()
  t.setMessages([{ seq: 2, role: 'user', text: 'x' }, { seq: 3, role: 'assistant', text: 'partial' }])
  t.machine.dispatch('turnStarted')
  t.machine.dispatch('turnEnded', { failed: true })
  await flush()
  assert.equal(t.name(), 'listening')
  assert.equal(t.speaker.said.length, 0)
  t.machine.dispatch('end')
  done()
})

test('machine: a tool-only turn (no assistant text) returns to listening without speaking', async () => {
  const t = setup()
  await t.machine.dispatch('start')
  await t.say()
  t.setMessages([{ seq: 2, role: 'user', text: 'x' }, { seq: 3, role: 'tool', text: 'ran' }])
  t.machine.dispatch('turnStarted')
  t.machine.dispatch('turnEnded', { failed: false })
  await flush()
  assert.equal(t.name(), 'listening')
  assert.equal(t.speaker.said.length, 0)
  t.machine.dispatch('end')
  done()
})

test('machine: a stopped turn returns to listening', async () => {
  const t = setup()
  await t.machine.dispatch('start')
  await t.say()
  t.machine.dispatch('turnStarted')
  t.machine.dispatch('turnEnded', { failed: true })
  await flush()
  assert.equal(t.name(), 'listening')
  await t.say()
  assert.equal(t.calls.transcribe, 2)
  t.machine.dispatch('end')
  done()
})

test('machine: a turn that never starts ends after a grace period', async () => {
  const t = setup()
  await t.machine.dispatch('start')
  await t.say()
  assert.equal(t.name(), 'thinking')
  t.advance(6000)
  await flush()
  assert.equal(t.name(), 'listening')
  t.machine.dispatch('end')
  done()
})

test('machine: only the text after the last sent line is spoken', async () => {
  const t = setup()
  t.setMessages([{ seq: 1, role: 'user', text: 'old' }, { seq: 2, role: 'assistant', text: 'old answer' }])
  await t.machine.dispatch('start')
  await t.say()
  t.setMessages([
    { seq: 1, role: 'user', text: 'old' }, { seq: 2, role: 'assistant', text: 'old answer' },
    { seq: 3, role: 'user', text: 'hello there' }, { seq: 4, role: 'assistant', text: 'new answer' },
  ])
  t.machine.dispatch('turnStarted')
  t.machine.dispatch('turnEnded', { failed: false })
  await flush()
  assert.deepEqual(t.speaker.said, [['new answer']])
  t.machine.dispatch('end')
  done()
})

test('machine: a transcription failure shows the transcribe error and retry returns to listening', async () => {
  const t = setup({ transcribe: async () => { throw new Error('stt down') } })
  await t.machine.dispatch('start')
  await t.say()
  assert.equal(t.name(), 'error')
  assert.equal(t.machine.state.error.code, 'transcribe')
  assert.match(t.machine.state.error.message, /stt down/)
  assert.equal(t.calls.send.length, 0)
  t.machine.dispatch('retry')
  assert.equal(t.name(), 'listening')
  t.machine.dispatch('end')
  done()
})

test('machine: a send failure shows the send error', async () => {
  const t = setup({ send: async () => { throw new Error('nope') } })
  await t.machine.dispatch('start')
  await t.say()
  assert.equal(t.name(), 'error')
  assert.equal(t.machine.state.error.code, 'send')
  t.machine.dispatch('retry')
  assert.equal(t.name(), 'listening')
  t.machine.dispatch('end')
  done()
})

test('machine: a transcript read failure shows the turn error', async () => {
  let reads = 0
  const t = setup({ getMessages: async () => { if (reads++ > 0) throw new Error('gone'); return [] } })
  await t.machine.dispatch('start')
  await t.say()
  t.machine.dispatch('turnStarted')
  t.machine.dispatch('turnEnded', { failed: false })
  await flush()
  assert.equal(t.name(), 'error')
  assert.equal(t.machine.state.error.code, 'turn')
  t.machine.dispatch('retry')
  assert.equal(t.name(), 'listening')
  t.machine.dispatch('end')
  done()
})

test('machine: a speech failure shows the speech error and the thread is untouched', async () => {
  const t = setup()
  await toSpeaking(t)
  const sends = t.calls.send.length
  t.speaker.fire('error', 'audio playback failed')
  assert.equal(t.name(), 'error')
  assert.equal(t.machine.state.error.code, 'speech')
  assert.equal(t.calls.send.length, sends)
  await t.say(2000)
  assert.equal(t.calls.transcribe, 1)
  t.machine.dispatch('retry')
  assert.equal(t.name(), 'listening')
  t.machine.dispatch('end')
  done()
})

test('machine: stopVoice while speaking returns to listening', async () => {
  const t = setup()
  await toSpeaking(t)
  t.machine.dispatch('stopVoice')
  assert.equal(t.name(), 'listening')
  assert.equal(t.speaker.stops, 1)
  await t.say()
  assert.equal(t.calls.transcribe, 2)
  t.machine.dispatch('end')
  done()
})

test('machine: end releases the mic and is safe twice', async () => {
  const t = setup()
  await t.machine.dispatch('start')
  assert.equal(t.speaker.listeners(), 4)
  t.machine.dispatch('end')
  assert.equal(t.name(), 'off')
  assert.equal(t.state.closes >= 1, true)
  assert.equal(t.speaker.stops, 1)
  assert.equal(t.speaker.listeners(), 0)
  const closes = t.state.closes
  t.machine.dispatch('end')
  assert.equal(t.name(), 'off')
  assert.equal(t.state.closes, closes)
  done()
})

test('machine: end while the mic is still opening closes it when it arrives', async () => {
  let release
  const t = setup({ openMic: () => new Promise((r) => { release = r }) })
  const p = t.machine.dispatch('start')
  assert.equal(t.name(), 'starting')
  t.machine.dispatch('end')
  release(t.mic)
  await p
  assert.equal(t.name(), 'off')
  assert.equal(t.state.closes, 1)
  assert.equal(t.calls.vads, 0)
  done()
})

test('machine: hidden pauses and visible resumes', async () => {
  const t = setup()
  await t.machine.dispatch('start')
  t.machine.dispatch('hidden')
  assert.equal(t.name(), 'paused')
  await t.say()
  assert.equal(t.calls.transcribe, 0)
  t.machine.dispatch('visible')
  assert.equal(t.name(), 'listening')
  await t.say()
  assert.equal(t.calls.transcribe, 1)
  t.machine.dispatch('end')
  done()
})

test('machine: mute pauses the VAD but keeps the mic, unmute resumes', async () => {
  const t = setup()
  await t.machine.dispatch('start')
  t.machine.dispatch('mute')
  assert.equal(t.name(), 'paused')
  assert.equal(t.state.closes, 0)
  await t.say()
  assert.equal(t.calls.transcribe, 0)
  t.machine.dispatch('unmute')
  assert.equal(t.name(), 'listening')
  t.machine.dispatch('end')
  done()
})

test('machine: a new Live after End starts unmuted', async () => {
  const t = setup()
  await t.machine.dispatch('start')
  t.machine.dispatch('mute')
  assert.equal(t.name(), 'paused')
  t.machine.dispatch('end')
  await t.machine.dispatch('start')
  assert.equal(t.name(), 'listening')
  t.machine.dispatch('end')
  done()
})

test('machine: muting during a turn takes effect when the turn is over', async () => {
  const t = setup()
  await t.machine.dispatch('start')
  await t.say()
  t.machine.dispatch('mute')
  assert.equal(t.name(), 'thinking')
  t.machine.dispatch('turnStarted')
  t.machine.dispatch('turnEnded', { failed: true })
  await flush()
  assert.equal(t.name(), 'paused')
  t.machine.dispatch('end')
  done()
})

test('machine: a denied mic goes to error with code denied and no send happens', async () => {
  const t = setup({ openMic: async () => { throw new MicPermissionError() } })
  await t.machine.dispatch('start')
  assert.equal(t.name(), 'error')
  assert.equal(t.machine.state.error.code, 'denied')
  assert.equal(t.calls.send.length, 0)
  assert.equal(t.calls.vads, 0)
  t.machine.dispatch('end')
  done()
})

test('machine: another mic failure goes to error with code unavailable, retry opens again', async () => {
  const gate = { fail: true }
  const fake = createFakeMic()
  const t = setup({ openMic: async () => { if (gate.fail) throw new Error('busy'); return fake.mic } })
  await t.machine.dispatch('start')
  assert.equal(t.name(), 'error')
  assert.equal(t.machine.state.error.code, 'unavailable')
  gate.fail = false
  await t.machine.dispatch('retry')
  assert.equal(t.name(), 'listening')
  t.machine.dispatch('end')
  assert.equal(fake.state.closes >= 1, true)
  done()
})

test('machine: a blocked play goes to blocked and resume continues', async () => {
  const t = setup()
  await toSpeaking(t)
  t.speaker.fire('blocked')
  assert.equal(t.name(), 'blocked')
  await t.say(2000)
  assert.equal(t.calls.transcribe, 1)
  t.speaker.fire('start')
  assert.equal(t.name(), 'speaking')
  t.speaker.fire('end')
  t.advance(speechHoldMs + 10)
  assert.equal(t.name(), 'listening')
  t.machine.dispatch('end')
  done()
})

test('machine: subscribers are told about every change and can unsubscribe', async () => {
  const t = setup()
  const seen = []
  const off = t.machine.subscribe((s) => seen.push(s.name))
  await t.machine.dispatch('start')
  off()
  t.machine.dispatch('end')
  assert.deepEqual(seen, ['starting', 'listening'])
  done()
})

test('machine: start from error does not leak the first mic, detector or listeners', async () => {
  const mics = []
  const t = setup({
    openMic: async () => { const f = createFakeMic(); mics.push(f); return f.mic },
    transcribe: async () => { throw new Error('stt down') },
  })
  await t.machine.dispatch('start')
  mics[0].setLevel(SPEECH)
  for (let i = 0; i < 20; i++) { mock.timers.tick(50) }
  mics[0].setLevel(SILENCE)
  for (let i = 0; i < 30; i++) { mock.timers.tick(50) }
  await flush()
  assert.equal(t.name(), 'error')
  await t.machine.dispatch('start')
  assert.equal(mics.length, 2)
  assert.equal(mics[0].state.closes >= 1, true, 'first mic closed')
  assert.equal(t.speaker.listeners(), 4)
  t.machine.dispatch('end')
  assert.equal(mics[1].state.closes >= 1, true)
  assert.equal(t.speaker.listeners(), 0)
  done()
})

test('machine: mute while transcribing does not send the message', async () => {
  let release
  const t = setup({ transcribe: () => new Promise((r) => { release = r }) })
  await t.machine.dispatch('start')
  t.play(600, SPEECH)
  t.play(silenceHoldMs + 100, SILENCE)
  assert.equal(t.name(), 'transcribing')
  t.machine.dispatch('mute')
  release({ text: 'hi' })
  await flush()
  assert.equal(t.calls.send.length, 0)
  assert.equal(t.name(), 'paused')
  t.machine.dispatch('end')
  done()
})

test('machine: a hidden tab while transcribing does not send the message', async () => {
  let release
  const t = setup({ transcribe: () => new Promise((r) => { release = r }) })
  await t.machine.dispatch('start')
  t.play(600, SPEECH)
  t.play(silenceHoldMs + 100, SILENCE)
  t.machine.dispatch('hidden')
  release({ text: 'hi' })
  await flush()
  assert.equal(t.calls.send.length, 0)
  assert.equal(t.name(), 'paused')
  t.machine.dispatch('end')
  done()
})

test('machine: no utterance is accepted while the chat runs, accepted again after', async () => {
  const t = setup()
  await t.machine.dispatch('start')
  t.machine.dispatch('busy', true)
  await t.say()
  assert.equal(t.calls.transcribe, 0)
  t.machine.dispatch('busy', false)
  await t.say()
  assert.equal(t.calls.transcribe, 1)
  t.machine.dispatch('end')
  done()
})

test('machine: the chat running keeps the detector paused after the reply until it is over', async () => {
  const t = setup()
  await t.machine.dispatch('start')
  t.machine.dispatch('busy', true)
  await t.say()
  t.machine.dispatch('busy', false)
  await t.say()
  t.machine.dispatch('busy', true)
  t.setMessages([{ seq: 2, role: 'user', text: 'x' }, { seq: 3, role: 'assistant', text: 'ok' }])
  t.machine.dispatch('turnStarted')
  t.machine.dispatch('turnEnded', { failed: false })
  await flush()
  t.speaker.fire('end')
  t.advance(speechHoldMs + 10)
  assert.equal(t.name(), 'listening')
  const before = t.calls.transcribe
  await t.say()
  assert.equal(t.calls.transcribe, before)
  t.machine.dispatch('end')
  done()
})

test('machine: a message queued behind another turn is not dropped by the grace', async () => {
  const t = setup({ send: async (text) => { t.calls.send.push(text); return { started: false } } })
  await t.machine.dispatch('start')
  await t.say()
  t.advance(20000)
  await flush()
  assert.equal(t.name(), 'thinking')
  t.setMessages([{ seq: 2, role: 'user', text: 'hello there' }, { seq: 3, role: 'assistant', text: 'late reply' }])
  t.machine.dispatch('turnStarted')
  t.machine.dispatch('turnEnded', { failed: false })
  await flush()
  assert.deepEqual(t.speaker.said, [['late reply']])
  t.machine.dispatch('end')
  done()
})

test('machine: stop during thinking before the turn starts does not silence the next turn', async () => {
  const stops = []
  const t = setup({ stop: () => stops.push(1) })
  await t.machine.dispatch('start')
  await t.say()
  t.machine.dispatch('stopVoice')
  assert.equal(stops.length, 1)
  // No turn had started: stop ends it at once instead of waiting.
  assert.equal(t.name(), 'listening')
  await t.say()
  t.setMessages([{ seq: 4, role: 'user', text: 'again' }, { seq: 5, role: 'assistant', text: 'spoken' }])
  t.machine.dispatch('turnStarted')
  t.machine.dispatch('turnEnded', { failed: false })
  await flush()
  assert.deepEqual(t.speaker.said, [['spoken']])
  t.machine.dispatch('end')
  done()
})

test('machine: a stopped turn is not spoken', async () => {
  const t = setup({ stop: () => {} })
  await t.machine.dispatch('start')
  await t.say()
  t.machine.dispatch('turnStarted')
  t.machine.dispatch('stopVoice')
  t.setMessages([{ seq: 2, role: 'user', text: 'x' }, { seq: 3, role: 'assistant', text: 'partial' }])
  t.machine.dispatch('turnEnded', { failed: false })
  await flush()
  assert.equal(t.name(), 'listening')
  assert.equal(t.speaker.said.length, 0)
  t.machine.dispatch('end')
  done()
})

test('machine: a transcription that hangs times out after 90 s with the transcribe error', async () => {
  const t = setup({
    transcribe: (blob, signal) => new Promise((_, reject) => {
      signal.addEventListener('abort', () => reject(new Error('aborted')))
    }),
  })
  await t.machine.dispatch('start')
  t.play(600, SPEECH)
  t.play(silenceHoldMs + 100, SILENCE)
  assert.equal(t.name(), 'transcribing')
  t.advance(90_000)
  await flush()
  assert.equal(t.name(), 'error')
  assert.equal(t.machine.state.error.code, 'transcribe')
  assert.match(t.machine.state.error.message, /timed out/)
  t.machine.dispatch('end')
  done()
})

test('machine: end aborts a transcription in flight', async () => {
  let signal
  const t = setup({ transcribe: (blob, s) => { signal = s; return new Promise(() => {}) } })
  await t.machine.dispatch('start')
  t.play(600, SPEECH)
  t.play(silenceHoldMs + 100, SILENCE)
  t.machine.dispatch('end')
  assert.equal(signal.aborted, true)
  done()
})

test('machine: a queued message whose turn is never seen ends after the watchdog (A)', async () => {
  const u = setup({ send: async (text) => { u.machine.dispatch('busy', true); u.calls.send.push(text); return { started: false } } })
  await u.machine.dispatch('start')
  await u.say()
  assert.equal(u.name(), 'thinking')
  u.advance(queuedWatchdogMs * 2)
  assert.equal(u.name(), 'thinking', 'the chat is busy: still waiting')
  u.machine.dispatch('busy', false) // the other turn ended; ours never showed
  u.advance(queuedWatchdogMs - 1000)
  assert.equal(u.name(), 'thinking')
  u.advance(2000)
  await flush()
  assert.equal(u.name(), 'listening')
  assert.equal(u.speaker.said.length, 0)
  u.machine.dispatch('end')
  done()
})

test('machine: a queued turn whose start edge was missed is still spoken at the watchdog (B)', async () => {
  const t = setup({ send: async (text) => { t.machine.dispatch('busy', true); t.calls.send.push(text); return { started: false } } })
  await t.machine.dispatch('start')
  await t.say()
  t.setMessages([{ seq: 2, role: 'user', text: 'hello there' }, { seq: 3, role: 'assistant', text: 'queued reply' }])
  t.machine.dispatch('busy', false)
  t.advance(queuedWatchdogMs + 100)
  await flush()
  assert.deepEqual(t.speaker.said, [['queued reply']])
  t.machine.dispatch('end')
  done()
})

test('machine: the watchdog is cleared by a real turn start and by end', async () => {
  const t = setup({ send: async (text) => { t.calls.send.push(text); return { started: false } } })
  await t.machine.dispatch('start')
  await t.say()
  t.advance(queuedWatchdogMs - 1000) // chat idle: the watchdog runs
  t.machine.dispatch('turnStarted')
  t.advance(queuedWatchdogMs * 2)
  assert.equal(t.name(), 'thinking')
  t.machine.dispatch('end')
  t.advance(queuedWatchdogMs * 2)
  assert.equal(t.name(), 'off')
  done()
})

test('machine: stop on a queued message that has not started returns to listening (C)', async () => {
  const stops = []
  const t = setup({ send: async (text) => { t.calls.send.push(text); return { started: false } }, stop: () => stops.push(1) })
  await t.machine.dispatch('start')
  await t.say()
  assert.equal(t.name(), 'thinking')
  t.machine.dispatch('stopVoice')
  assert.equal(stops.length, 1)
  assert.equal(t.name(), 'listening')
  t.advance(queuedWatchdogMs * 2)
  assert.equal(t.name(), 'listening')
  t.machine.dispatch('end')
  done()
})

test('machine: a transcript that arrives while the chat runs is dropped (N2)', async () => {
  let release
  const t = setup({ transcribe: () => new Promise((r) => { release = r }) })
  await t.machine.dispatch('start')
  t.play(600, SPEECH)
  t.play(silenceHoldMs + 100, SILENCE)
  assert.equal(t.name(), 'transcribing')
  t.machine.dispatch('busy', true)
  release({ text: 'hi' })
  await flush()
  assert.equal(t.calls.send.length, 0)
  assert.equal(t.name(), 'listening')
  t.machine.dispatch('end')
  done()
})
