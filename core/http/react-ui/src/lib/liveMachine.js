// The Live conversation loop as a plain state machine. It owns no browser API:
// the microphone, the voice detector, the speaker, the transcription call and
// the chat are injected through `deps`, so node tests drive it with fakes.
//
// Half-duplex rule: the voice detector is paused from the moment an utterance
// is accepted until the reply voice has ended plus a short hold (or until the
// turn is over when there is nothing to speak). The assistant never hears
// itself, and a person cannot talk over a running turn.
import { MicPermissionError } from './liveMic.js'
import { speechText, chunkSpeech, lastAssistantText } from '../utils/liveSpeech.js'

/** The detector stays paused this long after the voice ended (room echo). */
export const speechHoldMs = 400
/** A sent message that never reports a running turn is treated as over. */
export const sendGraceMs = 5000
/**
 * A message queued behind another turn is given this long, counted while the
 * chat is idle, to show its own turn; then the turn is read and ended.
 */
export const queuedWatchdogMs = 60_000
/** A transcription call that takes longer than this is given up. */
export const transcribeTimeoutMs = 90_000

const busyStates = new Set(['starting', 'transcribing', 'thinking', 'speaking', 'blocked', 'error', 'off'])

const messageOf = (err, fallback) => (err && err.message) || fallback

