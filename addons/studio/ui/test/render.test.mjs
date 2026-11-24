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
  const mod = await import(pathToFileURL(join(dir, 'ui/studio.js')).href + `?t=${Date.now()}`)
  const host = await makeSdk({ addonId: 'studio', pages: ['studio'], overrides })
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

test('render: the overview draws the tab strip with its words from the media locale', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const html = renderToString(React.createElement(addon.Component, { page: 'studio', path: '', search: '' }))
  assert.match(html, /class="studio-tabs"/)
  assert.match(html, /data-tab="images"/)
  assert.match(html, /data-tab="overview"/)
  assert.doesNotMatch(html, /studio\.tabs\./, 'a key was shown instead of its words')
})

test('render: every tab draws without throwing', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  for (const tab of ['images', 'video', 'threed', 'tts', 'sound', 'transform', 'diarization']) {
    const html = renderToString(React.createElement(addon.Component, { page: 'studio', path: tab, search: '' }))
    assert.match(html, new RegExp(`studio-tab-active[^>]*|data-tab="${tab}"`), tab)
    assert.doesNotMatch(html, /[a-z]+\.[a-z]+\.[a-zA-Z.]+<\/(span|h1|p|button|label)>/, `${tab}: a key was shown instead of its words`)
  }
})

test('render: the manifest declares one page, the main page and SDK 2', () => {
  const m = JSON.parse(readFileSync(join(here, '../../manifest.json'), 'utf8'))
  assert.equal(m.id, 'studio')
  assert.deepEqual(m.capabilities.ui.pages, ['studio'])
  assert.deepEqual(m.capabilities.ui.navigation, ['studio'])
  assert.equal(m.capabilities.ui.mainPage, 'studio')
  assert.deepEqual(m.capabilities.ui.sdk, { min: 2, max: 2 })
  assert.deepEqual(m.capabilities.tools ?? [], [])
})
