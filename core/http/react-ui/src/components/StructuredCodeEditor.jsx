import { useEffect, useState } from 'react'
import YAML from 'yaml'
import CodeEditor from './CodeEditor'

// StructuredCodeEditor is the wrapper that lets a `code-editor`
// field hold a structured value (object / array) rather than a raw
// string. Two reasons we need this:
//
//   1. CodeMirror's EditorState.create({ doc }) requires a string —
//      pass an array and it crashes inside CM's Text class with
//      "(intermediate value).split is not a function".
//   2. The model-editor save path uses unflattenConfig + YAML.stringify
//      which needs the structured value to round-trip cleanly into
//      YAML (otherwise a YAML-string-of-YAML appears in the file).
//
// The component keeps two pieces of state in sync:
//   - `text`: the YAML representation shown to the user. The user
//     edits this; we don't reformat while they type.
//   - upstream `value`: the parsed structured value held by the
//     editor form. We try to parse `text` on every edit; if the
//     parse succeeds we publish the new structure, otherwise the
//     structured value lags until the YAML is syntactically valid
//     again (the linter shows the error inline).
export default function StructuredCodeEditor({ value, onChange, minHeight }) {
  // Lazy-init: stringify the initial structured value once. Subsequent
  // re-renders driven by our own onChange keep `text` authoritative —
  // we only re-sync from `value` when it changes due to an external
  // edit (template selection, YAML-tab save).
  const [text, setText] = useState(() => structuredToYAML(value))
  const [lastExternal, setLastExternal] = useState(value)

  useEffect(() => {
    // Detect external changes (a different `value` reference that
    // didn't come from our own parse). reference-equality is enough
    // because onChange always publishes the parsed object, never the
    // text.
    if (value !== lastExternal) {
      const next = structuredToYAML(value)
      setText(next)
      setLastExternal(value)
    }
  }, [value, lastExternal])

  const handleTextChange = (nextText) => {
    setText(nextText)
    // Empty buffer publishes empty array — the most common "I want to
    // start fresh" case and keeps a YAML-valid round-trip.
    if (!nextText.trim()) {
      onChange([])
      setLastExternal([])
      return
    }
    try {
      const parsed = YAML.parse(nextText)
      onChange(parsed)
      setLastExternal(parsed)
    } catch {
      // Hold the structured value steady while YAML is being typed
      // and is temporarily invalid. The CodeMirror YAML linter
      // surfaces the syntax error inline.
    }
  }

  return <CodeEditor value={text} onChange={handleTextChange} minHeight={minHeight} />
}

// structuredToYAML renders the form-state value as the YAML text the
// editor shows. Strings pass through untouched (so a legacy template
// that supplied a pre-formatted YAML string still renders cleanly).
// null/undefined renders as empty so the editor starts blank rather
// than showing the literal "null\n".
export function structuredToYAML(value) {
  if (value === null || value === undefined) return ''
  if (typeof value === 'string') return value
  try {
    return YAML.stringify(value)
  } catch {
    return ''
  }
}
