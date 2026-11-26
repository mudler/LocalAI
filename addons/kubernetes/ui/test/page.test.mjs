// Drives the real Kubernetes page, built as the host loads it, in a DOM: a
// person pastes a kubeconfig and saves, the page runs the check, an
// administrator edits the policy, and the approval card draws a preview. The
// core routes and the host are stand-ins; the page code and the shared
// connector page are the real ones.
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

const dom = new JSDOM('<!doctype html><html lang="en"><body><div id="root"></div></body></html>', { url: 'http://localhost/', pretendToBeVisual: true })
for (const k of ['window', 'document', 'navigator', 'HTMLElement', 'HTMLInputElement', 'HTMLTextAreaElement', 'HTMLSelectElement', 'Node', 'Event', 'MouseEvent', 'KeyboardEvent', 'MutationObserver', 'getComputedStyle', 'requestAnimationFrame', 'cancelAnimationFrame']) {
  if (!(k in globalThis) || k === 'navigator') Object.defineProperty(globalThis, k, { value: dom.window[k], configurable: true, writable: true })
}
globalThis.IS_REACT_ACT_ENVIRONMENT = true
const { createRoot } = require('react-dom/client')
const { act } = React

const KEY = 'https://cluster.example:6443#context=team-a'
// The policy is keyed by the server of the cluster.
const SERVER = 'https://cluster.example:6443'
const KUBECONFIG = `apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCg==
    server: https://cluster.example:6443
  name: team-a
contexts:
- context:
    cluster: team-a
    user: team-a-bot
  name: team-a
current-context: team-a
kind: Config
users:
- name: team-a-bot
  user:
    token: abc.def.ghi
`

const json = (body, status = 200) => ({ ok: status < 400, status, json: async () => body })
const kinds = { kinds: [{ kind: 'kubernetes', addon_id: 'kubernetes', tool_only: true, agent_optional: true, title: 'Kubernetes' }], capabilities: [] }
const WRITE_TOOLS = ['scale', 'rollout-restart', 'rollout-undo', 'delete-pod', 'cordon', 'uncordon', 'drain', 'apply', 'patch', 'exec']
// The built-in default: reads, Secret values included, no write.
const DEFAULTS = { namespaces: ['*'], writeTools: [], readSecretValues: true }
// The policy of every cluster while the policy file is damaged.
const DAMAGED = { namespaces: [], writeTools: [], readSecretValues: false }

function ConfirmDialog({ open, title, onConfirm }) {
  if (!open) return null
  return React.createElement('div', { role: 'alertdialog' }, title, React.createElement('button', { type: 'button', onClick: onConfirm }, 'OK'))
}

