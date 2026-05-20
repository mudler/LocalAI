import { useEffect, useState } from 'react'

// Module-scoped lazy AudioContext: WaveformPlayer / WaveformStrip / Strip can
// all coexist on a single page (the AudioTransform page mounts three at once)
// and most browsers cap concurrent AudioContexts at ~6. Keep one alive for
// the lifetime of the tab and reuse it across decodes.
let sharedCtx = null
function getSharedAudioContext() {
  if (sharedCtx) return sharedCtx
  const Ctx = window.AudioContext || window.webkitAudioContext
  if (!Ctx) return null
  sharedCtx = new Ctx()
  return sharedCtx
}

// useAudioPeaks — decode an audio source (data URL, blob URL, or http URL)
// into a mono peak array suitable for canvas waveform rendering. Returns
// `{ peaks, duration, error, loading }`. Safe under rapid src changes —
// in-flight decodes are cancelled.
export default function useAudioPeaks(src, buckets = 480) {
  const [peaks, setPeaks] = useState(null)
  const [duration, setDuration] = useState(0)
  const [error, setError] = useState(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    setPeaks(null)
    setDuration(0)
    setError(null)
    setLoading(false)
    if (!src) return
    let cancelled = false
    setLoading(true)

    async function decode() {
      try {
        const response = await fetch(src)
        const buf = await response.arrayBuffer()
        const ctx = getSharedAudioContext()
        if (!ctx) throw new Error('Web Audio API not available')
        const audioBuf = await ctx.decodeAudioData(buf.slice(0))
        if (cancelled) return
        const data = audioBuf.getChannelData(0)
        const step = Math.max(1, Math.floor(data.length / buckets))
        const result = new Float32Array(buckets)
        for (let i = 0; i < buckets; i++) {
          let peak = 0
          const start = i * step
          const end = Math.min(start + step, data.length)
          for (let j = start; j < end; j++) {
            const v = Math.abs(data[j])
            if (v > peak) peak = v
          }
          result[i] = peak
        }
        setPeaks(result)
        setDuration(audioBuf.duration)
        setLoading(false)
      } catch (e) {
        if (!cancelled) {
          setError(e?.message || 'Could not decode audio')
          setLoading(false)
        }
      }
    }
    decode()
    return () => { cancelled = true }
  }, [src, buckets])

  return { peaks, duration, error, loading }
}
