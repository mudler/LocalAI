import test from 'node:test'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { load, hostUi, React, draw } from './load.mjs'

// A real browser tree (jsdom) so the picker's effects and clicks run. React DOM
// reads the document at import time, so it is required after the globals.
const require = createRequire(join(hostUi, 'package.json'))
const { JSDOM } = require('jsdom')
const dom = new JSDOM('<div id="root"></div>', { url: 'http://localhost/' })
globalThis.window = dom.window
globalThis.document = dom.window.document
globalThis.IS_REACT_ACT_ENVIRONMENT = true
const { createRoot } = require('react-dom/client')
const { act } = React
const { userSettingsRequest } = await import(pathToFileURL(join(hostUi, 'src/addons/userSettingsCalls.js')).href)

const DECLARED = { settings: [{ key: 'save-mode', values: ['ask', 'auto', 'never'], default: 'ask', value: 'ask' }] }
const view = (value) => ({ settings: [{ ...DECLARED.settings[0], value }] })

// The host's Choice with a working change handler: the kit stub does not wire one.
const Choice = (p) => React.createElement('fieldset', { 'data-testid': p.testId, disabled: p.disabled },
  React.createElement('legend', null, p.legend),
  p.options.map((o) => React.createElement('label', { key: o.value, 'data-value': o.value },
    React.createElement('input', { type: 'radio', name: 'mode', value: o.value, checked: p.value === o.value, disabled: o.disabled, onChange: () => p.onChange(o.value) }),
    React.createElement('span', { className: 'label' }, o.label),
    React.createElement('span', { className: 'desc' }, o.description))))

// mount draws the page with a fake owner behind sdk.userSettings. Every call
// goes through the real request builder, so a wrong route or header fails.
async function mount(handler) {
  const calls = []
  const { mod, sdk } = await load({
    components: { Choice },
    api: { userSettings: async (call) => { const r = userSettingsRequest(call); calls.push(r); return handler(r) } },
  })
  const addon = mod.createAddon(sdk)
  const root = createRoot(document.getElementById('root'))
  await act(async () => root.render(React.createElement(addon.Component, { page: 'memory' })))
  const q = (sel) => document.querySelector(sel)
  return { calls, q, root, async close() { await act(async () => root.unmount()) } }
}
const settle = () => act(async () => { await new Promise((r) => setTimeout(r, 0)) })

test('settings: it shows the three choices with a line each and the stored one selected', async () => {
  const p = await mount(async () => view('auto'))
  await settle()
  assert.deepEqual(p.calls.map((c) => [c.method, c.path]), [['GET', '/api/addons/memory/user-settings']])
  const labels = [...document.querySelectorAll('[data-testid="memory-save-mode"] label')].map((l) => [l.dataset.value, l.querySelector('.label').textContent, l.querySelector('.desc').textContent.length > 20])
  assert.deepEqual(labels, [['ask', 'Ask me first', true], ['auto', 'Save it and tell me', true], ['never', 'Never', true]])
  assert.equal(p.q('input[value="auto"]').checked, true)
  assert.equal(p.q('input[value="ask"]').checked, false)
  assert.match(p.q('[data-testid="memory-save-mode"] legend').textContent, /When the assistant wants to save something to my memory/)
  await p.close()
})

test('settings: the help text says that messaging apps and background runs keep asking', async () => {
  const p = await mount(async () => view('ask'))
  await settle()
  const text = p.q('[data-testid="memory-save-exception"]').textContent
  assert.match(text, /messaging app always ask first, whatever you choose/)
  assert.match(text, /Scheduled and background runs cannot save notes/)
  assert.match(text, /cannot widen your choice/)
  await p.close()
})

test('settings: choosing a mode sends that one key from the page of this add-on and shows the saved choice', async () => {
  const p = await mount(async (r) => (r.method === 'PUT' ? view(r.body.value) : view('ask')))
  await settle()
  await act(async () => { p.q('input[value="auto"]').click() })
  await settle()
  const put = p.calls.find((c) => c.method === 'PUT')
  assert.equal(put.path, '/api/addons/memory/user-settings')
  assert.deepEqual(put.body, { key: 'save-mode', value: 'auto' })
  assert.equal(put.headers['X-LocalAI-Confirm'], 'memory', 'the own-session confirmation names the add-on')
  assert.equal(p.q('input[value="auto"]').checked, true)
  assert.match(p.q('[data-testid="memory-save-status"]').textContent, /Your choice is saved/)
  assert.equal(p.q('[data-testid="memory-save-failed"]'), null)
  await p.close()
})

test('settings: a failed save keeps the earlier choice and says so', async () => {
  const p = await mount(async (r) => { if (r.method === 'PUT') throw new Error('503'); return view('ask') })
  await settle()
  await act(async () => { p.q('input[value="never"]').click() })
  await settle()
  assert.equal(p.q('input[value="ask"]').checked, true, 'the earlier choice stays')
  assert.equal(p.q('input[value="never"]').checked, false)
  assert.match(p.q('[data-testid="memory-save-failed"]').textContent, /earlier choice is still in force/)
  await p.close()
})

test('settings: the host answer decides, not the click', async () => {
  // The owner stored something else (a concurrent change): the page shows it.
  const p = await mount(async (r) => (r.method === 'PUT' ? view('never') : view('ask')))
  await settle()
  await act(async () => { p.q('input[value="auto"]').click() })
  await settle()
  assert.equal(p.q('input[value="never"]').checked, true)
  await p.close()
})

test('settings: an unreadable choice shows a warning and no radios', async () => {
  const p = await mount(async () => { throw new Error('503') })
  await settle()
  assert.match(p.q('[data-testid="memory-save-unreadable"]').textContent, /could not be read/)
  assert.equal(p.q('[data-testid="memory-save-mode"]'), null)
  await p.close()
})

test('settings: a package that declares no such setting shows no picker', async () => {
  const p = await mount(async () => ({ settings: [] }))
  await settle()
  assert.equal(p.q('[data-testid="memory-save"]'), null)
  assert.ok(p.q('[data-testid="memory-open"]'), 'the rest of the page stays')
  await p.close()
})

test('settings: the page reads the setting and does not guess before it arrives', async () => {
  const { mod, sdk } = await load({ components: { Choice } })
  const html = draw(mod.createAddon(sdk), { page: 'memory' })
  assert.match(html, /data-testid="memory-save-loading"/)
  assert.doesNotMatch(html, /type="radio"/)
})

test('settings: the page says memory is built in and links to Settings > Memory', async () => {
  const p = await mount(async () => view('ask'))
  await settle()
  const n = p.q('[data-testid="memory-builtin-notice"]')
  assert.ok(n, 'the notice is on the page')
  assert.match(n.textContent, /Memory is built in/)
  assert.match(n.textContent, /Settings > Memory/)
  await p.close()
})
