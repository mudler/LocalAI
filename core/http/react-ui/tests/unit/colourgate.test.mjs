// The colour ratchet (scripts/colour-gate.mjs): what counts as a literal
// colour, what is left alone, and how a run compares with the baseline.
import test from 'node:test'
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { scanFile, countAll, compare, formatBaseline, parseBaseline } from '../../scripts/colour-gate.mjs'

const root = join(dirname(fileURLToPath(import.meta.url)), '../..')
const count = (path, src) => scanFile(path, src).length

test('colour gate: hex values and colour functions are literals', () => {
  assert.equal(count('src/a.css', '.a { color: #fff; background: #0b6663; border-color: #0b666380 }'), 3)
  assert.equal(count('src/a.css', '.a { background: rgba(0, 0, 0, .2); color: rgb(0 0 0 / .2); fill: hsl(10 50% 50%) }'), 3)
  assert.equal(count('src/a.jsx', "const c = flag ? '#abc' : 'var(--dk-text)'"), 1)
})

test('colour gate: roles, tints from a role, comments, masks and entities are not literals', () => {
  assert.equal(count('src/a.css', '.a { color: var(--dk-text); box-shadow: 0 1px rgb(var(--dk-shadow-tint) / .1) }'), 0)
  assert.equal(count('src/a.css', '/* was #fff */ .a { color: color-mix(in srgb, var(--dk-text) 20%, transparent) }'), 0)
  assert.equal(count('src/a.css', '.a { mask-image: linear-gradient(to right, #000, transparent) }'), 0)
  assert.equal(count('src/a.jsx', "// see #fff\nconst h = '&#10003;'; const a = '#aed-sec-card'; const u = 'x/#abc'"), 0)
})

test('colour gate: the palette files and the vendor colours are exempt', () => {
  assert.equal(count('src/theme-localai.css', ':root { --dk-canvas: #fff4dc }'), 0)
  assert.equal(count('src/theme/theme-localai.json', '#fff4dc'), 0)
  assert.equal(count('src/vendor/ui-kit/tokens.css', ':root { --x: #fff }'), 0)
  assert.equal(count('src/utils/format.js', "return '#76b900'"), 0)
})

test('colour gate: a new literal fails, a removed one asks for a refresh', () => {
  const base = parseBaseline('colour src/a.css 2\n')
  const files = n => countAll([['src/a.css', '.a { color: #fff }\n'.repeat(n)]])
  assert.deepEqual(compare(files(2), base), { added: [], stale: [] })
  assert.equal(compare(files(3), base).added.length, 1)
  assert.equal(compare(files(1), base).stale.length, 1)
  assert.equal(formatBaseline(files(2)), 'colour src/a.css 2\n')
})

test('colour gate: the saved baseline matches the tree', () => {
  const out = execFileSync('node', ['scripts/colour-gate.mjs'], { cwd: root, encoding: 'utf8' })
  assert.match(out, /at baseline/)
})
