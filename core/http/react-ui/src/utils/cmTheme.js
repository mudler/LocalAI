import { EditorView } from '@codemirror/view'
import { HighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { tags } from '@lezer/highlight'

// Dark theme — restated from theme.css because CodeMirror cannot read CSS
// variables. Keep in step with the tokens or the editor drifts off-palette.
const darkEditorTheme = EditorView.theme({
  '&': {
    backgroundColor: '#0d1117',
    color: '#edf4fc',
    fontFamily: 'var(--font-mono)',
    fontSize: '0.8125rem',
    lineHeight: '1.5',
  },
  '.cm-content': {
    caretColor: '#4f8cff',
    padding: '0',
  },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: '#4f8cff', borderLeftWidth: '2px' },
  '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection': {
    backgroundColor: 'rgba(79, 140, 255, 0.25)',
  },
  '.cm-gutters': {
    backgroundColor: '#131a23',
    color: '#67748a',
    borderRight: '1px solid #223046',
  },
  '.cm-activeLineGutter': { backgroundColor: 'rgba(79, 140, 255, 0.1)', color: '#9aabc0' },
  '.cm-activeLine': { backgroundColor: 'rgba(79, 140, 255, 0.06)' },
  '.cm-foldPlaceholder': { backgroundColor: '#223046', border: 'none', color: '#9aabc0' },
  '.cm-matchingBracket': { backgroundColor: 'rgba(79, 140, 255, 0.22)', outline: '1px solid rgba(79, 140, 255, 0.5)' },
  '.cm-tooltip': {
    backgroundColor: '#131a23',
    border: '1px solid #223046',
    borderRadius: 'var(--radius-md)',
    boxShadow: '0 4px 16px rgba(0,0,0,0.5)',
  },
  '.cm-tooltip-autocomplete': {
    '& > ul': { fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' },
    '& > ul > li': { padding: 'var(--spacing-xs) var(--spacing-sm)' },
    '& > ul > li[aria-selected]': { backgroundColor: 'rgba(79, 140, 255, 0.22)', color: '#edf4fc' },
  },
  '.cm-tooltip.cm-completionInfo': { padding: 'var(--spacing-sm)', maxWidth: '300px' },
  '.cm-completionDetail': { color: '#9aabc0', fontStyle: 'italic', marginLeft: '0.5em' },
  '.cm-panels': { backgroundColor: '#131a23', color: '#edf4fc' },
  '.cm-panels.cm-panels-top': { borderBottom: '1px solid #223046' },
  '.cm-panels.cm-panels-bottom': { borderTop: '1px solid #223046' },
  '.cm-searchMatch': { backgroundColor: 'rgba(235, 203, 139, 0.2)', outline: '1px solid rgba(235, 203, 139, 0.45)' },
  '.cm-searchMatch.cm-searchMatch-selected': { backgroundColor: 'rgba(235, 203, 139, 0.42)' },
  '.cm-selectionMatch': { backgroundColor: 'rgba(79, 140, 255, 0.12)' },
}, { dark: true })

const darkHighlightStyle = HighlightStyle.define([
  { tag: tags.propertyName, color: '#4f8cff', fontWeight: '500' }, // YAML keys — action blue
  { tag: tags.string, color: '#56d6a4' },               // strings — mint
  { tag: tags.number, color: '#d08770' },               // numbers — aurora orange
  { tag: tags.bool, color: '#b48ead' },                 // booleans — aurora purple
  { tag: tags.null, color: '#b48ead' },                 // null — aurora purple
  { tag: tags.keyword, color: '#7aa7e8' },              // keywords — frost blue
  { tag: tags.comment, color: '#67748a', fontStyle: 'italic' }, // comments — muted
  { tag: tags.meta, color: '#d3dee9' },                 // directives — snow storm
  { tag: tags.punctuation, color: '#5ec8c0' },          // colons, dashes — frost teal
  { tag: tags.atom, color: '#bf616a' },                 // special values — aurora red
  { tag: tags.labelName, color: '#4f8cff', fontWeight: '500' }, // anchors/aliases
])

// Light theme — Nord snow-storm surfaces with darkened aurora highlighting
const lightEditorTheme = EditorView.theme({
  '&': {
    backgroundColor: '#ffffff',
    color: '#2e3440',
    fontFamily: 'var(--font-mono)',
    fontSize: '0.8125rem',
    lineHeight: '1.5',
  },
  '.cm-content': {
    caretColor: '#5e81ac',
    padding: '0',
  },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: '#5e81ac', borderLeftWidth: '2px' },
  '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection': {
    backgroundColor: 'rgba(94, 129, 172, 0.18)',
  },
  '.cm-gutters': {
    backgroundColor: '#e5e9f0',
    color: '#67748a',
    borderRight: '1px solid #d3dee9',
  },
  '.cm-activeLineGutter': { backgroundColor: 'rgba(94, 129, 172, 0.1)', color: '#3b4252' },
  '.cm-activeLine': { backgroundColor: 'rgba(94, 129, 172, 0.05)' },
  '.cm-foldPlaceholder': { backgroundColor: '#d3dee9', border: 'none', color: '#4c566a' },
  '.cm-matchingBracket': { backgroundColor: 'rgba(94, 129, 172, 0.18)', outline: '1px solid rgba(94, 129, 172, 0.35)' },
  '.cm-tooltip': {
    backgroundColor: '#ffffff',
    border: '1px solid #d3dee9',
    borderRadius: 'var(--radius-md)',
    boxShadow: '0 4px 16px rgba(46, 52, 64, 0.12)',
  },
  '.cm-tooltip-autocomplete': {
    '& > ul': { fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' },
    '& > ul > li': { padding: 'var(--spacing-xs) var(--spacing-sm)' },
    '& > ul > li[aria-selected]': { backgroundColor: 'rgba(94, 129, 172, 0.14)', color: '#2e3440' },
  },
  '.cm-tooltip.cm-completionInfo': { padding: 'var(--spacing-sm)', maxWidth: '300px' },
  '.cm-completionDetail': { color: '#67748a', fontStyle: 'italic', marginLeft: '0.5em' },
  '.cm-panels': { backgroundColor: '#e5e9f0', color: '#2e3440' },
  '.cm-panels.cm-panels-top': { borderBottom: '1px solid #d3dee9' },
  '.cm-panels.cm-panels-bottom': { borderTop: '1px solid #d3dee9' },
  '.cm-searchMatch': { backgroundColor: 'rgba(176, 131, 52, 0.22)', outline: '1px solid rgba(176, 131, 52, 0.45)' },
  '.cm-searchMatch.cm-searchMatch-selected': { backgroundColor: 'rgba(176, 131, 52, 0.4)' },
  '.cm-selectionMatch': { backgroundColor: 'rgba(94, 129, 172, 0.1)' },
})

const lightHighlightStyle = HighlightStyle.define([
  { tag: tags.propertyName, color: '#5e81ac', fontWeight: '500' }, // YAML keys — frost blue
  { tag: tags.string, color: '#4c6b3a' },                // strings — deep aurora green
  { tag: tags.number, color: '#b8684f' },                // numbers — warm orange
  { tag: tags.bool, color: '#8b5a92' },                  // booleans — muted purple
  { tag: tags.null, color: '#8b5a92' },                  // null — muted purple
  { tag: tags.keyword, color: '#4c6d92' },               // keywords — deeper frost
  { tag: tags.comment, color: '#7a8598', fontStyle: 'italic' }, // comments — cool gray
  { tag: tags.meta, color: '#3b4252' },                  // directives
  { tag: tags.punctuation, color: '#5a8080' },           // colons, dashes — muted teal
  { tag: tags.atom, color: '#a13e47' },                  // special values — deep aurora red
  { tag: tags.labelName, color: '#5e81ac', fontWeight: '500' }, // anchors/aliases
])

export const darkTheme = [darkEditorTheme, syntaxHighlighting(darkHighlightStyle)]
export const lightTheme = [lightEditorTheme, syntaxHighlighting(lightHighlightStyle)]

export function getThemeExtension(theme) {
  return theme === 'light' ? lightTheme : darkTheme
}
