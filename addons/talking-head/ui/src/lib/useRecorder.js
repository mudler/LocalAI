import { useCallback, useEffect, useRef, useState } from 'react'

// Push to talk: start() opens the microphone and records, stop() ends the
// recording and resolves with the clip (a Blob), or null when it was too short
// to hold a word. The microphone is open only while recording.
//
// state: idle | starting | recording | blocked | missing | error
export function useRecorder({ minMs = 300 } = {}) {
  const [state, setState] = useState('idle')
  const [elapsed, setElapsed] = useState(0)
  const recorderRef = useRef(null)
  const chunksRef = useRef([])
  const startedRef = useRef(0)
  const tickRef = useRef(null)
  const stopWaiterRef = useRef(null)
  // A release that comes while the microphone is still opening stops the
  // recording as soon as it starts.
  const pendingStopRef = useRef(false)

  const supported = typeof navigator !== 'undefined' && !!navigator.mediaDevices?.getUserMedia && typeof MediaRecorder !== 'undefined'

  const release = useCallback(() => {
    clearInterval(tickRef.current)
    tickRef.current = null
    const r = recorderRef.current
    r?.stream?.getTracks().forEach((t) => t.stop())
    recorderRef.current = null
  }, [])

  useEffect(() => () => {
    const r = recorderRef.current
    if (r && r.state !== 'inactive') { r.onstop = null; r.stop() }
    release()
  }, [release])

  const stop = useCallback(() => new Promise((resolve) => {
    const r = recorderRef.current
    if (!r) {
      if (state === 'starting') { pendingStopRef.current = true; stopWaiterRef.current = resolve; return }
      resolve(null)
      return
    }
    stopWaiterRef.current = resolve
    if (r.state !== 'inactive') r.stop()
  }), [state])

  const start = useCallback(async () => {
    if (!supported) { setState('missing'); return false }
    if (recorderRef.current) return true
    pendingStopRef.current = false
    setState('starting')
    let stream
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: true, noiseSuppression: true } })
    } catch (err) {
      const name = err?.name || ''
      setState(name === 'NotAllowedError' || name === 'SecurityError' ? 'blocked' : name === 'NotFoundError' ? 'missing' : 'error')
      stopWaiterRef.current?.(null)
      stopWaiterRef.current = null
      return false
    }
    const recorder = new MediaRecorder(stream)
    chunksRef.current = []
    recorder.ondataavailable = (e) => { if (e.data && e.data.size > 0) chunksRef.current.push(e.data) }
    recorder.onstop = () => {
      const ms = Date.now() - startedRef.current
      const blob = new Blob(chunksRef.current, { type: recorder.mimeType || 'audio/webm' })
      release()
      setState('idle')
      setElapsed(0)
      const done = stopWaiterRef.current
      stopWaiterRef.current = null
      done?.(ms < minMs || blob.size === 0 ? null : blob)
    }
    recorderRef.current = recorder
    startedRef.current = Date.now()
    recorder.start()
    setState('recording')
    tickRef.current = setInterval(() => setElapsed(Date.now() - startedRef.current), 100)
    if (pendingStopRef.current) recorder.stop()
    return true
  }, [supported, minMs, release])

  return { supported, state, elapsed, start, stop }
}
