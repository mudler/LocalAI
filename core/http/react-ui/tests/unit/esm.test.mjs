import test from 'node:test'
import assert from 'node:assert/strict'
import * as React from 'react'
import { renderToString } from 'react-dom/server'
import { createHostSDK, sdkAccepts, resolveSubpath, SDK_VERSION, THEME_TOKENS, ICON_NAMES } from '../../src/addons/sdk.js'
import { scopeCss } from '../../src/addons/cssScope.js'
import { assertAssetUrl, loadAddonUI, validateChatActions, chatActionContext, LoadError } from '../../src/addons/loader.js'
import { createRuntimeManager } from '../../src/addons/runtime.js'
import { invalidReason, navigationItems, availableChatActions, appStatus, usable, contributesUI, shouldPoll } from '../../src/addons/registryState.js'
import { visibleItems, isTabVisible, buildHub } from '../../src/components/hub/hubConfig.js'

const ORIGIN = 'https://host.example'
const DIGEST = 'ab'.repeat(32)
const asset = (id, p, d = DIGEST) => `/api/addons/${id}/assets/${d}/${p}`

function fakeHost(over = {}) {
  const calls = { dispatch: [], propose: [], themeListeners: new Set() }
  const Component = (props) => React.createElement('button', props, props.children)
  const host = {
    React,
    components: { Button: Component, Text: Component },
    Icon: (props) => React.createElement('svg', { 'data-icon': props.name }),
    theme: { mode: () => 'dark', subscribe: (fn) => { calls.themeListeners.add(fn); return () => calls.themeListeners.delete(fn) } },
    language: () => 'it-IT',
    subscribeLanguage: () => () => {},
    api: {
      async dispatch(body, signal) { calls.dispatch.push({ body, signal }); return { ok: true } },
      async propose(body, signal) { calls.propose.push({ body, signal }); return { id: body.operationId } },
    },
    ...over,
  }
  return { host, calls }
}

function makeSDK(over = {}) {
  const controller = new AbortController()
  const { host, calls } = fakeHost(over.host)
  const events = { navigate: [], notify: [], dirty: [], layers: [] }
  const sdk = createHostSDK({
    addonId: 'notes',
    generation: 3,
    signal: controller.signal,
    pages: ['home', 'detail'],
    host,
    bindings: {
      navigate: (p, o) => events.navigate.push([p, o]),
      notify: (k, m) => events.notify.push([k, m]),
      setDirty: (f) => events.dirty.push(f),
      openLayer: (kind, spec) => { events.layers.push([kind, spec]); return { close() { events.layers.push(['closed']) } } },
      current: () => ({ page: 'home', path: '', search: '' }),
    },
    ...over.sdk,
  })
  return { sdk, controller, calls, events, host }
}

const app = (over = {}) => ({
  id: 'notes', title: 'Notes', generation: 3, enabled: true, state: 'running',
  ui: { entry: asset('notes', 'ui.js'), css: [asset('notes', 'ui/style.css')], sdk: { min: 1, max: 2 }, pages: ['home', 'detail'], navigation: ['home'], chatActions: ['summarize'] },
  ...over,
})

// ---- SDK ----------------------------------------------------------------

test('sdk: compatibility accepts only a range that holds the host SDK version', () => {
  assert.equal(SDK_VERSION, 2)
  assert.equal(sdkAccepts({ min: 1, max: 1 }), false)
  assert.equal(sdkAccepts({ min: 1, max: 2 }), true)
  assert.equal(sdkAccepts({ min: 1, max: 3 }), true)
  assert.equal(sdkAccepts({ min: 2, max: 3 }), true)
  assert.equal(sdkAccepts({ min: 3, max: 3 }), false)
  assert.equal(sdkAccepts({ min: 0, max: 1 }), false)
  assert.equal(sdkAccepts({ min: 3, max: 2 }), false)
  assert.equal(sdkAccepts({ min: '1', max: 1 }), false)
  assert.equal(sdkAccepts(null), false)
  assert.equal(sdkAccepts(undefined), false)
})

test('sdk: the object is a frozen allowlist that carries the host React and no host internals', () => {
  const { sdk, host } = makeSDK()
  assert.equal(sdk.React, host.React)
  assert.equal(Object.isFrozen(sdk), true)
  assert.deepEqual(Object.keys(sdk).sort(), [
    'Icon', 'React', 'addonId', 'api', 'approvals', 'core', 'files', 'generation', 'hooks', 'iconNames', 'isDirty', 'layers', 'locale', 'markDirty', 'navigation',
    'notify', 'onDispose', 'portal', 'resources', 'signal', 'theme', 'ui', 'version', 'viewer',
  ])
  assert.deepEqual(Object.keys(sdk.api).sort(), ['propose', 'read'])
  assert.deepEqual(Object.keys(sdk.ui).sort(), ['Button', 'Text'])
  for (const forbidden of ['router', 'navigate', 'fetch', 'host', 'store', 'auth', 'conversation', 'history', 'send', 'messages']) {
    assert.equal(forbidden in sdk, false, forbidden)
  }
  assert.throws(() => { sdk.React = {} }, TypeError)
  assert.throws(() => createHostSDK({ addonId: 'notes', generation: 0, signal: new AbortController().signal, host }), /host SDK needs/)
})

