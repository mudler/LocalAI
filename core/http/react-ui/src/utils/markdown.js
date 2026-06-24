import { marked } from 'marked'
import DOMPurify from 'dompurify'
import hljs from './hljs'
import { copyToClipboard } from './clipboard'

marked.setOptions({
  highlight(code, lang) {
    if (lang && hljs.getLanguage(lang)) {
      return hljs.highlight(code, { language: lang }).value
    }
    return hljs.highlightAuto(code).value
  },
  breaks: true,
  gfm: true,
})

export function renderMarkdown(text) {
  if (!text) return ''
  const html = marked.parse(text)
  return DOMPurify.sanitize(html)
}

// Recursively pull the human-readable text out of a marked token, dropping the
// syntax that carries it. Link/image tokens keep their label and lose the URL;
// code keeps its literal content; raw HTML is dropped rather than leaked as
// angle-bracket noise into a table cell.
function tokenToPlainText(token) {
  if (!token) return ''
  switch (token.type) {
    case 'space':
    case 'hr':
    case 'br':
    case 'html':
      return ' '
    case 'code':
    case 'codespan':
    case 'image':
      return token.text || ''
    case 'list':
      return (token.items || []).map(tokenToPlainText).join(' ')
    case 'table':
      return [...(token.header || []), ...(token.rows || []).flat()]
        .map(tokenToPlainText)
        .join(' ')
    default:
      break
  }
  if (Array.isArray(token.tokens) && token.tokens.length > 0) {
    return token.tokens.map(tokenToPlainText).join('')
  }
  return token.text || ''
}

// Reduce Markdown to a single line of readable plain text. Used by the
// truncated one-line description cells and their `title` tooltips, where
// rendering real Markdown would turn a leading `#` into an <h1> and wreck the
// row rhythm. Output lands in JSX text nodes, so React escapes it.
export function stripMarkdown(text) {
  if (!text) return ''
  let plain
  try {
    plain = marked.lexer(text).map(tokenToPlainText).join(' ')
  } catch {
    // A malformed gallery description must never break the row it labels.
    plain = text
  }
  return plain.replace(/\s+/g, ' ').trim()
}

export function highlightAll(element) {
  if (!element) return
  element.querySelectorAll('pre code').forEach((block) => {
    hljs.highlightElement(block)
  })
}

// Decorate each (not-yet-enhanced) <pre> code block in `element` with a header
// bar carrying the language label and a copy button. Idempotent: re-running on
// the same DOM (e.g. while streaming) only touches new blocks. Copy clicks are
// handled by a single delegated document listener (registered below).
export function enhanceCodeBlocks(element) {
  if (!element) return
  element.querySelectorAll('pre:not([data-enhanced])').forEach((pre) => {
    pre.setAttribute('data-enhanced', '1')
    const code = pre.querySelector('code')
    const langMatch = code && code.className.match(/language-(\w+)/)
    const lang = langMatch ? langMatch[1] : 'text'
    const wrap = document.createElement('div')
    wrap.className = 'code-block'
    const head = document.createElement('div')
    head.className = 'code-block__head'
    const label = document.createElement('span')
    label.className = 'code-block__lang'
    label.textContent = lang
    const btn = document.createElement('button')
    btn.type = 'button'
    btn.className = 'code-copy-btn'
    btn.setAttribute('aria-label', 'Copy code')
    btn.innerHTML = '<i class="fas fa-copy" aria-hidden="true"></i>'
    head.appendChild(label)
    head.appendChild(btn)
    pre.parentNode.insertBefore(wrap, pre)
    wrap.appendChild(head)
    wrap.appendChild(pre)
  })
}

// One delegated handler for every code-copy button, anywhere in the app.
if (typeof document !== 'undefined' && !window.__codeCopyDelegate) {
  window.__codeCopyDelegate = true
  document.addEventListener('click', async (e) => {
    const btn = e.target.closest?.('.code-copy-btn')
    if (!btn) return
    const code = btn.closest('.code-block')?.querySelector('pre code')
    if (!code) return
    const ok = await copyToClipboard(code.innerText)
    if (!ok) return
    btn.innerHTML = '<i class="fas fa-check" aria-hidden="true"></i>'
    btn.classList.add('code-copy-btn--ok')
    setTimeout(() => {
      btn.innerHTML = '<i class="fas fa-copy" aria-hidden="true"></i>'
      btn.classList.remove('code-copy-btn--ok')
    }, 2000)
  })
}
