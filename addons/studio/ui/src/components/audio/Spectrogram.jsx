import { useEffect, useRef } from 'react'
import useSpectrogram from '../../hooks/useSpectrogram'

// Spectrogram — canvas heatmap of a clip's magnitude STFT (time × frequency).
// Time runs left→right, frequency low→high bottom→top, brighter = more energy.
// Used on the AudioTransform page to show input next to output so the user can
// see which bands the model attenuates (dark gaps that were bright in the
// input). Mirrors WaveformPlayer's canvas/label/overlay structure.
export default function Spectrogram({ src, label, height = 140, testId }) {
  const canvasRef = useRef(null)
  const { spectrogram, frames, bins, maxFreq, duration, error, loading } = useSpectrogram(src)

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const dpr = window.devicePixelRatio || 1
    const cssW = canvas.clientWidth
    const cssH = height
    canvas.width = Math.floor(cssW * dpr)
    canvas.height = Math.floor(cssH * dpr)
    const ctx = canvas.getContext('2d')
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.clearRect(0, 0, cssW, cssH)
    if (!spectrogram || !frames || !bins) return

    // Paint at native (frames × bins) resolution into an offscreen canvas,
    // then let drawImage smooth-scale it up — far cheaper than filling
    // cssW×cssH rects, and the GPU handles the interpolation.
    const img = ctx.createImageData(frames, bins)
    for (let f = 0; f < frames; f++) {
      for (let b = 0; b < bins; b++) {
        const [r, g, bl] = magma(spectrogram[f * bins + b])
        // Flip the frequency axis: image row 0 is the top = highest freq.
        const o = ((bins - 1 - b) * frames + f) * 4
        img.data[o] = r
        img.data[o + 1] = g
        img.data[o + 2] = bl
        img.data[o + 3] = 255
      }
    }
    const off = document.createElement('canvas')
    off.width = frames
    off.height = bins
    off.getContext('2d').putImageData(img, 0, 0)
    ctx.imageSmoothingEnabled = true
    ctx.drawImage(off, 0, 0, cssW, cssH)
  }, [spectrogram, frames, bins, height])

  if (!src) return null

  return (
    <div className="audio-spectrogram">
      {label && <div className="audio-spectrogram__label">{label}</div>}
      <div className="audio-spectrogram__canvas-wrap" style={{ height }}>
        {error ? (
          <div className="audio-spectrogram__error">{error}</div>
        ) : (
          <canvas ref={canvasRef} data-testid={testId} style={{ width: '100%', height: '100%' }} />
        )}
        {maxFreq > 0 && !error && (
          <>
            <span className="audio-spectrogram__axis audio-spectrogram__axis--top">{fmtHz(maxFreq)}</span>
            <span className="audio-spectrogram__axis audio-spectrogram__axis--bottom">0 Hz</span>
          </>
        )}
        {duration > 0 && !error && (
          <span className="audio-spectrogram__duration">{duration.toFixed(1)}s</span>
        )}
        {loading && !error && <div className="audio-spectrogram__loading">Analysing…</div>}
      </div>
    </div>
  )
}

function fmtHz(hz) {
  if (hz >= 1000) return `${(hz / 1000).toFixed(hz % 1000 === 0 ? 0 : 1)} kHz`
  return `${Math.round(hz)} Hz`
}

// magma — compact perceptual colormap (black→purple→orange→white) sampled at 8
// control points and linearly interpolated. Perceptually uniform maps read
// far better for spectral magnitude than a raw hue ramp. v is clamped to [0,1].
const MAGMA = [
  [0, 0, 4],
  [40, 11, 84],
  [101, 21, 110],
  [159, 42, 99],
  [212, 72, 66],
  [245, 125, 21],
  [250, 193, 39],
  [252, 253, 191],
]
function magma(v) {
  const t = v <= 0 ? 0 : v >= 1 ? 1 : v
  const x = t * (MAGMA.length - 1)
  const i = Math.floor(x)
  const frac = x - i
  const a = MAGMA[i]
  const b = MAGMA[Math.min(i + 1, MAGMA.length - 1)]
  return [
    Math.round(a[0] + (b[0] - a[0]) * frac),
    Math.round(a[1] + (b[1] - a[1]) * frac),
    Math.round(a[2] + (b[2] - a[2]) * frac),
  ]
}
