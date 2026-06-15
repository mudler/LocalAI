import { useCallback, useEffect, useRef, useState } from 'react'

// Encode an AudioBuffer as a 16-bit PCM mono WAV blob. Libsndfile (which the
// SpeechBrain / ONNX voice backends use) reads this shape without extra
// decoders. We downmix to mono because speaker-encoder models expect a single
// channel and sample-rate resampling is handled server-side.
function audioBufferToWavBlob(audioBuffer) {
  const sampleRate = audioBuffer.sampleRate
  const numFrames = audioBuffer.length
  const bitsPerSample = 16
  const blockAlign = bitsPerSample / 8 // mono, 1 channel
  const byteRate = sampleRate * blockAlign
  const dataSize = numFrames * blockAlign
  const out = new ArrayBuffer(44 + dataSize)
  const view = new DataView(out)

  const writeAscii = (offset, s) => {
    for (let i = 0; i < s.length; i++) view.setUint8(offset + i, s.charCodeAt(i))
  }
  writeAscii(0, 'RIFF')
  view.setUint32(4, 36 + dataSize, true)
  writeAscii(8, 'WAVE')
  writeAscii(12, 'fmt ')
  view.setUint32(16, 16, true)           // fmt chunk size
  view.setUint16(20, 1, true)            // PCM
  view.setUint16(22, 1, true)            // mono
  view.setUint32(24, sampleRate, true)
  view.setUint32(28, byteRate, true)
  view.setUint16(32, blockAlign, true)
  view.setUint16(34, bitsPerSample, true)
  writeAscii(36, 'data')
  view.setUint32(40, dataSize, true)

  // Average all input channels into mono, then clamp + convert to int16.
  const numChannels = audioBuffer.numberOfChannels
  const channels = []
  for (let c = 0; c < numChannels; c++) channels.push(audioBuffer.getChannelData(c))
  let offset = 44
  for (let i = 0; i < numFrames; i++) {
    let sum = 0
    for (let c = 0; c < numChannels; c++) sum += channels[c][i]
    const mono = Math.max(-1, Math.min(1, sum / numChannels))
    view.setInt16(offset, mono < 0 ? mono * 0x8000 : mono * 0x7FFF, true)
    offset += 2
  }
  return new Blob([out], { type: 'audio/wav' })
}

