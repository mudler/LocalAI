// Drives the real SSH page, built as the host loads it, in a DOM: a person who
// never pressed "Check host key" presses Save. The core routes and the host
// are stand-ins; the page code, the shared connector page and the SSH add-on's
// step are the real ones.
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
const { JSDOM } = require('jsdom')

const PIN = 'ab'.repeat(32)
const dom = new JSDOM('<!doctype html><html lang="en"><body><div id="root"></div></body></html>', { url: 'http://localhost/', pretendToBeVisual: true })
for (const k of ['window', 'document', 'navigator', 'HTMLElement', 'HTMLInputElement', 'HTMLTextAreaElement', 'Node', 'Event', 'MouseEvent', 'KeyboardEvent', 'MutationObserver', 'getComputedStyle', 'requestAnimationFrame', 'cancelAnimationFrame']) {
  if (!(k in globalThis) || k === 'navigator') Object.defineProperty(globalThis, k, { value: dom.window[k], configurable: true, writable: true })
}
globalThis.IS_REACT_ACT_ENVIRONMENT = true
const { createRoot } = require('react-dom/client')
const { act } = React

const json = (body, status = 200) => ({ ok: status < 400, status, json: async () => body })
const kinds = { kinds: [{ kind: 'ssh', addon_id: 'ssh', tool_only: true, agent_optional: true, title: 'SSH' }], capabilities: [{ name: 'files.upload', usual: true, sensitive: false }] }

// The ConfirmDialog of the host, reduced to what the step uses.
function ConfirmDialog({ open, title, message, confirmLabel, confirmDisabled, cancelLabel, onConfirm, onCancel }) {
  if (!open) return null
  return React.createElement('div', { role: 'alertdialog', 'data-testid': 'dialog' },
    React.createElement('h2', null, title), React.createElement('div', null, message),
    React.createElement('button', { type: 'button', 'data-testid': 'dialog-cancel', onClick: onCancel }, cancelLabel || 'Cancel'),
    React.createElement('button', { type: 'button', 'data-testid': 'dialog-confirm', disabled: confirmDisabled, onClick: onConfirm }, confirmLabel))
}

// scan decides what a scan-host-key call answers; it may change between calls.
async function mount(scan) {
  const posts = []
  const scans = []
  const dir = await build()
  const mod = await import(pathToFileURL(join(dir, 'ui/ssh.js')).href + `?t=${Date.now()}`)
  const { createHostSDK } = await import(pathToFileURL(join(hostUi, 'src/addons/sdk.js')).href)
  const sdk = createHostSDK({
    addonId: 'ssh', generation: 1, signal: new AbortController().signal, pages: ['ssh'],
    host: {
      React, components: {
        ConfirmDialog,
        PaneLayout: (p) => React.createElement('div', null, p.children),
        SidePane: () => null,
        ActionMenu: () => null,
        StatusChip: (p) => React.createElement('span', null, p.children),
        LoadingSpinner: () => null,
        SideSheet: (p) => React.createElement('div', { 'data-testid': p.testId }, p.children, p.footer),
      }, hooks: {}, viewer: { isAdmin: () => true, hasFeature: () => true }, basePath: '',
      fetch: async (url, init = {}) => {
        const path = String(url).replace(/^https?:\/\/[^/]+/, '')
        const method = init.method || 'GET'
        if (method === 'POST' && path === '/api/chats/connectors') {
          const body = JSON.parse(init.body)
          posts.push(body)
          return json({ id: 'c1', kind: 'ssh', name: body.name, resources: body.resources, write_mode: body.write_mode, credentials_only: true, caps: [], grants: [] }, 201)
        }
        if (method === 'POST' && path.endsWith('/test')) return json({})
        if (path.startsWith('/api/chats/connectors/kinds')) return json(kinds)
        if (path === '/api/chats/connectors') return json({ connectors: posts.length ? [{ id: 'c1', kind: 'ssh', name: posts[0].name, resources: posts[0].resources, write_mode: 'propose', credentials_only: true }] : [] })
        if (path === '/api/chats/agents') return json({ agents: [] })
        return json({})
      },
      Icon: ({ name }) => React.createElement('i', { 'data-icon': name }), theme: { mode: () => 'light', subscribe: () => () => {} },
      language: () => 'en', subscribeLanguage: () => () => {},
      api: {
        dispatch: async (body) => {
          if (body.arguments?.tool !== 'scan-host-key') return { data: {} }
          scans.push(body.arguments)
          return scan(body.arguments)
        },
        propose: async () => ({}), resource: async () => ({}),
      },
    },
    bindings: { current: () => ({ page: 'ssh', path: '', search: '' }), openLayer: () => ({ close() {} }), notify() {} },
  })
  const addon = await mod.createAddon(sdk)
  document.body.innerHTML = '<div id="root"></div>'
  const root = createRoot(document.getElementById('root'))
  await act(async () => { root.render(React.createElement(addon.Component, { page: 'ssh', path: '' })) })
  await settle()
  return { posts, scans, root, q: (id) => document.querySelector(`[data-testid="${id}"]`) }
}