async function mount({ admin = true, existing = false, broken = false, invalid = [] } = {}) {
  const posts = existing ? [{ name: 'Team A', resources: [KEY], write_mode: 'propose' }] : []
  const reads = []
  // The add-on's policies: a save changes them, as the add-on does.
  const pol = { clusters: {}, default: broken ? DAMAGED : DEFAULTS, defaultSet: false, broken, legacy: [], invalid }
  const settings = []
  const dir = await build()
  const mod = await import(pathToFileURL(join(dir, 'ui/kubernetes.js')).href + `?t=${Date.now()}`)
  const { createHostSDK } = await import(pathToFileURL(join(hostUi, 'src/addons/sdk.js')).href)
  const saved = () => (posts.length ? [{ id: 'c1', kind: 'kubernetes', name: posts[0].name, resources: posts[0].resources, write_mode: posts[0].write_mode, credentials_only: true }] : [])
  const sdk = createHostSDK({
    addonId: 'kubernetes', generation: 1, signal: new AbortController().signal, pages: ['kubernetes'],
    host: {
      React, components: {
        ConfirmDialog,
        PaneLayout: (p) => React.createElement('div', null, p.children),
        SidePane: (p) => React.createElement('div', { 'data-testid': 'pane' }, p.children),
        ActionMenu: () => null,
        StatusChip: (p) => React.createElement('span', null, p.children),
        LoadingSpinner: () => null,
        SideSheet: (p) => React.createElement('div', { 'data-testid': p.testId }, p.children, p.footer),
      }, hooks: {}, viewer: { isAdmin: () => admin, hasFeature: () => true }, basePath: '',
      fetch: async (url, init = {}) => {
        const path = String(url).replace(/^https?:\/\/[^/]+/, '')
        const method = init.method || 'GET'
        if (method === 'POST' && path === '/api/chats/connectors') {
          const body = JSON.parse(init.body)
          posts.push(body)
          return json({ id: 'c1', kind: 'kubernetes', name: body.name, resources: body.resources, write_mode: body.write_mode, credentials_only: true, caps: [], grants: [] }, 201)
        }
        if (method === 'POST' && path.endsWith('/test')) return json({})
        if (path.startsWith('/api/chats/connectors/kinds')) return json(kinds)
        if (path === '/api/chats/connectors') return json({ connectors: saved() })
        if (path === '/api/chats/agents') return json({ agents: [] })
        return json({})
      },
      Icon: ({ name }) => React.createElement('i', { 'data-icon': name }), theme: { mode: () => 'light', subscribe: () => () => {} },
      language: () => 'en', subscribeLanguage: () => () => {},
      api: {
        dispatch: async (body) => {
          const a = body.arguments || {}
          reads.push(a)
          if (a.tool === 'check') return { data: { cluster: KEY, server: 'https://cluster.example:6443', context: 'team-a', check: { version: 'v1.31.2', namespace: 'default', rules: [{ verbs: ['get', 'list'], apiGroups: ['apps'], resources: ['deployments'] }], incomplete: false } } }
          if (a.tool === 'get-policy') return { data: { ...pol, builtinDefault: DEFAULTS, writeTools: WRITE_TOOLS, admin } }
          if (a.tool === 'set-policy') {
            const policy = { namespaces: a.namespaces, writeTools: a.writeTools, readSecretValues: a.readSecretValues }
            if (pol.broken) Object.assign(pol, { broken: false, default: DAMAGED, defaultSet: true })
            if (a.default) Object.assign(pol, { default: policy, defaultSet: true })
            else pol.clusters = { ...pol.clusters, [a.key]: policy }
            pol.invalid = []
            return { data: { saved: true, key: a.default ? '' : a.key, default: a.default === true, policy } }
          }
          return { data: {} }
        },
        userSettings: async (body) => {
          if (body.op === 'set') settings.push([body.key, body.value])
          return { settings: ['scale', 'rollout-restart', 'rollout-undo', 'delete-pod', 'cordon', 'uncordon', 'drain', 'apply', 'patch', 'exec'].map((t) => ({ key: `approval-${t}`, values: ['ask', 'skip'], default: 'ask', value: settings.some(([k, v]) => k === `approval-${t}` && v === 'skip') ? 'skip' : 'ask' })) }
        },
        propose: async () => ({}), resource: async () => ({}),
      },
    },
    bindings: { current: () => ({ page: 'kubernetes', path: '', search: '' }), openLayer: () => ({ close() {} }), notify() {} },
  })
  const addon = await mod.createAddon(sdk)
  document.body.innerHTML = '<div id="root"></div>'
  const root = createRoot(document.getElementById('root'))
  await act(async () => { root.render(React.createElement(addon.Component, { page: 'kubernetes', path: '' })) })
  await settle()
  return { addon, posts, reads, settings, root, q: (id) => document.querySelector(`[data-testid="${id}"]`) }
}

const settle = () => act(async () => { await new Promise((r) => setTimeout(r, 60)) })
const click = async (el) => { await act(async () => { el.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, cancelable: true })) }); await settle() }
const type = async (el, value) => {
  const proto = el instanceof dom.window.HTMLTextAreaElement ? dom.window.HTMLTextAreaElement.prototype : dom.window.HTMLInputElement.prototype
  await act(async () => {
    Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, value)
    el.dispatchEvent(new dom.window.Event('input', { bubbles: true }))
  })
  await settle()
}
const saveButton = () => [...document.querySelectorAll('button[type="submit"]')].filter((b) => !b.closest('[data-testid="kubernetes-policy"]')).pop()

