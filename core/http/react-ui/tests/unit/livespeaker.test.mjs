import test from 'node:test'
import assert from 'node:assert/strict'
import { createSpeaker } from '../../src/lib/liveSpeaker.js'

// A fake audio element: play() resolves (or rejects with playError), and
// finish() fires `ended` for the clip that is playing.
function fakeAudio() {
  const handlers = {}
  const a = {
    src: '',
    played: [],
    paused: 0,
    playError: null,
    addEventListener(name, fn) { (handlers[name] ||= new Set()).add(fn) },
    removeEventListener(name, fn) { handlers[name]?.delete(fn) },
    async play() {
      if (a.playError) throw a.playError
      a.played.push(a.src)
    },
    pause() { a.paused++ },
    error: null,
    finish() { for (const fn of [...(handlers.ended || [])]) fn() },
    fail(err) { a.error = err; for (const fn of [...(handlers.error || [])]) fn() },
  }
  return a
}

const tick = () => new Promise(r => setTimeout(r, 0))

function spyUrls(t) {
  const made = []
  const revoked = []
  const create = URL.createObjectURL
  const revoke = URL.revokeObjectURL
  URL.createObjectURL = (b) => { const u = `blob:fake/${made.length}`; made.push(u); return u }
  URL.revokeObjectURL = (u) => { revoked.push(u) }
  t.after(() => { URL.createObjectURL = create; URL.revokeObjectURL = revoke })
  return { made, revoked }
}

function fakeSpeak() {
  const calls = []
  const speak = (text, signal) => {
    const c = { text, signal, resolve: null, reject: null }
    c.promise = new Promise((res, rej) => { c.resolve = () => res(new Blob([text])); c.reject = rej })
    signal.addEventListener('abort', () => c.reject(Object.assign(new Error('aborted'), { name: 'AbortError' })))
    calls.push(c)
    return c.promise
  }
  return { speak, calls }
}

test('plays chunks in order and prefetches the next', async (t) => {
  spyUrls(t)
  const audio = fakeAudio()
  const { speak, calls } = fakeSpeak()
  const sp = createSpeaker({ speak, makeAudio: () => audio })
  const events = []
  sp.on('start', () => events.push('start'))
  sp.on('end', () => events.push('end'))
  const done = sp.say(['one', 'two'])
  await tick()
  assert.deepEqual(calls.map(c => c.text), ['one'])
  calls[0].resolve()
  await tick()
  assert.equal(audio.played.length, 1)
  assert.deepEqual(calls.map(c => c.text), ['one', 'two'], 'chunk 2 is fetched while chunk 1 plays')
  calls[1].resolve()
  await tick()
  assert.equal(audio.played.length, 1, 'chunk 2 waits for chunk 1 to end')
  audio.finish()
  await tick()
  assert.equal(audio.played.length, 2)
  audio.finish()
  await done
  assert.deepEqual(events, ['start', 'end'])
})

test('stop aborts the pending fetch and emits end once', async (t) => {
  spyUrls(t)
  const audio = fakeAudio()
  const { speak, calls } = fakeSpeak()
  const sp = createSpeaker({ speak, makeAudio: () => audio })
  let ends = 0
  sp.on('end', () => ends++)
  const done = sp.say(['one', 'two'])
  await tick()
  sp.stop()
  sp.stop()
  await done
  assert.equal(calls[0].signal.aborted, true)
  assert.equal(ends, 1)
  assert.equal(audio.played.length, 0)
})

test('stop when idle does nothing', () => {
  const sp = createSpeaker({ speak: async () => new Blob([]), makeAudio: () => fakeAudio() })
  let ends = 0
  sp.on('end', () => ends++)
  sp.stop()
  assert.equal(ends, 0)
})

test('a new say supersedes the old one and aborts its fetches', async (t) => {
  spyUrls(t)
  const audio = fakeAudio()
  const { speak, calls } = fakeSpeak()
  const sp = createSpeaker({ speak, makeAudio: () => audio })
  let ends = 0
  sp.on('end', () => ends++)
  const first = sp.say(['old'])
  await tick()
  const second = sp.say(['new'])
  await first
  assert.equal(calls[0].signal.aborted, true)
  assert.equal(ends, 0, 'the superseded run emits nothing')
  await tick()
  calls[1].resolve()
  await tick()
  assert.equal(ends, 0)
  audio.finish()
  await second
  assert.equal(ends, 1)
  assert.equal(audio.played.length, 1)
})

