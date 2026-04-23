import { useEffect, useRef, useState } from 'react'

// WaveformStrip — decode an audio source (data URL or blob URL) via AudioContext,
// render a mono waveform, and overlay colored segment regions.
// segments: [{ start: seconds, end: seconds, label?, tone? }]
export default function WaveformStrip({ src, segments = [], height = 120 }) {
  const canvasRef = useRef(null)
  const [duration, setDuration] = useState(0)
  const [peaks, setPeaks] = useState(null)
  const [err, setErr] = useState(null)

  useEffect(() => {
    setPeaks(null)
    setDuration(0)
    setErr(null)
    if (!src) return
    let cancelled = false

    async function decode() {
      try {
        const response = await fetch(src)
        const buf = await response.arrayBuffer()
        const Ctx = window.AudioContext || window.webkitAudioContext
        const ctx = new Ctx()
        const audioBuf = await ctx.decodeAudioData(buf.slice(0))
        if (cancelled) { ctx.close(); return }
        const data = audioBuf.getChannelData(0)
        const BUCKETS = 480
        const step = Math.max(1, Math.floor(data.length / BUCKETS))
        const result = new Float32Array(BUCKETS)
        for (let i = 0; i < BUCKETS; i++) {
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
        ctx.close()
      } catch (e) {
        if (!cancelled) setErr(e?.message || 'Could not decode audio')
      }
    }
    decode()
    return () => { cancelled = true }
  }, [src])

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

    // Waveform
    const accent = getComputedStyle(canvas).getPropertyValue('--biometrics-wave').trim() || '#e8a87c'
    ctx.fillStyle = accent
    const mid = cssH / 2
    const barW = Math.max(1, cssW / peaks.length)
    for (let i = 0; i < peaks.length; i++) {
      const h = Math.max(1, peaks[i] * (cssH * 0.9))
      ctx.fillRect(i * barW, mid - h / 2, Math.max(0.5, barW - 0.5), h)
    }
  }, [peaks, height])

  if (err) return <div className="biometrics-waveform biometrics-waveform--error">{err}</div>
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
