// SPDX-License-Identifier: MIT
import test from 'node:test'
import assert from 'node:assert/strict'
import { JSDOM } from 'jsdom'
import React, { act } from 'react'
import { createServer } from 'vite'
import { createHostSettingsClient } from '../../src/utils/hostSettingsClient.js'
import { createAddonsApi } from '../../src/utils/addonsApi.js'
import { useHostSettings } from '../../src/hooks/useHostSettings.js'
import { checkHost } from '../../src/utils/egressHosts.js'
import { runChecks } from '../../src/utils/settingsPending.js'
import { FIELD_BY_KEY } from '../../src/utils/settingsSchema.js'
const dom = new JSDOM('<div id="root"></div>', { url: 'http://localhost/' })
globalThis.window = dom.window
globalThis.document = dom.window.document
globalThis.IS_REACT_ACT_ENVIRONMENT = true
// React DOM detects input event support at import time, after JSDOM exists.
const { createRoot } = await import('react-dom/client')
const dto = (revision = 1, policy = { chats_egress_allowed_hosts: '' }) => ({ schema_version: 1, revision, policy })
const reply = data => new Response(JSON.stringify({ payload: { data } }), { status: 200 })

test('owner client dispatches exact immutable CAS payload and same identity on retry', async () => {
  const calls = []
  const client = createHostSettingsClient({ api: createAddonsApi({ fetch: async (url, opts) => { calls.push({ url, body: JSON.parse(opts.body) }); return reply(dto()) } }) })
  await client.read()
  const patch = { chats_egress_allowed_hosts: '*.example.com' }
  const write = client.prepare(patch, 7)
  patch.chats_egress_allowed_hosts = '*'
  await write.update(); await write.update()
  assert.equal(calls[0].url, '/api/addons/dispatch')
  assert.deepEqual(calls[0].body.arguments, {})
  assert.deepEqual(calls[1].body, { operationId: write.operationId, addonId: '', generation: 0, action: 'host-settings-update', arguments: { expectedRevision: 7, patch: { chats_egress_allowed_hosts: '*.example.com' } } })
  assert.deepEqual(calls[1], calls[2])
})

test('wildcard validator accepts whole-label patterns and rejects malformed or registry globs', () => {
  for (const [raw, entry] of [['*', '*'], ['*.ExAmple.com.:443', '*.example.com:443'], ['*:8443', '*:8443'], ['*.bücher.example', '*.xn--bcher-kva.example']]) assert.deepEqual(checkHost(raw), { entry })
  for (const raw of ['foo*bar.com', '*.*.com', '*.127.0.0.1', '*.foo_bar.com', '*:22', '*:25', '*:65536', '*://x', '*.']) assert.ok(checkHost(raw).error, raw)
  const field = FIELD_BY_KEY.chats_egress_extra_registries
  assert.ok(runChecks([{ field, to: '*.example.com' }], {}, {}).some(c => c.level === 'error'))
})

async function mount(client) {
  let owner
  function Harness({ scope }) { owner = useHostSettings(client, scope); return React.createElement('div', null, React.createElement('input', { value: owner.draft?.chats_egress_allowed_hosts || '', onChange: e => owner.setDraft({ ...owner.draft, chats_egress_allowed_hosts: e.target.value }) }), React.createElement('button', { onClick: () => owner.save({ chats_egress_allowed_hosts: owner.draft.chats_egress_allowed_hosts }) }, 'Save'), React.createElement('output', null, JSON.stringify({ revision: owner.snapshot?.revision, error: owner.error?.kind, uncertain: owner.uncertain }))) }
  const root = createRoot(document.getElementById('root'))
  await act(async () => root.render(React.createElement(Harness, { scope: 'a' })))
  return { get owner() { return owner }, root, async scope(scope) { await act(async () => root.render(React.createElement(Harness, { scope }))) }, async close() { await act(async () => root.unmount()) } }
}

test('rendered hook keeps conflict edits for rebase, blocks outage fallback and ignores stale reads/unmount', async () => {
  let mode = 'ok', revision = 1, resolve
  const calls = []
  const client = createHostSettingsClient({ api: createAddonsApi({ fetch: async (url, opts) => {
    const body = JSON.parse(opts.body); calls.push({ url, body, signal: opts.signal })
    if (mode === 'defer') return new Promise(r => { resolve = () => r(reply(dto(99))) })
    if (body.action === 'host-settings-update') return new Response(JSON.stringify({ payload: { error: 'revision_conflict' } }))
    if (mode === 'outage') throw new Error('offline')
    return reply(dto(revision))
  } }) })
  const h = await mount(client)
  await act(async () => h.owner.setDraft({ chats_egress_allowed_hosts: '*.example.com' }))
  await act(async () => document.querySelector('button').click())
  assert.equal(h.owner.error.kind, 'revision_conflict')
  assert.equal(h.owner.uncertain, false)
  revision = 2
  await act(async () => h.owner.reload())
  assert.equal(h.owner.snapshot.revision, 2)
  assert.equal(h.owner.draft.chats_egress_allowed_hosts, '*.example.com')
  mode = 'outage'; await act(async () => h.owner.reload())
  assert.ok(h.owner.error); assert.equal(h.owner.snapshot.revision, 2)
  mode = 'defer'; let old
  await act(async () => { old = h.owner.reload() })
  mode = 'ok'; revision = 3; await h.scope('b')
  await act(async () => { resolve(); await old })
  assert.equal(h.owner.snapshot.revision, 3)
  mode = 'defer'; await act(async () => { old = h.owner.reload() })
  await h.close(); resolve(); await old
  assert.equal(calls.at(-1).signal.aborted, true)
  assert.ok(calls.every(c => c.url === '/api/addons/dispatch'))
})

