// A reference recording for a speech voice: decoded and written back as 24 kHz
// 16-bit mono WAV, the form the voice library stores.
import { audioBufferToWavBlob } from '../hooks/useMediaCapture'

export const MAX_AUDIO_BYTES = 50 * 1024 * 1024
const REFERENCE_SAMPLE_RATE = 24000

function base64ToArrayBuffer(value) {
  const binary = window.atob(value)
  const bytes = new Uint8Array(binary.length)
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index)
  return bytes.buffer
}

export async function normalizeAudioSample(sample) {
  const source = sample.blob?.arrayBuffer
    ? await sample.blob.arrayBuffer()
    : base64ToArrayBuffer(sample.base64)
  const AudioCtx = window.AudioContext || window.webkitAudioContext
  if (!AudioCtx) throw new Error('Web Audio API is not available in this browser')
  const context = new AudioCtx()
  try {
    const decoded = await context.decodeAudioData(source.slice(0))
    const blob = audioBufferToWavBlob(decoded, REFERENCE_SAMPLE_RATE)
    if (blob.size > MAX_AUDIO_BYTES) throw new Error('Normalized audio is larger than 50 MiB')
    return {
      ...sample,
      blob,
      dataUrl: URL.createObjectURL(blob),
      objectUrl: true,
      mime: 'audio/wav',
      duration: decoded.duration,
      sampleRate: REFERENCE_SAMPLE_RATE,
      name: sample.name || 'recording.wav',
    }
  } finally {
    await context.close().catch(() => {})
  }
}
