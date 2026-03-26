import { EditorView } from '@codemirror/view'
import { HighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { tags } from '@lezer/highlight'

// Dark theme — vibrant palette on deep indigo background
const darkEditorTheme = EditorView.theme({
  '&': {
    backgroundColor: '#1a1a2e',
    color: '#e2e8f0',
    fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
    fontSize: '0.8125rem',
    lineHeight: '1.5',
  },
  '.cm-content': {
    caretColor: '#a78bfa',
    padding: '0',
  },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: '#a78bfa', borderLeftWidth: '2px' },
  '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection': {
    backgroundColor: 'rgba(139, 92, 246, 0.3)',
  },
  '.cm-gutters': {
    backgroundColor: '#16162a',
    color: '#4c5772',
    borderRight: '1px solid #2d2b55',
  },
  '.cm-activeLineGutter': { backgroundColor: 'rgba(139, 92, 246, 0.12)', color: '#8b8db5' },
  '.cm-activeLine': { backgroundColor: 'rgba(139, 92, 246, 0.06)' },
  '.cm-foldPlaceholder': { backgroundColor: '#2d2b55', border: 'none', color: '#8b8db5' },
  '.cm-matchingBracket': { backgroundColor: 'rgba(139, 92, 246, 0.25)', outline: '1px solid rgba(139, 92, 246, 0.5)' },
  '.cm-tooltip': {
    backgroundColor: '#1e1e3a',
    border: '1px solid #2d2b55',
    borderRadius: '6px',
    boxShadow: '0 4px 16px rgba(0,0,0,0.5)',
  },
  '.cm-tooltip-autocomplete': {
    '& > ul': { fontFamily: "'JetBrains Mono', 'Fira Code', monospace", fontSize: '0.8125rem' },
    '& > ul > li': { padding: '4px 8px' },
    '& > ul > li[aria-selected]': { backgroundColor: 'rgba(139, 92, 246, 0.3)', color: '#f1f5f9' },
  },
  '.cm-tooltip.cm-completionInfo': { padding: '8px 10px', maxWidth: '300px' },
  '.cm-completionDetail': { color: '#8b8db5', fontStyle: 'italic', marginLeft: '0.5em' },
  '.cm-panels': { backgroundColor: '#16162a', color: '#e2e8f0' },
  '.cm-panels.cm-panels-top': { borderBottom: '1px solid #2d2b55' },
  '.cm-panels.cm-panels-bottom': { borderTop: '1px solid #2d2b55' },
  '.cm-searchMatch': { backgroundColor: 'rgba(250, 204, 21, 0.2)', outline: '1px solid rgba(250, 204, 21, 0.4)' },
  '.cm-searchMatch.cm-searchMatch-selected': { backgroundColor: 'rgba(250, 204, 21, 0.4)' },
  '.cm-selectionMatch': { backgroundColor: 'rgba(139, 92, 246, 0.15)' },
}, { dark: true })

const darkHighlightStyle = HighlightStyle.define([
  { tag: tags.propertyName, color: '#79c0ff', fontWeight: '500' }, // YAML keys — bright blue
  { tag: tags.string, color: '#7ee787' },              // strings — vivid green
  { tag: tags.number, color: '#ffa657' },               // numbers — warm orange
  { tag: tags.bool, color: '#ff7eb6' },                 // booleans — hot pink
  { tag: tags.null, color: '#ff7eb6' },                 // null — hot pink
  { tag: tags.keyword, color: '#d2a8ff' },              // keywords — bright purple
  { tag: tags.comment, color: '#5c6a82', fontStyle: 'italic' }, // comments — subtle
  { tag: tags.meta, color: '#a5b4cf' },                 // directives
  { tag: tags.punctuation, color: '#8b949e' },          // colons, dashes
  { tag: tags.atom, color: '#ff7eb6' },                 // special values
  { tag: tags.labelName, color: '#79c0ff', fontWeight: '500' }, // anchors/aliases
])