test('a blocked play emits blocked and resume continues from that chunk', async (t) => {
  spyUrls(t)
  const audio = fakeAudio()
  const { speak, calls } = fakeSpeak()
  const sp = createSpeaker({ speak, makeAudio: () => audio })
  const events = []
  sp.on('blocked', () => events.push('blocked'))
  sp.on('start', () => events.push('start'))
  sp.on('end', () => events.push('end'))
  audio.playError = Object.assign(new Error('no gesture'), { name: 'NotAllowedError' })
  const done = sp.say(['one', 'two'])
  await tick()
  calls[0].resolve()
  await tick()
  assert.deepEqual(events, ['blocked'])
  audio.playError = null
  sp.resume()
  await tick()
  assert.equal(audio.played.length, 1)
  assert.equal(calls.filter(c => c.text === 'one').length, 1, 'chunk 1 is not fetched again')
  audio.finish()
  await tick()
  calls[1].resolve()
  await tick()
  audio.finish()
  await done
  assert.deepEqual(events, ['blocked', 'start', 'end'])
})

test('a failed speech request emits error and not end', async (t) => {
  spyUrls(t)
  const audio = fakeAudio()
  const { speak, calls } = fakeSpeak()
  const sp = createSpeaker({ speak, makeAudio: () => audio })
  const events = []
  sp.on('error', (m) => events.push(['error', m]))
  sp.on('end', () => events.push('end'))
  const done = sp.say(['one'])
  await tick()
  calls[0].reject(new Error('model not loaded'))
  await done
  assert.deepEqual(events, [['error', 'model not loaded']])
})

test('object URLs are revoked', async (t) => {
  const { made, revoked } = spyUrls(t)
  const audio = fakeAudio()
  const { speak, calls } = fakeSpeak()
  const sp = createSpeaker({ speak, makeAudio: () => audio })
  const done = sp.say(['one', 'two'])
  await tick()
  calls[0].resolve()
  await tick()
  calls[1].resolve()
  await tick()
  audio.finish()
  await tick()
  audio.finish()
  await done
  assert.ok(made.length >= 2)
  assert.deepEqual([...revoked].sort(), [...made].sort())
})

test('stop revokes prefetched URLs and pauses the audio', async (t) => {
  const { made, revoked } = spyUrls(t)
  const audio = fakeAudio()
  const { speak, calls } = fakeSpeak()
  const sp = createSpeaker({ speak, makeAudio: () => audio })
  const done = sp.say(['one', 'two'])
  await tick()
  calls[0].resolve()
  await tick()
  calls[1].resolve()
  await tick()
  sp.stop()
  await done
  assert.ok(audio.paused >= 1)
  assert.deepEqual([...revoked].sort(), [...made].sort())
})

test('unlock plays a silent clip on the one persistent element', async (t) => {
  spyUrls(t)
  let made = 0
  const audio = fakeAudio()
  const sp = createSpeaker({ speak: async () => new Blob([]), makeAudio: () => { made++; return audio } })
  sp.unlock()
  await tick()
  assert.equal(made, 1)
  assert.equal(audio.played.length, 1)
})

test('a throwing end listener does not block the promise or stop()', async (t) => {
  spyUrls(t)
  const audio = fakeAudio()
  const { speak } = fakeSpeak()
  const sp = createSpeaker({ speak, makeAudio: () => audio })
  sp.on('end', () => { throw new Error('listener bug') })
  const done = sp.say(['one'])
  await tick()
  assert.doesNotThrow(() => sp.stop())
  await done
})

test('a throwing blocked listener does not become an error', async (t) => {
  spyUrls(t)
  const audio = fakeAudio()
  const { speak, calls } = fakeSpeak()
  const sp = createSpeaker({ speak, makeAudio: () => audio })
  const events = []
  sp.on('blocked', () => { throw new Error('listener bug') })
  sp.on('error', (m) => events.push(['error', m]))
  audio.playError = Object.assign(new Error('no gesture'), { name: 'NotAllowedError' })
  sp.say(['one'])
  await tick()
  calls[0].resolve()
  await tick()
  assert.deepEqual(events, [])
  sp.stop()
})

test('an audio error ends the run with one error and no end', async (t) => {
  const { made, revoked } = spyUrls(t)
  const audio = fakeAudio()
  const { speak, calls } = fakeSpeak()
  const sp = createSpeaker({ speak, makeAudio: () => audio })
  const events = []
  sp.on('error', (m) => events.push(['error', m]))
  sp.on('end', () => events.push('end'))
  const done = sp.say(['one', 'two'])
  await tick()
  calls[0].resolve()
  await tick()
  calls[1].resolve()
  await tick()
  audio.fail({ message: 'decode failed' })
  await done
  assert.deepEqual(events, [['error', 'decode failed']])
  assert.equal(audio.played.length, 1, 'remaining chunks are dropped')
  assert.deepEqual([...revoked].sort(), [...made].sort())
})

test('an audio error without detail uses a default message', async (t) => {
  spyUrls(t)
  const audio = fakeAudio()
  const { speak, calls } = fakeSpeak()
  const sp = createSpeaker({ speak, makeAudio: () => audio })
  const events = []
  sp.on('error', (m) => events.push(m))
  const done = sp.say(['one'])
  await tick()
  calls[0].resolve()
  await tick()
  audio.fail(null)
  await done
  assert.deepEqual(events, ['audio playback failed'])
})
