// Microphone capture as raw PCM, and the WAV encoder that packages it.
//
// Capture goes through an AudioWorklet instead of MediaRecorder for two
// reasons. First, the server opens a 16 kHz mono 16-bit PCM WAV without
// ffmpeg, so that is exactly what we send. Second, MediaRecorder delivers its
// data in events that fire after stop() returns, which makes "end this segment
// and start the next" a race. Here endSegment() is synchronous and returns the
// audio it closed over.

/** The sample rate the server takes without transcoding. */
export const targetSampleRate = 16000

/** Thrown when the user (or the OS) refused microphone access. */
export class MicPermissionError extends Error {
  constructor() {
    super('microphone permission denied')
    this.name = 'MicPermissionError'
  }
}

/** True when this browser can open a microphone at all. */
export function micAvailable() {
  return typeof navigator !== 'undefined' && typeof navigator.mediaDevices?.getUserMedia === 'function'
}

function concat(chunks) {
  let total = 0
  for (const c of chunks) total += c.length
  const out = new Float32Array(total)
  let at = 0
  for (const c of chunks) {
    out.set(c, at)
    at += c.length
  }
  return out
}

// Linear interpolation is enough for a downmix to 16 kHz for a speech model.
function resample(input, from, to) {
  if (from === to || input.length === 0) return input
  const ratio = from / to
  const out = new Float32Array(Math.round(input.length / ratio))
  for (let i = 0; i < out.length; i++) {
    const pos = i * ratio
    const low = Math.floor(pos)
    const high = Math.min(low + 1, input.length - 1)
    out[i] = input[low] + (input[high] - input[low]) * (pos - low)
  }
  return out
}

/**
 * Packages captured mono float samples as a 16 kHz 16-bit PCM WAV. `inputRate`
 * is the rate the samples were captured at; browsers may ignore the rate we
 * ask an AudioContext for, so anything else is resampled here.
 */
export function encodeWav(chunks, inputRate) {
  const mono = resample(concat(chunks), inputRate, targetSampleRate)
  const bytes = new ArrayBuffer(44 + mono.length * 2)
  const view = new DataView(bytes)
  const tag = (off, s) => {
    for (let i = 0; i < s.length; i++) view.setUint8(off + i, s.charCodeAt(i))
  }
  tag(0, 'RIFF')
  view.setUint32(4, 36 + mono.length * 2, true)
  tag(8, 'WAVE')
  tag(12, 'fmt ')
  view.setUint32(16, 16, true)
  view.setUint16(20, 1, true) // PCM
  view.setUint16(22, 1, true) // mono
  view.setUint32(24, targetSampleRate, true)
  view.setUint32(28, targetSampleRate * 2, true)
  view.setUint16(32, 2, true)
  view.setUint16(34, 16, true)
  tag(36, 'data')
  view.setUint32(40, mono.length * 2, true)
  for (let i = 0; i < mono.length; i++) {
    // Clamp first: a sample past +-1 would wrap to the opposite rail.
    const s = Math.max(-1, Math.min(1, mono[i]))
    view.setInt16(44 + i * 2, s < 0 ? s * 0x8000 : s * 0x7fff, true)
  }
  return new Blob([bytes], { type: 'audio/wav' })
}

/**
 * Opens the default microphone. Resolves to
 * { beginSegment(), endSegment(): Blob|null, level(): number, close() }.
 * Rejects with MicPermissionError when access was refused.
 */
export async function openMic() {
  if (!micAvailable()) throw new Error('no microphone api')
  let stream
  try {
    stream = await navigator.mediaDevices.getUserMedia({
      audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true, autoGainControl: true },
    })
  } catch (err) {
    const name = err?.name
    if (name === 'NotAllowedError' || name === 'SecurityError' || name === 'PermissionDeniedError') {
      throw new MicPermissionError()
    }
    throw err
  }

  let ctx = null
  let tap
  try {
    try {
      ctx = new AudioContext({ sampleRate: targetSampleRate })
    } catch {
      // Some runtimes reject a forced rate; take the default and resample.
      ctx = new AudioContext()
    }
    // A context made after an awaited getUserMedia can start suspended.
    if (ctx.state === 'suspended') await ctx.resume()
    await ctx.audioWorklet.addModule(new URL('./pcmTap.worklet.js', import.meta.url))
    const source = ctx.createMediaStreamSource(stream)
    tap = new AudioWorkletNode(ctx, 'pcm-tap')
    source.connect(tap)
    // A node with no path to the destination is not guaranteed to be pulled.
    const mute = ctx.createGain()
    mute.gain.value = 0
    tap.connect(mute).connect(ctx.destination)
  } catch (err) {
    stream.getTracks().forEach((t) => t.stop())
    if (ctx) void ctx.close().catch(() => {})
    throw err
  }

  let segment = null
  let windowSum = 0
  let windowCount = 0
  let lastLevel = 0
  let closed = false

  tap.port.onmessage = (e) => {
    const chunk = e.data
    if (segment) segment.push(chunk)
    for (let i = 0; i < chunk.length; i++) windowSum += chunk[i] * chunk[i]
    windowCount += chunk.length
  }

  return {
    beginSegment() {
      segment = []
    },
    endSegment() {
      const done = segment
      segment = null
      if (!done) return null
      return encodeWav(done, ctx.sampleRate)
    },
    // RMS of the audio since the previous call; the last value if none arrived.
    level() {
      if (windowCount === 0) return lastLevel
      lastLevel = Math.sqrt(windowSum / windowCount)
      windowSum = 0
      windowCount = 0
      return lastLevel
    },
    close() {
      if (closed) return
      closed = true
      segment = null
      tap.port.onmessage = null
      stream.getTracks().forEach((t) => t.stop())
      void ctx.close()
    },
  }
}