// Light theme — rich saturated colors on warm paper background
const lightEditorTheme = EditorView.theme({
  '&': {
    backgroundColor: '#fafaf9',
    color: '#1c1917',
    fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
    fontSize: '0.8125rem',
    lineHeight: '1.5',
  },
  '.cm-content': {
    caretColor: '#7c3aed',
    padding: '0',
  },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: '#7c3aed', borderLeftWidth: '2px' },
  '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection': {
    backgroundColor: 'rgba(124, 58, 237, 0.15)',
  },
  '.cm-gutters': {
    backgroundColor: '#f5f5f4',
    color: '#a8a29e',
    borderRight: '1px solid #e7e5e4',
  },
  '.cm-activeLineGutter': { backgroundColor: 'rgba(124, 58, 237, 0.06)', color: '#78716c' },
  '.cm-activeLine': { backgroundColor: 'rgba(124, 58, 237, 0.03)' },
  '.cm-foldPlaceholder': { backgroundColor: '#e7e5e4', border: 'none', color: '#78716c' },
  '.cm-matchingBracket': { backgroundColor: 'rgba(124, 58, 237, 0.15)', outline: '1px solid rgba(124, 58, 237, 0.3)' },
  '.cm-tooltip': {
    backgroundColor: '#ffffff',
    border: '1px solid #e7e5e4',
    borderRadius: '6px',
    boxShadow: '0 4px 16px rgba(0,0,0,0.08)',
  },
  '.cm-tooltip-autocomplete': {
    '& > ul': { fontFamily: "'JetBrains Mono', 'Fira Code', monospace", fontSize: '0.8125rem' },
    '& > ul > li': { padding: '4px 8px' },
    '& > ul > li[aria-selected]': { backgroundColor: 'rgba(124, 58, 237, 0.1)', color: '#1c1917' },
  },
  '.cm-tooltip.cm-completionInfo': { padding: '8px 10px', maxWidth: '300px' },
  '.cm-completionDetail': { color: '#78716c', fontStyle: 'italic', marginLeft: '0.5em' },
  '.cm-panels': { backgroundColor: '#f5f5f4', color: '#1c1917' },
  '.cm-panels.cm-panels-top': { borderBottom: '1px solid #e7e5e4' },
  '.cm-panels.cm-panels-bottom': { borderTop: '1px solid #e7e5e4' },
  '.cm-searchMatch': { backgroundColor: 'rgba(234, 179, 8, 0.25)', outline: '1px solid rgba(234, 179, 8, 0.5)' },
  '.cm-searchMatch.cm-searchMatch-selected': { backgroundColor: 'rgba(234, 179, 8, 0.45)' },
  '.cm-selectionMatch': { backgroundColor: 'rgba(124, 58, 237, 0.08)' },
})

const lightHighlightStyle = HighlightStyle.define([
  { tag: tags.propertyName, color: '#0550ae', fontWeight: '500' }, // YAML keys — deep blue
  { tag: tags.string, color: '#116329' },               // strings — forest green
  { tag: tags.number, color: '#cf5500' },                // numbers — burnt orange
  { tag: tags.bool, color: '#cf222e' },                  // booleans — crimson
  { tag: tags.null, color: '#cf222e' },                  // null — crimson
  { tag: tags.keyword, color: '#8250df' },               // keywords — vivid purple
  { tag: tags.comment, color: '#a3a3a3', fontStyle: 'italic' }, // comments — soft gray
  { tag: tags.meta, color: '#57606a' },                  // directives
  { tag: tags.punctuation, color: '#6e7781' },           // colons, dashes
  { tag: tags.atom, color: '#cf222e' },                  // special values
  { tag: tags.labelName, color: '#0550ae', fontWeight: '500' }, // anchors/aliases
])

export const darkTheme = [darkEditorTheme, syntaxHighlighting(darkHighlightStyle)]
export const lightTheme = [lightEditorTheme, syntaxHighlighting(lightHighlightStyle)]

export function getThemeExtension(theme) {
  return theme === 'light' ? lightTheme : darkTheme
}
