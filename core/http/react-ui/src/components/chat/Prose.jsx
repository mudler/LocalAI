import { useEffect, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { chatFileTarget } from '../../utils/chatMedia'
import { filesApi } from '../../utils/api'
import { iconHtml } from '../../utils/iconHtml'

// The files of the chat that an answer's images and links name, made ready for
// the page: a path of the model's sandbox or of the person's files becomes the
// address that serves the file, and a link to a file opens it in the pane.
// Anything else stays as written. Without a DOM the HTML is
// returned as it is.
function withChatFiles(html, files) {
  if (!html || typeof DOMParser === 'undefined' || !/<(img|a)\s/i.test(html)) return html
  const doc = new DOMParser().parseFromString(html, 'text/html')
  for (const img of doc.querySelectorAll('img')) {
    img.setAttribute('loading', 'lazy')
    const target = chatFileTarget(img.getAttribute('src'), files)
    if (!target) continue
    img.setAttribute('src', filesApi.contentUrl(target.path, true))
    img.setAttribute('data-chat-path', target.path)
    if (!img.getAttribute('alt')) img.setAttribute('alt', target.name)
  }
  for (const a of doc.querySelectorAll('a[href]')) {
    const target = chatFileTarget(a.getAttribute('href'), files)
    if (!target || !target.known) continue
    a.setAttribute('href', filesApi.contentUrl(target.path))
    a.setAttribute('target', '_blank')
    a.setAttribute('rel', 'noopener noreferrer')
    a.setAttribute('data-chat-path', target.path)
  }
  return doc.body.innerHTML
}

// An image of an answer that does not load (the file is gone, not written yet,
// or the model named a path that is not one) becomes a chip with its name, never
// a broken-image icon. With the file known the chip opens the pane on it.
function imageFallback(img, label) {
  const path = img.getAttribute('data-chat-path')
  const name = img.getAttribute('alt') || (path || img.getAttribute('src') || '').split('/').pop() || ''
  const chip = document.createElement(path ? 'button' : 'span')
  chip.className = 'cx-fchip cx-fchip--missing'
  chip.setAttribute('data-testid', 'chat-image-fallback')
  if (path) {
    chip.setAttribute('type', 'button')
    chip.setAttribute('data-chat-path', path)
    chip.setAttribute('data-open-file', '1')
  }
  chip.innerHTML = iconHtml('image')
  const nameEl = document.createElement('span')
  nameEl.className = 'cx-fchip__name'
  nameEl.textContent = name
  chip.appendChild(nameEl)
  if (path) {
    const open = document.createElement('span')
    open.className = 'cx-fchip__open'
    open.textContent = label
    chip.appendChild(open)
  }
  img.replaceWith(chip)
}

// Assistant prose. The HTML string is memoised, so React leaves the node alone
// while the page re-renders for other reasons (polling, streaming elsewhere):
// a text selection inside an answer survives. files and onOpenFile tie its
// images and links to the chat's files.
function Prose({ html, className = '', files, onOpenFile }) {
  const { t } = useTranslation('chat')
  const ref = useRef(null)
  const shown = useMemo(() => withChatFiles(html, files || []), [html, files])
  // An error does not bubble: the capture phase sees the images of the HTML.
  useEffect(() => {
    const el = ref.current
    if (!el) return undefined
    const onError = (e) => { if (e.target instanceof HTMLImageElement) imageFallback(e.target, t('preview.open')) }
    el.addEventListener('error', onError, true)
    // An image that failed before this listener was there.
    el.querySelectorAll('img').forEach((img) => { if (img.complete && img.naturalWidth === 0 && img.getAttribute('src')) imageFallback(img, t('preview.open')) })
    return () => el.removeEventListener('error', onError, true)
  }, [shown, t])
  const onClick = (e) => {
    const hit = onOpenFile && e.target.closest?.('[data-chat-path]:not(img)')
    if (!hit) return
    e.preventDefault()
    onOpenFile(hit.getAttribute('data-chat-path'))
  }
  return <div ref={ref} className={`cx-prose ${className}`} onClick={onClick} dangerouslySetInnerHTML={{ __html: shown }} />
}

export default Prose
