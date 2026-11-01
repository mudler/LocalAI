// Checks the design tokens in src/theme.css and ratchets the raw values the
// tokens replace. The ratchet works like inline-style-baseline.txt: the counts
// in css-literal-baseline.json may only go down. After converting rules to
// tokens, lower the numbers in the same change.
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = join(dirname(fileURLToPath(import.meta.url)), '../..')
const src = join(root, 'src')
const theme = readFileSync(join(src, 'theme.css'), 'utf8')
const index = readFileSync(join(src, 'index.css'), 'utf8')

// The value of a custom property in the first :root block that sets it.
function token(name) {
  const m = theme.match(new RegExp(`\\n\\s*${name}:\\s*([^;]+);`))
  return m ? m[1].trim() : undefined
}

test('the kit font roles point at the product fonts', () => {
  assert.equal(token('--dk-font-body'), 'var(--font-sans)')
  assert.equal(token('--dk-font-display'), 'var(--font-sans)')
  assert.equal(token('--dk-font-mono'), 'var(--font-mono)')
  // The packages register the faces under these names; they come first.
  assert.match(token('--font-sans'), /^"Geist Variable"/)
  assert.match(token('--font-mono'), /^"Geist Mono Variable"/)
})

test('the type, space, radius, control and motion scales are defined', () => {
  const expected = {
    '--fs-meta': '12px', '--fs-small': '13px', '--fs-body': '15px', '--fs-lead': '17px',
    '--fs-title': '28px', '--fs-display': '40px',
    '--sp-1': '4px', '--sp-2': '8px', '--sp-3': '12px', '--sp-4': '16px', '--sp-5': '20px',
    '--sp-6': '24px', '--sp-8': '32px', '--sp-10': '40px', '--sp-14': '56px',
    '--r-xs': '4px', '--r-sm': '6px', '--r-md': '8px', '--r-lg': '12px', '--r-xl': '16px',
    '--ctl-h': '36px', '--ctl-h-sm': '28px', '--ctl-h-lg': '40px',
    '--dur-instant': '90ms', '--dur-fast': '140ms', '--dur-base': '200ms', '--dur-slow': '280ms', '--dur-page': '180ms',
  }
  for (const [name, value] of Object.entries(expected)) assert.equal(token(name), value, name)
  for (const name of ['--ease-out', '--ease-out-strong', '--ease-in', '--ease-in-out', '--focus-ring']) {
    assert.ok(token(name), `${name} is defined`)
  }
})

test('the old token names are aliases of the new scale', () => {
  assert.equal(token('--control-h'), 'var(--ctl-h)')
  assert.equal(token('--control-h-sm'), 'var(--ctl-h-sm)')
  assert.equal(token('--text-sm'), 'var(--fs-small)')
  assert.equal(token('--radius-md'), 'var(--r-md)')
  assert.equal(token('--dk-radius-2'), 'var(--r-md)')
  assert.equal(token('--duration-fast'), 'var(--dur-fast)')
  assert.equal(token('--dk-dur-1'), 'var(--dur-fast)')
  assert.equal(token('--dk-ease-spring'), 'var(--ease-out)')
})

test('no easing in the theme overshoots', () => {
  for (const css of [theme, index]) {
    for (const m of css.matchAll(/cubic-bezier\(([^)]+)\)/g)) {
      const [, y1, , y2] = m[1].split(',').map(Number)
      assert.ok(y1 >= 0 && y1 <= 1 && y2 >= 0 && y2 <= 1, `${m[0]} overshoots`)
    }
  }
})

test('a coarse pointer keeps 40px controls and reduced motion has a rule', () => {
  assert.match(theme, /@media \(pointer: coarse\)\s*\{\s*:root\s*\{\s*--ctl-h: var\(--ctl-h-lg\);/)
  assert.match(index, /@media \(prefers-reduced-motion: reduce\)/)
})

// Raw values outside the token files. theme.css defines the tokens; the
// vendored kit is generated and is overridden, not edited.
function cssFiles(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) {
      if (entry !== 'vendor') cssFiles(full, out)
    } else if (entry.endsWith('.css') && full !== join(src, 'theme.css')) out.push(full)
  }
  return out
}

function countLiterals() {
  const counts = { 'font-size': 0, 'border-radius': 0, '@keyframes': 0 }
  for (const file of cssFiles(src)) {
    const css = readFileSync(file, 'utf8').replace(/\/\*[\s\S]*?\*\//g, '')
    counts['font-size'] += (css.match(/font-size:\s*[0-9.]+(px|rem|em)\b/g) || []).length
    counts['border-radius'] += (css.match(/border-radius:[^;}]*\b[1-9][0-9.]*(px|rem)/g) || []).length
    counts['@keyframes'] += (css.match(/@keyframes\s/g) || []).length
  }
  return counts
}

test('raw font sizes, radii and keyframes do not grow', () => {
  const baseline = JSON.parse(readFileSync(join(root, 'css-literal-baseline.json'), 'utf8'))
  const now = countLiterals()
  for (const key of Object.keys(now)) {
    assert.ok(now[key] <= baseline[key], `${key}: ${now[key]} raw values, baseline ${baseline[key]}; use a token from src/theme.css`)
  }
})

