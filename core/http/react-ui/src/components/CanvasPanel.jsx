import { useState, useEffect, useRef } from 'react'
import { renderMarkdown } from '../utils/markdown'
import { getArtifactIcon } from '../utils/artifacts'
import DOMPurify from 'dompurify'
import hljs from 'highlight.js'

export default function CanvasPanel({ artifacts, selectedId, onSelect, onClose }) {
  const [showPreview, setShowPreview] = useState(true)
  const [copySuccess, setCopySuccess] = useState(false)
  const codeRef = useRef(null)

  const current = artifacts.find(a => a.id === selectedId) || artifacts[0]
  if (!current) return null

  const hasPreview = current.type === 'code' && ['html', 'svg', 'md', 'markdown'].includes(current.language)

  useEffect(() => {
    if (codeRef.current && !showPreview && current.type === 'code') {
      codeRef.current.querySelectorAll('pre code').forEach(block => {
        hljs.highlightElement(block)
      })
    }
  }, [current, showPreview])

  const handleCopy = () => {
    const text = current.code || current.url || ''
    navigator.clipboard.writeText(text)
    setCopySuccess(true)
    setTimeout(() => setCopySuccess(false), 2000)
  }

  const handleDownload = () => {
    if (current.type === 'code') {
      const blob = new Blob([current.code], { type: 'text/plain' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = current.title || 'download.txt'
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
          <a href={current.url} target="_blank" rel="noopener noreferrer">{current.url}</a>
        </div>
      )
    }
    if (current.type === 'file') {
      return (
        <div className="canvas-url-card">
          <i className="fas fa-file" />
          <a href={current.url} target="_blank" rel="noopener noreferrer" download={current.title}>{current.title}</a>
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
    <div className="canvas-panel">
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
