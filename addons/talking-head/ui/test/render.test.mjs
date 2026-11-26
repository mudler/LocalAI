import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { build } from '../build.mjs'
import { React, renderToString, makeSdk } from '../../../internal/host-compat/test/host.mjs'

const here = dirname(fileURLToPath(import.meta.url))
let built

async function load(overrides) {
  built ??= await build()
  const mod = await import(pathToFileURL(join(built, 'ui/talking-head.js')).href + `?t=${Date.now()}`)
  const host = await makeSdk({ addonId: 'talking-head', pages: ['talking-head'], overrides })
  return { mod, ...host }
}

const page = (addon) => renderToString(React.createElement(addon.Component, { page: 'talking-head', path: '', search: '' }))

// A host whose capability listing holds the given models.
const withModels = (models) => ({
  hooks: {
    useModels: () => ({ models, loading: false, error: null, refetch() {} }),
    useMCPClient: () => ({}),
  },
})

const INSTALLED = [
  { id: 'qwen', capabilities: ['FLAG_CHAT'], backend: 'llama-cpp' },
  { id: 'piper', capabilities: ['FLAG_TTS'], backend: 'piper' },
  { id: 'whisper', capabilities: ['FLAG_TRANSCRIPT'], backend: 'whisper' },
  { id: 'avatarforcing', capabilities: ['FLAG_VIDEO'], backend: 'avatarforcing-cpp' },
]

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

test('render: the page draws exactly one h1, its own title, and no key', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const html = page(addon)
  assert.deepEqual([...html.matchAll(/<h1[^>]*>(.*?)<\/h1>/g)].map((m) => m[1]), ['Talking Head'])
  assert.doesNotMatch(html, />(chips|empty|status|composer|transcript|settings|stages|face)\.[A-Za-z.]+</, 'a key was shown instead of its words')
})

test('render: without an avatar or a speech model the page says what to install', async () => {
  const { mod, sdk } = await load(withModels(INSTALLED.slice(0, 1)))
  const addon = await mod.createAddon(sdk)
  const html = page(addon)
  assert.match(html, /data-testid="head-missing"/)
  assert.match(html, /avatarforcing-cpp backend/)
  assert.match(html, /text-to-speech model/)
  assert.match(html, /CC BY-NC 4\.0/)
  assert.doesNotMatch(html, /data-testid="head-text"/)
})

test('render: a video model of another backend is not an avatar model', async () => {
  const { mod, sdk } = await load(withModels([...INSTALLED.slice(0, 3), { id: 'wan', capabilities: ['FLAG_VIDEO'], backend: 'diffusers' }]))
  const addon = await mod.createAddon(sdk)
  assert.match(page(addon), /data-testid="head-missing"/)
})

test('render: with the models and no portrait the page asks for one and locks the composer', async () => {
  const { mod, sdk } = await load(withModels(INSTALLED))
  const addon = await mod.createAddon(sdk)
  const html = page(addon)
  assert.doesNotMatch(html, /data-testid="head-missing"/)
  assert.match(html, /Upload a portrait/)
  assert.match(html, /<textarea[^>]*disabled=""[^>]*data-testid="head-text"/)
})

test('render: with the models and a stored portrait the composer is open', async () => {
  const { mod, sdk } = await load(withModels(INSTALLED))
  globalThis.localStorage.setItem('localai.talking-head.portraits', JSON.stringify([{ id: 'p1', name: 'Me', dataUrl: 'data:image/jpeg;base64,AAAA' }]))
  const addon = await mod.createAddon(sdk)
  const html = page(addon)
  assert.match(html, /<img class="head-face__still" src="data:image\/jpeg;base64,AAAA"/)
  assert.doesNotMatch(html, /<textarea[^>]*disabled=""[^>]*data-testid="head-text"/)
  assert.match(html, /Hold to talk/)
  assert.match(html, /Say this/)
})

test('createAddon fails readably on a host without the pieces the page needs', async () => {
  const { mod, sdk } = await load()
  const old = Object.freeze({ ...sdk, hooks: Object.freeze({}) })
  await assert.rejects(mod.createAddon(old), /needs a newer LocalAI/)
})

test('render: the manifest declares one page, the main page, the video model action and SDK 2', () => {
  const m = JSON.parse(readFileSync(join(here, '../../manifest.json'), 'utf8'))
  assert.equal(m.id, 'talking-head')
  assert.equal(m.executable, 'talking-head')
  assert.deepEqual(m.capabilities.ui.pages, ['talking-head'])
  assert.deepEqual(m.capabilities.ui.navigation, ['talking-head'])
  assert.equal(m.capabilities.ui.mainPage, 'talking-head')
  assert.deepEqual(m.capabilities.ui.modelActions, [{ capability: 'FLAG_VIDEO', page: 'talking-head' }])
  assert.deepEqual(m.capabilities.ui.sdk, { min: 2, max: 2 })
  assert.deepEqual(m.capabilities.tools ?? [], [])
})
