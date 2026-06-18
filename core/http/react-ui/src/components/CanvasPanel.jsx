import { useState, useEffect, useRef } from 'react'
import { renderMarkdown } from '../utils/markdown'
import { getArtifactIcon, extensionForLanguage } from '../utils/artifacts'
import { safeHref } from '../utils/url'
import { copyToClipboard } from '../utils/clipboard'
import DOMPurify from 'dompurify'
import hljs from '../utils/hljs'

const WIDTH_KEY = 'localai_canvas_width'
const MIME_BY_EXT = { html: 'text/html', svg: 'image/svg+xml', json: 'application/json', css: 'text/css' }

export default function CanvasPanel({ artifacts, selectedId, onSelect, onClose }) {
  const [showPreview, setShowPreview] = useState(true)
  const [copySuccess, setCopySuccess] = useState(false)
  // Persisted drag-to-resize width (px). null = use the CSS default (45%).
  const [width, setWidth] = useState(() => {
    try { const v = localStorage.getItem(WIDTH_KEY); return v ? Number(v) : null } catch { return null }
  })
  const codeRef = useRef(null)
  const panelRef = useRef(null)

  const current = artifacts.find(a => a.id === selectedId) || artifacts[0]
  const hasPreview = !!current && current.type === 'code' && ['html', 'svg', 'md', 'markdown'].includes(current.language)

  // All hooks must run unconditionally (no early return above them).
  useEffect(() => {
    if (codeRef.current && !showPreview && current?.type === 'code') {
      codeRef.current.querySelectorAll('pre code').forEach(block => {
        hljs.highlightElement(block)
      })
    }
  }, [current, showPreview])

  // Drag the left edge to resize; clamp to a sane range; persist on release.
  const startResize = (e) => {
    e.preventDefault()
    const startX = e.clientX
    const startW = panelRef.current?.offsetWidth || 0
    const maxW = Math.round(window.innerWidth * 0.75)
    const onMove = (ev) => {
      const next = Math.min(Math.max(startW + (startX - ev.clientX), 360), maxW)
      setWidth(next)
    }
    const onUp = () => {
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
      document.body.style.userSelect = ''
      try { localStorage.setItem(WIDTH_KEY, String(panelRef.current?.offsetWidth || '')) } catch { /* ignore */ }
    }
    document.body.style.userSelect = 'none'
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
  }

  const resetWidth = () => {
    setWidth(null)
    try { localStorage.removeItem(WIDTH_KEY) } catch { /* ignore */ }
  }

  if (!current) return null

  const handleCopy = async () => {
    const text = current.code || current.url || ''
    const ok = await copyToClipboard(text)
    if (ok) {
      setCopySuccess(true)
      setTimeout(() => setCopySuccess(false), 2000)
    }
  }

  const handleDownload = () => {
    if (current.type === 'code') {
      const ext = extensionForLanguage(current.language)
      const blob = new Blob([current.code], { type: MIME_BY_EXT[ext] || 'text/plain' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      // Keep a title that already has an extension; otherwise slugify + add ext.
      a.download = current.title && /\.[a-z0-9]+$/i.test(current.title)
        ? current.title
        : `${(current.title || 'artifact').replace(/[^\w.-]+/g, '-').replace(/^-+|-+$/g, '') || 'artifact'}.${ext}`
      a.click()
      URL.revokeObjectURL(url)
    } else if (current.url) {
      const a = document.createElement('a')
      a.href = current.url
      a.download = current.title || 'download'
      a.target = '_blank'
      a.click()
    }
  }

  const renderBody = () => {
    if (current.type === 'image') {
      return <img src={current.url} alt={current.title} className="canvas-preview-image" />
    }
    if (current.type === 'pdf') {
      return <iframe src={current.url} className="canvas-preview-iframe" title={current.title} />
    }
    if (current.type === 'audio') {
      return (
        <div className="canvas-audio-wrapper">
          <i className="fas fa-music canvas-audio-icon" />
          <p>{current.title}</p>
          <audio controls src={current.url} style={{ width: '100%' }} />
        </div>
      )
    }
    if (current.type === 'video') {
      return <video controls src={current.url} className="canvas-preview-image" />
    }
    if (current.type === 'url') {
      return (
        <div className="canvas-url-card">
          <i className="fas fa-external-link-alt" />
          <a href={safeHref(current.url)} target="_blank" rel="noopener noreferrer">{current.url}</a>
        </div>
      )
    }
    if (current.type === 'file') {
      return (
        <div className="canvas-url-card">
          <i className="fas fa-file" />
          <a href={safeHref(current.url)} target="_blank" rel="noopener noreferrer" download={current.title}>{current.title}</a>
        </div>
      )
    }
    // Code artifacts
    if (showPreview && hasPreview) {
      if (current.language === 'html') {
        return <iframe srcDoc={current.code} sandbox="allow-scripts" className="canvas-preview-iframe" title="HTML Preview" />
      }
      if (current.language === 'svg') {
        return <div className="canvas-preview-svg" dangerouslySetInnerHTML={{
          __html: DOMPurify.sanitize(current.code, { USE_PROFILES: { svg: true, svgFilters: true } })
        }} />
      }
      if (current.language === 'md' || current.language === 'markdown') {
        return <div className="canvas-preview-markdown" dangerouslySetInnerHTML={{
          __html: renderMarkdown(current.code)
        }} />
      }
    }
    return (
      <pre ref={codeRef}><code className={current.language ? `language-${current.language}` : ''}>
        {current.code}
      </code></pre>
    )
  }

  return (
    <div className="canvas-panel" ref={panelRef} style={width ? { width: `${width}px`, maxWidth: 'none' } : undefined}>
      <div
        className="canvas-resize-handle"
        onMouseDown={startResize}
        onDoubleClick={resetWidth}
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize canvas (double-click to reset)"
        title="Drag to resize, double-click to reset"
      />
      <div className="canvas-panel-header">
        <span className="canvas-panel-title">{current.title || 'Artifact'}</span>
        <button className="btn btn-secondary btn-sm" onClick={onClose} title="Close canvas">
          <i className="fas fa-times" />
        </button>
      </div>

      {artifacts.length > 1 && (
        <div className="canvas-panel-tabs">
          {artifacts.map(a => (
            <button
              key={a.id}
              className={`canvas-panel-tab${a.id === (current?.id) ? ' active' : ''}`}
              onClick={() => onSelect(a.id)}
              title={a.title}
            >
              <i className={`fas ${getArtifactIcon(a.type, a.language)}`} />
              <span>{a.title}</span>
            </button>
          ))}
        </div>
      )}

      <div className="canvas-panel-toolbar">
        <span className="badge badge-sm">{current.type === 'code' ? current.language : current.type}</span>
        {hasPreview && (
          <div className="canvas-toggle-group">
            <button
              className={`canvas-toggle-btn${!showPreview ? ' active' : ''}`}
              onClick={() => setShowPreview(false)}
            >Code</button>
            <button
              className={`canvas-toggle-btn${showPreview ? ' active' : ''}`}
              onClick={() => setShowPreview(true)}
            >Preview</button>
          </div>
        )}
        <div style={{ flex: 1 }} />
        <button className="btn btn-secondary btn-sm" onClick={handleCopy} title="Copy">
          <i className={`fas ${copySuccess ? 'fa-check' : 'fa-copy'}`} />
        </button>
        <button className="btn btn-secondary btn-sm" onClick={handleDownload} title="Download">
          <i className="fas fa-download" />
        </button>
      </div>

      <div className="canvas-panel-body">
        {renderBody()}
      </div>
    </div>
  )
}
