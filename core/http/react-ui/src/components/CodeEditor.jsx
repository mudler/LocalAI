import { useRef, useMemo } from 'react'
import { keymap, lineNumbers, highlightActiveLineGutter, highlightActiveLine, drawSelection } from '@codemirror/view'
import { EditorView } from '@codemirror/view'
import { EditorState } from '@codemirror/state'
import { yaml } from '@codemirror/lang-yaml'
import { autocompletion } from '@codemirror/autocomplete'
import { linter, lintGutter } from '@codemirror/lint'
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import { searchKeymap, highlightSelectionMatches } from '@codemirror/search'
import { indentOnInput, indentUnit, bracketMatching, foldGutter, foldKeymap } from '@codemirror/language'
import YAML from 'yaml'
import { useCodeMirror } from '../hooks/useCodeMirror'
import { useTheme } from '../contexts/ThemeContext'
import { getThemeExtension } from '../utils/cmTheme'
import { createYamlCompletionSource } from '../utils/cmYamlComplete'

function yamlIssueToDiagnostic(issue, cmDoc, severity) {
  const len = cmDoc.length
  if (issue.linePos && issue.linePos[0]) {
    const startLine = Math.min(issue.linePos[0].line, cmDoc.lines)
    const from = cmDoc.line(startLine).from + issue.linePos[0].col - 1
    let to = from + 1
    if (issue.linePos[1]) {
      const endLine = Math.min(issue.linePos[1].line, cmDoc.lines)
      to = cmDoc.line(endLine).from + issue.linePos[1].col - 1
    }
    return { from: Math.min(from, len), to: Math.min(Math.max(to, from + 1), len), severity, message: issue.message.split('\n')[0] }
  }
  return { from: 0, to: Math.min(1, len), severity, message: issue.message.split('\n')[0] }
}

const yamlLinter = linter(view => {
  const text = view.state.doc.toString()
  if (!text.trim()) return []
  const parsed = YAML.parseDocument(text, { strict: true, prettyErrors: true })
  const diagnostics = []
  for (const err of parsed.errors) {
    diagnostics.push(yamlIssueToDiagnostic(err, view.state.doc, 'error'))
  }
  for (const warn of parsed.warnings) {
    diagnostics.push(yamlIssueToDiagnostic(warn, view.state.doc, 'warning'))
  }
  return diagnostics
})

export default function CodeEditor({ value, onChange, disabled, minHeight = '500px', fields }) {
  const containerRef = useRef(null)
  const { theme } = useTheme()

  // Static extensions — only recreate when fields change
  const extensions = useMemo(() => {
    const exts = [
      yaml(),
      lineNumbers(),
      highlightActiveLineGutter(),
      highlightActiveLine(),
      drawSelection(),
      foldGutter(),
      indentOnInput(),
      bracketMatching(),
      highlightSelectionMatches(),
      yamlLinter,
      lintGutter(),
      history(),
      indentUnit.of('  '),
      EditorState.tabSize.of(2),
      keymap.of([
        indentWithTab,
        ...defaultKeymap,
        ...historyKeymap,
        ...searchKeymap,
        ...foldKeymap,
      ]),
      EditorView.theme({
        '&': { minHeight },
        '.cm-scroller': { overflow: 'auto' },
      }),
    ]

    if (fields && fields.length > 0) {
      exts.push(autocompletion({
        override: [createYamlCompletionSource(fields)],
        activateOnTyping: true,
      }))
    }

    return exts
  }, [minHeight, fields])

  // Dynamic extensions — reconfigured via Compartments (preserves undo/cursor/scroll)
  const dynamicExtensions = useMemo(() => ({
    theme: getThemeExtension(theme),
    readOnly: [EditorState.readOnly.of(!!disabled), EditorView.editable.of(!disabled)],
  }), [theme, disabled])

  useCodeMirror({ containerRef, value, onChange, extensions, dynamicExtensions })

  return <div ref={containerRef} className="code-editor-cm" />
}