test('sdk: hooks run on the host React instance and a wrapper keeps one component type', () => {
  const { sdk, host } = makeSDK()
  // The factory-scoped form an add-on module compiles to: no bare React import.
  const make = (sdkArg) => {
    const R = sdkArg.React
    return function Counter() {
      const [n] = R.useState(41)
      const doubled = R.useMemo(() => n + 1, [n])
      return R.createElement('output', null, String(doubled), R.createElement(sdkArg.ui.Button, null, 'ok'))
    }
  }
  const Counter = make(sdk)
  assert.equal(sdk.React.useState, host.React.useState)
  const html = renderToString(React.createElement(Counter))
  assert.match(html, /<output>42/)
  assert.match(html, /<button[^>]*>ok<\/button>/)
  // The same wrapper object every time: React never remounts it.
  assert.equal(sdk.ui.Button, sdk.ui.Button)
  assert.equal(sdk.Icon, sdk.Icon)
})

test('sdk: icons outside the semantic set fall back and theme tokens are a fixed set', () => {
  const { sdk } = makeSDK()
  const known = renderToString(React.createElement(sdk.Icon, { name: 'table' }))
  assert.match(known, /data-icon="table"/)
  const unknown = renderToString(React.createElement(sdk.Icon, { name: 'sprite-internal-secret' }))
  assert.match(unknown, /data-icon="puzzle"/)
  assert.deepEqual(sdk.iconNames, ICON_NAMES)
  assert.equal(sdk.theme.tokens, THEME_TOKENS)
  for (const value of Object.values(sdk.theme.tokens)) assert.match(value, /^var\(--dk-/)
  assert.equal(sdk.theme.mode(), 'dark')
})

test('sdk: navigation stays inside the add-on prefix and its declared pages', () => {
  const { sdk, events } = makeSDK()
  assert.equal(sdk.navigation.navigate('detail/42?tab=a'), true)
  assert.deepEqual(events.navigate[0], ['/app/addons/notes/detail/42?tab=a', { replace: false }])
  assert.equal(sdk.navigation.href('home'), '/app/addons/notes/home')
  for (const bad of ['/app/settings', '../settings', 'home/../../x', 'https://evil.example/', '//evil.example', 'javascript:alert(1)', 'other', 'home#frag', '', 'home/%2e%2e', 'home\\x', 'a'.repeat(300)]) {
    assert.throws(() => sdk.navigation.navigate(bad), /own declared pages/, bad)
    assert.equal(resolveSubpath(bad, ['home', 'detail']), null, bad)
  }
  assert.equal(events.navigate.length, 1)
  assert.deepEqual(resolveSubpath('/detail/1/2', ['detail']), { page: 'detail', rest: '1/2', path: 'detail/1/2', search: '' })
  assert.deepEqual(sdk.navigation.current(), { page: 'home', path: '', search: '' })
})

test('sdk: api calls carry the add-on and its generation and nothing else is reachable', async () => {
  const { sdk, calls } = makeSDK()
  await sdk.api.read({ arguments: { q: 'x' }, action: 'delete-everything', addonId: 'other', generation: 99 }, { operationId: 'op-1' })
  const sent = calls.dispatch[0].body
  assert.equal(sent.addonId, 'notes')
  assert.equal(sent.generation, 3)
  assert.equal(sent.action, 'read', 'a read is always a read, whatever the add-on wrote')
  assert.equal(sent.operationId, 'op-1')
  assert.deepEqual(sent.arguments, { q: 'x' })
  await sdk.api.propose({ action: 'rows.update', arguments: { id: 1 }, resourceId: 'r1', addonId: 'other', generation: 1 })
  const proposed = calls.propose[0].body
  assert.equal(proposed.addonId, 'notes')
  assert.equal(proposed.generation, 3)
  assert.equal(proposed.resourceId, 'r1')
  assert.match(proposed.operationId, /^(op-)?[0-9a-f-]{8,}/)
})

test('sdk: after the signal aborts every service refuses and each disposer runs once', async () => {
  const { sdk, controller, calls, events } = makeSDK()
  let ran = 0
  sdk.onDispose(() => { ran++ })
  sdk.onDispose(() => { throw new Error('a failing disposer blocks nothing') })
  sdk.onDispose(() => { ran++ })
  const off = sdk.theme.subscribe(() => {})
  assert.equal(calls.themeListeners.size, 1)
  const layer = sdk.layers.openDialog({ title: 't', render: () => null })
  controller.abort('withdrawn')
  controller.abort('again')
  assert.equal(ran, 2)
  assert.equal(calls.themeListeners.size, 0, 'subscriptions are dropped')
  assert.equal(events.layers.at(-1)[0], 'closed', 'layers close with the add-on')
  await assert.rejects(sdk.api.read({}), /withdrawn/)
  await assert.rejects(async () => sdk.api.propose({}), /withdrawn/)
  assert.throws(() => sdk.navigation.navigate('home'), /withdrawn/)
  assert.throws(() => sdk.layers.openPanel({ title: 't', render: () => null }), /withdrawn/)
  sdk.notify.error('late')
  sdk.markDirty(true)
  assert.deepEqual(events.notify, [])
  assert.deepEqual(events.dirty, [])
  assert.equal(calls.dispatch.length, 0)
  off()
  layer.close()
})

test('sdk: markDirty reports unsaved edits once per change and notifications are bounded', () => {
  const { sdk, events } = makeSDK()
  sdk.markDirty()
  sdk.markDirty(true)
  assert.equal(sdk.isDirty(), true)
  sdk.markDirty(false)
  assert.deepEqual(events.dirty, [true, false])
  sdk.notify.success('x'.repeat(2000))
  assert.equal(events.notify[0][0], 'success')
  assert.equal(events.notify[0][1].length, 500)
})

test('sdk: locale is scoped to the add-on and falls back to the default text', () => {
  const a = makeSDK().sdk
  const b = makeSDK().sdk
  a.locale.define('it', { greeting: 'Ciao {{name}}' })
  assert.equal(a.locale.t('greeting', 'Hello {{name}}', { name: 'Ada' }), 'Ciao Ada')
  assert.equal(b.locale.t('greeting', 'Hello {{name}}', { name: 'Ada' }), 'Hello Ada', 'another add-on never sees these strings')
  assert.equal(a.locale.t('missing'), 'missing')
  assert.equal(a.locale.language(), 'it-IT')
})

test('sdk: layers are host wrappers with a bounded spec', () => {
  const { sdk, events } = makeSDK()
  const handle = sdk.layers.openPanel({ title: 'T'.repeat(500), render: () => null, wide: true })
  assert.equal(events.layers[0][0], 'panel')
  assert.equal(events.layers[0][1].title.length, 200)
  assert.equal(typeof handle.close, 'function')
  assert.throws(() => sdk.layers.openDialog({ title: 'x' }), /render/)
})

// ---- CSS ----------------------------------------------------------------

test('css: every selector is scoped to the add-on root and the global ones are neutralised', () => {
  const { css, dropped } = scopeCss(`
    /* comment */
    body { margin: 0; background: red }
    :root { --accent: red }
    html.dark .x, .title:hover, a > b { color: blue }
    @media (max-width: 400px) { .card { padding: 0 } }
    .dk-btn { display: none }
  `, 'notes')
  const scope = ':where([data-addon="notes"])'
  assert.match(css, new RegExp(`^${scope.replace(/[()[\]"=]/g, '\\$&')}\\{margin: 0;background: red\\}`, 'm'))
  assert.match(css, /:where\(\[data-addon="notes"\]\)\{--accent: red\}/)
  assert.match(css, /:where\(\[data-addon="notes"\]\) \.dark \.x,:where\(\[data-addon="notes"\]\) \.title:hover,:where\(\[data-addon="notes"\]\) a > b\{color: blue\}/)
  assert.match(css, /@media \(max-width: 400px\)\{:where\(\[data-addon="notes"\]\) \.card\{padding: 0\}\}/)
  assert.match(css, /:where\(\[data-addon="notes"\]\) \.dk-btn\{display: none\}/)
  // No rule starts with an unscoped selector.
  for (const line of css.split('\n')) {
    if (!line.startsWith('@media')) assert.ok(line.startsWith(scope), line)
  }
  assert.deepEqual(dropped, [])
})

test('css: imports, fonts, external urls and unknown at-rules are dropped and keyframes are renamed', () => {
  const { css, dropped } = scopeCss(`
    @import url("https://evil.example/x.css");
    @font-face { font-family: X; src: url(/fonts/x.woff2) }
    @charset "utf-8";
    .a { background: url(https://evil.example/pixel.gif); color: red }
    .b { background-image: url("data:image/png;base64,AAAA"); animation: spin 1s linear }
    .c { content: "a;b}" ; color: green }
    @keyframes spin { from { opacity: 0 } to { opacity: 1 } }
  `, 'notes')
  assert.doesNotMatch(css, /evil\.example|@import|@font-face|woff2/)
  assert.match(css, /\.a\{color: red\}/)
  // A data: image is dropped with every other URL-loading value.
  assert.doesNotMatch(css, /data:image/)
  assert.match(css, /\.b\{animation: ad-notes-spin 1s linear\}/)
  assert.match(css, /@keyframes ad-notes-spin\{from\{opacity: 0\}to\{opacity: 1\}\}/)
  assert.match(css, /animation:\s*ad-notes-spin 1s linear/)
  assert.match(css, /\.c\{content: "a;b\}";color: green\}/)
  const kinds = dropped.map(d => d.kind)
  assert.ok(kinds.includes('import') && kinds.includes('font-face') && kinds.includes('url'))
})

// Every value below would load a resource, directly or through an escape, a
// keyframe, a nested rule or an at-rule block. Nothing of it may survive.
const URL_LOADING = [
  ['image-set with a bare string', '.a{background:image-set("https://evil.example/x.png" 1x)}'],
  ['escaped function name', '.a{background:u\\72l(https://evil.example/x.png)}'],
  ['escaped function name with a trailing space', '.a{background:\\75 rl(https://evil.example/x.png)}'],
  ['keyframe body', '@keyframes k{from{background:url(https://evil.example/x.png)}to{opacity:1}}'],
  ['keyframe body with escape', '@-webkit-keyframes k{50%{background:u\\72l(https://evil.example/x.png)}}'],
  ['nested rule', '.a{color:red;& .b{background:u\\72l(https://evil.example/x.png)}}'],
  ['nested rule without ampersand', '.a{.b{background:url(https://evil.example/x.png)}}'],
  ['webkit image-set with url2', '.a{background:-webkit-image-set(url2(https://evil.example/x.png) 1x)}'],
  ['data image', '.a{background:url("data:image/png;base64,AAAA")}'],
  ['svg data image', '.a{background:url(data:image/svg+xml;utf8,<svg/>)}'],
  ['src function', '.a{background:src("https://evil.example/x.png")}'],
  ['cross-fade', '.a{background:cross-fade(url(x.png), url(y.png), 50%)}'],
  ['element', '.a{background:element(#id)}'],
  ['paint', '.a{background:paint(worklet)}'],
  ['image function', '.a{background:image("https://evil.example/x.png")}'],
  ['custom property', '.a{--x:url(https://evil.example/x.png)}'],
  ['comment between name and paren', '.a{background:url/**/(https://evil.example/x.png)}'],
  ['uppercase', '.a{background:URL(https://evil.example/x.png)}'],
  ['expression', '.a{width:expression(alert(1))}'],
  ['behavior property', '.a{behavior:url(x.htc)}'],
  ['moz binding', '.a{-moz-binding:url(x.xml#y)}'],
  ['media block', '@media (min-width:1px){.a{background:url(https://evil.example/x.png)}}'],
  ['supports block', '@supports (display:grid){.a{background:image-set("https://evil.example/x.png" 1x)}}'],
  ['layer block', '@layer base{.a{background:u\\72l(https://evil.example/x.png)}}'],
  ['container block', '@container (min-width:1px){.a{background:url(https://evil.example/x.png)}}'],
  ['nested at-rule in a rule', '.a{@media (min-width:1px){background:url(https://evil.example/x.png)}}'],
  ['supports condition', '@supports (background:url(https://evil.example/x.png)){.a{color:red}}'],
  ['deeply nested', '@layer a{@media (min-width:1px){@supports (display:grid){.a{.b{& .c{background:u\\72l(https://evil.example/x.png)}}}}}}'],
]

test('css: no value that loads a URL survives anywhere in the sheet', () => {
  for (const [name, sheet] of URL_LOADING) {
    const { css, dropped } = scopeCss(sheet, 'notes')
    assert.doesNotMatch(css, /evil\.example|data:|AAAA|x\.htc|x\.xml|worklet|element\(|expression|\\|url\s*\(|src\s*\(|image(-set)?\s*\(|cross-fade|paint\(|behavior|binding/i, `${name}: ${css}`)
    assert.ok(dropped.length > 0, `${name}: something is reported as dropped`)
  }
})

test('css: harmless values, escapes in strings and nested rules are kept', () => {
  const { css, dropped } = scopeCss(`
    .a { color: red; scroll-behavior: smooth; background: linear-gradient(red, blue); content: "\\201C" }
    .b { color: blue; & .c { color: green } }
    @media (min-width: 1px) { .d { transform: translate3d(0,0,0) } }
  `, 'notes')
  assert.match(css, /scroll-behavior: smooth/)
  assert.match(css, /linear-gradient\(red, blue\)/)
  assert.match(css, /content: "\\201C"/)
  assert.match(css, /& \.c\{color: green\}/)
  assert.match(css, /translate3d\(0,0,0\)/)
  assert.deepEqual(dropped, [])
})

test('css: two add-ons that use the same class names get disjoint scopes', () => {
  const sheet = '.title{color:red} body{background:white} h1{font-size:40px}'
  const a = scopeCss(sheet, 'alpha').css
  const b = scopeCss(sheet, 'beta').css
  assert.ok(a.includes('[data-addon="alpha"]') && !a.includes('beta'))
  assert.ok(b.includes('[data-addon="beta"]') && !b.includes('alpha'))
  assert.equal(scopeCss('x'.repeat(10), 'a', { maxBytes: 5 }).css, '')
  // An identifier cannot break out of the attribute selector.
  assert.equal(scopeCss('.a{color:red}', 'x"] body, [x="').css.includes('"] body'), false)
})

// ---- Loader -------------------------------------------------------------

test('loader: asset URLs must be same-origin immutable URLs of the add-on own package', () => {
  const ok = assertAssetUrl(asset('notes', 'ui/main.js'), { addonId: 'notes', origin: ORIGIN })
  assert.equal(ok.digest, DIGEST)
  assert.equal(ok.url, `${ORIGIN}${asset('notes', 'ui/main.js')}`)
  // Under a path prefix the host puts its base path in front of the registry path.
  const proxied = assertAssetUrl(asset('notes', 'ui.js'), { addonId: 'notes', origin: ORIGIN, basePath: '/base' })
  assert.equal(proxied.url, `${ORIGIN}/base${asset('notes', 'ui.js')}`)
  const bad = [
    'https://evil.example' + asset('notes', 'ui.js'),
    '//evil.example' + asset('notes', 'ui.js'),
    asset('other', 'ui.js'),
    asset('notes', 'ui.js') + '?x=1',
    asset('notes', 'ui.js') + '#h',
    asset('notes', '../ui.js'),
    asset('notes', 'ui.js', 'short'),
    asset('notes', 'ui.js', 'AB'.repeat(32)),
    '/api/addons/notes/assets/' + DIGEST,
    '/api/addons/notes/assets/' + DIGEST + '/',
    'data:text/javascript,export default 1',
    'blob:https://host.example/uuid',
    'javascript:alert(1)',
    `https://u:p@host.example${asset('notes', 'ui.js')}`,
    '/app/addons/notes/ui.js',
  ]
  for (const url of bad) {
    assert.throws(() => assertAssetUrl(url, { addonId: 'notes', origin: ORIGIN }), (e) => e instanceof LoadError && e.code === 'untrusted_asset', url)
  }
  assert.throws(() => assertAssetUrl(`/other${asset('notes', 'ui.js')}`, { addonId: 'notes', origin: ORIGIN, basePath: '/base' }), LoadError, 'a foreign prefix never matches')
  assert.throws(() => assertAssetUrl(`/base${asset('notes', 'ui.js')}`, { addonId: 'notes', origin: ORIGIN, basePath: '/base' }), LoadError, 'the prefix is added once, by the host')
})

function loaderEnv(over = {}) {
  const styles = []
  const doc = {
    head: { appendChild: (el) => styles.push(el) },
    createElement: () => {
      const el = { attrs: {}, setAttribute(k, v) { this.attrs[k] = v }, removed: false, remove() { this.removed = true } }
      return el
    },
  }
  const fetchImpl = async (url) => ({ ok: true, status: 200, headers: { get: () => 'text/css; charset=utf-8' }, text: async () => '.x{color:red} body{margin:0}', url })
  const imports = []
  return { styles, doc, fetchImpl, imports, ...over }
}

test('loader: an incompatible SDK range is never imported and leaves a clear error', async () => {
  const env = loaderEnv()
  let imported = false
  await assert.rejects(loadAddonUI({
    app: app({ ui: { ...app().ui, sdk: { min: 3, max: 4 } } }),
    createSDK: () => { throw new Error('must not run') },
    importer: async () => { imported = true; return {} },
    fetchImpl: env.fetchImpl, document: env.doc, origin: ORIGIN,
  }), (e) => e instanceof LoadError && e.code === 'incompatible_sdk')
  assert.equal(imported, false)
  assert.equal(env.styles.length, 0, 'no style sheet was injected')
  assert.equal(appStatus(app({ ui: { ...app().ui, sdk: { min: 3, max: 4 } } })), 'incompatible')
  assert.equal(usable(app({ ui: { ...app().ui, sdk: { min: 3, max: 4 } } })), false)
})

test('loader: the module receives only the host SDK, its result is validated and styles are scoped', async () => {
  const env = loaderEnv()
  const { host } = fakeHost()
  let received
  const mod = {
    createAddon: (sdk) => {
      received = sdk
      return { Component: () => null, chatActions: [{ id: 'summarize', label: 'Summarize', icon: 'sparkles', open() {} }], dispose() {} }
    },
    secret: 'never read',
  }
  const out = await loadAddonUI({
    app: app(),
    createSDK: ({ signal }) => createHostSDK({ addonId: 'notes', generation: 3, signal, pages: ['home'], host }),
    importer: async (url) => { env.imports.push(url); return mod },
    fetchImpl: env.fetchImpl, document: env.doc, origin: ORIGIN,
  })
  assert.deepEqual(env.imports, [`${ORIGIN}${asset('notes', 'ui.js')}`])
  assert.equal(received.React, host.React)
  assert.equal(Object.keys(received).includes('secret'), false)
  assert.equal(env.styles.length, 1)
  assert.equal(env.styles[0].attrs['data-addon-style'], 'notes')
  assert.match(env.styles[0].textContent, /^:where\(\[data-addon="notes"\]\) \.x\{color:red\}/)
  assert.equal(typeof out.component, 'function')
  assert.equal(out.chatActions.length, 1)
})

test('loader: a module without createAddon, with a bad result or a bare import is a load error', async () => {
  const env = loaderEnv()
  const load = (importer, over = {}) => loadAddonUI({
    app: app(), createSDK: ({ signal }) => createHostSDK({ addonId: 'notes', generation: 3, signal, host: fakeHost().host }),
    importer, fetchImpl: env.fetchImpl, document: env.doc, origin: ORIGIN, ...over,
  })
  await assert.rejects(load(async () => ({ default: {} })), (e) => e.code === 'invalid_module')
  await assert.rejects(load(async () => ({ createAddon: () => ({ Component: 'not a component', dispose() {} }) })), (e) => e.code === 'invalid_module')
  await assert.rejects(load(async () => ({ createAddon: () => ({}) })), (e) => e.code === 'invalid_module')
  // What the browser does with `import React from 'react'`: a TypeError.
  await assert.rejects(load(async () => { throw new TypeError('Failed to resolve module specifier "react"') }), (e) => e.code === 'import_failed')
  await assert.rejects(load(async () => ({ createAddon: () => { throw new Error('boom') } })), (e) => e.code === 'create_failed')
  assert.ok(env.styles.every(el => el.removed), 'every failed load removes its styles')
})

test('loader: a hanging import times out and an abort cancels and cleans up', async () => {
  const env = loaderEnv()
  const never = new Promise(() => {})
  const base = { app: app(), createSDK: () => ({}), fetchImpl: env.fetchImpl, document: env.doc, origin: ORIGIN }
  await assert.rejects(loadAddonUI({ ...base, importer: () => never, timeoutMs: 20 }), (e) => e.code === 'timeout')
  const ctl = new AbortController()
  const pending = loadAddonUI({ ...base, importer: () => never, timeoutMs: 5000, signal: ctl.signal })
  setTimeout(() => ctl.abort('closed'), 10)
  await assert.rejects(pending, (e) => e.code === 'aborted')
  assert.ok(env.styles.every(el => el.removed))
  const stylesheet404 = async () => ({ ok: false, status: 404, headers: { get: () => '' }, text: async () => '' })
  await assert.rejects(loadAddonUI({ ...base, importer: async () => ({}), fetchImpl: stylesheet404 }), (e) => e.code === 'css_failed')
  const wrongType = async () => ({ ok: true, status: 200, headers: { get: () => 'text/html' }, text: async () => '<html>' })
  await assert.rejects(loadAddonUI({ ...base, importer: async () => ({}), fetchImpl: wrongType }), (e) => e.code === 'css_failed')
})

test('loader: dispose runs once, aborts the SDK signal and removes the styles', async () => {
  const env = loaderEnv()
  let disposed = 0
  let signalSeen
  const out = await loadAddonUI({
    app: app(),
    createSDK: ({ signal }) => { signalSeen = signal; return createHostSDK({ addonId: 'notes', generation: 3, signal, host: fakeHost().host }) },
    importer: async () => ({ createAddon: () => ({ dispose() { disposed++ } }) }),
    fetchImpl: env.fetchImpl, document: env.doc, origin: ORIGIN,
  })
  assert.equal(signalSeen.aborted, false)
  out.dispose()
  out.dispose()
  assert.equal(disposed, 1)
  assert.equal(signalSeen.aborted, true)
  assert.ok(env.styles.every(el => el.removed))
})

test('loader: chat actions are limited to declared ids and carry only the conversation context', () => {
  const opened = []
  const raw = [
    { id: 'summarize', label: 'Summarize', icon: 'sparkles', open: (c) => opened.push(c) },
    { id: 'undeclared', label: 'Hidden', icon: 'sparkles', open() {} },
    { id: 'summarize', label: 'Duplicate', icon: 'sparkles', open() {} },
    { id: 'bad-label', label: '', icon: 'sparkles', open() {} },
    { id: 'no-open', label: 'x', icon: 'sparkles' },
    { id: 'throws', label: 'Throws', icon: 'not-an-icon', open() { throw new Error('boom') } },
  ]
  const actions = validateChatActions(raw, ['summarize', 'throws', 'bad-label', 'no-open'], ['sparkles', 'puzzle'])
  assert.deepEqual(actions.map(a => a.id), ['summarize', 'throws'])
  assert.equal(actions[1].icon, 'puzzle', 'an unknown icon name falls back')
  // The host passes more than it should; the action sees two keys at most.
  const result = actions[0].open({ conversationId: 'c1', resourceRef: 'doc:7', history: [{ role: 'user', content: 'secret' }], send: () => {}, messages: [] })
  assert.deepEqual(result, { ok: true })
  assert.deepEqual(opened, [{ conversationId: 'c1', resourceRef: 'doc:7' }])
  assert.deepEqual(Object.keys(opened[0]).sort(), ['conversationId', 'resourceRef'])
  assert.equal(actions[0].open({ history: [] }).ok, false, 'no conversation, no call')
  assert.equal(actions[1].open({ conversationId: 'c1' }).ok, false, 'a throwing add-on never throws into the host')
  assert.equal(chatActionContext({ conversationId: 'c', resourceRef: 5 }), null)
  assert.deepEqual(chatActionContext({ conversationId: 'c' }), { conversationId: 'c' })
  assert.throws(() => validateChatActions({}, []), LoadError)
  assert.deepEqual(validateChatActions(undefined, []), [])
})

// ---- Registry -----------------------------------------------------------

function managerEnv() {
  const log = { loads: [], disposed: [], aborted: [] }
  const manager = createRuntimeManager({
    load: async (a, { signal, setDirty }) => {
      log.loads.push(`${a.id}@${a.generation}`)
      signal.addEventListener('abort', () => log.aborted.push(`${a.id}@${a.generation}`))
      return {
        component: () => null,
        chatActions: a.ui.chatActions.map(id => ({ id, label: id, icon: 'puzzle', open() {} })),
        dispose: () => log.disposed.push(`${a.id}@${a.generation}`),
        abort() {},
        setDirty,
      }
    },
  })
  return { manager, log }
}
const settle = () => new Promise(r => setTimeout(r, 0))

test('registry: disable, uninstall, crash and update withdraw a loaded add-on at once', async () => {
  for (const [name, next, reason] of [
    ['disable', app({ enabled: false, state: 'stopped' }), 'disabled'],
    ['uninstall', null, 'removed'],
    ['crash', app({ state: 'failed' }), 'failed'],
    ['update', app({ generation: 4, ui: { ...app().ui, entry: asset('notes', 'ui.js', 'cd'.repeat(32)) } }), 'updated'],
    ['no ui', app({ ui: undefined }), 'no-ui'],
  ]) {
    const { manager, log } = managerEnv()
    const hold = manager.hold(app())
    await settle()
    assert.equal(manager.get('notes').status, 'ready', name)
    assert.deepEqual(manager.get('notes').chatActions.map(a => a.id), ['summarize'], name)
    manager.revalidate(next ? [next] : [])
    const inst = manager.get('notes')
    assert.equal(inst.status, 'unavailable', name)
    assert.equal(inst.reason, reason, name)
    assert.equal(inst.Component, null, name)
    assert.deepEqual(inst.chatActions, [], 'slots go away with the instance: ' + name)
    assert.deepEqual(log.disposed, ['notes@3'], name)
    assert.deepEqual(log.aborted, ['notes@3'], name)
    hold.release()
  }
  assert.equal(invalidReason(app(), { generation: 3, entry: app().ui.entry }), '')
})

test('registry: unsaved edits keep a withdrawn add-on stale until the user reloads', async () => {
  const captured = { disposed: 0, aborted: 0 }
  const manager = createRuntimeManager({
    load: async (a, opts) => {
      captured.setDirty = opts.setDirty
      return {
        component: () => 'draft',
        chatActions: [{ id: 'summarize', label: 's', icon: 'puzzle', open() {} }],
        dispose: () => { captured.disposed++ },
        abort: () => { captured.aborted++ },
      }
    },
  })
  manager.hold(app())
  await settle()
  manager.revalidate([app()])
  assert.equal(manager.get('notes').status, 'ready', 'a list that still agrees changes nothing')
  captured.setDirty(true)
  assert.equal(manager.get('notes').dirty, true)
  manager.revalidate([app({ generation: 4 })])
  const stale = manager.get('notes')
  assert.equal(stale.status, 'stale')
  assert.equal(stale.reason, 'updated')
  assert.equal(typeof stale.Component, 'function', 'the edits stay on screen')
  assert.deepEqual(stale.chatActions, [], 'a stale add-on contributes no slot')
  assert.equal(captured.aborted, 1, 'calls are cut at once')
  assert.equal(captured.disposed, 0, 'nothing is torn down under the user')
  manager.reload(app({ generation: 4 }))
  await settle()
  assert.equal(captured.disposed, 1)
  assert.equal(manager.get('notes').status, 'ready')
  assert.equal(manager.get('notes').generation, 4)
  assert.equal(manager.get('notes').dirty, false)
})

test('registry: a poll never replaces a withdrawn instance; only reload does', async () => {
  const { manager, log } = managerEnv()
  manager.hold(app())
  await settle()
  manager.revalidate([app({ enabled: false })])
  manager.revalidate([app()])
  assert.equal(manager.get('notes').status, 'unavailable')
  assert.deepEqual(log.loads, ['notes@3'])
  manager.reload(app())
  await settle()
  assert.equal(manager.get('notes').status, 'ready')
  assert.deepEqual(log.loads, ['notes@3', 'notes@3'])
  manager.disposeAll()
  assert.equal(manager.get('notes'), null)
})

test('registry: a hold ends with its last holder and a load that was withdrawn leaves no instance behind', async () => {
  const { manager, log } = managerEnv()
  const a = manager.hold(app())
  const b = manager.hold(app())
  await settle()
  a.release()
  assert.equal(manager.get('notes').status, 'ready')
  b.release()
  b.release()
  assert.equal(manager.get('notes'), null)
  assert.deepEqual(log.disposed, ['notes@3'])
  const slow = createRuntimeManager({ load: (a2, { signal }) => new Promise((_, reject) => signal.addEventListener('abort', () => reject(new LoadError('aborted')))) })
  const h = slow.hold(app())
  h.release()
  await settle()
  assert.equal(slow.get('notes'), null)
})

test('registry: navigation and chat actions come only from usable add-ons of the current generation', async () => {
  const list = [
    app(),
    app({ id: 'tables', title: 'Tables', ui: { ...app().ui, entry: asset('tables', 'ui.js'), css: [], navigation: ['home', 'detail'] } }),
    app({ id: 'off', enabled: false, state: 'stopped' }),
    app({ id: 'crashed', state: 'failed' }),
    app({ id: 'future', ui: { ...app().ui, sdk: { min: 3, max: 3 } } }),
    app({ id: 'headless', ui: undefined }),
  ]
  const items = navigationItems(list, { language: 'en' })
  assert.deepEqual(items.map(i => i.path), ['/app/addons/notes/home', '/app/addons/tables/home', '/app/addons/tables/detail'])
  assert.deepEqual(items.map(i => i.label), ['Notes', 'Tables: home', 'Tables: detail'])
  // Titles follow the interface language.
  const it = navigationItems([app({ titles: { it: 'Appunti' } })], { language: 'it-IT' })
  assert.equal(it[0].label, 'Appunti')
  // A navigation entry that is not a declared page is never drawn.
  assert.deepEqual(navigationItems([app({ ui: { ...app().ui, navigation: ['ghost'] } })]), [])

  const { manager } = managerEnv()
  manager.hold(app())
  await settle()
  const all = () => availableChatActions(manager.snapshot(), [app()], { hasFeature: (f) => f === 'addons' })
  assert.deepEqual(all().map(a => `${a.addonId}:${a.id}:${a.generation}`), ['notes:summarize:3'])
  // A viewer without the feature gets none, whatever is loaded.
  assert.deepEqual(availableChatActions(manager.snapshot(), [app()], { hasFeature: () => false }), [])
  // The list moved on to another generation: the old actions are gone before the instance is.
  assert.deepEqual(availableChatActions(manager.snapshot(), [app({ generation: 4 })], { hasFeature: () => true }), [])
  assert.deepEqual(availableChatActions(manager.snapshot(), [app({ state: 'failed' })], { hasFeature: () => true }), [])
  assert.deepEqual(availableChatActions(manager.snapshot(), [], { hasFeature: () => true }), [])
  // Whatever status says, only a ready instance contributes.
  const action = { id: 'summarize', label: 's', icon: 'puzzle', open() {} }
  for (const status of ['stale', 'loading', 'unavailable', 'error']) {
    const fake = [{ id: 'notes', generation: 3, entry: app().ui.entry, status, chatActions: [action] }]
    assert.deepEqual(availableChatActions(fake, [app()], { hasFeature: () => true }), [], status)
  }
})

test('registry: the legacy Add-ons tab lists page links only when the registry has usable add-ons', () => {
  const tab = buildHub.tabs.find(t => t.id === 'addons')
  const auth = { isAdmin: false, authEnabled: true, hasFeature: () => true, features: { addon_navigation: false } }
  // The tab exists only under the legacy switch: hidden in the new layout and until /api/features answers.
  assert.equal(isTabVisible(tab, auth), true)
  assert.equal(isTabVisible(tab, { ...auth, features: { addon_navigation: true } }), false)
  assert.equal(isTabVisible(tab, { ...auth, features: {} }), false)
  assert.deepEqual(visibleItems(tab, auth).map(i => i.path), ['/app/addons'])
  const nav = navigationItems([app()], { language: 'en' })
  const items = visibleItems(tab, { ...auth, addonNavigation: nav })
  assert.deepEqual(items.map(i => i.path), ['/app/addons', '/app/addons/notes/home'])
  assert.equal(items[1].label, 'Notes')
  // A viewer who may not use add-ons has no tab and so no links.
  assert.deepEqual(visibleItems(tab, { ...auth, hasFeature: () => false, addonNavigation: nav }), [])
  // Other tabs never pick up add-on links.
  const agents = buildHub.tabs.find(t => t.id === 'agents')
  assert.deepEqual(visibleItems(agents, { ...auth, addonNavigation: nav }).map(i => i.path), ['/app/agents'])
})

test('registry: the list is read off the Add-ons page only for a viewer with the feature and a UI contribution', () => {
  const plain = [app({ ui: undefined })]
  const nav = [app({ ui: { ...app().ui, navigation: ['home'], chatActions: [], pages: ['home'] } })]
  const bare = [app({ ui: { ...app().ui, navigation: [], chatActions: [], pages: [] } })]
  assert.equal(contributesUI(plain), false)
  assert.equal(contributesUI(bare), false)
  assert.equal(contributesUI(nav), true)
  // Once a list says it is disabled or crashed it contributes nothing more.
  assert.equal(contributesUI([{ ...nav[0], enabled: false }]), false)
  assert.equal(contributesUI([{ ...nav[0], state: 'failed' }]), false)
  assert.equal(contributesUI(null), false)
  const base = { allowed: true, onAddonsPage: false, apps: plain, status: 'ready', consumers: 1 }
  // Nothing contributes and the list is known: no read at all.
  assert.equal(shouldPoll(base), false)
  // A contribution keeps it current, with or without a consumer.
  assert.equal(shouldPoll({ ...base, apps: nav, consumers: 0 }), true)
  // The Add-ons page always reads it.
  assert.equal(shouldPoll({ ...base, onAddonsPage: true }), true)
  // A never-read list is learned once, and only for a consumer.
  assert.equal(shouldPoll({ ...base, status: 'idle', apps: [] }), true)
  assert.equal(shouldPoll({ ...base, status: 'idle', apps: [], consumers: 0 }), false)
  // Without the feature nothing ever reads, whatever else holds.
  assert.equal(shouldPoll({ ...base, allowed: false, onAddonsPage: true, apps: nav, status: 'idle' }), false)
})
