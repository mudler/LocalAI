import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { load, draw, here, React } from './load.mjs'

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

test('render: the manifest declares one page, the main page, SDK 2 and no navigation or hub entry', () => {
  const m = JSON.parse(readFileSync(join(here, '../../manifest.json'), 'utf8'))
  assert.equal(m.id, 'quantization')
  assert.deepEqual(m.capabilities.ui.pages, ['jobs'])
  assert.equal(m.capabilities.ui.mainPage, 'jobs')
  assert.deepEqual(m.capabilities.ui.sdk, { min: 2, max: 2 })
  assert.equal(m.capabilities.ui.title, 'Quantization')
  assert.equal(m.capabilities.ui.hub, undefined)
})

test('render: a registered page is drawn with its path', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk, { pages: { jobs: ({ path }) => React.createElement('p', null, `page:${path}`) } })
  assert.equal(draw(addon, { page: 'jobs', path: 'abc' }), '<p>page:abc</p>')
})

test('render: the jobs page is drawn by the real registration path, which binds the JSX helper', async () => {
  // A helper that lost its binding throws here: the page draws JSX in every
  // component the factories build.
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const html = draw(addon, { page: 'jobs', path: '' })
  assert.match(html, /data-testid="quantize-page"/)
  assert.match(html, /data-testid="quantize-form"/)
  assert.match(html, /data-testid="tool-steps"/)
  assert.match(html, /data-testid="qz-bar"/)
  assert.match(html, /data-testid="qz-jobs"/)
  assert.match(html, /Quantize/)
})
