import { useRef, useEffect } from 'react'

// The Talk page's orb. A calm outline that breathes while idle, follows the
// microphone while it listens and the reply's audio while it speaks. It owns
// its own AudioContext and analysers (built from the output <audio> stream and
// the mic stream), so it does not touch the WebRTC or diagnostics graph. The
// levels are the real ones; nothing is simulated except the idle breathing.
//
// `status` is what the page is doing (connecting, listening, thinking,
// speaking, error and so on) and `interrupted` marks a reply that was cut off.
// Colours come from custom properties on the canvas, so the theme owns them.

function readColors(canvas) {
  const cs = getComputedStyle(canvas)
  const get = (name, fallback) => cs.getPropertyValue(name).trim() || fallback
  return {
    ring: get('--orb-ring', '#cccccc'),
    line: get('--orb-line', '#444444'),
    accent: get('--orb-accent', '#0b6663'),
    warn: get('--orb-warn', '#8a5a00'),
    error: get('--orb-error', '#b3261e'),
    quiet: get('--orb-quiet', '#888888'),
    fill: get('--orb-fill', '#eeeeee'),
  }
}

export default function VoiceVisualizer({ audioRef, micStreamRef, status, active, interrupted = false }) {
  const canvasRef = useRef(null)
  const rafRef = useRef(null)
  const acRef = useRef(null)
  const outRef = useRef(null)
  const micRef = useRef(null)
  // Keep the latest state without restarting the animation loop.
  const stateRef = useRef({ status, interrupted })
  stateRef.current = { status, interrupted }

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
        if (!micRef.current && micStreamRef.current && micStreamRef.current.getAudioTracks().length > 0) {
          const a = ac.createAnalyser(); a.fftSize = 1024; a.smoothingTimeConstant = 0.75
          ac.createMediaStreamSource(micStreamRef.current).connect(a)
          micRef.current = a
        }
      } catch { /* analyser unavailable; the idle breathing still draws */ }
    }
    // The draw loop always runs; analysers attach once connected, and the
    // streams can arrive a beat after connect.
    if (active) {
      setup()
      setupTimer = setInterval(() => { if (outRef.current && micRef.current) clearInterval(setupTimer); else setup() }, 400)
    }

    const reduce = typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches
    let level = 0
    let t = 0
    let spin = 0
    let last = 0

    const draw = (now) => {
      rafRef.current = requestAnimationFrame(draw)
      const canvas = canvasRef.current
      if (!canvas) return
      const dt = Math.min(0.05, ((now - last) / 1000) || 0.016)
      last = now
      if (!reduce) { t += dt; spin += dt * (stateRef.current.status === 'connecting' ? 3.2 : 2.0) }
      const ctx = canvas.getContext('2d')
      const dpr = Math.min(2, window.devicePixelRatio || 1)
      const w = canvas.clientWidth || 1
      const h = canvas.clientHeight || 1
      if (canvas.width !== Math.round(w * dpr)) { canvas.width = Math.round(w * dpr); canvas.height = Math.round(h * dpr) }
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
      ctx.clearRect(0, 0, w, h)
      const c = readColors(canvas)

      const { status: st, interrupted: cut } = stateRef.current
      const an = st === 'listening' ? micRef.current : st === 'speaking' ? outRef.current : null
      let target = 0.02
      if (an) {
        const data = new Uint8Array(an.frequencyBinCount)
        an.getByteFrequencyData(data)
        let sum = 0
        const n = Math.max(1, Math.floor(data.length * 0.4))
        for (let i = 0; i < n; i++) sum += data[i]
        target = Math.min(1, (sum / n / 255) * 2.2)
      } else if (st === 'speaking') {
        target = 0.3
      }
      level += (target - level) * (cut ? 0.3 : 0.14)

      const cx = w / 2
      const cy = h / 2
      const R = Math.min(w, h) * 0.3
      const failed = st === 'error' || st === 'lost' || st === 'blocked'
      const still = st === 'disconnected' || st === 'connecting' || failed
      const amp = still ? 0.012 : 0.02 + 0.16 * level

      ctx.lineWidth = 1.25
      ctx.strokeStyle = c.ring
      ctx.beginPath(); ctx.arc(cx, cy, R * 1.42, 0, Math.PI * 2); ctx.stroke()
      ctx.setLineDash([2, 7])
      ctx.beginPath(); ctx.arc(cx, cy, R * 1.72, 0, Math.PI * 2); ctx.stroke()
      ctx.setLineDash([])

      if (failed) {
        ctx.strokeStyle = c.error
        ctx.lineWidth = 2
        ctx.setLineDash([6, 8])
        ctx.beginPath(); ctx.arc(cx, cy, R, 0, Math.PI * 2); ctx.stroke()
        ctx.setLineDash([])
        return
      }

      const color = st === 'speaking' ? c.accent : cut ? c.warn : (st === 'thinking' || still) ? c.quiet : c.line
      ctx.beginPath()
      for (let i = 0; i <= 120; i++) {
        const a = (i / 120) * Math.PI * 2
        const k = 0.5 * Math.sin(a * 3 + t * 1.2) + 0.3 * Math.sin(a * 5 - t * 1.7) + 0.2 * Math.sin(a * 8 + t * 2.3)
        const rr = R * (1 + amp * k * 2.2)
        const x = cx + Math.cos(a) * rr
        const y = cy + Math.sin(a) * rr
        if (i) ctx.lineTo(x, y); else ctx.moveTo(x, y)
      }
      ctx.closePath()
      ctx.fillStyle = c.fill
      ctx.fill()
      ctx.lineWidth = 2.25
      ctx.strokeStyle = color
      ctx.stroke()

      if (st === 'thinking' || st === 'connecting') {
        ctx.strokeStyle = c.accent
        ctx.lineWidth = 3.5
        ctx.lineCap = 'round'
        ctx.beginPath(); ctx.arc(cx, cy, R * 1.42, spin, spin + 1.1); ctx.stroke()
        ctx.beginPath(); ctx.arc(cx, cy, R * 1.42, spin + Math.PI, spin + Math.PI + 0.45); ctx.stroke()
        ctx.lineCap = 'butt'
      }
      if (st === 'speaking' || (st === 'listening' && level > 0.12)) {
        ctx.strokeStyle = st === 'speaking' ? c.accent : c.line
        ctx.globalAlpha = 0.35
        ctx.lineWidth = 1.5
        ctx.beginPath(); ctx.arc(cx, cy, R * (1.15 + level * 0.25), 0, Math.PI * 2); ctx.stroke()
        ctx.globalAlpha = 1
      }
      if (cut) {
        ctx.strokeStyle = c.warn
        ctx.lineWidth = 2
        for (let q = 0; q < 8; q++) {
          const aa = (q / 8) * Math.PI * 2 + 0.2
          ctx.beginPath()
          ctx.moveTo(cx + Math.cos(aa) * R * 1.14, cy + Math.sin(aa) * R * 1.14)
          ctx.lineTo(cx + Math.cos(aa) * R * 1.3, cy + Math.sin(aa) * R * 1.3)
          ctx.stroke()
        }
      }
    }
    rafRef.current = requestAnimationFrame(draw)

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

  return <canvas ref={canvasRef} className="talk-orb__canvas" aria-hidden="true" />
}
