import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { build } from '../build.mjs'
import { React, renderToString, makeSdk } from '../../../internal/host-compat/test/host.mjs'

const here = dirname(fileURLToPath(import.meta.url))

async function load(overrides) {
  const dir = await build()
  const mod = await import(pathToFileURL(join(dir, 'ui/talk.js')).href + `?t=${Date.now()}`)
  const host = await makeSdk({ addonId: 'talk', pages: ['talk'], overrides })
  return { mod, ...host }
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
  assert.equal(renderToString(React.createElement(addon.Component, { page: 'nope', path: '', search: '' })), '')
})

test('render: the page draws its title from the talk locale and no key', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const html = renderToString(React.createElement(addon.Component, { page: 'talk', path: '', search: '' }))
  assert.ok(html.length > 200, 'the page drew something')
  assert.doesNotMatch(html, />(stage|picker|view|controls|transcript|settings|toasts)\.[A-Za-z.]+</, 'a key was shown instead of its words')
})

test('createAddon fails readably on a host without the pieces the page needs', async () => {
  const { mod, sdk } = await load()
  const old = Object.freeze({ ...sdk, ui: Object.freeze({}) })
  await assert.rejects(mod.createAddon(old), /needs a newer LocalAI/)
})

test('render: the manifest declares one page, the main page and SDK 2', () => {
  const m = JSON.parse(readFileSync(join(here, '../../manifest.json'), 'utf8'))
  assert.equal(m.id, 'talk')
  assert.deepEqual(m.capabilities.ui.pages, ['talk'])
  assert.deepEqual(m.capabilities.ui.navigation, ['talk'])
  assert.equal(m.capabilities.ui.mainPage, 'talk')
  assert.deepEqual(m.capabilities.ui.sdk, { min: 2, max: 2 })
  assert.deepEqual(m.capabilities.tools ?? [], [])
})
