// A display-only waveform with optional coloured segment overlays. Ported from
// components/biometrics/WaveformStrip.jsx. Only the Voice add-on draws it, so
// it stays here. The peak extraction of the host hook (useAudioPeaks) is a
// small private copy: the host hook is not part of the SDK.

const BUCKETS = 480

// One AudioContext for the add-on: browsers cap the number of live contexts.
let shared = null
function audioContext() {
  if (shared) return shared
  const Ctx = typeof window === 'undefined' ? null : (window.AudioContext || window.webkitAudioContext)
  if (Ctx) shared = new Ctx()
  return shared
}

export function createWaveformStrip(ctx) {
  const { h } = ctx
  const { useEffect, useRef, useState } = ctx.React

  // Decode a data URL, blob URL or http URL into a mono peak array.
  // Safe under rapid changes of `src`: an older decode is dropped.
  function useAudioPeaks(src) {
    const [state, setState] = useState({ peaks: null, duration: 0, error: null })
    useEffect(() => {
      setState({ peaks: null, duration: 0, error: null })
      if (!src) return undefined
      let cancelled = false
      ;(async () => {
        try {
          const response = await fetch(src)
          const buffer = await response.arrayBuffer()
          const audio = audioContext()
          if (!audio) throw new Error('Web Audio API not available')
          const decoded = await audio.decodeAudioData(buffer.slice(0))
          if (cancelled) return
          const data = decoded.getChannelData(0)
          const step = Math.max(1, Math.floor(data.length / BUCKETS))
          const peaks = new Float32Array(BUCKETS)
          for (let i = 0; i < BUCKETS; i++) {
            let peak = 0
            const end = Math.min(i * step + step, data.length)
            for (let j = i * step; j < end; j++) peak = Math.max(peak, Math.abs(data[j]))
            peaks[i] = peak
          }
          setState({ peaks, duration: decoded.duration, error: null })
        } catch (err) {
          if (!cancelled) setState({ peaks: null, duration: 0, error: err?.message || 'Could not decode the audio' })
        }
      })()
      return () => { cancelled = true }
    }, [src])
    return state
  }

  // segments: [{ start: seconds, end: seconds, label?, tone? }]
  function WaveformStrip({ src, segments = [], height = 120 }) {
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
      const g = canvas.getContext('2d')
      g.scale(dpr, dpr)
      g.clearRect(0, 0, cssW, cssH)
      g.fillStyle = getComputedStyle(canvas).getPropertyValue('--biometrics-wave').trim() || '#e8a87c'
      const mid = cssH / 2
      const barW = Math.max(1, cssW / peaks.length)
      for (let i = 0; i < peaks.length; i++) {
        const barH = Math.max(1, peaks[i] * (cssH * 0.9))
        g.fillRect(i * barW, mid - barH / 2, Math.max(0.5, barW - 0.5), barH)
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
  return { WaveformStrip }
}
