import { createRequire } from 'node:module'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { dirname, join, resolve } from 'node:path'
import { build } from '../build.mjs'

export const here = dirname(fileURLToPath(import.meta.url))
export const hostUi = resolve(here, '../../../../core/http/react-ui')
const require = createRequire(join(hostUi, 'package.json'))
export const React = require('react')
export const { renderToString } = require('react-dom/server')

const COMPONENTS = ['Button', 'Spinner', 'EmptyState', 'Badge', 'Stack', 'Text', 'PageHeader', 'ConfirmDialog', 'ErrorWithTraceLink', 'ModelSelector', 'MediaInput', 'WaveformPlayer', 'DistributionBars', 'EmbeddingInspector', 'TabSwitch']

// Builds the bundle and draws it through the real registration path: the host
// SDK, the module's createAddon and its Component. Options: viewer, fetch,
// components, layers (the openLayer binding), language.
export async function load(extra = {}) {
  const dir = await build()
  const mod = await import(pathToFileURL(join(dir, 'ui/quantization.js')).href + `?t=${Date.now()}`)
  const { createHostSDK } = await import(pathToFileURL(join(hostUi, 'src/addons/sdk.js')).href)
  const stub = (tag) => (p) => React.createElement(tag, { 'data-stub': tag }, p.title || p.label || p.children || null)
  const calls = []
  const layers = []
  const controller = new AbortController()
  const { viewer, components, layerSpec, fetch, ...rest } = extra
  const stubs = Object.fromEntries(COMPONENTS.map(n => [n, stub('div')]))
  stubs.PageHeader = (p) => React.createElement('header', { 'data-stub': 'header' }, p.title, p.supporting, p.actions)
  const sdk = createHostSDK({
    addonId: 'quantization', generation: 1, signal: controller.signal, pages: ['jobs'],
    host: {
      React,
      components: { ...stubs, ...components },
      hooks: {},
      viewer: viewer || { isAdmin: () => true, hasFeature: () => true },
      basePath: '/base',
      fetch: fetch || (async (url, init) => { calls.push([url, init]); return { ok: true, json: async () => [], blob: async () => new Blob([]) } }),
      Icon: ({ name }) => React.createElement('i', { 'data-icon': name }), theme: { mode: () => 'light', subscribe: () => () => {} }, language: () => 'en', subscribeLanguage: () => () => {}, api: {},
      ...rest,
    },
    bindings: {
      current: () => ({ page: 'jobs', path: '', search: '' }),
      openLayer: (kind, spec) => { layers.push({ kind, spec }); return { close() {} } },
      notify() {},
    },
  })
  return { mod, sdk, controller, calls, layers }
}

export const draw = (addon, props) => renderToString(React.createElement(addon.Component, props))
