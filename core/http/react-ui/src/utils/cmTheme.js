import { EditorView } from '@codemirror/view'
import { HighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { tags } from '@lezer/highlight'

// Dark theme — Nord polar-night surfaces with aurora syntax highlighting
const darkEditorTheme = EditorView.theme({
  '&': {
    backgroundColor: '#13171f',
    color: '#eceff4',
    fontFamily: 'var(--font-mono)',
    fontSize: '0.8125rem',
    lineHeight: '1.5',
  },
  '.cm-content': {
    caretColor: '#88c0d0',
    padding: '0',
  },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: '#88c0d0', borderLeftWidth: '2px' },
  '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection': {
    backgroundColor: 'rgba(136, 192, 208, 0.25)',
  },
  '.cm-gutters': {
    backgroundColor: '#1a1f2a',
    color: '#6e7a8c',
    borderRight: '1px solid #2f3644',
  },
  '.cm-activeLineGutter': { backgroundColor: 'rgba(136, 192, 208, 0.1)', color: '#a1acb9' },
  '.cm-activeLine': { backgroundColor: 'rgba(136, 192, 208, 0.06)' },
  '.cm-foldPlaceholder': { backgroundColor: '#2f3644', border: 'none', color: '#a1acb9' },
  '.cm-matchingBracket': { backgroundColor: 'rgba(136, 192, 208, 0.22)', outline: '1px solid rgba(136, 192, 208, 0.5)' },
  '.cm-tooltip': {
    backgroundColor: '#1a1f2a',
    border: '1px solid #2f3644',
    borderRadius: 'var(--radius-md)',
    boxShadow: '0 4px 16px rgba(0,0,0,0.5)',
  },
  '.cm-tooltip-autocomplete': {
    '& > ul': { fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' },
    '& > ul > li': { padding: 'var(--spacing-xs) var(--spacing-sm)' },
    '& > ul > li[aria-selected]': { backgroundColor: 'rgba(136, 192, 208, 0.22)', color: '#eceff4' },
  },
  '.cm-tooltip.cm-completionInfo': { padding: 'var(--spacing-sm)', maxWidth: '300px' },
  '.cm-completionDetail': { color: '#a1acb9', fontStyle: 'italic', marginLeft: '0.5em' },
  '.cm-panels': { backgroundColor: '#1a1f2a', color: '#eceff4' },
  '.cm-panels.cm-panels-top': { borderBottom: '1px solid #2f3644' },
  '.cm-panels.cm-panels-bottom': { borderTop: '1px solid #2f3644' },
  '.cm-searchMatch': { backgroundColor: 'rgba(235, 203, 139, 0.2)', outline: '1px solid rgba(235, 203, 139, 0.45)' },
  '.cm-searchMatch.cm-searchMatch-selected': { backgroundColor: 'rgba(235, 203, 139, 0.42)' },
  '.cm-selectionMatch': { backgroundColor: 'rgba(136, 192, 208, 0.12)' },
}, { dark: true })

const darkHighlightStyle = HighlightStyle.define([
  { tag: tags.propertyName, color: '#88c0d0', fontWeight: '500' }, // YAML keys — frost cyan
  { tag: tags.string, color: '#a3be8c' },               // strings — aurora green
  { tag: tags.number, color: '#d08770' },               // numbers — aurora orange
  { tag: tags.bool, color: '#b48ead' },                 // booleans — aurora purple
  { tag: tags.null, color: '#b48ead' },                 // null — aurora purple
  { tag: tags.keyword, color: '#81a1c1' },              // keywords — frost blue
  { tag: tags.comment, color: '#6e7a8c', fontStyle: 'italic' }, // comments — muted
  { tag: tags.meta, color: '#d8dee9' },                 // directives — snow storm
  { tag: tags.punctuation, color: '#8fbcbb' },          // colons, dashes — frost teal
  { tag: tags.atom, color: '#bf616a' },                 // special values — aurora red
  { tag: tags.labelName, color: '#88c0d0', fontWeight: '500' }, // anchors/aliases
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
    color: '#6e7a8c',
    borderRight: '1px solid #d8dee9',
  },
  '.cm-activeLineGutter': { backgroundColor: 'rgba(94, 129, 172, 0.1)', color: '#3b4252' },
  '.cm-activeLine': { backgroundColor: 'rgba(94, 129, 172, 0.05)' },
  '.cm-foldPlaceholder': { backgroundColor: '#d8dee9', border: 'none', color: '#4c566a' },
  '.cm-matchingBracket': { backgroundColor: 'rgba(94, 129, 172, 0.18)', outline: '1px solid rgba(94, 129, 172, 0.35)' },
  '.cm-tooltip': {
    backgroundColor: '#ffffff',
    border: '1px solid #d8dee9',
    borderRadius: 'var(--radius-md)',
    boxShadow: '0 4px 16px rgba(46, 52, 64, 0.12)',
  },
  '.cm-tooltip-autocomplete': {
    '& > ul': { fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' },
    '& > ul > li': { padding: 'var(--spacing-xs) var(--spacing-sm)' },
    '& > ul > li[aria-selected]': { backgroundColor: 'rgba(94, 129, 172, 0.14)', color: '#2e3440' },
  },
  '.cm-tooltip.cm-completionInfo': { padding: 'var(--spacing-sm)', maxWidth: '300px' },
  '.cm-completionDetail': { color: '#6e7a8c', fontStyle: 'italic', marginLeft: '0.5em' },
  '.cm-panels': { backgroundColor: '#e5e9f0', color: '#2e3440' },
  '.cm-panels.cm-panels-top': { borderBottom: '1px solid #d8dee9' },
  '.cm-panels.cm-panels-bottom': { borderTop: '1px solid #d8dee9' },
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
