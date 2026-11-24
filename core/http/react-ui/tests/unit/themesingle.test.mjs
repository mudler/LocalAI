// One source of truth for the theme: the browser keys, ui.json and the frames.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import vm from 'node:vm'
import { readFileSync } from 'node:fs'
import { readStored, writeStored, removeStored, onStored } from '../../src/utils/storage.js'
import { readTheme, writeTheme, uiThemeToApply, saveUiTheme } from '../../src/utils/theme.js'
import { keepIfSame } from '../../src/utils/sameData.js'
import { buildPageSrcdoc, createPageHost } from '../../src/utils/pagesBridge.js'
import { createHost } from '../../src/utils/chatApps.js'
import { initialSession, sessionStep } from '../../src/utils/authSession.js'
import { readSidebarCollapsed, writeSidebarCollapsed } from '../../src/utils/sidebarState.js'

const memory = (seed = {}) => {
  const m = new Map(Object.entries(seed))
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => { m.set(k, String(v)) },
    removeItem: (k) => { m.delete(k) },
    dump: () => Object.fromEntries(m),
  }
}

// --- the storage helper -----------------------------------------------------

test('storage: one prefix, and a missing or blocked storage reads as null', () => {
  const s = memory()
  writeStored('x', 'a', s)
  assert.deepEqual(s.dump(), { 'localai.x': 'a' })
  assert.equal(readStored('x', [], s), 'a')
  removeStored('x', s)
  assert.equal(readStored('x', [], s), null)
  const blocked = { getItem() { throw new Error('blocked') }, setItem() { throw new Error('blocked') }, removeItem() { throw new Error('blocked') } }
  assert.equal(readStored('x', ['old'], blocked), null)
  writeStored('x', 'a', blocked)
  removeStored('x', blocked)
})

test('storage: an old key is read once, carried to the new key and removed', () => {
  const s = memory({ 'localai-models-density': 'compact', 'import-form-tab': 'yaml' })
  assert.equal(readStored('models.density', ['localai-models-density'], s), 'compact')
  assert.deepEqual(s.dump(), { 'import-form-tab': 'yaml', 'localai.models.density': 'compact' })
  assert.equal(readStored('models.density', ['localai-models-density'], s), 'compact')
  assert.equal(readStored('import.tab', ['import-form-tab'], s), 'yaml')
  assert.deepEqual(s.dump(), { 'localai.models.density': 'compact', 'localai.import.tab': 'yaml' })
})

test('storage: the new key wins over an old one, which stays untouched', () => {
  const s = memory({ 'localai.x': 'new', 'localai_x': 'old' })
  assert.equal(readStored('x', ['localai_x'], s), 'new')
  assert.equal(s.getItem('localai_x'), 'old')
})

test('storage: the first old key that holds a value is carried over', () => {
  const s = memory({ b: '2' })
  assert.equal(readStored('x', ['a', 'b'], s), '2')
  assert.equal(s.getItem('localai.x'), '2')
})

test('storage: the sidebar keeps the value of the old spelling', () => {
  const s = memory({ localai_sidebar_collapsed: 'true' })
  globalThis.window = { localStorage: s }
  try {
    assert.equal(readSidebarCollapsed(), true)
    writeSidebarCollapsed(false)
    assert.equal(readSidebarCollapsed(), false)
    assert.deepEqual(s.dump(), { 'localai.sidebar.collapsed': 'false' })
  } finally { delete globalThis.window }
})

test('storage: onStored hears another tab for its own key only', () => {
  const handlers = []
  globalThis.window = {
    addEventListener: (t, fn) => { if (t === 'storage') handlers.push(fn) },
    removeEventListener: (t, fn) => { const i = handlers.indexOf(fn); if (i >= 0) handlers.splice(i, 1) },
  }
  try {
    const heard = []
    const off = onStored('theme', (v) => heard.push(v))
    handlers.forEach((h) => h({ key: 'localai.theme', newValue: 'light' }))
    handlers.forEach((h) => h({ key: 'localai.other', newValue: 'x' }))
    off()
    assert.deepEqual(heard, ['light'])
    assert.equal(handlers.length, 0)
  } finally { delete globalThis.window }
})

// --- the theme in the browser ------------------------------------------------

test('theme: a fresh browser is dark, the old key is carried over', () => {
  assert.equal(readTheme('', memory()), 'dark')
  const s = memory({ 'localai-theme': 'light' })
  assert.equal(readTheme('', s), 'light')
  assert.deepEqual(s.dump(), { 'localai.theme': 'light' })
  assert.equal(readTheme('', memory({ 'localai.theme': 'nonsense' })), 'dark')
})

