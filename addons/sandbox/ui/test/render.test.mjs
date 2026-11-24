import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { load, draw, here } from './load.mjs'

test('render: createAddon returns a page component and dispose', async () => {
  const { mod, sdk, controller } = await load()
  const addon = await mod.createAddon(sdk)
  assert.equal(typeof addon.Component, 'function')
  assert.equal(typeof addon.dispose, 'function')
  addon.dispose()
  controller.abort('done')
})

test('render: an unknown page draws nothing and the main page has one title', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  assert.equal(draw(addon, { page: 'nope', path: '' }), '')
  const html = draw(addon, { page: 'sandbox', path: '' })
  assert.match(html, /data-testid="sandbox-page"/)
  assert.equal((html.match(/<h1[ >]/g) || []).length, 1)
})

test('render: the manifest declares the main page and SDK 2', () => {
  const m = JSON.parse(readFileSync(join(here, '../../manifest.json'), 'utf8'))
  assert.equal(m.id, 'sandbox')
  assert.equal(m.capabilities.ui.mainPage, 'sandbox')
  assert.deepEqual(m.capabilities.ui.sdk, { min: 2, max: 2 })
})
