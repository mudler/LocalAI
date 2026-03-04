import { useRef, useEffect, useCallback } from 'react'
import hljs from 'highlight.js/lib/core'
import yaml from 'highlight.js/lib/languages/yaml'

hljs.registerLanguage('yaml', yaml)

export default function CodeEditor({ value, onChange, disabled, minHeight = '500px' }) {
  const codeRef = useRef(null)
  const textareaRef = useRef(null)
  const preRef = useRef(null)

  const highlight = useCallback(() => {
    if (!codeRef.current) return
    const result = hljs.highlight(value + '\n', { language: 'yaml', ignoreIllegals: true })
    codeRef.current.innerHTML = result.value
  }, [value])

  useEffect(() => {
    highlight()
  }, [highlight])

  const handleScroll = () => {
    if (preRef.current && textareaRef.current) {
      preRef.current.scrollTop = textareaRef.current.scrollTop
      preRef.current.scrollLeft = textareaRef.current.scrollLeft
    }
  }

  const handleKeyDown = (e) => {
    if (e.key === 'Tab') {
      e.preventDefault()
      const ta = e.target
      const start = ta.selectionStart
      const end = ta.selectionEnd
      const newValue = value.substring(0, start) + '  ' + value.substring(end)
      onChange(newValue)
      requestAnimationFrame(() => {
        ta.selectionStart = ta.selectionEnd = start + 2
      })
    }
  }

  return (
    <div className="code-editor-wrapper" style={{ position: 'relative', minHeight, fontSize: '0.8125rem' }}>
      <pre
        ref={preRef}
        className="code-editor-highlight"
        aria-hidden="true"
        style={{
          position: 'absolute',
          top: 0, left: 0, right: 0, bottom: 0,
          margin: 0,
          padding: 'var(--spacing-sm)',
          overflow: 'auto',
          pointerEvents: 'none',
          fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
          fontSize: 'inherit',
          lineHeight: 1.5,
          tabSize: 2,
          whiteSpace: 'pre-wrap',
          wordWrap: 'break-word',
          background: 'var(--color-bg-tertiary)',
          borderRadius: 'var(--radius-md)',
          border: '1px solid var(--color-border-default)',
        }}
      >
        <code
          ref={codeRef}
          className="language-yaml"
          style={{
            fontFamily: 'inherit',
            fontSize: 'inherit',
            lineHeight: 'inherit',
            padding: 0,
            background: 'transparent',
          }}
        />
      </pre>
      <textarea
        ref={textareaRef}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onScroll={handleScroll}
        onKeyDown={handleKeyDown}
        disabled={disabled}
        spellCheck={false}
        style={{
          position: 'relative',
          width: '100%',
          minHeight,
          margin: 0,
          padding: 'var(--spacing-sm)',
          fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
          fontSize: 'inherit',
          lineHeight: 1.5,
          tabSize: 2,
          whiteSpace: 'pre-wrap',
          wordWrap: 'break-word',
          color: 'transparent',
          caretColor: 'var(--color-text-primary)',
          background: 'transparent',
          border: '1px solid var(--color-border-default)',
          borderRadius: 'var(--radius-md)',
          outline: 'none',
          resize: 'vertical',
          overflow: 'auto',
        }}
      />
    </div>
  )
}
