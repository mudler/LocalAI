import { useState, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { renderMarkdown } from '../utils/markdown'
import { getArtifactIcon, extensionForLanguage } from '../utils/artifacts'
import { safeHref } from '../utils/url'
import { copyToClipboard } from '../utils/clipboard'
import DOMPurify from 'dompurify'
import hljs from '../utils/hljs'
import Icon from './Icon'

const WIDTH_KEY = 'localai_canvas_width'
const MIME_BY_EXT = { html: 'text/html', svg: 'image/svg+xml', json: 'application/json', css: 'text/css' }

export default function CanvasPanel({ artifacts, selectedId, onSelect, onClose }) {
  const { t } = useTranslation('chat')
  const [showPreview, setShowPreview] = useState(true)
  const [copySuccess, setCopySuccess] = useState(false)
  // Persisted drag-to-resize width (px). null = use the CSS default (45%).
  const [width, setWidth] = useState(() => {
    try { const v = localStorage.getItem(WIDTH_KEY); return v ? Number(v) : null } catch { return null }
  })
  const [fullscreen, setFullscreen] = useState(false)
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
          <Icon name="music" className="canvas-audio-icon" />
          <p>{current.title}</p>
          <audio controls src={current.url} className="w-full" />
        </div>
      )
    }
    if (current.type === 'video') {
      return <video controls src={current.url} className="canvas-preview-image" />
    }
    if (current.type === 'url') {
      return (
        <div className="canvas-url-card">
          <Icon name="external-link" />
          <a href={safeHref(current.url)} target="_blank" rel="noopener noreferrer">{current.url}</a>
        </div>
      )
    }
    if (current.type === 'file') {
      return (
        <div className="canvas-url-card">
          <Icon name="file" />
          <a href={safeHref(current.url)} target="_blank" rel="noopener noreferrer" download={current.title}>{current.title}</a>
        </div>
      )
    }
    // Code artifacts
    if (showPreview && hasPreview) {
      if (current.language === 'html') {
        return <iframe srcDoc={current.code} sandbox="allow-scripts" className="canvas-preview-iframe" title={t('canvas.htmlPreview')} />
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
    <aside
      className={`canvas-panel${fullscreen ? ' canvas-panel--fullscreen' : ''}`}
      aria-label={t('canvas.title')}
      data-testid="canvas-panel"
      ref={panelRef}
      style={!fullscreen && width ? { width: `${width}px`, maxWidth: 'none' } : undefined}
    >
      {!fullscreen && (
        <div
          className="canvas-resize-handle"
          onMouseDown={startResize}
          onDoubleClick={resetWidth}
          role="separator"
          aria-orientation="vertical"
          aria-label={t('canvas.resize')}
          title={t('canvas.resizeHint')}
        />
      )}
      <div className="canvas-panel-header">
        <span className="canvas-panel-title">{current.title || t('canvas.artifact')}</span>
        <div className="canvas-header-actions">
          <button
            type="button"
            className="canvas-icobtn"
            onClick={() => setFullscreen(f => !f)}
            title={fullscreen ? t('canvas.exitFullscreen') : t('canvas.fullscreen')}
            aria-label={fullscreen ? t('canvas.exitFullscreen') : t('canvas.fullscreen')}
          >
            <Icon name={fullscreen ? 'minimize' : 'maximize'} />
          </button>
          <button type="button" className="canvas-icobtn" onClick={onClose} title={t('canvas.close')} aria-label={t('canvas.close')}>
            <Icon name="close" />
          </button>
        </div>
      </div>

      {artifacts.length > 1 && (
        <div
          className="canvas-panel-tabs"
          role="tablist"
          aria-label={t('canvas.artifacts')}
          onKeyDown={(e) => {
            if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return
            e.preventDefault()
            const idx = artifacts.findIndex(a => a.id === current.id)
            const n = e.key === 'ArrowRight'
              ? (idx + 1) % artifacts.length
              : (idx - 1 + artifacts.length) % artifacts.length
            onSelect(artifacts[n].id)
          }}
        >
          {artifacts.map(a => (
            <button
              key={a.id}
              role="tab"
              aria-selected={a.id === current.id}
              tabIndex={a.id === current.id ? 0 : -1}
              className={`canvas-panel-tab${a.id === (current?.id) ? ' active' : ''}`}
              onClick={() => onSelect(a.id)}
              title={a.title}
            >
              <Icon name={getArtifactIcon(a.type, a.language)} />
              <span>{a.title}</span>
            </button>
          ))}
        </div>
      )}

      <div className="canvas-panel-toolbar">
        <span className="badge badge-sm">{current.type === 'code' ? current.language : current.type}</span>
        {hasPreview && (
          <div className="canvas-toggle-group" role="group" aria-label={t('canvas.view')}>
            <button
              type="button"
              className={`canvas-toggle-btn${!showPreview ? ' active' : ''}`}
              aria-pressed={!showPreview}
              onClick={() => setShowPreview(false)}
            >{t('canvas.code')}</button>
            <button
              type="button"
              className={`canvas-toggle-btn${showPreview ? ' active' : ''}`}
              aria-pressed={showPreview}
              onClick={() => setShowPreview(true)}
            >{t('canvas.preview')}</button>
          </div>
        )}
        <div className="flex-1" />
        <button type="button" className="canvas-textbtn" onClick={handleCopy} title={t('actions.copy')}>
          <Icon name={copySuccess ? 'check' : 'copy'} /> <span>{t('actions.copy')}</span>
        </button>
        <button type="button" className="canvas-textbtn" onClick={handleDownload} title={t('canvas.download')}>
          <Icon name="download" /> <span>{t('canvas.download')}</span>
        </button>
      </div>

      <div className="canvas-panel-body">
        {renderBody()}
      </div>
    </aside>
  )
}