test('owner client rejects malformed DTOs rather than inventing local settings', async () => {
  for (const value of [null, dto(0), dto(1, []), { ...dto(), schema_version: 2 }]) {
    const client = createHostSettingsClient({ api: { dispatch: async () => ({ data: value }) } })
    await assert.rejects(client.read(), /unavailable/i)
  }
})

// Load the real JSX form and network-policy block. Only routing, translation
// and the unrelated egress telemetry read are stubbed; owner reads/writes use
// the real client and dispatch serializer against a strict fetch boundary.
test('mounted HostControlsForm confirms private access locally and sends only the owner policy patch', async () => {
  const server = await createServer({
    configFile: false,
    ssr: { noExternal: ['react-i18next'] },
    server: { middlewareMode: true },
    plugins: [{
      name: 'host-settings-render-stubs',
      enforce: 'pre',
      resolveId(id) {
        if (id.endsWith('/UnsavedChangesGuard') || id === 'react-i18next' || id === '../../utils/api') return `\0host-settings-stub:${id}`
      },
      load(id) {
        if (!id.startsWith('\0host-settings-stub:')) return
        if (id.endsWith('/UnsavedChangesGuard')) return 'export default function Guard() { return null }'
        if (id.endsWith('react-i18next')) return 'export const useTranslation = () => ({ t: key => key })'
        return 'export const chatEgressApi = { admin: async () => ({ registries: [] }), events: async () => ({ events: [] }) }'
      },
    }],
  })
  let root
  try {
    const { default: HostControlsForm } = await server.ssrLoadModule('/src/components/hostcontrols/HostControlsForm.jsx')
    // Resolve the lazy block before mounting so input assertions do not race
    // module loading (the real registry still chooses the real component).
    await server.ssrLoadModule('/src/components/hostcontrols/NetworkPolicySettings.jsx')
    const calls = [], rejected = []
    let revision = 7, allowPrivate = false
    const client = createHostSettingsClient({ api: createAddonsApi({ fetch: async (url, opts) => {
      const body = JSON.parse(opts.body)
      calls.push({ url, body })
      assert.equal(url, '/api/addons/dispatch')
      if (body.action === 'host-settings-read') return reply(dto(revision, { chats_egress_allow_private: allowPrivate }))
      assert.equal(body.action, 'host-settings-update')
      const unknown = Object.keys(body.arguments.patch).filter(key => key !== 'chats_egress_allow_private')
      if (unknown.length) {
        rejected.push(...unknown)
        return new Response(JSON.stringify({ payload: { error: 'unknown_policy_field' } }))
      }
      revision = 8; allowPrivate = true
      return reply(dto(revision, { chats_egress_allow_private: allowPrivate }))
    } }) })
    root = createRoot(document.getElementById('root'))
    await act(async () => {
      root.render(React.createElement(HostControlsForm, { app: { id: 'sandbox', ui: { controls: ['network-policy'] } }, client }))
    })
    const toggle = document.querySelector('[data-testid="settings-egress-private-switch"]')
    assert.ok(toggle, 'real network-policy switch rendered')
    assert.equal(toggle.getAttribute('aria-checked'), 'false')
    await act(async () => toggle.click())
    const apply = () => [...document.querySelectorAll('button')].find(b => b.textContent === 'Apply')
    assert.equal(apply().disabled, true, 'typed confirmation remains mandatory')
    const phrase = document.querySelector('[data-testid="settings-egress-private-phrase"]')
    const { EGRESS_PRIVATE_PHRASE } = await import('../../src/utils/settingsPending.js')
    await act(async () => {
      Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set.call(phrase, EGRESS_PRIVATE_PHRASE)
      phrase.dispatchEvent(new window.Event('input', { bubbles: true }))
    })
    assert.equal(apply().disabled, false, 'correct phrase enables Apply')
    await act(async () => apply().click())
    assert.deepEqual(rejected, [], 'strict owner rejected unknown policy fields')
    assert.deepEqual(calls.map(c => c.body.action), ['host-settings-read', 'host-settings-update', 'host-settings-read'])
    assert.deepEqual(calls[1].body.arguments, { expectedRevision: 7, patch: { chats_egress_allow_private: true } })
    assert.ok(calls.every(c => c.url !== '/api/settings'))
    assert.match(document.body.textContent, /revision 8/)
    assert.equal(document.querySelector('[data-testid="settings-pending"]'), null)
  } finally {
    if (root) await act(async () => root.unmount())
    await server.close()
  }
})
