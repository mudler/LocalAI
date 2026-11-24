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

test('render: an unknown page draws nothing', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  assert.equal(draw(addon, { page: 'nope', path: '' }), '')
})

test('render: the main page says what the add-on does and which file types it handles', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const html = draw(addon, { page: 'office', path: '' })
  assert.match(html, /data-testid="office-page"/)
  for (const kind of ['Word', 'Excel', 'PowerPoint', 'PDF']) assert.match(html, new RegExp(kind))
  assert.equal((html.match(/<h1[ >]/g) || []).length, 1, 'one title')
})

test('render: the manifest declares the one main page and SDK 2', () => {
  const m = JSON.parse(readFileSync(join(here, '../../manifest.json'), 'utf8'))
  assert.equal(m.id, 'office')
  assert.deepEqual(m.capabilities.ui.pages, ['office'])
  assert.equal(m.capabilities.ui.mainPage, 'office')
  assert.deepEqual(m.capabilities.ui.sdk, { min: 2, max: 2 })
})