export function createLiveMachine(deps) {
  const { openMic, startVad, speaker, transcribe, send, getMessages } = deps
  // Optional: stops the chat's running turn.
  const stopTurn = deps.stop
  const later = (fn, ms) => (deps.setTimeout || globalThis.setTimeout)(fn, ms)
  const unlater = (id) => (deps.clearTimeout || globalThis.clearTimeout)(id)

  let state = { name: 'off' }
  const subscribers = new Set()
  // Bumped on every start and end; async work from an older run is dropped.
  let epoch = 0
  let mic = null
  let vad = null
  let offSpeaker = []
  let muted = false
  let hidden = false
  let holdTimer = null
  let graceTimer = null
  let watchdog = null
  let turn = null
  // The chat is running a turn (ours or a typed one): the person cannot be
  // heard until it is over.
  let chatBusy = false
  let abortTranscribe = null

  const clearTimers = () => {
    if (holdTimer) unlater(holdTimer)
    if (graceTimer) unlater(graceTimer)
    holdTimer = null
    graceTimer = null
    if (watchdog) unlater(watchdog)
    watchdog = null
  }

  const syncVad = () => {
    if (!vad) return
    if (busyStates.has(state.name) || muted || hidden || chatBusy) vad.pause()
    else vad.resume()
  }

  const setState = (name, error) => {
    state = error ? { name, error } : { name }
    syncVad()
    for (const fn of [...subscribers]) {
      try { fn(state) } catch { /* subscriber bug, ignored */ }
    }
  }

  const rest = () => setState(muted || hidden ? 'paused' : 'listening')
  // The microphone stays open in an error state, so that retry can continue at
  // once; the browser's recording indicator stays on until Live ends.
  const fail = (code, message) => {
    clearTimers()
    turn = null
    setState('error', { code, message })
  }

  function release() {
    for (const off of offSpeaker) off()
    offSpeaker = []
    const v = vad
    const m = mic
    vad = null
    mic = null
    try { v?.stop() } catch { /* already stopped */ }
    try { m?.close() } catch { /* already closed */ }
  }

  async function begin() {
    const mine = ++epoch
    clearTimers()
    // A start from an error state still holds the old mic, detector and
    // listeners; free them so nothing is left open behind the new ones.
    release()
    setState('starting')
    let opened
    try {
      opened = await openMic()
    } catch (err) {
      if (mine !== epoch) return
      if (err instanceof MicPermissionError) fail('denied', 'Microphone access was denied.')
      else fail('unavailable', messageOf(err, 'The microphone is not available.'))
      return
    }
    if (mine !== epoch) {
      try { opened.close() } catch { /* already closed */ }
      return
    }
    mic = opened
    try {
      vad = startVad({
        mic,
        onUtterance: (blob) => dispatch('utterance', blob),
        onState: (s) => dispatch('vadState', s),
      })
    } catch (err) {
      release()
      fail('unavailable', messageOf(err, 'The microphone is not available.'))
      return
    }
    offSpeaker = [
      speaker.on('start', () => dispatch('speechStart')),
      speaker.on('end', () => dispatch('speechEnd')),
      speaker.on('blocked', () => dispatch('speechBlocked')),
      speaker.on('error', (message) => dispatch('speechError', message)),
    ]
    rest()
  }

  async function onUtterance(blob) {
    const mine = epoch
    setState('transcribing')
    const ctrl = new AbortController()
    let timedOut = false
    abortTranscribe = () => ctrl.abort()
    const timer = later(() => { timedOut = true; ctrl.abort() }, transcribeTimeoutMs)
    let text
    try {
      const res = await transcribe(blob, ctrl.signal)
      text = String(typeof res === 'string' ? res : res?.text || '').trim()
    } catch (err) {
      if (mine === epoch) fail('transcribe', timedOut ? 'Transcription timed out.' : messageOf(err, 'Transcription failed.'))
      return
    } finally {
      unlater(timer)
      abortTranscribe = null
    }
    if (mine !== epoch || state.name !== 'transcribing') return
    // Muted or hidden while the clip was being read: the person asked for
    // silence, so the message is not sent.
    if (!text || muted || hidden || chatBusy) { rest(); return }
    await onTranscript(text)
  }

  async function onTranscript(text) {
    const mine = epoch
    turn = { seqBefore: 0, started: false, stopped: false, queued: false }
    const mineTurn = turn
    setState('thinking')
    try {
      const lines = await getMessages()
      mineTurn.seqBefore = (lines || []).reduce((n, m) => Math.max(n, m?.seq || 0), 0)
    } catch { /* unread transcript: the last user line still bounds the reply */ }
    if (mine !== epoch || turn !== mineTurn) return
    let sent
    try {
      sent = await send(text)
    } catch (err) {
      if (mine === epoch && turn === mineTurn) fail('send', messageOf(err, 'The message could not be sent.'))
      return
    }
    if (mine !== epoch || turn !== mineTurn || state.name !== 'thinking' || mineTurn.started) return
    // A message queued behind another turn (started: false) is delivered
    // later; its own turn is then reported, so no grace applies.
    if (sent && sent.started === false) { mineTurn.queued = true; armWatchdog(); return }
    graceTimer = later(() => {
      graceTimer = null
      dispatch('turnEnded', { failed: false })
    }, sendGraceMs)
  }

  // Only while the chat is idle and the turn has not shown itself: a busy chat
  // may be running our queued turn, or the one in front of it.
  function armWatchdog() {
    if (watchdog || !turn || turn.started || !turn.queued || chatBusy || state.name !== 'thinking') return
    watchdog = later(() => {
      watchdog = null
      dispatch('turnEnded', { failed: false })
    }, queuedWatchdogMs)
  }

  async function onTurnEnded(failed) {
    const mine = epoch
    const mineTurn = turn
    if (graceTimer) { unlater(graceTimer); graceTimer = null }
    if (watchdog) { unlater(watchdog); watchdog = null }
    if (failed || mineTurn.stopped) { turn = null; rest(); return }
    let lines
    try {
      lines = await getMessages()
    } catch (err) {
      if (mine === epoch && turn === mineTurn) fail('turn', messageOf(err, 'The reply could not be read.'))
      return
    }
    if (mine !== epoch || turn !== mineTurn || state.name !== 'thinking') return
    turn = null
    const chunks = chunkSpeech(speechText(lastAssistantText(lines, mineTurn.seqBefore)))
    if (!chunks.length) { rest(); return }
    setState('speaking')
    Promise.resolve(speaker.say(chunks)).catch(() => {})
  }

  const handlers = {
    start() {
      if (state.name !== 'off' && state.name !== 'error') return undefined
      return begin()
    },
    utterance(blob) {
      if (state.name !== 'listening' && state.name !== 'hearing') return undefined
      return onUtterance(blob)
    },
    vadState(s) {
      if (s === 'hearing' && state.name === 'listening') setState('hearing')
      else if (s === 'listening' && state.name === 'hearing') setState('listening')
      return undefined
    },
    busy(value) {
      chatBusy = Boolean(value)
      if (chatBusy && state.name === 'hearing') setState('listening')
      else syncVad()
      if (chatBusy) { if (watchdog) { unlater(watchdog); watchdog = null } } else armWatchdog()
    },
    turnStarted() {
      if (state.name !== 'thinking' || !turn) return
      turn.started = true
      if (graceTimer) { unlater(graceTimer); graceTimer = null }
      if (watchdog) { unlater(watchdog); watchdog = null }
    },
    turnEnded(arg) {
      if (state.name !== 'thinking' || !turn) return undefined
      return onTurnEnded(Boolean(arg?.failed))
    },
    speechStart() {
      if (state.name === 'blocked') setState('speaking')
    },
    speechEnd() {
      if ((state.name !== 'speaking' && state.name !== 'blocked') || holdTimer) return
      holdTimer = later(() => {
        holdTimer = null
        if (state.name === 'speaking' || state.name === 'blocked') rest()
      }, speechHoldMs)
    },
    speechBlocked() {
      if (state.name === 'speaking') setState('blocked')
    },
    speechError(message) {
      if (state.name !== 'speaking' && state.name !== 'blocked') return
      fail('speech', typeof message === 'string' && message ? message : 'The voice could not be played.')
    },
    mute() {
      muted = true
      if (state.name === 'listening' || state.name === 'hearing') setState('paused')
    },
    unmute() {
      muted = false
      if (state.name === 'paused' && !hidden) setState('listening')
    },
    hidden() {
      hidden = true
      if (state.name === 'listening' || state.name === 'hearing') setState('paused')
    },
    visible() {
      hidden = false
      if (state.name === 'paused' && !muted) setState('listening')
    },
    stopVoice() {
      // Before the reply: stop the turn itself; its text is not spoken.
      if (state.name === 'thinking' && turn) {
        turn.stopped = true
        try { stopTurn?.() } catch { /* the turn ends by itself or by the grace */ }
        // A turn that has not started has no end to wait for.
        if (!turn.started) { clearTimers(); turn = null; rest() }
        return
      }
      if (state.name !== 'speaking' && state.name !== 'blocked') return
      clearTimers()
      // Leave the state first, so the end event of the stop is ignored.
      rest()
      speaker.stop()
    },
    retry() {
      if (state.name !== 'error') return undefined
      const code = state.error?.code
      if (code === 'denied' || code === 'unavailable') return begin()
      rest()
      return undefined
    },
    end() {
      if (state.name === 'off' && !mic && !vad) return
      epoch++
      clearTimers()
      turn = null
      // The next Live starts with the microphone on.
      muted = false
      abortTranscribe?.()
      release()
      try { speaker.stop() } catch { /* nothing playing */ }
      setState('off')
    },
  }

  function dispatch(type, arg) {
    const handler = handlers[type]
    return handler ? handler(arg) : undefined
  }

  return {
    get state() { return state },
    dispatch,
    subscribe(fn) {
      subscribers.add(fn)
      return () => subscribers.delete(fn)
    },
  }
}
