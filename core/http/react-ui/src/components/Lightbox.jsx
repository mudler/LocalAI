import { useEffect, useCallback, useRef } from 'react'
import Icon from './Icon'

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

  // The key listener is registered once and reads the latest handlers from a
  // ref. A parent that re-renders on the same key press (the Chat page does on
  // Esc) would otherwise swap the listener in the middle of the dispatch, and
  // the new one does not receive the event that is already on its way.
  const latest = useRef({ onClose, go })
  latest.current = { onClose, go }
  useEffect(() => {
    const onKey = (e) => {
      if (e.key === 'Escape') latest.current.onClose()
      else if (e.key === 'ArrowRight') latest.current.go(1)
      else if (e.key === 'ArrowLeft') latest.current.go(-1)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  if (!has) return null
  const img = images[index] || images[0]

  return (
    <div className="lightbox" role="dialog" aria-modal="true" onClick={onClose}>
      <div className="lightbox__toolbar" onClick={(e) => e.stopPropagation()}>
        {count > 1 && <span className="lightbox__count">{index + 1} / {count}</span>}
        <a className="btn btn-secondary btn-sm" href={img.url} download target="_blank" rel="noopener noreferrer" aria-label="Download">
          <Icon name="download" />
        </a>
        <button type="button" className="btn btn-secondary btn-sm" onClick={onClose} aria-label="Close">
          <Icon name="close" />
        </button>
      </div>

      {count > 1 && (
        <button type="button" className="lightbox__nav lightbox__nav--prev" onClick={(e) => { e.stopPropagation(); go(-1) }} aria-label="Previous">
          <Icon name="chevron-left" />
        </button>
      )}

      <img src={img.url} alt={img.alt || ''} className="lightbox__img" onClick={(e) => e.stopPropagation()} />

      {count > 1 && (
        <button type="button" className="lightbox__nav lightbox__nav--next" onClick={(e) => { e.stopPropagation(); go(1) }} aria-label="Next">
          <Icon name="chevron-right" />
        </button>
      )}
    </div>
  )
}
