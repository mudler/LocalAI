import test from 'node:test'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { dirname, join, resolve } from 'node:path'
import { load } from './render.test.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const require = createRequire(join(resolve(here, '../../../../core/http/react-ui'), 'package.json'))
const React = require('react')
const { renderToString } = require('react-dom/server')

test('recognition: the first paint has the faces page test id', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const html = renderToString(React.createElement(addon.Component, { page: 'recognition', path: '' }))
  assert.match(html, /data-testid="faces-page"/)
})

test('recognition: a viewer without face_recognition gets the off notice', async () => {
  const { mod, sdk } = await load({ viewer: { isAdmin: () => false, hasFeature: (f) => f !== 'face_recognition' } })
  const addon = await mod.createAddon(sdk)
  const html = renderToString(React.createElement(addon.Component, { page: 'recognition', path: '' }))
  assert.match(html, /data-testid="face-off"/)
  assert.doesNotMatch(html, /idn-bench/)
})

test('recognition: register, identify and forget use the core face routes', async () => {
  const { createApi } = await import('../src/api.js')
  const seen = []
  const ctx = { core: { fetch: async (p, i) => { seen.push([p, i?.method]); return { ok: true, json: async () => ({}) } } }, json: async (p, i) => { seen.push([p, i?.method]); return {} } }
  const api = createApi(ctx)
  await api.face.register({ model: 'm', img: 'x', name: 'a' })
  await api.face.identify({ model: 'm', img: 'x' })
  await api.face.forget({ model: 'm', id: 'a' })
  assert.deepEqual(seen.map(s => s[0]), ['/v1/face/register', '/v1/face/identify', '/v1/face/forget'])
  assert.deepEqual(seen.map(s => s[1]), ['POST', 'POST', 'POST'])
})

test('recognition: any viewer who holds face_recognition gets the library, administrator or not', async () => {
  const { mod, sdk } = await load({ viewer: { isAdmin: () => false, hasFeature: () => true } })
  const addon = await mod.createAddon(sdk)
  const html = renderToString(React.createElement(addon.Component, { page: 'recognition', path: '' }))
  assert.match(html, /idn-bench/)
  assert.doesNotMatch(html, /data-testid="face-off"/)
})

test('recognition: the model of the path reaches the model selection', async () => {
  const seen = []
  const names = ['Button', 'Spinner', 'EmptyState', 'Badge', 'Stack', 'Text', 'PageHeader', 'ConfirmDialog', 'ErrorWithTraceLink', 'MediaInput', 'WaveformPlayer', 'DistributionBars', 'EmbeddingInspector', 'TabSwitch']
  const components = Object.fromEntries(names.map(n => [n, (p) => React.createElement('div', null, p.title || p.label || p.children || null)]))
  components.PageHeader = (p) => React.createElement('header', null, p.title, p.actions)
  components.ModelSelector = (p) => { seen.push(p.value); return React.createElement('div') }
  const { mod, sdk } = await load({ components })
  const addon = await mod.createAddon(sdk)
  renderToString(React.createElement(addon.Component, { page: 'recognition', path: 'insightface' }))
  assert.deepEqual(seen, ['insightface'])
})

test('recognition: the bundle has the face canvas, the face strings and no host import', async () => {
  const { readFileSync } = await import('node:fs')
  const { build } = await import('../build.mjs')
  const dir = await build()
  const js = readFileSync(join(dir, 'ui/face.js'), 'utf8')
  assert.match(js, /biometrics-bbox/)
  assert.doesNotMatch(js, /from ['"]react(-dom)?/)
  assert.match(js, /faces\.title/)
})

test('recognition: every translation key the sources read is in the Face bundle', async () => {
  const { readFileSync, readdirSync, statSync } = await import('node:fs')
  const { default: en } = await import('../src/i18n/en.js')
  const roots = [join(here, '../src'), join(here, '../../../internal/identity-ui/src')]
  const files = []
  const walk = (d) => { for (const f of readdirSync(d)) { const p = join(d, f); if (statSync(p).isDirectory()) walk(p); else if (/\.jsx?$/.test(p) && !p.includes('i18n')) files.push(p) } }
  roots.forEach(walk)
  const missing = []
  for (const f of files) {
    for (const m of readFileSync(f, 'utf8').matchAll(/\bt\(\s*'([a-zA-Z]+\.[a-zA-Z0-9_.]+)'/g)) {
      // The voice branch of a shared view is never reached on this page.
      if (m[1].startsWith('voice.')) continue
      if (!(m[1] in en)) missing.push(`${f.split('/').pop()}: ${m[1]}`)
    }
  }
  assert.deepEqual(missing, [])
})
