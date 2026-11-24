// The "AI" of the product name and the first-run llama belong to the bundled
// brand only. These checks read the sources: the colour is a theme role with
// 4.5:1 on every surface that shows the name, the mascot is gated on the
// default branding, and the logo and favicon paths are left as they were.
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, statSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { loadTheme } from '../../scripts/gen-theme.mjs'

const root = join(dirname(fileURLToPath(import.meta.url)), '../..')
const read = f => readFileSync(join(root, f), 'utf8')
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

test('brand name: the AI colour is a theme role, teal on light and yellow on dark', () => {
  const css = read('src/theme.css')
  assert.match(css, /:root[^{]*\{[^}]*--color-brand-ai: var\(--brand-logo-teal\)/s)
  assert.match(css, /\[data-theme="dark"\] \{\s*--color-brand-ai: var\(--brand-yellow\)/)
  assert.match(read('src/components/brand-name.css'), /\.brand-ai \{ color: var\(--color-brand-ai\); \}/)
})

for (const [mode, brand] of [['light', 'logo-teal'], ['dark', 'yellow']]) {
  test(`brand name: the AI keeps 4.5:1 on the surfaces that show the name in ${mode}`, () => {
    for (const bg of ['canvas', 'inset', 'card']) {
      const r = ratio(theme.brand[brand], theme[mode][bg])
      assert.ok(r >= 4.5, `${mode} ${brand} on ${bg}: ${r.toFixed(2)}`)
    }
  })
}

test('brand name: only the bundled name gets the span', () => {
  const src = read('src/components/BrandName.jsx')
  assert.match(src, /if \(!isDefaultName\(instanceName\)\) return instanceName/)
  const ctx = read('src/contexts/BrandingContext.jsx')
  assert.match(ctx, /isDefaultName = \(name\) => name === DEFAULT_BRANDING\.instanceName/)
})

test('brand name: the sidebar, login and mobile title use it, the tile and favicon code is untouched', () => {
  assert.match(read('src/components/Sidebar.jsx'), /sidebar-wordmark"><BrandName \/>/)
  assert.match(read('src/pages/Login.jsx'), /lg-brand__name"><BrandName \/>/)
  assert.match(read('src/App.jsx'), /mobile-title"><BrandName \/>/)
  const sb = read('src/components/Sidebar.jsx')
  assert.match(sb, /branding\.logoHorizontalUrl === DEFAULT_BRANDING\.logoHorizontalUrl/)
  assert.match(sb, /src=\{apiUrl\(branding\.logoUrl\)\}/)
  assert.match(read('src/contexts/BrandingContext.jsx'), /link\.href = href/)
})

test('first-run mascot: shown only for the default name and logo', () => {
  const ctx = read('src/contexts/BrandingContext.jsx')
  assert.match(ctx, /isDefaultName\(b\?\.instanceName\) && b\?\.logoUrl === DEFAULT_BRANDING\.logoUrl/)
  const guide = read('src/components/home/HomeFirstRun.jsx')
  assert.match(guide, /const showMascot = isDefaultBranding\(branding\)/)
  assert.equal((guide.match(/\{showMascot && <Mascot pose="circle"/g) || []).length, 2, 'step 1 of the local and of the remote mode')
})

test('first-run mascot: a 208 px image under 20 KB for the 104 px badge', () => {
  const size = statSync(join(root, 'packages/ui-kit/src/assets/mascot-circle.webp')).size
  assert.ok(size < 20 * 1024, `mascot-circle.webp is ${size} bytes`)
  assert.match(read('packages/ui-kit/css/kit.css'), /\.ui-mascot--badge \{ height: 104px; \}/)
})
