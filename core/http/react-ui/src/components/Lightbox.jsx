import { useEffect, useCallback } from 'react'

// Fullscreen image viewer with prev/next, download, and keyboard control
// (Esc to close, Left/Right to navigate). `images` is [{ url, alt }]; `index`
// is the active entry; `onIndex` and `onClose` are controlled by the parent.
export default function Lightbox({ images, index, onClose, onIndex }) {
  const has = Array.isArray(images) && images.length > 0
  const count = has ? images.length : 0

  const go = useCallback((delta) => {
    if (count < 2) return
    onIndex(((index + delta) % count + count) % count)
  }, [count, index, onIndex])

  useEffect(() => {
    const onKey = (e) => {
      if (e.key === 'Escape') onClose()
      else if (e.key === 'ArrowRight') go(1)
      else if (e.key === 'ArrowLeft') go(-1)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose, go])

  if (!has) return null
  const img = images[index] || images[0]

  return (
    <div className="lightbox" role="dialog" aria-modal="true" onClick={onClose}>
      <div className="lightbox__toolbar" onClick={(e) => e.stopPropagation()}>
        {count > 1 && <span className="lightbox__count">{index + 1} / {count}</span>}
        <a className="btn btn-secondary btn-sm" href={img.url} download target="_blank" rel="noopener noreferrer" aria-label="Download">
          <i className="fas fa-download" aria-hidden="true" />
        </a>
        <button type="button" className="btn btn-secondary btn-sm" onClick={onClose} aria-label="Close">
          <i className="fas fa-times" aria-hidden="true" />
        </button>
      </div>

      {count > 1 && (
        <button type="button" className="lightbox__nav lightbox__nav--prev" onClick={(e) => { e.stopPropagation(); go(-1) }} aria-label="Previous">
          <i className="fas fa-chevron-left" aria-hidden="true" />
        </button>
      )}

      <img src={img.url} alt={img.alt || ''} className="lightbox__img" onClick={(e) => e.stopPropagation()} />

      {count > 1 && (
        <button type="button" className="lightbox__nav lightbox__nav--next" onClick={(e) => { e.stopPropagation(); go(1) }} aria-label="Next">
          <i className="fas fa-chevron-right" aria-hidden="true" />
        </button>
      )}
    </div>
  )
}
