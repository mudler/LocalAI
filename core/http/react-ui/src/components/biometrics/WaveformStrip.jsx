import { useEffect, useRef } from 'react'
import useAudioPeaks from '../../hooks/useAudioPeaks'

// WaveformStrip — display-only waveform with optional colored segment
// overlays. For a player with click-to-seek + audio controls, use
// `components/audio/WaveformPlayer` instead. Both share the
// `useAudioPeaks` hook for peak extraction.
// segments: [{ start: seconds, end: seconds, label?, tone? }]
export default function WaveformStrip({ src, segments = [], height = 120 }) {
  const canvasRef = useRef(null)
  const { peaks, duration, error } = useAudioPeaks(src)

  useEffect(() => {
    if (!canvasRef.current || !peaks) return
    const canvas = canvasRef.current
    const dpr = window.devicePixelRatio || 1
    const cssW = canvas.clientWidth
    const cssH = height
    canvas.width = Math.floor(cssW * dpr)
    canvas.height = Math.floor(cssH * dpr)
    const ctx = canvas.getContext('2d')
    ctx.scale(dpr, dpr)
    ctx.clearRect(0, 0, cssW, cssH)

    const accent = getComputedStyle(canvas).getPropertyValue('--biometrics-wave').trim() || '#e8a87c'
    ctx.fillStyle = accent
    const mid = cssH / 2
    const barW = Math.max(1, cssW / peaks.length)
    for (let i = 0; i < peaks.length; i++) {
      const h = Math.max(1, peaks[i] * (cssH * 0.9))
      ctx.fillRect(i * barW, mid - h / 2, Math.max(0.5, barW - 0.5), h)
    }
  }, [peaks, height])

  if (error) return <div className="biometrics-waveform biometrics-waveform--error">{error}</div>
  if (!src) return null

  return (
    <div className="biometrics-waveform" style={{ height }}>
      <canvas ref={canvasRef} style={{ width: '100%', height: '100%' }} />
      {duration > 0 && segments.map((s, i) => {
        const left = (Math.max(0, s.start) / duration) * 100
        const right = (Math.min(duration, s.end) / duration) * 100
        return (
          <div key={i} className={`biometrics-waveform__segment tone-${s.tone || 'accent'}`}
            style={{ left: `${left}%`, width: `${Math.max(0.5, right - left)}%` }}>
            {s.label && <span className="biometrics-waveform__seglabel">{s.label}</span>}
          </div>
        )
      })}
      {duration > 0 && (
        <div className="biometrics-waveform__duration" aria-hidden="true">{duration.toFixed(1)}s</div>
      )}
      {!peaks && (
        <div className="biometrics-waveform__loading">Decoding…</div>
      )}
    </div>
  )
}
