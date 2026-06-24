import { useState, useEffect } from 'react'

// Loading feedback for slow media generation: shimmer placeholder tiles that
// match the requested count, plus a live elapsed-time readout. Replaces a bare
// spinner so the wait feels accountable.
export default function GenerationProgress({ count = 1, label }) {
  const [elapsed, setElapsed] = useState(0)
  useEffect(() => {
    const t = setInterval(() => setElapsed(e => e + 1), 1000)
    return () => clearInterval(t)
  }, [])
  const tiles = Math.min(Math.max(count, 1), 4)
  const mm = Math.floor(elapsed / 60)
  const ss = String(elapsed % 60).padStart(2, '0')
  return (
    <div className="gen-progress" role="status" aria-live="polite">
      <div className={`gen-progress__tiles gen-progress__tiles--n${tiles}`}>
        {Array.from({ length: tiles }).map((_, i) => (
          <span key={i} className="gen-progress__tile skeleton skeleton--block" aria-hidden="true" />
        ))}
      </div>
      <div className="gen-progress__status">
        <i className="fas fa-circle-notch fa-spin" aria-hidden="true" />
        <span>{label || 'Generating'}</span>
        <span className="gen-progress__time mono">{mm}:{ss}</span>
      </div>
    </div>
  )
}
