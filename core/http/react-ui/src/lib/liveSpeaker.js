// Voice playback for Live. One persistent audio element plays the reply chunk
// by chunk: chunk i+1 is fetched while chunk i plays. The element is created
// once and unlocked from the click that starts Live, so browser autoplay rules
// allow the later, script-started playback.

function silentWavBlob() {
  const bytes = new Uint8Array(46)
  const view = new DataView(bytes.buffer)
  const text = (at, s) => { for (let i = 0; i < s.length; i++) bytes[at + i] = s.charCodeAt(i) }
  text(0, 'RIFF'); view.setUint32(4, 38, true); text(8, 'WAVE')
  text(12, 'fmt '); view.setUint32(16, 16, true); view.setUint16(20, 1, true)
  view.setUint16(22, 1, true); view.setUint32(24, 8000, true)
  view.setUint32(28, 16000, true); view.setUint16(32, 2, true); view.setUint16(34, 16, true)
  text(36, 'data'); view.setUint32(40, 2, true)
  return new Blob([bytes], { type: 'audio/wav' })
}

export function createSpeaker({ speak, makeAudio = () => new Audio() }) {
  const audio = makeAudio()
  const listeners = { start: new Set(), end: new Set(), blocked: new Set(), error: new Set() }
  let run = null

  // A listener that throws must not break playback or other listeners.
  const emit = (name, arg) => {
    for (const fn of [...listeners[name]]) {
      try { fn(arg) } catch { /* listener bug, ignored */ }
    }
  }

  // kind: 'end' | 'error' | 'silent' (superseded: the new run owns the events)
  function finish(r, kind, message) {
    if (r.done) return
    r.done = true
    r.ctrl.abort()
    if (run === r) run = null
    try { audio.pause() } catch { /* nothing to pause */ }
    for (const slot of r.slots.values()) {
      slot.promise.then((url) => { if (url) URL.revokeObjectURL(url) }, () => {})
    }
    r.slots.clear()
    r.release?.()
    r.resolve()
    if (kind === 'error') emit('error', message)
    else if (kind === 'end') emit('end')
  }

  // Fetch chunk idx once; the promise resolves to an object URL.
  function slotFor(r, idx) {
    let slot = r.slots.get(idx)
    if (slot) return slot
    slot = { promise: null, url: null }
    slot.promise = speak(r.chunks[idx], r.ctrl.signal).then((blob) => {
      const url = URL.createObjectURL(blob)
      slot.url = url
      if (r.done) { URL.revokeObjectURL(url); return null }
      return url
    })
    slot.promise.catch(() => {})
    r.slots.set(idx, slot)
    return slot
  }

  function waitEnded(r) {
    return new Promise((resolve) => {
      const settle = (outcome) => {
        audio.removeEventListener('ended', onEnded)
        audio.removeEventListener('error', onError)
        r.release = null
        resolve(outcome)
      }
      const onEnded = () => settle('ended')
      const onError = () => settle('error')
      r.release = () => settle('released')
      audio.addEventListener('ended', onEnded)
      audio.addEventListener('error', onError)
    })
  }

  async function play(r) {
    try {
      while (!r.done && r.i < r.chunks.length) {
        const slot = slotFor(r, r.i)
        const url = await slot.promise
        if (r.done || !url) return
        if (r.i + 1 < r.chunks.length) slotFor(r, r.i + 1)
        audio.src = url
        try {
          await audio.play()
        } catch (err) {
          if (r.done) return
          if (err && err.name === 'NotAllowedError') {
            r.blocked = true
            emit('blocked')
            return
          }
          throw err
        }
        if (r.done) return
        if (!r.started) { r.started = true; emit('start') }
        const outcome = await waitEnded(r)
        if (r.done) return
        if (outcome === 'error') {
          finish(r, 'error', audio.error?.message || 'audio playback failed')
          return
        }
        r.slots.delete(r.i)
        URL.revokeObjectURL(url)
        r.i++
      }
      finish(r, 'end')
    } catch (err) {
      if (r.done) return
      finish(r, 'error', err?.message || String(err))
    }
  }

  return {
    unlock() {
      if (run) return
      const url = URL.createObjectURL(silentWavBlob())
      audio.src = url
      const release = () => URL.revokeObjectURL(url)
      Promise.resolve(audio.play()).then(release, release)
    },
    say(chunks) {
      if (run) finish(run, 'silent')
      const r = {
        chunks: [...chunks], i: 0, ctrl: new AbortController(), slots: new Map(),
        done: false, started: false, blocked: false, release: null, resolve: null,
      }
      const promise = new Promise((resolve) => { r.resolve = resolve })
      run = r
      if (!r.chunks.length) { finish(r, 'end'); return promise }
      play(r)
      return promise
    },
    resume() {
      if (!run || !run.blocked) return
      run.blocked = false
      play(run)
    },
    stop() {
      if (run) finish(run, 'end')
    },
    on(event, fn) {
      listeners[event].add(fn)
      return () => listeners[event].delete(fn)
    },
  }
}
