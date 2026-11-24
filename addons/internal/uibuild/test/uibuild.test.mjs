import test from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { buildAddonUI } from '../uibuild.mjs'

const here = dirname(fileURLToPath(import.meta.url))

// Reads the 256x256 RGBA icon back into rows of pixels.
async function decode(png) {
  const { inflateSync } = await import('node:zlib')
  let off = 8, idat = []
  while (off < png.length) {
    const len = png.readUInt32BE(off)
    if (png.toString('latin1', off + 4, off + 8) === 'IDAT') idat.push(png.subarray(off + 8, off + 8 + len))
    off += 12 + len
  }
  const raw = inflateSync(Buffer.concat(idat))
  const size = png.readUInt32BE(16)
  const pixel = (x, y) => [...raw.subarray(y * (size * 4 + 1) + 1 + x * 4, y * (size * 4 + 1) + 1 + x * 4 + 4)]
  return { size, pixel }
}

test('buildAddonUI writes the module, the style sheet and a PNG icon', async () => {
  const dist = await buildAddonUI({ root: join(here, 'fixture'), name: 'fixture', rgb: [26, 62, 138] })
  assert.ok(existsSync(join(dist, 'ui/fixture.js')))
  assert.equal(readFileSync(join(dist, 'ui/fixture.css'), 'utf8'), readFileSync(join(here, 'fixture/src/style.css'), 'utf8'))
  const png = readFileSync(join(dist, 'ui/icon.png'))
  assert.deepEqual([...png.subarray(0, 8)], [137, 80, 78, 71, 13, 10, 26, 10])
  assert.ok(png.length < 256 * 1024, 'the icon must fit the 256 KiB manifest bound')
  const img = await decode(png)
  assert.equal(img.size, 256, 'the generated tile has the size of the committed icons')
  assert.deepEqual(img.pixel(12, 128).slice(0, 3), [26, 62, 138], 'the tile keeps the colour of the add-on')
  assert.equal(img.pixel(0, 0)[3], 0, 'the corners are transparent')
})

test('a generated tile carries a white line glyph, and a different glyph draws a different icon', async () => {
  const { tileIcon } = await import('../uibuild.mjs')
  const { GLYPHS } = await import('../glyphs.mjs')
  const mail = tileIcon([52, 99, 160], 'mail')
  const img = await decode(mail)
  assert.deepEqual(img.pixel(128, 128).length, 4)
  let white = 0
  for (let y = 0; y < 256; y += 2) for (let x = 0; x < 256; x += 2) { const p = img.pixel(x, y); if (p[0] > 240 && p[1] > 240 && p[2] > 240 && p[3] === 255) white++ }
  assert.ok(white > 300, 'the glyph is drawn in white on the tile')
  assert.ok(!mail.equals(tileIcon([52, 99, 160], 'hash')))
  assert.ok(tileIcon([1, 2, 3], 'no-such-glyph').equals(tileIcon([1, 2, 3], 'app')), 'an unknown glyph name draws the plain one')
  assert.ok(mail.length < 256 * 1024)
  for (const name of ['mail', 'memory', 'calendar', 'contacts', 'web', 'hash', 'send', 'chat', 'branch', 'home', 'play', 'screen', 'app']) assert.ok(GLYPHS[name], name)
})

test('a committed icon.png next to src is copied unchanged and no square is generated', async () => {
  const root = join(here, 'fixture-icon')
  const dist = await buildAddonUI({ root, name: 'fixture', rgb: [26, 62, 138] })
  const out = readFileSync(join(dist, 'ui/icon.png'))
  assert.ok(out.equals(readFileSync(join(root, 'icon.png'))), 'the packaged icon is the committed file')
  assert.equal(out.readUInt32BE(16), 256, 'the committed icon keeps its own size, not the 128 pixel square')
})

