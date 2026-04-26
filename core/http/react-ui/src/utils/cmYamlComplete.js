import { fetchCachedAutocomplete } from '../hooks/useAutocomplete'

const NO_COMPLETION = Object.freeze({ position: 'none', path: [], currentWord: '', keyName: '' })

function analyzeYamlContext(state, pos) {
  const doc = state.doc
  const line = doc.lineAt(pos)
  const lineText = line.text
  const cursorCol = pos - line.from

  // Check if we're inside a block scalar (| or >)
  for (let ln = line.number - 1; ln >= 1; ln--) {
    const prevLine = doc.line(ln)
    const prevText = prevLine.text
    const trimmed = prevText.trimEnd()
    if (/:\s*[|>][+-]?\s*$/.test(trimmed)) {
      const scalarIndent = prevText.length - prevText.trimStart().length + 2
      const currentIndent = lineText.length - lineText.trimStart().length
      if (currentIndent >= scalarIndent) return NO_COMPLETION
      break
    }
    if (prevText.trim() !== '' && !prevText.trim().startsWith('#')) break
  }

  // Check if line is a comment
  if (lineText.trimStart().startsWith('#')) return NO_COMPLETION

  const currentIndent = lineText.length - lineText.trimStart().length
  const colonIdx = lineText.indexOf(':')

  // Build parent path by scanning backward for less-indented keys
  const path = []
  let targetIndent = currentIndent
  for (let ln = line.number - 1; ln >= 1 && targetIndent > 0; ln--) {
    const prevText = doc.line(ln).text
    if (prevText.trim() === '' || prevText.trim().startsWith('#')) continue
    const prevIndent = prevText.length - prevText.trimStart().length
    if (prevIndent < targetIndent) {
      const match = prevText.match(/^(\s*)([a-zA-Z_][\w.-]*):\s*/)
      if (match && match[1].length === prevIndent) {
        path.unshift(match[2])
        targetIndent = prevIndent
      }
    }
  }

  // List items (e.g. "  - value") need value completions from their parent key
  const listItemMatch = lineText.match(/^(\s*)-\s*(.*)$/)
  if (listItemMatch) {
    const listIndent = listItemMatch[1].length
    let parentKey = ''
    for (let ln = line.number - 1; ln >= 1; ln--) {
      const prevText = doc.line(ln).text
      if (prevText.trim() === '' || prevText.trim().startsWith('#')) continue
      const prevIndent = prevText.length - prevText.trimStart().length
      if (prevIndent < listIndent) {
        const km = prevText.match(/^(\s*)([a-zA-Z_][\w.-]*):\s*$/)
        if (km && km[1].length === prevIndent) parentKey = km[2]
        break
      }
      // Another list item at same indent — keep scanning for the key
      if (prevText.match(/^\s*-\s/)) continue
      break
    }
    if (parentKey) {
      const dashPos = lineText.indexOf('- ')
      const valueStart = dashPos + 2
      const currentWord = cursorCol > valueStart ? lineText.substring(valueStart, cursorCol) : ''
      return { position: 'list-item', path, currentWord, keyName: parentKey }
    }
  }

  if (colonIdx === -1 || cursorCol <= colonIdx) {
    const textBeforeCursor = lineText.substring(0, cursorCol)
    const currentWord = textBeforeCursor.trimStart()
    return { position: 'key', path, currentWord, keyName: '' }
  }

  const keyMatch = lineText.match(/^\s*([a-zA-Z_][\w.-]*):\s*/)
  const keyName = keyMatch ? keyMatch[1] : ''
  const valueStart = colonIdx + 1
  const textAfterColon = lineText.substring(valueStart, cursorCol).trimStart()
  return { position: 'value', path, currentWord: textAfterColon, keyName }
}

// Build lookup structures from field metadata
function buildFieldIndex(fields) {
  // Map from dot-path to field metadata
  const byPath = new Map()
  // Map from prefix to child key names (for key completions at each nesting level)
  // e.g., '' -> ['name', 'backend', 'parameters', 'pipeline', ...]
  //        'parameters' -> ['temperature', 'top_p', 'top_k', ...]
  const childKeys = new Map()

  for (const field of fields) {
    byPath.set(field.path, field)

    const parts = field.path.split('.')
    for (let i = 0; i < parts.length; i++) {
      const prefix = parts.slice(0, i).join('.')
      const key = parts[i]
      if (!childKeys.has(prefix)) childKeys.set(prefix, new Map())
      const siblings = childKeys.get(prefix)
      if (!siblings.has(key)) {
        // For intermediate keys (not the leaf), store a synthetic entry
        // For the leaf key, store the actual field
        const isLeaf = i === parts.length - 1
        siblings.set(key, isLeaf ? field : { path: parts.slice(0, i + 1).join('.'), label: key, section: field.section })
      }
    }
  }

  return { byPath, childKeys }
}

export function createYamlCompletionSource(fields) {
  if (!fields || fields.length === 0) return () => null

  const { byPath, childKeys } = buildFieldIndex(fields)

  return async (context) => {
    const { state, pos } = context
    const ctx = analyzeYamlContext(state, pos)

    if (ctx.position === 'none') return null

    if (ctx.position === 'key') {
      const prefix = ctx.path.join('.')
      const siblings = childKeys.get(prefix)
      if (!siblings) return null

      const word = context.matchBefore(/[\w.-]*/)
      if (!word && !context.explicit) return null

      const options = []
      for (const [key, info] of siblings) {
        const field = byPath.get(info.path) || info
        options.push({
          label: key,
          detail: field.ui_type || (byPath.has(info.path) ? '' : 'section'),
          info: field.description || '',
          type: byPath.has(info.path) ? 'property' : 'namespace',
          apply: byPath.has(info.path) ? key + ': ' : key + ':\n' + ' '.repeat((ctx.path.length + 1) * 2),
          boost: field.order != null ? -field.order : 0,
        })
      }

      return { from: word ? word.from : pos, options, validFor: /^[\w.-]*$/ }
    }

    if ((ctx.position === 'value' || ctx.position === 'list-item') && ctx.keyName) {
      // For list items, path already includes the parent key; for values, append keyName
      const fullPath = ctx.position === 'list-item'
        ? ctx.path.join('.')
        : (ctx.path.length > 0 ? ctx.path.join('.') + '.' + ctx.keyName : ctx.keyName)
      const field = byPath.get(fullPath)
      if (!field) return null

      const word = context.matchBefore(/\S*/)
      const from = word ? word.from : pos

      // Static options from field metadata
      if (field.options && field.options.length > 0) {
        return {
          from,
          options: field.options.map(opt => ({
            label: opt.value,
            detail: opt.label !== opt.value ? opt.label : '',
            type: 'enum',
          })),
          validFor: /^\S*$/,
        }
      }

      // Dynamic autocomplete from provider
      if (field.autocomplete_provider) {
        const values = await fetchCachedAutocomplete(field.autocomplete_provider)
        if (values.length === 0) return null
        return {
          from,
          options: values.map(v => ({ label: v, type: 'value' })),
          validFor: /^\S*$/,
        }
      }

      // Boolean fields
      if (field.ui_type === 'bool') {
        return {
          from,
          options: [
            { label: 'true', type: 'enum' },
            { label: 'false', type: 'enum' },
          ],
          validFor: /^\S*$/,
        }
      }
    }

    return null
  }
}
