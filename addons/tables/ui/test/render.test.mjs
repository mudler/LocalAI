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

// The real host SDK over a fake host: what the page draws first, before any
// answer arrived, and what the add-on returns to the loader.
async function load() {
  const dir = await build()
  const mod = await import(pathToFileURL(join(dir, 'ui/tables.js')).href + `?t=${Date.now()}`)
  const { createHostSDK } = await import(pathToFileURL(join(hostUi, 'src/addons/sdk.js')).href)
  const wrap = (tag) => (p) => React.createElement(tag, null, p.children)
  const calls = []
  const controller = new AbortController()
  const sdk = createHostSDK({
    addonId: 'tables', generation: 1, signal: controller.signal, pages: ['databases'],
    host: {
      React,
      components: { Button: wrap('button'), Spinner: wrap('i'), EmptyState: wrap('section'), Badge: wrap('b'), Text: wrap('p'), Stack: wrap('div') },
      Icon: () => null,
      theme: { mode: () => 'light', subscribe: () => () => {} },
      language: () => 'en',
      subscribeLanguage: () => () => {},
      api: {
        async dispatch(b) { calls.push(['dispatch', b]); return {} },
        async propose() { return {} },
        async resource(b) { calls.push(['resource', b]); return { resources: [] } },
      },
    },
    bindings: { current: () => ({ page: 'databases', path: '', search: '' }), openLayer: () => ({ close() {} }) },
  })
  return { mod, sdk, controller, calls }
}

test('render: createAddon returns the page, the declared component, a chat action and dispose', async () => {
  const { mod, sdk, controller } = await load()
  const addon = await mod.createAddon(sdk)
  assert.equal(typeof addon.Component, 'function')
  assert.deepEqual(Object.keys(addon.components), ['SelectDatabase'])
  assert.equal(addon.chatActions.length, 1)
  assert.equal(addon.chatActions[0].id, 'select-database')
  assert.equal(addon.chatActions[0].label, 'Select database')
  assert.equal(typeof addon.dispose, 'function')
  addon.dispose()
  controller.abort('done')
})

test('render: the list, one database and the picker draw their loading state without a host', async () => {
  const { mod, sdk, calls } = await load()
  const addon = await mod.createAddon(sdk)
  const list = renderToString(React.createElement(addon.Component, { page: 'databases', path: '' }))
  assert.match(list, /Your databases/)
  assert.match(list, /data-addon="tables"/)
  const one = renderToString(React.createElement(addon.Component, { page: 'databases', path: 'r0123456789abcdef0123456789abcde' }))
  assert.match(one, /tbl-loading/)
  const pick = renderToString(React.createElement(addon.components.SelectDatabase, { conversationId: 'chat-1', close() {} }))
  assert.match(pick, /Select database|choose the database|Choose the database/i)
  assert.equal(renderToString(React.createElement(addon.Component, { page: 'other', path: '' })), '')
  // Drawing never sends anything of its own.
  assert.deepEqual(calls, [])
})

test('render: what the page read is emptied when the add-on is withdrawn, by signal and by dispose', async () => {
  const { mod, sdk, controller } = await load()
  const cache = new Map([['db:Vault', { rows: ['SECRET'] }]])
  const addon = await mod.createAddon(sdk, { cache })
  assert.equal(cache.size, 1)
  controller.abort('withdrawn')
  assert.equal(cache.size, 0)
  addon.dispose()

  const second = await load()
  const kept = new Map([['db:Vault', { rows: ['SECRET'] }]])
  const live = await second.mod.createAddon(second.sdk, { cache: kept })
  live.dispose()
  assert.equal(kept.size, 0)
  second.controller.abort('done')
})