test('theme: each account keeps its own, and a new account starts from what was shown', () => {
  const s = memory()
  writeTheme('alice', 'light', s)
  writeTheme('bob', 'dark', s)
  assert.equal(readTheme('alice', s), 'light')
  assert.equal(readTheme('bob', s), 'dark')
  assert.equal(readTheme('carol', s), 'dark', 'the last shown theme: bob dark')
  writeTheme('alice', 'light', s)
  assert.equal(readTheme('carol', s), 'light')
  assert.equal(s.getItem('localai.theme'), 'light', 'the first-paint copy follows the last write')
  writeTheme('alice', 'sepia', s)
  assert.equal(readTheme('alice', s), 'light', 'an unknown theme is never stored')
})

// --- ui.json -----------------------------------------------------------------

// The sequence AppearanceSync runs: one call per version of ui.json.
function sync(versions) {
  let applied
  const calls = []
  for (const theme of versions) {
    const change = uiThemeToApply(applied, theme)
    if (change) { applied = change; calls.push(change) }
  }
  return calls
}

test('appearance sync: a version bump with the same theme does not call setTheme', () => {
  // a pin, a start page, an accent: ui.json changes, its theme does not
  assert.deepEqual(sync(['light', 'light', 'light']), ['light'])
  assert.deepEqual(sync([undefined, undefined]), [], 'no theme in the file: nothing to apply')
})

test('appearance sync: a changed theme calls setTheme, system included', () => {
  assert.deepEqual(sync(['dark', 'light', 'light', 'dark']), ['dark', 'light', 'dark'])
  assert.deepEqual(sync(['dark', 'system', 'system']), ['dark', 'system'])
  assert.deepEqual(sync(['purple']), [], 'an unknown theme is ignored')
})

function fakeStore(doc, { conflicts = 0 } = {}) {
  const saves = []
  let left = conflicts
  return {
    saves,
    load: async () => doc,
    save: async (name, values, opts) => {
      saves.push({ name, values, opts })
      if (left-- > 0) throw Object.assign(new Error('conflict'), { conflict: true, current: { version: doc.version + 10, data: {} } })
      return { version: opts.version + 1, data: values }
    },
  }
}

test('toggle: the theme is written to ui.json, and only the theme key', async () => {
  const store = fakeStore({ version: 4, data: { pinnedPages: ['a'], accent: '#112233' } })
  assert.equal(await saveUiTheme('light', store), null)
  assert.deepEqual(store.saves, [{ name: 'ui', values: { theme: 'light' }, opts: { version: 4 } }])
})

test('toggle: a conflict is tried again on top of the newer file, a failure is returned', async () => {
  const store = fakeStore({ version: 1, data: {} }, { conflicts: 1 })
  assert.equal(await saveUiTheme('dark', store), null)
  assert.deepEqual(store.saves.map((s) => s.opts.version), [1, 11])
  const stubborn = fakeStore({ version: 1, data: {} }, { conflicts: 99 })
  assert.match((await saveUiTheme('dark', stubborn)).message, /keeps changing/)
  const down = { load: async () => null, save: async () => { throw new Error('offline') } }
  assert.match((await saveUiTheme('dark', down)).message, /offline/)
})

test('toggle: quick toggles are written in order', async () => {
  const store = fakeStore({ version: 1, data: {} })
  await Promise.all([saveUiTheme('light', store), saveUiTheme('dark', store), saveUiTheme('light', store)])
  assert.deepEqual(store.saves.map((s) => s.values.theme), ['light', 'dark', 'light'])
})

// --- the page frames ---------------------------------------------------------

test('page frame: the document is the same text for a theme change, so the frame is not reloaded', () => {
  const src = readFileSync(new URL('../../src/components/PageFrame.jsx', import.meta.url), 'utf8')
  const memo = /const srcDoc = useMemo\(\(\) => \{([\s\S]*?)\n  \}, \[([^\]]*)\]\)/.exec(src)
  assert.ok(memo, 'the srcDoc memo is found')
  assert.ok(!/\btheme\b/.test(memo[2]), `theme is not a dependency of the document: ${memo[2]}`)
  assert.ok(/themeNow\.current/.test(memo[1]), 'the document reads the theme of the moment from the ref')
  assert.ok(/emit\('theme'/.test(src), 'a later change goes to the live frame')
  // The builder is a pure function of its inputs: with the theme of the moment
  // fixed, a rebuild gives the same string.
  const opts = { name: 'n', title: 't', theme: 'dark', mode: 'page' }
  assert.equal(buildPageSrcdoc('<p>x</p>', opts), buildPageSrcdoc('<p>x</p>', { ...opts }))
})

