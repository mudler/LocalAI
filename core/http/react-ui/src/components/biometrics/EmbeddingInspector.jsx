import { useMemo, useRef, useEffect, useState } from 'react'

// EmbeddingInspector — compact visualization of a raw vector returned by /v1/face|voice/embed.
// embedding: number[] (can be large). dim: int. model: string.
export default function EmbeddingInspector({ embedding, dim, model, elapsedMs }) {
  const canvasRef = useRef(null)
  const [copied, setCopied] = useState(false)

  const summary = useMemo(() => {
    if (!embedding || !embedding.length) return null
    let sum = 0, sumSq = 0, min = Infinity, max = -Infinity
    for (const v of embedding) {
      sum += v
      sumSq += v * v
      if (v < min) min = v
      if (v > max) max = v
    }
    const mean = sum / embedding.length
    const norm = Math.sqrt(sumSq)
    return { mean, norm, min, max }
  }, [embedding])

  useEffect(() => {
    if (!canvasRef.current || !embedding?.length) return
    const canvas = canvasRef.current
    const dpr = window.devicePixelRatio || 1
    const cssW = canvas.clientWidth
    const cssH = 60
    canvas.width = Math.floor(cssW * dpr)
    canvas.height = Math.floor(cssH * dpr)
    const ctx = canvas.getContext('2d')
    ctx.scale(dpr, dpr)
    ctx.clearRect(0, 0, cssW, cssH)

    const COUNT = Math.min(embedding.length, 128)
    const values = embedding.slice(0, COUNT)
    const max = Math.max(...values.map(Math.abs)) || 1
    const mid = cssH / 2
    const barW = cssW / COUNT
    const accent = getComputedStyle(canvas).getPropertyValue('--color-accent').trim() || '#e8a87c'
    const accentMuted = getComputedStyle(canvas).getPropertyValue('--color-text-muted').trim() || '#6c7084'
    ctx.strokeStyle = accentMuted
    ctx.beginPath()
    ctx.moveTo(0, mid + 0.5)
    ctx.lineTo(cssW, mid + 0.5)
    ctx.stroke()
    ctx.fillStyle = accent
    for (let i = 0; i < COUNT; i++) {
      const v = values[i]
      const h = (Math.abs(v) / max) * (cssH * 0.45)
      if (v >= 0) ctx.fillRect(i * barW, mid - h, Math.max(0.5, barW - 0.5), h)
      else ctx.fillRect(i * barW, mid, Math.max(0.5, barW - 0.5), h)
    }
  }, [embedding])

  if (!embedding) return null

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(JSON.stringify(embedding))
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch (_) {
      /* clipboard gated */
    }
  }

  return (
    <div className="biometrics-embed card">
      <div className="biometrics-embed__head">
        <div>
          <div className="biometrics-embed__title">Embedding vector</div>
          <div className="biometrics-embed__meta">
            {dim != null && <span><strong>{dim}</strong> dims</span>}
            {summary && <span>L2 <strong>{summary.norm.toFixed(3)}</strong></span>}
            {summary && <span>range <strong>[{summary.min.toFixed(3)}, {summary.max.toFixed(3)}]</strong></span>}
            {model && <span>model <code>{model}</code></span>}
            {elapsedMs != null && <span>{elapsedMs.toFixed(0)} ms</span>}
          </div>
        </div>
        <button type="button" className="btn btn-secondary btn-sm" onClick={copy}>
          <i className={`fas ${copied ? 'fa-check' : 'fa-copy'}`} aria-hidden="true" />
          {copied ? ' Copied' : ' Copy JSON'}
        </button>
      </div>
      <canvas ref={canvasRef} style={{ width: '100%', height: 60 }} aria-label="Embedding sparkline (first 128 dimensions)" />
    </div>
  )
}
