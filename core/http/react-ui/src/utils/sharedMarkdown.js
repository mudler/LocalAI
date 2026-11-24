// Markdown of a project that another person shares is the owner's text, so
// opening the page must never make the browser fetch an address the owner
// chose. This renders it with a short list of text tags and attributes (no
// images, media, frames, styles, forms or scripts, no src, srcset, poster or
// style anywhere) and turns every link into a plain, safe one: only http(s)
// and mailto links keep their address; any other href is dropped, so the link
// stays as text.
import { marked } from 'marked'
import createDOMPurify from 'dompurify'

const TAGS = ['p', 'br', 'hr', 'strong', 'b', 'em', 'i', 'u', 's', 'del', 'code', 'pre', 'blockquote', 'ul', 'ol', 'li',
  'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'a', 'table', 'thead', 'tbody', 'tr', 'th', 'td', 'span', 'div', 'sup', 'sub']
const ATTRS = ['href', 'title', 'colspan', 'rowspan', 'align']
const SAFE_HREF = /^(https?:\/\/|mailto:)/i

const keepSafeLinks = (node) => {
  if (node.tagName !== 'A') return
  const href = node.getAttribute('href') || ''
  if (!SAFE_HREF.test(href.trim())) { node.removeAttribute('href'); return }
  node.setAttribute('target', '_blank')
  node.setAttribute('rel', 'noopener noreferrer nofollow')
}

// One purifier of its own per window, so these hooks never touch the page's
// other markdown. purify is for tests, which pass one bound to a fake window.
const bound = new WeakSet()
let own = null
function purifier(purify) {
  let p = purify
  if (!p) { if (!own) own = createDOMPurify(window); p = own }
  if (!bound.has(p)) { p.addHook('afterSanitizeAttributes', keepSafeLinks); bound.add(p) }
  return p
}

export function renderSharedMarkdown(text, purify) {
  if (!text) return ''
  return purifier(purify).sanitize(marked.parse(String(text)), {
    ALLOWED_TAGS: TAGS, ALLOWED_ATTR: ATTRS, ALLOW_DATA_ATTR: false, ALLOW_ARIA_ATTR: false,
  })
}
