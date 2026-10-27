// Find in chat, done in the browser on what is already on the page. Nothing is
// sent to the server: the search covers the messages that are loaded here.

const SKIP = 'button, textarea, input, .cx-acts, .cx-who, .code-block__head, script, style, mark.cx-hit'

export function clearFind(root) {
  if (!root) return
  root.querySelectorAll('mark.cx-hit').forEach((mark) => {
    const parent = mark.parentNode
    if (!parent) return
    parent.replaceChild(document.createTextNode(mark.textContent), mark)
    parent.normalize()
  })
}

// Wrap each case-insensitive match of `query` in <mark class="cx-hit"> and
// return the marks in reading order.
export function applyFind(root, query) {
  clearFind(root)
  const q = (query || '').toLowerCase()
  if (!root || !q) return []

  const nodes = []
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      if (!node.nodeValue || !node.nodeValue.toLowerCase().includes(q)) return NodeFilter.FILTER_REJECT
      if (node.parentElement?.closest(SKIP)) return NodeFilter.FILTER_REJECT
      return NodeFilter.FILTER_ACCEPT
    },
  })
  while (walker.nextNode()) nodes.push(walker.currentNode)

  const marks = []
  for (const node of nodes) {
    const text = node.nodeValue
    const lower = text.toLowerCase()
    const frag = document.createDocumentFragment()
    let from = 0
    let at = lower.indexOf(q)
    while (at !== -1) {
      if (at > from) frag.appendChild(document.createTextNode(text.slice(from, at)))
      const mark = document.createElement('mark')
      mark.className = 'cx-hit'
      mark.textContent = text.slice(at, at + q.length)
      frag.appendChild(mark)
      marks.push(mark)
      from = at + q.length
      at = lower.indexOf(q, from)
    }
    if (from < text.length) frag.appendChild(document.createTextNode(text.slice(from)))
    node.parentNode.replaceChild(frag, node)
  }
  return marks
}
