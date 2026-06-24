import { useEffect, useState } from 'react'
import { getSharedAudioContext } from './useAudioPeaks'
import { fftRadix2 } from '../utils/fft'

// Hann windows are reused across frames and across clips, so cache one per
// size. The window tapers each frame to suppress spectral leakage (the
// vertical smearing you'd otherwise get from hard frame edges).
const windowCache = new Map()
function hann(n) {
  let w = windowCache.get(n)
  if (w) return w
  w = new Float32Array(n)
  for (let i = 0; i < n; i++) w[i] = 0.5 - 0.5 * Math.cos((2 * Math.PI * i) / (n - 1))
  windowCache.set(n, w)
  return w
}

const EMPTY = { spectrogram: null, frames: 0, bins: 0, maxFreq: 0, duration: 0, error: null, loading: false }

// useSpectrogram — decode an audio source (blob/data/http URL) and compute a
// magnitude STFT suitable for a spectrogram heatmap. Returns
// `{ spectrogram, frames, bins, maxFreq, duration, error, loading }` where
// `spectrogram` is a Float32Array of `frames * bins` values, row-major by
// frame, normalised so the dB floor maps to 0 and the loudest bin to 1.
// `bins` spans 0..Nyquist (`maxFreq`).
//
// fftSize/hop default to the LocalVQE frame geometry (512/256) so the picture
// lines up with how the model itself frames the audio. Long clips are
// strided down to at most `maxFrames` columns — the heatmap is only a few
// hundred px wide, so computing an FFT per native hop would be wasted work.
export default function useSpectrogram(
  src,
  { fftSize = 512, hop = 256, maxFrames = 900, dbFloor = -90 } = {},
) {
  const [state, setState] = useState(EMPTY)

  useEffect(() => {
    setState(EMPTY)
    if (!src) return
    let cancelled = false
    setState((s) => ({ ...s, loading: true }))

    async function run() {
      try {
        const resp = await fetch(src)
        const raw = await resp.arrayBuffer()
        const ctx = getSharedAudioContext()
        if (!ctx) throw new Error('Web Audio API not available')
        const audio = await ctx.decodeAudioData(raw.slice(0))
        if (cancelled) return

        const data = audio.getChannelData(0)
        const bins = fftSize >> 1
        const win = hann(fftSize)

        // Frame count, then a stride so we never run more than maxFrames FFTs.
        const rawFrames = data.length >= fftSize ? 1 + Math.floor((data.length - fftSize) / hop) : 1
        const stride = rawFrames > maxFrames ? Math.ceil(rawFrames / maxFrames) : 1
        const frames = Math.ceil(rawFrames / stride)

        const spec = new Float32Array(frames * bins)
        const re = new Float64Array(fftSize)
        const im = new Float64Array(fftSize)
        let peakDb = dbFloor

        for (let f = 0; f < frames; f++) {
          const start = f * stride * hop
          for (let i = 0; i < fftSize; i++) {
            const s = start + i
            re[i] = s < data.length ? data[s] * win[i] : 0
            im[i] = 0
          }
          fftRadix2(re, im)
          for (let b = 0; b < bins; b++) {
            const mag = Math.hypot(re[b], im[b]) / fftSize
            let db = mag > 0 ? 20 * Math.log10(mag) : dbFloor
            if (db < dbFloor) db = dbFloor
            spec[f * bins + b] = db
            if (db > peakDb) peakDb = db
          }
        }

        // Normalise dB into [0,1] against [dbFloor, peakDb].
        const range = peakDb - dbFloor || 1
        for (let i = 0; i < spec.length; i++) spec[i] = (spec[i] - dbFloor) / range

        if (cancelled) return
        setState({
          spectrogram: spec,
          frames,
          bins,
          maxFreq: audio.sampleRate / 2,
          duration: audio.duration,
          error: null,
          loading: false,
        })
      } catch (e) {
        if (!cancelled) setState((s) => ({ ...s, error: e?.message || 'Could not analyse audio', loading: false }))
      }
    }

    run()
    return () => { cancelled = true }
  }, [src, fftSize, hop, maxFrames, dbFloor])

  return state
}