test('the bundle is an ES module that exports createAddon and calls h for JSX', async () => {
  const dist = await buildAddonUI({ root: join(here, 'fixture'), name: 'fixture', rgb: [1, 2, 3] })
  const js = readFileSync(join(dist, 'ui/fixture.js'), 'utf8')
  assert.match(js, /export\s*\{[^}]*createAddon/)
  assert.match(js, /\bh\(\s*["']p["']/)
  assert.doesNotMatch(js, /from\s*['"]react/)
})

test('a bare React import makes the build fail', async () => {
  await assert.rejects(buildAddonUI({ root: join(here, 'fixture-react'), name: 'x', rgb: [1, 2, 3] }), /must not import React.*react/)
})

test('a relative import of a host UI file makes the build fail', async () => {
  await assert.rejects(buildAddonUI({ root: join(here, 'fixture-core'), name: 'x', rgb: [1, 2, 3] }), /host UI file .*useObjectUrl\.js/)
})

test('compat mode answers host module names from the compat directory, collects imported style sheets and writes one file', async () => {
  const root = join(here, 'fixture-compat')
  const dist = await buildAddonUI({ root, name: 'fx', rgb: [1, 2, 3], compat: join(root, 'compat') })
  const js = readFileSync(join(dist, 'ui/fx.js'), 'utf8')
  assert.doesNotMatch(js, /from\s*['"]react/)
  assert.doesNotMatch(js, /^\s*import\s/m, 'the bundle is one file with no import')
  assert.match(js, /export\s*\{[^}]*createAddon/)
  assert.match(js, /badge/)
  const css = readFileSync(join(dist, 'ui/fx.css'), 'utf8')
  assert.match(css, /\/\* own \*\//)
  assert.match(css, /\.fx \{ color: red; \}/)
  assert.equal(css.split('.fx {').length - 1, 1, 'a sheet imported twice is added once')
})

test('compat mode still refuses a host UI file', async () => {
  await assert.rejects(buildAddonUI({ root: join(here, 'fixture-core'), name: 'x', rgb: [1, 2, 3], compat: join(here, 'fixture-compat/compat') }), /host UI file .*useObjectUrl\.js/)
})

test('compat mode runs the page: the page code reaches React through the stand-ins', async () => {
  const { createRequire } = await import('node:module')
  const { pathToFileURL } = await import('node:url')
  const require = createRequire(join(here, '../../../../core/http/react-ui/package.json'))
  const React = require('react')
  const { renderToString } = require('react-dom/server')
  const root = join(here, 'fixture-compat')
  const dist = await buildAddonUI({ root, name: 'fx', rgb: [1, 2, 3], compat: join(root, 'compat') })
  const mod = await import(pathToFileURL(join(dist, 'ui/fx.js')).href + `?t=${Date.now()}`)
  const addon = await mod.createAddon({ React })
  assert.match(renderToString(React.createElement(addon.Component)), /<p class="fx">compat <!-- -->3<b>badge<\/b><\/p>/)
})

test('every add-on with a UI commits a 256 pixel PNG icon under the manifest bound, and services record where the icon came from', async () => {
  const { readdirSync, statSync } = await import('node:fs')
  const root = join(here, '../../..')
  const missing = []
  for (const id of readdirSync(root)) {
    const manifest = join(root, id, 'manifest.json')
    if (!existsSync(manifest) || !existsSync(join(root, id, 'ui'))) continue
    if (!JSON.parse(readFileSync(manifest, 'utf8')).capabilities?.ui?.icon) continue
    const icon = join(root, id, 'ui/icon.png')
    if (!existsSync(icon)) { missing.push(id); continue }
    const png = readFileSync(icon)
    assert.deepEqual([...png.subarray(0, 8)], [137, 80, 78, 71, 13, 10, 26, 10], id)
    assert.equal(png.readUInt32BE(16), 256, `${id} icon width`)
    assert.equal(png.readUInt32BE(20), 256, `${id} icon height`)
    assert.ok(statSync(icon).size < 256 * 1024, id)
  }
  assert.deepEqual(missing, [], 'these add-ons declare an icon but commit no ui/icon.png')
  for (const id of ['slack', 'telegram', 'discord', 'github', 'gmail', 'gdrive', 'home-assistant', 'jellyfin']) {
    const note = readFileSync(join(root, id, 'ui/ICON-SOURCE.md'), 'utf8')
    assert.match(note, /Simple Icons/, id)
    assert.match(note, /CC0/, id)
    assert.match(note, /trademarks of their owner/, id)
  }
  // PiKVM has no mark in Simple Icons, so it keeps a generic glyph and says why.
  assert.match(readFileSync(join(root, 'pikvm/ui/ICON-SOURCE.md'), 'utf8'), /Why no brand mark/)
})
