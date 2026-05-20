import { useEffect, useRef, useState } from 'react'
import useAudioPeaks from '../../hooks/useAudioPeaks'

// WaveformPlayer — reusable audio player combining a standard <audio
// controls> element with a peak-waveform canvas overlay and a click-to-seek
// playhead. The peaks canvas redraws only on src/height/dimmed changes; the
// playhead is a separately positioned div so 4 Hz timeupdate ticks don't
// retrigger the canvas loop.
export default function WaveformPlayer({
  src,
  height = 96,
  label,
  download,
  dimmed = false,
  audioTestId,
}) {
  const canvasRef = useRef(null)
  const audioRef = useRef(null)
  const trackRef = useRef(null)
  const { peaks, duration, error } = useAudioPeaks(src)
  const [currentTime, setCurrentTime] = useState(0)

  useEffect(() => {
    const a = audioRef.current
    if (!a) return
    const onUpdate = () => setCurrentTime(a.currentTime)
    a.addEventListener('timeupdate', onUpdate)
    return () => a.removeEventListener('timeupdate', onUpdate)
  }, [])

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
    if (!peaks) return

    const accent =
      getComputedStyle(canvas).getPropertyValue('--audio-wave').trim() ||
      getComputedStyle(canvas).getPropertyValue('--color-primary').trim() ||
      '#88c0d0'
    ctx.fillStyle = dimmed ? withAlpha(accent, 0.32) : accent
    const mid = cssH / 2
    const barW = Math.max(1, cssW / peaks.length)
    for (let i = 0; i < peaks.length; i++) {
      const h = Math.max(1, peaks[i] * (cssH * 0.9))
      ctx.fillRect(i * barW, mid - h / 2, Math.max(0.5, barW - 0.5), h)
    }
  }, [peaks, height, dimmed])

  const handleSeek = (e) => {
    const a = audioRef.current
    if (!a || !duration) return
    const rect = e.currentTarget.getBoundingClientRect()
    const ratio = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width))
    a.currentTime = ratio * duration
    setCurrentTime(a.currentTime)
  }

  if (!src) return null

  const playheadPct = duration > 0 ? Math.min(100, (currentTime / duration) * 100) : 0

  return (
    <div className={`audio-waveform-player${dimmed ? ' audio-waveform-player--dimmed' : ''}`}>
      {label && <div className="audio-waveform-player__label">{label}</div>}
      <div
        ref={trackRef}
        className="audio-waveform-player__canvas-wrap"
        style={{ height }}
        onClick={handleSeek}
      >
        {error ? (
          <div className="audio-waveform-player__error">{error}</div>
        ) : (
          <canvas
            ref={canvasRef}
            style={{ width: '100%', height: '100%', cursor: duration > 0 ? 'pointer' : 'default' }}
          />
        )}
        {duration > 0 && (
          <div
            className="audio-waveform-player__playhead"
            style={{ left: `${playheadPct}%` }}
            aria-hidden="true"
          />
        )}
        {duration > 0 && (
          <div className="audio-waveform-player__duration" aria-hidden="true">{duration.toFixed(1)}s</div>
        )}
        {!peaks && !error && (
          <div className="audio-waveform-player__loading">Decoding…</div>
        )}
      </div>
      <audio
        ref={audioRef}
        controls
        src={src}
        className="audio-waveform-player__player"
        data-testid={audioTestId}
      />
      {download && (
        <a className="audio-waveform-player__download" href={src} download={download}>
          Download
        </a>
      )}
    </div>
  )
}

function withAlpha(color, alpha) {
  if (!color) return color
  const c = color.trim()
  if (c.startsWith('#') && c.length === 7) {
    const r = parseInt(c.slice(1, 3), 16)
    const g = parseInt(c.slice(3, 5), 16)
    const b = parseInt(c.slice(5, 7), 16)
    return `rgba(${r}, ${g}, ${b}, ${alpha})`
  }
  if (c.startsWith('rgb(')) {
    return c.replace('rgb(', 'rgba(').replace(')', `, ${alpha})`)
  }
  return c
}
