import { useRef, useEffect } from 'react'

// Self-contained voice visualizer for the Talk page. Owns its own AudioContext
// and analysers (built from the output <audio> stream and the mic stream) so it
// does not touch the existing WebRTC/diagnostics graph. Renders frequency bars
// scaled by the mic level while listening and the assistant's output level
// while speaking; a gentle idle animation otherwise.
const BARS = 32

export default function VoiceVisualizer({ audioRef, micStreamRef, status, active }) {
  const canvasRef = useRef(null)
  const rafRef = useRef(null)
  const acRef = useRef(null)
  const outRef = useRef(null)
  const micRef = useRef(null)
  // Keep the latest status without restarting the animation loop.
  const statusRef = useRef(status)
  statusRef.current = status

  useEffect(() => {
    let setupTimer

    const setup = () => {
      if (!active) return
      try {
        const AC = window.AudioContext || window.webkitAudioContext
        if (!AC) return
        if (!acRef.current) acRef.current = new AC()
        const ac = acRef.current
        if (!outRef.current && audioRef.current?.srcObject) {
          const a = ac.createAnalyser(); a.fftSize = 1024; a.smoothingTimeConstant = 0.75
          ac.createMediaStreamSource(audioRef.current.srcObject).connect(a)
          outRef.current = a
        }
        if (!micRef.current && micStreamRef.current) {
          const a = ac.createAnalyser(); a.fftSize = 1024; a.smoothingTimeConstant = 0.75
          ac.createMediaStreamSource(micStreamRef.current).connect(a)
          micRef.current = a
        }
      } catch { /* analyser unavailable; idle animation still renders */ }
    }
    // The draw loop always runs (idle wave); analysers attach only once
    // connected, and streams can arrive a beat after connect.
    if (active) {
      setup()
      setupTimer = setInterval(() => { if (outRef.current && micRef.current) clearInterval(setupTimer); else setup() }, 400)
    }

    const draw = () => {
      rafRef.current = requestAnimationFrame(draw)
      const canvas = canvasRef.current
      if (!canvas) return
      const ctx = canvas.getContext('2d')
      const dpr = window.devicePixelRatio || 1
      const w = canvas.clientWidth || 1
      const h = canvas.clientHeight || 1
      if (canvas.width !== Math.round(w * dpr)) { canvas.width = Math.round(w * dpr); canvas.height = Math.round(h * dpr) }
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
      ctx.clearRect(0, 0, w, h)

      const st = statusRef.current
      const an = st === 'listening' ? micRef.current
        : st === 'speaking' ? outRef.current
        : (outRef.current || micRef.current)
      let data = null
      if (an) { data = new Uint8Array(an.frequencyBinCount); an.getByteFrequencyData(data) }

      const color = getComputedStyle(canvas).getPropertyValue('--viz-color').trim() || '#88c0d0'
      ctx.fillStyle = color
      const slot = w / BARS
      const bw = slot * 0.5
      const radius = bw / 2
      const now = Date.now()
      for (let i = 0; i < BARS; i++) {
        let level
        if (data) {
          const idx = Math.floor((i / BARS) * (data.length * 0.55))
          level = data[idx] / 255
        } else {
          level = 0.10 + 0.05 * Math.sin(now / 320 + i * 0.45)
        }
        const bh = Math.max(bw, level * h)
        const x = i * slot + (slot - bw) / 2
        const y = (h - bh) / 2
        // rounded bar
        ctx.beginPath()
        ctx.roundRect ? ctx.roundRect(x, y, bw, bh, radius) : ctx.rect(x, y, bw, bh)
        ctx.fill()
      }
    }
    draw()

    return () => {
      clearInterval(setupTimer)
      cancelAnimationFrame(rafRef.current)
    }
  }, [active, audioRef, micStreamRef])

  // Close the audio context only on final unmount.
  useEffect(() => () => {
    try { acRef.current?.close() } catch { /* ignore */ }
    acRef.current = null; outRef.current = null; micRef.current = null
  }, [])

  return <canvas ref={canvasRef} className={`voice-viz voice-viz--${status}`} aria-hidden="true" />
}
