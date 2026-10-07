import { useLayoutEffect, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { renderMarkdown, highlightAll, enhanceCodeBlocks } from '../../utils/markdown'

// Markdown prose. Code blocks get Copy; tables and code blocks that may be
// wider than the column get "Open wider", which breaks them out of the column
// until pressed again.
export default function Prose({ text, live = false }) {
  const { t } = useTranslation('agents')
  const ref = useRef(null)
  const html = useMemo(() => renderMarkdown(text || ''), [text])
  // The markup is set and decorated here, not through dangerouslySetInnerHTML:
  // React would put the plain markup back on a later render and drop the
  // buttons added below.
  useLayoutEffect(() => {
    const root = ref.current
    if (!root) return
    root.innerHTML = html
    highlightAll(root)
    enhanceCodeBlocks(root)
    root.querySelectorAll('table, .code-block').forEach((el) => {
      // Content that is, or may be, wider than the column gets the button: a
      // code block that overflows, a table that overflows or has many columns.
      const isCode = el.matches('.code-block')
      const scroller = isCode ? el.querySelector('pre') : el
      if (!scroller) return
      const overflows = scroller.scrollWidth > scroller.clientWidth + 2
      const manyColumns = !isCode && el.querySelectorAll('thead th').length >= 4
      if (!overflows && !manyColumns) return
      const wrap = document.createElement('div')
      wrap.className = 'ag-wide'
      const bar = document.createElement('div')
      bar.className = 'ag-wide__bar'
      const btn = document.createElement('button')
      btn.type = 'button'
      btn.className = 'ag-wide__btn'
      btn.setAttribute('aria-pressed', 'false')
      btn.textContent = t('run.openWider')
      btn.addEventListener('click', () => {
        const on = wrap.toggleAttribute('data-wide')
        btn.textContent = on ? t('run.backToColumn') : t('run.openWider')
        btn.setAttribute('aria-pressed', String(on))
      })
      bar.appendChild(btn)
      el.parentNode.insertBefore(wrap, el)
      wrap.append(bar, el)
    })
  }, [html, t])
  return <div ref={ref} className={`cx-prose${live ? ' cx-prose--live' : ''}`} />
}