function boot(theme) {
  const attrs = {}
  const style = {}
  let onMessage = null
  const sandbox = {
    console: { warn() {}, error() {}, log() {} },
    setTimeout, Promise, setInterval: () => 1, Response, Request, Blob, TextEncoder, DOMException, JSON, Object, String, Error, TypeError, URL,
    document: {
      baseURI: 'http://localai.test/app', readyState: 'complete',
      documentElement: { setAttribute: (k, v) => { attrs[k] = v }, removeAttribute: (k) => { delete attrs[k] }, style },
      addEventListener() {}, getElementById: () => null, getElementsByName: () => [],
    },
    scrollTo() {},
    addEventListener: (t, fn) => { if (t === 'message') onMessage = fn },
  }
  sandbox.window = sandbox
  sandbox.parent = {}
  vm.createContext(sandbox)
  vm.runInContext(/<script>([\s\S]*?)<\/script>/.exec(buildPageSrcdoc('', { name: 'x', theme }))[1], sandbox)
  let deliver = null
  const port = { postMessage() {}, set onmessage(fn) { deliver = fn } }
  onMessage({ source: sandbox.parent, data: { type: 'localai:init' }, ports: [port], stopImmediatePropagation() {} })
  return { attrs, style, localai: sandbox.localai, send: (m) => deliver({ data: m }) }
}

test('page frame: the live document follows a theme event without a rebuild', () => {
  const f = boot('light')
  assert.equal(f.attrs['data-theme'], 'light')
  const heard = []
  f.localai.on('theme', (d) => heard.push(d.theme))
  f.send({ event: 'theme', data: { theme: 'dark' } })
  assert.equal(f.attrs['data-theme'], 'dark')
  assert.equal(f.style.colorScheme, 'dark')
  f.send({ event: 'theme', data: { theme: 'dark' } })
  f.send({ event: 'theme', data: { theme: 'neon' } })
  f.send({ event: 'theme', data: { theme: 'light' } })
  assert.deepEqual(heard, ['dark', 'light'], 'same or unknown themes are not announced')
  assert.equal(f.attrs['data-theme'], 'light')
})

test('page host: emit(theme) sends a known theme only', () => {
  const sent = []
  const host = createPageHost({ post: (m) => sent.push(m), files: {}, chat: {}, links: {} })
  host.emit('theme', { theme: 'dark' })
  host.emit('theme', { theme: '<x>' })
  assert.deepEqual(sent, [{ event: 'theme', data: { theme: 'dark' } }, { event: 'theme', data: { theme: 'light' } }])
})

test('chat app: a later theme goes to an initialized app as a host context change', () => {
  const sent = []
  const host = createHost({ post: (m) => sent.push(m), input: {}, result: {}, callTool: async () => ({}), onMessage() {}, onOpenLink() {}, onSize() {}, theme: 'light' })
  host.setTheme('dark')
  assert.deepEqual(sent, [], 'nothing is sent before the app started')
  host.receive({ jsonrpc: '2.0', id: 1, method: 'ui/initialize', params: {} })
  return Promise.resolve().then(() => {
    assert.equal(sent[0].result.hostContext.theme, 'dark', 'the app starts with the current theme')
    host.setTheme('light')
    host.setTheme('light')
    assert.deepEqual(sent.slice(1), [{ jsonrpc: '2.0', method: 'ui/notifications/host-context-changed', params: { theme: 'light' } }])
  })
})

// --- polling and the session ---------------------------------------------------

test('polling: a poll with the same data keeps the state, a changed one replaces it', () => {
  const rows = [{ id: 1, name: 'a' }]
  assert.equal(keepIfSame(rows, [{ id: 1, name: 'a' }]), rows)
  const next = [{ id: 1, name: 'b' }]
  assert.equal(keepIfSame(rows, next), next)
  assert.equal(keepIfSame(null, null), null)
})

test('session: a failed refresh after an answer keeps the app and marks it stale', () => {
  const ok = (user) => ({ type: 'status-ok', data: { authEnabled: !!user, user } })
  for (const first of [ok(null), ok({ id: 'u', permissions: {} })]) {
    const s = sessionStep(initialSession, first)
    const failed = sessionStep(s, { type: 'status-failed' })
    assert.equal(failed.status, s.status)
    assert.equal(failed.user, s.user)
    assert.equal(failed.stale, true)
    assert.equal(failed.sessionEpoch, s.sessionEpoch)
    assert.equal(sessionStep(failed, { type: 'status-failed' }), failed)
    assert.equal(sessionStep(failed, first).stale, false)
  }
  assert.equal(sessionStep(initialSession, { type: 'status-failed' }).status, 'unreachable', 'a first read that fails is still unreachable')
})
