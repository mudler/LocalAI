import test from 'node:test'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { dirname, join, resolve } from 'node:path'
import { build } from '../build.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const hostUi = resolve(here, '../../../../core/http/react-ui')
const require = createRequire(join(hostUi, 'package.json'))
const React = require('react')
const { renderToString } = require('react-dom/server')

export async function load(extra = {}) {
  const dir = await build()
  const mod = await import(pathToFileURL(join(dir, 'ui/voice.js')).href + `?t=${Date.now()}`)
  const { createHostSDK } = await import(pathToFileURL(join(hostUi, 'src/addons/sdk.js')).href)
  const stub = (tag) => (p) => React.createElement(tag, { 'data-stub': tag }, p.title || p.label || p.children || null)
  const calls = []
  const controller = new AbortController()
  const sdk = createHostSDK({
    addonId: 'voice', generation: 1, signal: controller.signal, pages: ['voice', 'recognition', 'library', 'create', 'speech'],
    host: {
      React,
      components: Object.fromEntries(['Button', 'Spinner', 'EmptyState', 'Badge', 'Stack', 'Text', 'PageHeader', 'ConfirmDialog', 'ErrorWithTraceLink', 'ModelSelector', 'MediaInput', 'WaveformPlayer', 'DistributionBars', 'EmbeddingInspector', 'TabSwitch'].map(n => [n, stub(n === 'PageHeader' ? 'header' : 'div')])),
      hooks: { useMediaCapture: () => ({}), useObjectUrl: () => null, useDelayedAction: () => ({ pending: null, schedule() {}, undo() {} }) },
      viewer: { isAdmin: () => true, hasFeature: () => true },
      basePath: '',
      fetch: async (url, init) => { calls.push([url, init]); return { ok: true, json: async () => ({ data: [] }), blob: async () => new Blob([]) } },
      Icon: () => null, theme: { mode: () => 'light', subscribe: () => () => {} }, language: () => 'en', subscribeLanguage: () => () => {}, api: {},
      ...extra,
    },
    bindings: { current: () => ({ page: 'recognition', path: '', search: '' }), openLayer: () => ({ close() {} }), notify() {} },
  })
  return { mod, sdk, controller, calls }
}

test('render: createAddon returns a page component and dispose', async () => {
  const { mod, sdk, controller } = await load()
  const addon = await mod.createAddon(sdk)
  assert.equal(typeof addon.Component, 'function')
  assert.equal(typeof addon.dispose, 'function')
  addon.dispose()
  controller.abort('done')
})

test('render: an unknown page draws nothing', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  assert.equal(renderToString(React.createElement(addon.Component, { page: 'nope', path: '' })), '')
})

test('render: the manifest declares one page in the navigation, the older pages, the main page and SDK 2', async () => {
  const { readFileSync } = await import('node:fs')
  const m = JSON.parse(readFileSync(join(here, '../../manifest.json'), 'utf8'))
  assert.equal(m.id, 'voice')
  assert.deepEqual(m.capabilities.ui.pages, ['voice', 'recognition', 'library', 'create', 'speech'])
  assert.deepEqual(m.capabilities.ui.navigation, ['voice'])
  assert.equal(m.capabilities.ui.mainPage, 'voice')
  assert.deepEqual(m.capabilities.ui.sdk, { min: 2, max: 2 })
  assert.deepEqual(m.capabilities.tools ?? [], [])
})

test('render: a tab is drawn with the rest of the path and the tab strip', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk, { pages: { speech: ({ path, tabs }) => React.createElement('div', null, tabs, React.createElement('p', null, `page:${path}`)) } })
  const html = renderToString(React.createElement(addon.Component, { page: 'voice', path: 'speech/abc' }))
  assert.match(html, /<p>page:abc<\/p>/)
  assert.match(html, /data-testid="voice-tabs"/)
  assert.match(html, /data-tab="speech"[^>]*aria-selected="true"/)
})

test('render: no tab in the path opens the recognition tab', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  assert.match(renderToString(React.createElement(addon.Component, { page: 'voice', path: '' })), /data-testid="voices-page"/)
})

test('render: an older page name draws nothing itself; the shell sends it to its tab', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  for (const page of ['recognition', 'library', 'create', 'speech']) {
    assert.equal(renderToString(React.createElement(addon.Component, { page, path: '' })), '', page)
  }
})
