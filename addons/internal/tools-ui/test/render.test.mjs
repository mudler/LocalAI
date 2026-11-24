import test from 'node:test'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { copyFileSync, readFileSync } from 'node:fs'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { dirname, join, resolve } from 'node:path'
import { buildAddonUI } from '../../uibuild/uibuild.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const hostUi = resolve(here, '../../../../core/http/react-ui')
const require = createRequire(join(hostUi, 'package.json'))
const React = require('react')
const { renderToString } = require('react-dom/server')

const flatten = (obj, prefix = '') => Object.entries(obj).flatMap(([k, v]) =>
  typeof v === 'string' ? [[`${prefix}${k}`, v]] : v && typeof v === 'object' && !Array.isArray(v) ? flatten(v, `${prefix}${k}.`) : [])

// The bundle is built from the shared source and loaded through createAddon,
// the path the host takes, so a helper that loses its binding fails here.
async function load({ admin = true, features = [] } = {}) {
  const root = join(here, 'fixture')
  copyFileSync(join(here, '../src/tools.css'), join(root, 'src/style.css'))
  const dir = await buildAddonUI({ root, name: 'fixture', rgb: [1, 2, 3] })
  const mod = await import(pathToFileURL(join(dir, 'ui/fixture.js')).href + `?t=${Date.now()}`)
  const { createHostSDK } = await import(pathToFileURL(join(hostUi, 'src/addons/sdk.js')).href)
  const sdk = createHostSDK({
    addonId: 'fixture', generation: 1, signal: new AbortController().signal, pages: ['steps', 'checks', 'stage', 'log', 'off'],
    host: {
      React, components: {}, hooks: {}, viewer: { isAdmin: () => admin, hasFeature: (f) => features.includes(f) },
      basePath: '', fetch: async () => ({ ok: true, json: async () => ({}) }),
      Icon: ({ name }) => React.createElement('i', { 'data-icon': name }), theme: { mode: () => 'light', subscribe: () => () => {} },
      language: () => 'en', subscribeLanguage: () => () => {}, api: {},
    },
    bindings: { current: () => ({ page: 'steps', path: '', search: '' }), openLayer: () => ({ close() {} }), notify() {} },
  })
  const tools = JSON.parse(readFileSync(join(hostUi, 'public/locales/en/tools.json'), 'utf8'))
  sdk.locale.define('en', Object.fromEntries(flatten(tools)))
  return { mod, sdk }
}

const draw = async (page, props, viewer) => {
  const { mod, sdk } = await load(viewer)
  const addon = await mod.createAddon(sdk, { props: { [page]: props } })
  return renderToString(React.createElement(addon.Component, { page, path: '' }))
}

test('ToolSteps marks the steps before the current one as done', async () => {
  const html = await draw('steps', { steps: [{ key: 'a', label: 'One' }, { key: 'b', label: 'Two' }, { key: 'c', label: 'Three' }], current: 1, label: 'Progress' })
  assert.match(html, /data-testid="tool-steps"/)
  assert.match(html, /<li[^>]*data-state="done"[^>]*data-step="a"/)
  assert.match(html, /data-state="current"/)
  assert.match(html, /data-state="todo"/)
  assert.match(html, /data-icon="check"/)
})

test('ToolSteps shows a failed current step with a close mark', async () => {
  const html = await draw('steps', { steps: [{ key: 'a', label: 'One' }], current: 0, failed: true })
  assert.match(html, /data-state="failed"/)
  assert.match(html, /data-icon="close"/)
})

test('Checks draws one row per check, with sizes and the meter', async () => {
  const GB = 1024 ** 3
  const html = await draw('checks', {
    checks: [{ id: 'disk', key: 'disk.ok', tone: 'ok', vals: { free: 8 * GB, total: 16 * GB }, meter: { used: 8 * GB, total: 16 * GB } }],
  })
  assert.match(html, /data-testid="tool-checks"/)
  assert.match(html, /data-check="disk"/)
  assert.match(html, /data-tone="ok"/)
  assert.match(html, /dk-meter-seg--b/)
  assert.match(html, /--dk-w:50%/)
})

test('Checks links to the backends only for an administrator', async () => {
  const checks = [{ id: 'backend', key: 'backend.missing', tone: 'fail', action: 'backends', vals: {} }]
  assert.match(await draw('checks', { checks, isAdmin: true }), /href="\/app\/backends"/)
  assert.doesNotMatch(await draw('checks', { checks, isAdmin: false }), /\/app\/backends/)
})

test('StageLine fills the stages before the current one', async () => {
  const html = await draw('stage', { stages: ['queued', 'training', 'saving'], labels: { queued: 'Queued', training: 'Training', saving: 'Saving' }, status: 'training' })
  assert.match(html, /data-testid="job-stages"/)
  assert.match(html, /Training/)
  assert.equal((html.match(/data-state="done"/g) || []).length, 1)
  assert.equal((html.match(/data-state="current"/g) || []).length, 1)
})

test('JobLog lists the lines it is given, with the heading from the bundle', async () => {
  const html = await draw('log', { lines: [{ at: new Date(2026, 0, 1, 9, 5, 7), level: 'info', tag: 'job', text: 'started' }], name: 'ft' })
  assert.match(html, /data-testid="job-log"/)
  assert.match(html, /09:05:07/)
  assert.match(html, />started</)
  assert.doesNotMatch(html, /log\.title/)
})

test('ToolOff shows the notice when the feature is missing', async () => {
  const html = await draw('off', { tool: 'fineTune', feature: 'fine_tuning' }, { admin: false, features: [] })
  assert.match(html, /data-testid="tool-off"/)
  assert.match(html, /data-cause="account"/)
  assert.doesNotMatch(html, /children/)
  assert.doesNotMatch(html, /off\.fineTune\.title/)
  assert.match(html, /href="\/app"/)
})

test('ToolOff draws its children when the feature is held', async () => {
  const html = await draw('off', { tool: 'fineTune', feature: 'fine_tuning' }, { features: ['fine_tuning'] })
  assert.match(html, /<p>children<\/p>/)
  assert.doesNotMatch(html, /tool-off/)
})
