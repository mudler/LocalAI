// Splits a Markdown string into segments the page can draw incrementally.
// Each segment carries a stable `key` based on its start offset so that
// growing text only appends new segments and never invalidates earlier ones.
//
// A segment is either:
//   { kind: 'prose', key, text }
//   { kind: 'code', key, lang, code, open }
//
// `key` is always `'prose-<offset>'` or `'code-<offset>'`.

export function streamSegments(text) {
  if (typeof text !== 'string') return []
  const lines = text.split('\n')
  const segments = []
  let inCode = false
  let fenceChar = ''
  let fenceLen = 0
  let codeLines = []
  let codeLang = ''
  let proseLines = []
  let codeStart = -1
  let proseStart = 0
  let skipBlanks = false
  let hasRefLink = false

  // Running character offset: at the start of iteration i, `offset` equals
  // the byte/char position of lines[i][0] in the original text.
  let offset = 0

  const closeProse = () => {
    if (proseLines.length === 0) return
    // Drop trailing blank lines from the segment text
    while (proseLines.length > 0 && proseLines[proseLines.length - 1] === '') {
      proseLines.pop()
    }
    if (proseLines.length === 0) return
    segments.push({
      kind: 'prose',
      key: 'prose-' + proseStart,
      text: proseLines.join('\n'),
    })
    proseLines = []
    hasRefLink = false
  }

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    const lineStart = offset
    offset += line.length + 1

    // If we're in skip-blank-lines mode (after a mid-segment split),
    // skip blank lines until we reach the next non-blank line.
    if (skipBlanks) {
      if (line.trim() === '') continue
      skipBlanks = false
      hasRefLink = false
      proseStart = lineStart
      proseLines = []
      // fall through to process this line as the start of a new segment
    }

    if (!inCode) {
      // Check for fence opening
      const m = line.match(/^ {0,3}(`{3,}|~{3,})/)
      if (m) {
        closeProse()
        fenceChar = m[1][0]
        fenceLen = m[1].length
        codeStart = lineStart
        codeLang = line.slice(m[0].length).trim().split(/\s+/)[0]
        codeLines = []
        inCode = true
      } else if (line.trim() === '') {
        // Blank line: check whether to split the current prose chunk here.
        // Only split if the chunk has at least one non-blank line and all
        // four conditions below hold.
        if (proseLines.some(l => l.trim() !== '')) {
          // Look ahead to the next non-blank line.
          let nextNonBlankIdx = -1
          for (let j = i + 1; j < lines.length; j++) {
            if (lines[j].trim() !== '') {
              nextNonBlankIdx = j
              break
            }
          }

          if (nextNonBlankIdx !== -1) {
            const nextLine = lines[nextNonBlankIdx]
            // Rule 1: NOT a list item, blockquote, or table row.
            const isListItemOrBlockquoteOrTable =
              /^ {0,3}(?:[-*+][ \t]|\d{1,9}[.)][ \t]|>|\|)/.test(nextLine)
            // Rule 2: NOT indented by 2+ spaces (continuation paragraph
            // or indented code block).
            const isIndented = /^\s{2,}/.test(nextLine)
            // Rule 3: no reference-link syntax in the current chunk.
            // (the outer `hasRefLink` flag tracks this incrementally)

            if (!isListItemOrBlockquoteOrTable && !isIndented && !hasRefLink) {
              // Split here — the next chunk starts at the next non-blank line.
              closeProse()
              proseLines = []
              skipBlanks = true
              continue
            }
          }
        }
        proseLines.push(line)
      } else {
        if (proseLines.length === 0) {
          proseStart = lineStart
        }
        if (/\]\[|\]:/.test(line)) hasRefLink = true
        proseLines.push(line)
      }
    } else {
      // Check for fence closing: same char, >= fenceLen, optionally trailing blanks
      const closeMatch = line.match(/^ {0,3}(`{3,}|~{3,})[ \t]*$/)
      if (closeMatch && closeMatch[1][0] === fenceChar && closeMatch[1].length >= fenceLen) {
        closeCode()
      } else {
        codeLines.push(line)
      }
    }
  }

  if (inCode) {
    segments.push({
      kind: 'code',
      key: 'code-' + codeStart,
      lang: codeLang,
      code: codeLines.join('\n'),
      open: true,
    })
  } else {
    closeProse()
  }

  return segments

  function closeCode() {
    segments.push({
      kind: 'code',
      key: 'code-' + codeStart,
      lang: codeLang,
      code: codeLines.join('\n'),
      open: false,
    })
    codeLines = []
    inCode = false
  }
}
