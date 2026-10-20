import test from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { buildAddonUI } from '../uibuild.mjs'

const here = dirname(fileURLToPath(import.meta.url))

// The solid icon is 128x128 RGBA; read the first pixel's colour from the PNG.
async function firstPixel(png) {
  const { inflateSync } = await import('node:zlib')
  let off = 8, idat = []
  while (off < png.length) {
    const len = png.readUInt32BE(off)
    if (png.toString('latin1', off + 4, off + 8) === 'IDAT') idat.push(png.subarray(off + 8, off + 8 + len))
    off += 12 + len
  }
  const raw = inflateSync(Buffer.concat(idat))
  return [...raw.subarray(64 * (128 * 4 + 1) + 1 + 64 * 4, 64 * (128 * 4 + 1) + 1 + 64 * 4 + 3)]
}

test('buildAddonUI writes the module, the style sheet and a PNG icon', async () => {
  const dist = await buildAddonUI({ root: join(here, 'fixture'), name: 'fixture', rgb: [26, 62, 138] })
  assert.ok(existsSync(join(dist, 'ui/fixture.js')))
  assert.equal(readFileSync(join(dist, 'ui/fixture.css'), 'utf8'), readFileSync(join(here, 'fixture/src/style.css'), 'utf8'))
  const png = readFileSync(join(dist, 'ui/icon.png'))
  assert.deepEqual([...png.subarray(0, 8)], [137, 80, 78, 71, 13, 10, 26, 10])
  assert.ok(png.length < 256 * 1024, 'the icon must fit the 256 KiB manifest bound')
  assert.deepEqual(await firstPixel(png), [26, 62, 138])
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
