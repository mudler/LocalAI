import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { load, draw, here, React, renderToString } from './load.mjs'

const lib = () => import('../src/lib/model.js')

const status = (over = {}) => ({
  ok: true, admin: true, serving: ['brave', 'duckduckgo'],
  settings: {
    order: ['brave', 'duckduckgo'],
    providers: [
      { name: 'brave', keyed: true, keySet: true, ready: true },
      { name: 'tavily', keyed: true, keySet: false, ready: false, missing: 'no key' },
      { name: 'google', keyed: true, keySet: false, ready: false, missing: 'no key and no search engine ID' },
      { name: 'searxng', keyed: false, keySet: false, ready: false, missing: 'no address' },
      { name: 'duckduckgo', keyed: false, keySet: false, ready: true },
    ],
    googleEngine: '', searxngUrl: '',
  },
  ...over,
})

test('settings: only an administrator gets the settings', async () => {
  const { settingsModel } = await lib()
  assert.equal(settingsModel(status({ admin: false })).hidden, true)
  assert.equal(settingsModel(null).hidden, true)
  assert.equal(settingsModel({ ok: false, error: 'x' }).hidden, true)
  assert.equal(settingsModel(status()).hidden, false)
})

test('settings: the providers of the order come first, in order', async () => {
  const { settingsModel, draftOf, rows, move, toggle } = await lib()
  const m = settingsModel(status())
  let d = draftOf(m)
  assert.deepEqual(rows(m, d).map(r => [r.name, r.on]), [['brave', true], ['duckduckgo', true], ['tavily', false], ['google', false], ['searxng', false]])
  d = move(d, 'duckduckgo', -1)
  assert.deepEqual(d.order, ['duckduckgo', 'brave'])
  assert.deepEqual(move(d, 'duckduckgo', -1).order, ['duckduckgo', 'brave'], 'the first stays first')
  d = toggle(d, 'tavily')
  assert.deepEqual(d.order, ['duckduckgo', 'brave', 'tavily'])
  assert.deepEqual(toggle(d, 'brave').order, ['duckduckgo', 'tavily'])
})

test('settings: a saved key is never sent back, a typed one is, and a removal is explicit', async () => {
  const { settingsModel, draftOf, setKey, clearKey, change } = await lib()
  const d = draftOf(settingsModel(status()))
  assert.deepEqual(change(d), { order: ['brave', 'duckduckgo'], googleEngine: '', searxngUrl: '' })
  assert.deepEqual(change(setKey(d, 'tavily', '  abcdefghij  ')).keys, { tavily: 'abcdefghij' })
  const cleared = clearKey(d, 'brave')
  assert.deepEqual(change(cleared).clear, ['brave'])
  assert.equal(change(cleared).keys, undefined)
  assert.deepEqual(setKey(cleared, 'brave', 'newkey12345').clear, [], 'typing a new key cancels the removal')
})

test('settings: the test rows keep known providers and text only', async () => {
  const { testRows } = await lib()
  assert.deepEqual(testRows({ ok: true, providers: [{ name: 'brave', ok: true, ready: true, results: 3, host: 'api.search.brave.com' }, { name: 'nope' }, { name: 'tavily', ready: false, error: 'no key' }] }), [
    { name: 'brave', ok: true, ready: true, results: 3, host: 'api.search.brave.com', error: '' },
    { name: 'tavily', ok: false, ready: false, results: 0, host: '', error: 'no key' },
  ])
  assert.deepEqual(testRows({ ok: false }), [])
})

test('settings: the view draws the order, the key state as text and no saved key', async () => {
  const { settingsModel, draftOf } = await lib()
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const View = addon.components.WebSettingsView
  const m = settingsModel(status({ settings: { ...status().settings, note: '<script>alert(1)</script>' } }))
  const html = renderToString(React.createElement(View, { model: m, draft: draftOf(m), busy: false, error: '', saved: false, tests: [], onDraft() {}, onSave() {}, onTest() {} }))
  assert.ok(!html.includes('<script>'), html)
  assert.match(html, /data-testid="web-settings"/)
  assert.match(html, /Searches go to: Brave Search, DuckDuckGo\./)
  assert.match(html, /A key is set\./)
  assert.match(html, /data-testid="web-clear-brave"/)
  assert.match(html, /type="password"/)
  assert.ok(html.indexOf('data-provider="duckduckgo"') < html.indexOf('data-provider="tavily"'))
  assert.equal(renderToString(React.createElement(View, { model: { hidden: true } })), '')
})

test('render: the main page has one title and the manifest declares the settings section', async () => {
  const { mod, sdk, controller } = await load()
  const addon = await mod.createAddon(sdk)
  assert.equal(draw(addon, { page: 'nope', path: '' }), '')
  const html = draw(addon, { page: 'web', path: '' })
  assert.match(html, /data-testid="web-page"/)
  assert.equal((html.match(/<h1[ >]/g) || []).length, 1)
  assert.equal(typeof addon.components.WebSettings, 'function')
  addon.dispose()
  controller.abort('done')
  const m = JSON.parse(readFileSync(join(here, '../../manifest.json'), 'utf8'))
  assert.equal(m.id, 'web')
  assert.equal(m.capabilities.ui.mainPage, 'web')
  assert.deepEqual(m.capabilities.ui.sdk, { min: 2, max: 2 })
  assert.deepEqual(m.capabilities.ui.slots, [{ component: 'WebSettings', id: 'settings', slot: 'settings.sections' }])
})
