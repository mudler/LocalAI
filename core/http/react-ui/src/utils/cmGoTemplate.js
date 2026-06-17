import { StreamLanguage } from '@codemirror/language'

// Go text/template keywords valid inside an action `{{ ... }}`.
const KEYWORDS = new Set([
  'if', 'else', 'end', 'range', 'with', 'define', 'template',
  'block', 'break', 'continue', 'nil', 'true', 'false',
])

// Minimal Go text/template highlighter: distinguishes literal text from
// action bodies inside `{{ ... }}`. Highlighting only — it does not
// validate template grammar.
export const goTemplate = StreamLanguage.define({
  startState() {
    return { inAction: false }
  },
  token(stream, state) {
    if (!state.inAction) {
      if (stream.match('{{')) {
        state.inAction = true
        return 'meta'
      }
      while (!stream.eol()) {
        if (stream.match('{{', false)) break
        stream.next()
      }
      return null
    }

    if (stream.match('}}')) {
      state.inAction = false
      return 'meta'
    }
    if (stream.eatSpace()) return null
    if (stream.match(/^-(?=\s)/) || stream.match(/^[|()]/)) return 'operator'
    if (stream.match(/^"(?:[^"\\]|\\.)*"/)) return 'string'
    if (stream.match(/^`[^`]*`/)) return 'string'
    if (stream.match(/^\$[a-zA-Z0-9_]*/)) return 'variable-2'
    if (stream.match(/^\.[a-zA-Z0-9_.]*/)) return 'property'
    if (stream.match(/^[0-9]+(\.[0-9]+)?/)) return 'number'
    if (stream.match(/^[a-zA-Z_][a-zA-Z0-9_]*/)) {
      return KEYWORDS.has(stream.current()) ? 'keyword' : 'variable'
    }
    stream.next()
    return null
  },
})