test('kubernetes page: a pasted kubeconfig is saved with its cluster key, asks first, and the check shows the RBAC rules', async () => {
  const h = await mount()
  await click(h.q('connector-new'))
  await type(h.q('kubernetes-kubeconfig'), KUBECONFIG)
  assert.equal(h.q('kubernetes-context').value, 'team-a')
  assert.match(h.q('kubernetes-server-found').textContent, /https:\/\/cluster\.example:6443/)
  assert.match(h.q('kubernetes-bytes').textContent, /of 16384 bytes/)
  assert.equal(saveButton().disabled, false)
  await click(saveButton())
  await settle()
  assert.equal(h.posts.length, 1)
  assert.deepEqual(h.posts[0].resources, [KEY])
  assert.equal(h.posts[0].write_mode, 'propose')
  assert.equal(h.posts[0].token, KUBECONFIG.trim(), 'the page sends the kubeconfig as pasted, trimmed')
  assert.equal(h.posts[0].name, 'Kubernetes: team-a')
  assert.ok(h.reads.some((a) => a.tool === 'check' && a.cluster === KEY), 'the check runs after Save')
  if (!h.q('kubernetes-rbac')) await click(h.q('connector-open'))
  assert.match(h.q('kubernetes-rbac').textContent, /RBAC in default/)
  assert.match(h.q('kubernetes-rbac').textContent, /deployments\.apps/)
  assert.match(h.q('kubernetes-checks').textContent, /Kubernetes v1\.31\.2/)
  // The policy editor now lists the server of the new cluster.
  assert.equal(h.q('kubernetes-policy-cluster').value, SERVER)
})

test('kubernetes page: a kubeconfig with a credential plugin cannot be saved', async () => {
  const h = await mount()
  await click(h.q('connector-new'))
  await type(h.q('kubernetes-kubeconfig'), KUBECONFIG.replace('    token: abc.def.ghi\n', '    exec:\n      command: aws\n'))
  assert.match(document.body.textContent, /credential plugin/)
  assert.equal(saveButton().disabled, true)
})

test('kubernetes page: an administrator saves the policy of a cluster; exec is marked powerful', async () => {
  const h = await mount({ existing: true })
  assert.equal(h.q('kubernetes-policy-cluster').value, SERVER)
  assert.match(h.q('kubernetes-policy-own').textContent, /Not configured: writes are off/)
  assert.match(h.q('kubernetes-policy-cluster').textContent, /https:\/\/cluster\.example:6443 \(team-a\)/)
  assert.ok(h.q('kubernetes-policy-powerful-exec'))
  const exec = h.q('kubernetes-policy-tool-exec')
  assert.equal(exec.checked, false, 'exec is off by default')
  assert.equal(h.q('kubernetes-policy-save').disabled, true, 'nothing to save yet')
  await click(exec)
  await type(h.q('kubernetes-policy-ns-input'), 'team-a')
  await click(h.q('kubernetes-policy-ns-add'))
  await click(h.q('kubernetes-policy-save'))
  const set = h.reads.find((a) => a.tool === 'set-policy')
  assert.deepEqual(set, { tool: 'set-policy', key: SERVER, namespaces: ['team-a'], writeTools: ['exec'], readSecretValues: true })
  assert.ok(h.q('kubernetes-policy-saved'))
  assert.equal(h.q('kubernetes-policy-own').textContent, 'This cluster has its own policy.')
})

test('kubernetes page: an administrator sets the default policy of the clusters with none', async () => {
  const h = await mount({ existing: true })
  await act(async () => {
    const sel = h.q('kubernetes-policy-cluster')
    const set = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(sel), 'value').set
    set.call(sel, 'default')
    sel.dispatchEvent(new window.Event('change', { bubbles: true }))
  })
  assert.match(h.q('kubernetes-policy-own').textContent, /built-in default/)
  await click(h.q('kubernetes-policy-tool-scale'))
  await click(h.q('kubernetes-policy-save'))
  const set = h.reads.find((a) => a.tool === 'set-policy')
  assert.deepEqual(set, { tool: 'set-policy', default: true, namespaces: ['*'], writeTools: ['scale'], readSecretValues: true })
  assert.match(h.q('kubernetes-policy-own').textContent, /An administrator set this default/)
})

