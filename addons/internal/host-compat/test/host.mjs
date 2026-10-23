// A fake host for the tests of the add-ons that use the compat stand-ins: the
// real createHostSDK over stub components and hooks, and a minimal browser
// (storage, document) so page code that touches them at render time runs in node.
import { createRequire } from 'node:module'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { dirname, join, resolve } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
export const hostUi = resolve(here, '../../../../core/http/react-ui')
const require = createRequire(join(hostUi, 'package.json'))
export const React = require('react')
export const { renderToString, renderToStaticMarkup } = require('react-dom/server')

export function installBrowser() {
  const store = new Map()
  globalThis.localStorage = {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => { store.set(k, String(v)) },
    removeItem: (k) => { store.delete(k) },
    clear: () => store.clear(),
  }
  globalThis.document ??= { querySelector: () => null, documentElement: { getAttribute: () => 'light' }, addEventListener() {}, removeEventListener() {} }
  return store
}

const tag = (name) => (props) => React.createElement('div', { 'data-host': name }, props.title || props.label || props.children || null)

export async function makeSdk({ addonId, pages, overrides = {} }) {
  installBrowser()
  const { createHostSDK } = await import(pathToFileURL(join(hostUi, 'src/addons/sdk.js')).href)
  const calls = []
  const notices = []
  const navigations = []
  const controller = new AbortController()
  let location = { page: pages[0], path: '', search: '' }
  const components = Object.fromEntries([
    'Button', 'Spinner', 'EmptyState', 'Badge', 'Stack', 'Text', 'PageHeader', 'ConfirmDialog', 'ErrorWithTraceLink', 'ModelSelector',
    'MediaInput', 'WaveformPlayer', 'DistributionBars', 'EmbeddingInspector', 'TabSwitch', 'LoadingSpinner', 'Modal', 'Lightbox', 'HomeModelPicker',
  ].map((n) => [n, tag(n)]))
  const hooks = {
    useMediaCapture: () => ({ recording: false, start() {}, stop() {}, error: null }),
    useObjectUrl: () => null,
    useDelayedAction: () => ({ pending: null, schedule() {}, undo() {} }),
    audioBufferToWavBlob: () => new Blob([]),
    useModels: () => ({ models: [], loading: false, error: null, refetch() {} }),
    useResources: () => ({ resources: null, loading: false, error: null, refetch() {} }),
    useVoiceProfiles: () => ({ profiles: [], loading: false, error: null, refetch() {} }),
    useOperations: () => ({ operations: [], refetch() {} }),
    useMCPClient: () => ({ servers: [], connect() {}, disconnect() {}, tools: [] }),
  }
  const sdk = createHostSDK({
    addonId, generation: 1, signal: controller.signal, pages,
    host: {
      React, components, hooks, Icon: ({ name }) => React.createElement('i', { 'data-icon': name }),
      viewer: { isAdmin: () => true, hasFeature: () => true },
      basePath: '',
      fetch: async (url, init) => { calls.push([url, init]); return { ok: true, headers: new Map(), json: async () => ({}), blob: async () => new Blob([]) } },
      createPortal: (children) => children,
      theme: { mode: () => 'light', subscribe: () => () => {} }, language: () => 'en', subscribeLanguage: () => () => {}, api: {},
      ...overrides,
    },
    bindings: {
      current: () => location,
      openLayer: () => ({ close() {} }),
      notify: (kind, message) => notices.push([kind, message]),
      navigate: (path, options) => navigations.push([path, options]),
    },
  })
  return { sdk, controller, calls, notices, navigations, at: (next) => { location = { ...location, ...next } } }
}
