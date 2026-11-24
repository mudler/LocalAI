// Draws a Word document into an isolated frame. The renderer (docx-preview)
// loads on first use, so it is not in the main bundle.

// Renders buffer into the frame's document and returns { height } of the
// result at the given zoom. frame is an iframe whose document is ready.
export async function renderDocx(buffer, frame) {
  const { renderAsync } = await import('docx-preview')
  const doc = frame.contentDocument
  const root = doc.getElementById('root')
  // The renderer empties its style container, so it gets its own (the frame's
  // head keeps the policy and the pane's colours).
  const styles = doc.getElementById('styles')
  await renderAsync(buffer, root, styles, {
    className: 'docx',
    inWrapper: true,
    ignoreWidth: false,
    ignoreHeight: false,
    breakPages: true,
    useBase64URL: true,
    renderHeaders: true,
    renderFooters: true,
    renderFootnotes: true,
    renderEndnotes: true,
    renderComments: false,
    experimental: false,
  })
  return doc
}

// Scales the pages down to fit the frame's width (never up), or leaves them at
// full size when full is true, and returns the height of the frame that holds
// them.
export function fitDocx(frame, full = false) {
  const doc = frame.contentDocument
  const wrap = doc?.querySelector('.docx-wrapper')
  if (!wrap) return 0
  wrap.style.zoom = '1'
  // Centred pages that are wider than the frame would lose their left edge.
  wrap.style.alignItems = full ? 'flex-start' : ''
  const pages = [...wrap.querySelectorAll(':scope > section.docx')]
  const widest = Math.max(1, ...pages.map(p => p.offsetWidth))
  const zoom = full ? 1 : Math.min(1, frame.clientWidth / widest)
  wrap.style.zoom = String(zoom)
  frame.contentWindow?.scrollTo(0, 0)
  return Math.ceil(wrap.getBoundingClientRect().height) + 2
}
