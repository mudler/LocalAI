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
  const mod = await import(pathToFileURL(join(dir, 'ui/face.js')).href + `?t=${Date.now()}`)
  const { createHostSDK } = await import(pathToFileURL(join(hostUi, 'src/addons/sdk.js')).href)
  const stub = (tag) => (p) => React.createElement(tag, { 'data-stub': tag }, p.title || p.label || p.children || null)
  const calls = []
  const controller = new AbortController()
  const sdk = createHostSDK({
    addonId: 'face', generation: 1, signal: controller.signal, pages: ['recognition'],
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

test('render: the manifest declares the recognition page, the main page and SDK 2', async () => {
  const { readFileSync } = await import('node:fs')
  const m = JSON.parse(readFileSync(join(here, '../../manifest.json'), 'utf8'))
  assert.equal(m.id, 'face')
  assert.deepEqual(m.capabilities.ui.pages, ['recognition'])
  assert.deepEqual(m.capabilities.ui.navigation, [])
  assert.equal(m.capabilities.ui.mainPage, 'recognition')
  assert.deepEqual(m.capabilities.ui.sdk, { min: 2, max: 2 })
  assert.deepEqual(m.capabilities.tools ?? [], [])
})

test('render: a registered page is drawn with its path', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk, { pages: { recognition: ({ path }) => React.createElement('p', null, `page:${path}`) } })
  assert.equal(renderToString(React.createElement(addon.Component, { page: 'recognition', path: 'abc' })), '<p>page:abc</p>')
})
