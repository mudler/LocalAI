// The LocalAI theme: the CSS is generated from the one source file, the file
// fills the 36 kit roles in both modes, and every pair the kit checks keeps
// its WCAG 2.1 contrast (text 4.5, controls and chart series 3).
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { buildCss, loadTheme, isCurrent } from '../../scripts/gen-theme.mjs'

const root = join(dirname(fileURLToPath(import.meta.url)), '../..')
const theme = loadTheme()

const lum = hex => {
  const [r, g, b] = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map(c => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4))
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}
const ratio = (a, b) => {
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

const TEXT = [['text', 'canvas'], ['text', 'inset'], ['text', 'card'], ['text', 'hover'], ['text', 'accent-wash'],
  ['muted', 'canvas'], ['muted', 'inset'], ['muted', 'card'], ['on-accent', 'accent'],
  ['accent-text', 'canvas'], ['accent-text', 'card'], ['accent-text', 'accent-wash'],
  ['on-ok', 'ok'], ['ok-text', 'canvas'], ['ok-text', 'card'],
  ['on-warn', 'warn'], ['warn-text', 'canvas'], ['warn-text', 'card'],
  ['on-error', 'error'], ['error-text', 'canvas'], ['error-text', 'card'],
  ['ok-text', 'ok-wash'], ['warn-text', 'warn-wash'], ['error-text', 'error-wash'],
  ['muted', 'hover'], ['muted', 'accent-wash'], ['error-text', 'hover'], ['on-inverse', 'inverse']]
const CONTROL = [['control-edge', 'canvas'], ['control-edge', 'inset'], ['control-edge', 'card'],
  ['accent', 'canvas'], ['accent', 'card'], ['accent', 'inset'], ['ok', 'card'], ['warn', 'card'], ['error', 'card'],
  ...[1, 2, 3, 4, 5, 6].flatMap(k => [[`series-${k}`, 'canvas'], [`series-${k}`, 'card']])]

test('theme: the CSS is generated from the source file', () => {
  assert.ok(isCurrent(), 'src/theme-localai.css is out of date: run node scripts/gen-theme.mjs')
  const css = readFileSync(join(root, 'src/theme-localai.css'), 'utf8')
  assert.equal(css, buildCss(theme))
  assert.equal((css.match(/--dk-canvas:/g) || []).length, 3, 'one light and two dark blocks')
  assert.equal(readFileSync(join(root, '../static/theme-localai.css'), 'utf8'), css, 'the copy for the server-rendered pages')
})

test('theme: light and dark fill the same 36 roles', () => {
  assert.equal(Object.keys(theme.light).length, 36)
  assert.deepEqual(Object.keys(theme.dark), Object.keys(theme.light))
})

for (const mode of ['light', 'dark']) {
  test(`theme: every kit pair keeps its contrast in ${mode}`, () => {
    const t = theme[mode]
    for (const [fg, bg] of TEXT) assert.ok(ratio(t[fg], t[bg]) >= 4.5, `${mode} ${fg} on ${bg}: ${ratio(t[fg], t[bg]).toFixed(2)}`)
    for (const [fg, bg] of CONTROL) assert.ok(ratio(t[fg], t[bg]) >= 3, `${mode} ${fg} on ${bg}: ${ratio(t[fg], t[bg]).toFixed(2)}`)
  })
}