// useMediaCapture — wraps getUserMedia + MediaRecorder for the biometrics pages.
// mode: 'image' streams video-only for a snap-to-canvas; 'audio' records a clip via MediaRecorder.
// Consumers attach the returned videoRef to a <video autoPlay muted playsInline/> element.
export function useMediaCapture(mode) {
  const [active, setActive] = useState(false)
  const [recording, setRecording] = useState(false)
  const [error, setError] = useState(null)
  const [elapsed, setElapsed] = useState(0)

  const streamRef = useRef(null)
  const videoRef = useRef(null)
  const recorderRef = useRef(null)
  const chunksRef = useRef([])
  const tickRef = useRef(null)
  const resolveStopRef = useRef(null)

  const supported = typeof navigator !== 'undefined' && !!navigator.mediaDevices?.getUserMedia

  const stopStream = useCallback(() => {
    if (tickRef.current) {
      clearInterval(tickRef.current)
      tickRef.current = null
    }
    if (streamRef.current) {
      streamRef.current.getTracks().forEach(t => { try { t.stop() } catch (_) { /* ignore */ } })
      streamRef.current = null
    }
    if (videoRef.current) {
      try { videoRef.current.srcObject = null } catch (_) { /* ignore */ }
    }
    setActive(false)
    setRecording(false)
    setElapsed(0)
  }, [])

  const start = useCallback(async () => {
    if (!supported) {
      setError('Your browser does not support media capture.')
      return
    }
    setError(null)
    try {
      const constraints = mode === 'audio'
        ? { audio: true }
        : { video: { facingMode: 'user', width: { ideal: 640 }, height: { ideal: 480 } } }
      const stream = await navigator.mediaDevices.getUserMedia(constraints)
      streamRef.current = stream
      // Attachment happens in the useEffect below — videoRef.current is still
      // null at this point because the <video> element mounts only after React
      // processes the setActive(true) state change.
      setActive(true)
    } catch (e) {
      setError(e?.message || 'Could not access device')
      stopStream()
    }
  }, [mode, supported, stopStream])

  // Hook the stream into the <video> once both the stream and the element exist.
  useEffect(() => {
    if (mode !== 'image' || !active) return
    const v = videoRef.current
    const s = streamRef.current
    if (!v || !s) return
    if (v.srcObject !== s) v.srcObject = s
    const playPromise = v.play()
    if (playPromise && typeof playPromise.catch === 'function') {
      playPromise.catch(() => { /* autoplay gated */ })
    }
  }, [active, mode])

  // Snap a frame from the live video stream to a PNG base64 (image mode).
  const snap = useCallback(() => {
    if (mode !== 'image' || !videoRef.current || !streamRef.current) return null
    const v = videoRef.current
    const w = v.videoWidth || 640
    const h = v.videoHeight || 480
    const canvas = document.createElement('canvas')
    canvas.width = w
    canvas.height = h
    const ctx = canvas.getContext('2d')
    ctx.drawImage(v, 0, 0, w, h)
    const dataUrl = canvas.toDataURL('image/png')
    const base64 = dataUrl.split(',')[1] || ''
    return { base64, dataUrl, mime: 'image/png' }
  }, [mode])

  // Start an audio recording — returns a promise that resolves with a WAV-encoded
  // {base64, blob, dataUrl, mime} on stopRecording. Transcoding to 16-bit PCM mono
  // WAV is necessary because the voice backends open the file via libsndfile, which
  // doesn't handle WebM/Ogg-Opus containers — the browser's native MediaRecorder
  // output — out of the box.
  const startRecording = useCallback(() => {
    if (mode !== 'audio' || !streamRef.current) return null
    chunksRef.current = []
    const recMime = (typeof MediaRecorder !== 'undefined' && MediaRecorder.isTypeSupported('audio/webm;codecs=opus'))
      ? 'audio/webm;codecs=opus'
      : 'audio/webm'
    let rec
    try {
      rec = new MediaRecorder(streamRef.current, { mimeType: recMime })
    } catch (_) {
      rec = new MediaRecorder(streamRef.current)
    }
    recorderRef.current = rec
    rec.ondataavailable = (e) => { if (e.data && e.data.size > 0) chunksRef.current.push(e.data) }
    const donePromise = new Promise((resolve, reject) => {
      resolveStopRef.current = resolve
      rec.onstop = async () => {
        try {
          const recBlob = new Blob(chunksRef.current, { type: rec.mimeType || recMime })
          const arrayBuf = await recBlob.arrayBuffer()
          const Ctx = window.AudioContext || window.webkitAudioContext
          const ctx = new Ctx()
          const audioBuf = await ctx.decodeAudioData(arrayBuf.slice(0))
          const wavBlob = audioBufferToWavBlob(audioBuf)
          ctx.close()
          const dataUrl = await new Promise((res) => {
            const reader = new FileReader()
            reader.onloadend = () => res(reader.result)
            reader.readAsDataURL(wavBlob)
          })
          const base64 = typeof dataUrl === 'string' ? (dataUrl.split(',')[1] || '') : ''
          resolve({ blob: wavBlob, base64, dataUrl, mime: 'audio/wav' })
        } catch (err) {
          reject(err)
        } finally {
          resolveStopRef.current = null
        }
      }
    })
    rec.start()
    setRecording(true)
    setElapsed(0)
    const started = Date.now()
    tickRef.current = setInterval(() => setElapsed((Date.now() - started) / 1000), 100)
    return donePromise
  }, [mode])

  const stopRecording = useCallback(() => {
    if (recorderRef.current && recorderRef.current.state !== 'inactive') {
      recorderRef.current.stop()
    }
    if (tickRef.current) {
      clearInterval(tickRef.current)
      tickRef.current = null
    }
    setRecording(false)
  }, [])

  // Cleanup on unmount — always release the device.
  useEffect(() => () => stopStream(), [stopStream])

  return {
    supported, active, recording, error, elapsed,
    videoRef, start, stop: stopStream, snap, startRecording, stopRecording,
  }
}