const settle = () => act(async () => { await new Promise((r) => setTimeout(r, 60)) })
const click = async (el) => { await act(async () => { el.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, cancelable: true })) }); await settle() }
const type = async (el, value) => {
  const proto = el instanceof dom.window.HTMLTextAreaElement ? dom.window.HTMLTextAreaElement.prototype : dom.window.HTMLInputElement.prototype
  await act(async () => {
    Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, value)
    el.dispatchEvent(new dom.window.Event('input', { bubbles: true }))
  })
}
// The Save button of the sheet: the one that submits the form.
const saveButton = () => [...document.querySelectorAll('button[type="submit"]')].pop()

async function fillNew(h) {
  await click(h.q('connector-new'))
  await type(h.q('ssh-host'), 'host.example:2222')
  await type(h.q('ssh-user'), 'deploy')
  await type(h.q('ssh-passwd'), 'secret')
  await settle()
}

const found = async () => ({ data: { pin: PIN, keyType: 'ssh-ed25519' } })

test('ssh save step: Save with no host key opens the step and Cancel saves nothing', async () => {
  const h = await mount(found)
  await fillNew(h)
  assert.equal(saveButton().disabled, false, 'Save is enabled once the user, the host and the secret are in')
  assert.equal(h.q('ssh-hostkey-needed') !== null, true, 'the form says the key is checked at Save')
  await click(saveButton())
  assert.ok(h.q('ssh-hostkey-step'), 'the step is open')
  assert.match(h.q('ssh-step-print').textContent, /^ssh-ed25519 SHA256:/)
  assert.deepEqual(h.scans, [{ tool: 'scan-host-key', address: 'deploy@host.example:2222' }], 'the scan sends the address and no secret')
  assert.equal(h.posts.length, 0)
  await click(h.q('dialog-cancel'))
  assert.equal(h.q('ssh-hostkey-step'), null, 'the step is closed')
  assert.equal(h.posts.length, 0, 'Cancel saves nothing')
  assert.ok(saveButton(), 'the form is still open')
})

test('ssh save step: Trust and save pins the key and posts the connection', async () => {
  const h = await mount(found)
  await fillNew(h)
  await click(saveButton())
  assert.equal(h.q('dialog-confirm').textContent, 'Trust and save')
  await click(h.q('dialog-confirm'))
  assert.equal(h.posts.length, 1)
  assert.deepEqual(h.posts[0].resources, [`deploy@host.example:2222#sha256=${PIN}`])
  assert.equal(h.posts[0].agent_id, '')
  assert.deepEqual(h.posts[0].caps, [])
  assert.equal(h.posts[0].token, 'deploy:secret')
})

test('ssh save step: a host that cannot be read shows the error, offers Retry and saves nothing', async () => {
  let fail = true
  const h = await mount(async () => {
    if (fail) throw new Error('the host could not be reached')
    return found()
  })
  await fillNew(h)
  await click(saveButton())
  assert.equal(h.q('ssh-step-error').textContent, 'The host could not be reached.')
  assert.equal(h.q('dialog-confirm').textContent, 'Retry')
  assert.equal(h.q('ssh-step-print'), null)
  assert.equal(h.posts.length, 0)
  fail = false
  await click(h.q('dialog-confirm'))
  assert.equal(h.scans.length, 2, 'Retry reads the key again')
  assert.match(h.q('ssh-step-print').textContent, /SHA256:/)
  assert.equal(h.q('dialog-confirm').textContent, 'Trust and save')
  assert.equal(h.posts.length, 0, 'nothing is saved until the person trusts the key')
})

test('ssh save step: a key trusted with the Check host key button goes straight through', async () => {
  const h = await mount(found)
  await fillNew(h)
  await click(h.q('ssh-hostkey-check'))
  assert.match(h.q('ssh-hostkey-print').textContent, /SHA256:/)
  await click(h.q('ssh-hostkey-trust').querySelector('input') || h.q('ssh-hostkey-trust'))
  assert.equal(h.q('ssh-hostkey-trust').checked, true, 'the trust box is ticked')
  const before = h.scans.length
  await click(saveButton())
  assert.equal(h.q('ssh-hostkey-step'), null, 'no step: the key is already trusted')
  assert.equal(h.scans.length, before)
  assert.equal(h.posts.length, 1)
  assert.deepEqual(h.posts[0].resources, [`deploy@host.example:2222#sha256=${PIN}`])
})
