import { EditorView } from '@codemirror/view'
import { HighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { tags } from '@lezer/highlight'

// The editor reads the app's colour variables, so it follows the theme roles
// (and the light/dark switch) without a second copy of the palette. Only the
// `dark` flag differs between the two themes: it tells CodeMirror which of its
// own built-in base styles (search panel, inputs) to use.
function editorTheme(dark) {
  return EditorView.theme({
    '&': {
      backgroundColor: 'var(--color-bg-primary)',
      color: 'var(--color-text-primary)',
      fontFamily: 'var(--font-mono)',
      fontSize: '0.8125rem',
      lineHeight: '1.5',
    },
    '.cm-content': {
      caretColor: 'var(--color-primary)',
      padding: '0',
    },
    '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--color-primary)', borderLeftWidth: '2px' },
    '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection': {
      backgroundColor: 'var(--color-selection-bg)',
    },
    '.cm-gutters': {
      backgroundColor: 'var(--color-bg-secondary)',
      color: 'var(--color-text-disabled)',
      borderRight: '1px solid var(--color-border-default)',
    },
    '.cm-activeLineGutter': {
      backgroundColor: 'color-mix(in srgb, var(--dk-accent) 10%, transparent)',
      color: 'var(--color-text-muted)',
    },
    '.cm-activeLine': { backgroundColor: 'color-mix(in srgb, var(--dk-accent) 6%, transparent)' },
    '.cm-foldPlaceholder': {
      backgroundColor: 'var(--color-bg-tertiary)',
      border: 'none',
      color: 'var(--color-text-muted)',
    },
    '.cm-matchingBracket': {
      backgroundColor: 'color-mix(in srgb, var(--dk-accent) 22%, transparent)',
      outline: '1px solid color-mix(in srgb, var(--dk-accent) 50%, transparent)',
    },
    '.cm-tooltip': {
      backgroundColor: 'var(--color-bg-secondary)',
      border: '1px solid var(--color-border-default)',
      borderRadius: 'var(--radius-md)',
      boxShadow: 'var(--shadow-md)',
    },
    '.cm-tooltip-autocomplete': {
      '& > ul': { fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' },
      '& > ul > li': { padding: 'var(--spacing-xs) var(--spacing-sm)' },
      '& > ul > li[aria-selected]': {
        backgroundColor: 'color-mix(in srgb, var(--dk-accent) 22%, transparent)',
        color: 'var(--color-text-primary)',
      },
    },
    '.cm-tooltip.cm-completionInfo': { padding: 'var(--spacing-sm)', maxWidth: '300px' },
    '.cm-completionDetail': { color: 'var(--color-text-muted)', fontStyle: 'italic', marginLeft: '0.5em' },
    '.cm-panels': { backgroundColor: 'var(--color-bg-secondary)', color: 'var(--color-text-primary)' },
    '.cm-panels.cm-panels-top': { borderBottom: '1px solid var(--color-border-default)' },
    '.cm-panels.cm-panels-bottom': { borderTop: '1px solid var(--color-border-default)' },
    '.cm-searchMatch': {
      backgroundColor: 'color-mix(in srgb, var(--dk-warn) 24%, transparent)',
      outline: '1px solid color-mix(in srgb, var(--dk-warn) 45%, transparent)',
    },
    '.cm-searchMatch.cm-searchMatch-selected': {
      backgroundColor: 'color-mix(in srgb, var(--dk-warn) 42%, transparent)',
    },
    '.cm-selectionMatch': { backgroundColor: 'color-mix(in srgb, var(--dk-accent) 12%, transparent)' },
  }, { dark })
}

const highlightStyle = HighlightStyle.define([
  { tag: tags.propertyName, color: 'var(--dk-accent-text)', fontWeight: '500' }, // YAML keys
  { tag: tags.string, color: 'var(--dk-ok-text)' },
  { tag: tags.number, color: 'var(--color-data-6)' },
  { tag: tags.bool, color: 'var(--color-data-3)' },
  { tag: tags.null, color: 'var(--color-data-3)' },
  { tag: tags.keyword, color: 'var(--color-data-7)' },
  { tag: tags.comment, color: 'var(--color-text-muted)', fontStyle: 'italic' },
  { tag: tags.meta, color: 'var(--color-text-secondary)' },                       // directives
  { tag: tags.punctuation, color: 'var(--color-data-8)' },                         // colons, dashes
  { tag: tags.atom, color: 'var(--dk-error-text)' },                               // special values
  { tag: tags.labelName, color: 'var(--dk-accent-text)', fontWeight: '500' },      // anchors and aliases
])

export const darkTheme = [editorTheme(true), syntaxHighlighting(highlightStyle)]
export const lightTheme = [editorTheme(false), syntaxHighlighting(highlightStyle)]

export function getThemeExtension(theme) {
  return theme === 'light' ? lightTheme : darkTheme
}
