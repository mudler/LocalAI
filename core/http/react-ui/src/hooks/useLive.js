import { useCallback, useEffect, useRef, useSyncExternalStore } from 'react'
import { audioApi, chatsApi, liveApi } from '../utils/api'
import { openMic, micAvailable } from '../lib/liveMic'
import { startVad } from '../lib/liveVad'
import { createSpeaker } from '../lib/liveSpeaker'
import { createLiveMachine } from '../lib/liveMachine'
import { createTurnTracker } from '../lib/liveTurn'

// Why this page cannot open a microphone: 'insecure' (the browser gives one
// only over HTTPS or localhost), 'no-mic' (none is offered), or '' when it can.
// Home asks the same question before it creates a chat for Live.
export function liveBlockedReason() {
  const secure = typeof window === 'undefined' || window.isSecureContext !== false
  if (!secure) return 'insecure'
  if (!micAvailable()) return 'no-mic'
  return ''
}

// Hands-free voice conversation with the open chat. The loop itself lives in
// lib/liveMachine.js; this hook wires the real microphone, speaker, speech
// calls and chat to it and reports the end of a turn.
//
// The turn is followed by lib/liveTurn.js from the chat's `running` and
// `failure`, from the moment the message is sent. The chat stays running while
// a command waits for a yes, so a consent card holds the turn open. A turn the
// person stops, or one that ends in a new failure, has no reply to speak.
export function useLive({ chatId, running, failure = null, send, stop, model }) {
  // The machine is made once; its deps read the latest props through this ref.
  const latest = useRef({ chatId, running, failure, send, stop, model })
  latest.current = { chatId, running, failure, send, stop, model }
  const box = useRef(null)
  if (!box.current) {
    const speaker = createSpeaker({
      speak: (text, signal) => liveApi.speak(latest.current.model?.tts, text, signal),
    })
    const tracker = createTurnTracker()
    const machine = createLiveMachine({
      openMic,
      startVad,
      speaker,
      transcribe: async (blob, signal) => {
        const fd = new FormData()
        fd.append('file', blob, 'live.wav')
        fd.append('model', latest.current.model?.stt || '')
        return audioApi.transcribe(fd, signal)
      },
      // The turn is followed from the moment the message is out (see liveTurn.js).
      send: async (text) => {
        const res = await latest.current.send(text)
        const { running: now, failure: fail } = latest.current
        if (tracker.arm({ started: res?.started, running: now, failure: fail }) === 'started') {
          machine.dispatch('turnStarted')
        }
        return res
      },
      stop: () => latest.current.stop?.(),
      getMessages: async () => (await chatsApi.messages(latest.current.chatId)).messages || [],
    })
    box.current = { speaker, machine, tracker }
  }
  const { speaker, machine } = box.current

  const state = useSyncExternalStore(machine.subscribe, () => machine.state)

  const unavailableReason = liveBlockedReason() || (!model?.stt ? 'no-stt' : !model?.tts ? 'no-tts' : '')

  // The chat's flags drive the machine: while the chat runs no utterance is
  // accepted, and the tracker reports the start and end of the voice turn.
  useEffect(() => {
    const { tracker } = box.current
    machine.dispatch('busy', running)
    const ev = tracker.observe({ running, failure })
    if (ev === 'started') machine.dispatch('turnStarted')
    else if (ev && ev.ended) machine.dispatch('turnEnded', { failed: ev.failed })
  }, [running, failure, machine])
  useEffect(() => {
    if (state.name !== 'thinking') box.current.tracker.reset()
  }, [state.name])

  useEffect(() => {
    const onVisibility = () => machine.dispatch(document.hidden ? 'hidden' : 'visible')
    document.addEventListener('visibilitychange', onVisibility)
    return () => document.removeEventListener('visibilitychange', onVisibility)
  }, [machine])

  // Leaving the page or switching chats releases the microphone and the voice.
  useEffect(() => () => machine.dispatch('end'), [machine, chatId])

  // Must run from a click: unlock() lets the browser play audio later.
  const start = useCallback(async () => {
    speaker.unlock()
    await machine.dispatch('start')
  }, [speaker, machine])
  const end = useCallback(() => machine.dispatch('end'), [machine])
  const mute = useCallback(() => machine.dispatch('mute'), [machine])
  const unmute = useCallback(() => machine.dispatch('unmute'), [machine])
  const retry = useCallback(() => {
    speaker.unlock()
    machine.dispatch('retry')
  }, [speaker, machine])
  // Stops the voice. Before the reply it stops the turn (the machine calls
  // stop), and the turn's partial text is not spoken.
  const stopVoice = useCallback(() => {
    machine.dispatch('stopVoice')
  }, [machine])
  // Continues a voice the browser blocked.
  const resume = useCallback(() => speaker.resume(), [speaker])

  return { state, available: unavailableReason === '', unavailableReason, start, end, mute, unmute, stopVoice, retry, resume }
}
