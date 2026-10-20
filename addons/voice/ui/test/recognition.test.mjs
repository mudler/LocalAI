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

test('recognition: the first paint has the core test id and the page title', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const html = renderToString(React.createElement(addon.Component, { page: 'recognition', path: '' }))
  assert.match(html, /data-testid="voices-page"/)
  assert.match(html, /Voices/)
})

test('recognition: a viewer without voice_recognition gets the off notice, not the workbench', async () => {
  const { mod, sdk } = await load({ viewer: { isAdmin: () => false, hasFeature: (f) => f !== 'voice_recognition' } })
  const addon = await mod.createAddon(sdk)
  const html = renderToString(React.createElement(addon.Component, { page: 'recognition', path: '' }))
  assert.match(html, /data-testid="voice-off"/)
  assert.doesNotMatch(html, /idn-bench/)
})

test('recognition: the model of the path reaches the model selection', async () => {
  const seen = []
  const names = ['Button', 'Spinner', 'EmptyState', 'Badge', 'Stack', 'Text', 'PageHeader', 'ConfirmDialog', 'ErrorWithTraceLink', 'MediaInput', 'WaveformPlayer', 'DistributionBars', 'EmbeddingInspector', 'TabSwitch']
  const components = Object.fromEntries(names.map(n => [n, (p) => React.createElement('div', null, p.title || p.label || p.children || null)]))
  components.PageHeader = (p) => React.createElement('header', null, p.title, p.actions)
  components.ModelSelector = (p) => { seen.push(p.value); return React.createElement('div') }
  const { mod, sdk } = await load({ components })
  const addon = await mod.createAddon(sdk)
  renderToString(React.createElement(addon.Component, { page: 'recognition', path: 'speechbrain' }))
  assert.deepEqual(seen, ['speechbrain'])
})

test('recognition: a viewer who is not an administrator still gets the workbench and no speech voices tab', async () => {
  const { mod, sdk } = await load({ viewer: { isAdmin: () => false, hasFeature: () => true } })
  const addon = await mod.createAddon(sdk)
  const html = renderToString(React.createElement(addon.Component, { page: 'recognition', path: '' }))
  assert.match(html, /idn-bench/)
  assert.doesNotMatch(html, /Speech voices/)
})

test('recognition: an administrator sees the three tabs', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const html = renderToString(React.createElement(addon.Component, { page: 'recognition', path: '' }))
  for (const label of ['Speakers', 'Speech voices', 'From a recording']) assert.ok(html.includes(label), label)
})

test('recognition: the bundle carries no face-only canvas and no host import', async () => {
  const { readFileSync } = await import('node:fs')
  const { build } = await import('../build.mjs')
  const dir = await build()
  const js = readFileSync(join(dir, 'ui/voice.js'), 'utf8')
  assert.doesNotMatch(js, /biometrics-bbox/)
  assert.doesNotMatch(js, /from ['"]react(-dom)?/)
})