test('kubernetes page: a save over a damaged policy file reads the policies again and says the other clusters must be entered again', async () => {
  const h = await mount({ existing: true, broken: true })
  assert.match(h.q('kubernetes-policy-broken').textContent, /enter the policy of every other cluster again/)
  const before = h.reads.filter((a) => a.tool === 'get-policy').length
  await type(h.q('kubernetes-policy-ns-input'), 'team-a')
  await click(h.q('kubernetes-policy-ns-add'))
  await click(h.q('kubernetes-policy-save'))
  assert.ok(h.reads.filter((a) => a.tool === 'get-policy').length > before, 'the page reads the policies again after a save')
  assert.equal(h.q('kubernetes-policy-broken'), null)
  assert.equal(h.q('kubernetes-policy-own').textContent, 'This cluster has its own policy.')
  const sel = h.q('kubernetes-policy-cluster')
  await act(async () => { sel.value = 'default'; sel.dispatchEvent(new dom.window.Event('change', { bubbles: true })) })
  await settle()
  assert.equal(document.querySelectorAll('[data-testid="kubernetes-policy-namespaces"] li').length, 0, 'the default is now the damaged policy, with no namespace')
})

test('kubernetes page: saved entries that never apply are listed, and a save removes them', async () => {
  const h = await mount({ existing: true, invalid: ['not a server'] })
  assert.match(h.q('kubernetes-policy-invalid').textContent, /not a server/)
  await type(h.q('kubernetes-policy-ns-input'), 'team-a')
  await click(h.q('kubernetes-policy-ns-add'))
  await click(h.q('kubernetes-policy-save'))
  assert.equal(h.q('kubernetes-policy-invalid'), null)
})

test('kubernetes page: someone who is not an administrator sees the policy read-only', async () => {
  const h = await mount({ existing: true, admin: false })
  assert.ok(h.q('kubernetes-policy-readonly'))
  assert.equal(h.q('kubernetes-policy-save').disabled, true)
  assert.equal(h.q('kubernetes-policy-ns-input').disabled, true)
})

test('kubernetes page: the approval choice is saved as the person\'s own setting', async () => {
  const h = await mount()
  const skip = h.q('kubernetes-approval-drain').querySelector('input[value="skip"], input[type="radio"]:not(:checked)')
  await click(skip)
  assert.deepEqual(h.settings, [['approval-drain', 'skip']])
  assert.ok(h.q('kubernetes-approval-warn-drain'))
})

test('kubernetes card: the exec argv and the apply diff are drawn as they are', async () => {
  const h = await mount()
  const Card = h.addon.components.ProposalCard
  const root = createRoot(document.getElementById('root'))
  await act(async () => {
    root.render(React.createElement(Card, { tool: 'exec', arguments: {}, effects: { sample: { tool: 'exec', cluster: KEY, kind: 'Pod', namespace: 'team-a', name: 'web-0', container: 'app', command: ['sh', '-c', 'echo "a  b"'], timeoutSeconds: 30 } } }))
  })
  const items = [...document.querySelectorAll('[data-testid="kubernetes-proposal-argv"] li')].map((li) => li.textContent)
  assert.deepEqual(items, ['sh', '-c', 'echo "a  b"'])
  await act(async () => {
    root.render(React.createElement(Card, { tool: 'apply', arguments: {}, effects: { sample: { tool: 'apply', cluster: KEY, kind: 'Deployment', namespace: 'team-a', name: 'web', change: 'update', diff: '--- live\n+++ dry-run\n-  replicas: 2\n+  replicas: 3\n', diffCut: true } } }))
  })
  assert.equal(document.querySelectorAll('.kube-diff__line--add').length, 1)
  assert.equal(document.querySelectorAll('.kube-diff__line--del').length, 1)
  assert.ok(document.querySelector('[data-testid="kubernetes-proposal-diffcut"]'))
})
