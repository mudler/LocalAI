// SPDX-License-Identifier: MIT
import test from 'node:test'
import assert from 'node:assert/strict'
import { JSDOM } from 'jsdom'
import React, { act } from 'react'
import { createServer } from 'vite'
import { readFileSync } from 'node:fs'
import { MemoryRouter } from 'react-router-dom'
import { createHostSettingsClient } from '../../src/utils/hostSettingsClient.js'
import { createAddonsApi } from '../../src/utils/addonsApi.js'
import { useHostSettings } from '../../src/hooks/useHostSettings.js'
import { checkHost } from '../../src/utils/egressHosts.js'
import { runChecks } from '../../src/utils/settingsPending.js'
import { FIELD_BY_KEY } from '../../src/utils/settingsSchema.js'
const adminEnglish = JSON.parse(readFileSync(new URL('../../public/locales/en/admin.json', import.meta.url), 'utf8'))
const dom = new JSDOM('<div id="root"></div>', { url: 'http://localhost/' })
globalThis.window = dom.window
globalThis.document = dom.window.document
globalThis.IS_REACT_ACT_ENVIRONMENT = true
// React DOM detects input event support at import time, after JSDOM exists.
const { createRoot } = await import('react-dom/client')
// All nine fields are emitted by Go Policy.MarshalJSON.
const defaults = { chats_egress_ceiling: '', chats_egress_default: '', chats_egress_allow_private: false, chats_egress_ports: '', chats_egress_extra_registries: '', chats_egress_allowed_hosts: '', chats_tool_providers: {}, auto_upgrade_addons: false, connector_files_ceiling_bytes: 0 }
const dto = (revision = 1, policy = {}) => ({ schema_version: 1, revision, policy: { ...defaults, ...policy } })
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
  for (const value of [null, dto(0), { ...dto(), policy: [] }, { ...dto(), schema_version: 2 }]) {
    const client = createHostSettingsClient({ api: { dispatch: async () => ({ data: value }) } })
    await assert.rejects(client.read(), /unavailable/i)
  }
})

test('owner client rejects partial, mistyped, unknown and oversized owner policies on reads and writes', async () => {
  const malformed = [
    { ...dto(), policy: {} }, { ...dto(), policy: { chats_egress_allowed_hosts: '' } },
    ...Object.keys(defaults).map(key => { const value = dto(); delete value.policy[key]; return value }),
    ...Object.keys(defaults).map(key => dto(1, { [key]: null })),
    dto(1, { chats_egress_allow_private: 'false' }), dto(1, { auto_upgrade_addons: 0 }),
    dto(1, { connector_files_ceiling_bytes: '5' }), dto(1, { connector_files_ceiling_bytes: -1 }), dto(1, { connector_files_ceiling_bytes: 1.5 }), dto(1, { connector_files_ceiling_bytes: 2 ** 40 + 1 }),
    dto(1, { chats_egress_ports: 443 }), dto(1, { chats_egress_ceiling: 'future' }),
    dto(1, { chats_egress_allowed_hosts: 'é'.repeat(4097) }),
    dto(1, { chats_tool_providers: new Date() }), dto(1, { future_owner_guard: false }),
    dto(1, { chats_tool_providers: [] }), dto(1, { chats_tool_providers: { tool: false } }),
    dto(1, { chats_tool_providers: { ['x'.repeat(129)]: 'addon' } }),
    dto(1, { chats_tool_providers: { tool: 'x'.repeat(129) } }),
    dto(1, { chats_tool_providers: Object.fromEntries(Array.from({ length: 129 }, (_, i) => [`tool${i}`, 'addon'])) }),
    ...['chats_egress_ceiling', 'chats_egress_default', 'chats_egress_ports', 'chats_egress_extra_registries', 'chats_egress_allowed_hosts'].map(key => dto(1, { [key]: 'x'.repeat(8193) })),
    { ...dto(), future: true }, dto(Number.MAX_SAFE_INTEGER + 1),
  ]
  for (const value of malformed) {
    const client = createHostSettingsClient({ api: { dispatch: async () => ({ data: value }) } })
    await assert.rejects(client.read(), /unavailable/i)
    await assert.rejects(client.prepare({}, 1).update(), /unavailable/i)
  }
  for (const envelope of [{ data: dto(), error: 'invalid' }, { data: dto(), future: true }, { error: 'future_error' }]) {
    const client = createHostSettingsClient({ api: { dispatch: async () => envelope } })
    await assert.rejects(client.read(), /unavailable/i)
  }
  const client = createHostSettingsClient({ api: { dispatch: async () => ({ data: dto() }) } })
  assert.deepEqual(await client.read(), dto(), 'actual Go empty/default representation is accepted unchanged')
})

test('discard preserves an uncertain write identity until a successful retry and owner read', async () => {
  let fail = true
  const operations = []
  const client = createHostSettingsClient({ api: { dispatch: async request => {
    if (request.action === 'host-settings-update') {
      operations.push(structuredClone(request))
      if (fail) throw new Error('offline')
    }
    return { data: dto(fail ? 1 : 2) }
  } } })
  const h = await mount(client)
  try {
    await act(async () => h.owner.setDraft({ ...h.owner.draft, chats_egress_allowed_hosts: '*.example.com' }))
    await act(async () => h.owner.save({ chats_egress_allowed_hosts: '*.example.com' }))
    await act(async () => h.owner.discard())
    assert.equal(h.owner.uncertain, true)
    assert.ok(h.owner.error)
    assert.deepEqual(h.owner.draft, defaults)
    await act(async () => h.owner.reload())
    assert.equal(h.owner.uncertain, true, 'a read alone cannot resolve the pending operation')
    assert.ok(h.owner.error)
    fail = false
    await act(async () => h.owner.save({}))
    assert.deepEqual(operations[1], operations[0], 'discard must not replace the uncertain operation with a new write')
    assert.equal(h.owner.uncertain, false)
    assert.equal(h.owner.error, null)
    assert.equal(h.owner.snapshot.revision, 2)
  } finally { await h.close() }
})

test('malformed reload never publishes a partial snapshot and successful read reconciles availability', async () => {
  let value = dto()
  const h = await mount(createHostSettingsClient({ api: { dispatch: async () => ({ data: value }) } }))
  try {
    value = { ...dto(2), policy: { chats_egress_allow_private: 'false' } }
    await act(async () => h.owner.reload())
    assert.equal(h.owner.snapshot.revision, 1)
    assert.deepEqual(h.owner.snapshot.policy, defaults)
    await act(async () => h.owner.discard())
    assert.ok(h.owner.error)
    value = dto(3)
    await act(async () => h.owner.reload())
    assert.equal(h.owner.error, null)
    assert.equal(h.owner.snapshot.revision, 3)
  } finally { await h.close() }
})

// Load the real JSX form and network-policy block. Only routing, translation
// and the unrelated egress telemetry read are stubbed; owner reads/writes use
// the real client and dispatch serializer against a strict fetch boundary.
test('mounted HostControlsForm asks for the private access phrase and sends it to the owner with the policy patch', async () => {
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
    let revision = 7, allowPrivate = false, offline = false
    const client = createHostSettingsClient({ api: createAddonsApi({ fetch: async (url, opts) => {
      const body = JSON.parse(opts.body)
      calls.push({ url, body })
      assert.equal(url, '/api/addons/dispatch')
      if (offline) {
        if (body.action === 'host-settings-update') return new Response(JSON.stringify({ payload: { error: 'invalid' } }))
        throw new Error('offline')
      }
      if (body.action === 'host-settings-read') return reply(dto(revision, { chats_egress_allow_private: allowPrivate }))
      assert.equal(body.action, 'host-settings-update')
      const unknown = Object.keys(body.arguments.patch).filter(key => !['chats_egress_allow_private', 'chats_egress_private_confirm'].includes(key))
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
    // The owner checks the phrase too, so a direct call cannot skip it.
    assert.deepEqual(calls[1].body.arguments, { expectedRevision: 7, patch: { chats_egress_allow_private: true, chats_egress_private_confirm: EGRESS_PRIVATE_PHRASE } })
    assert.ok(calls.every(c => c.url !== '/api/settings'))
    assert.match(document.body.textContent, /revision 8/)
    assert.equal(document.querySelector('[data-testid="settings-pending"]'), null)
    await act(async () => document.querySelector('[data-testid="settings-egress-private-switch"]').click())
    offline = true
    const button = text => [...document.querySelectorAll('button')].find(b => b.textContent === text)
    // Trigger a failed write, then the real reload action, before discarding.
    await act(async () => apply().click())
    await act(async () => button('Reload and rebase edits').click())
    await act(async () => button('Discard').click())
    assert.ok(document.querySelector('[data-testid="addon-host-controls-error"]'), 'discard cannot clear owner unavailability')
    await act(async () => document.querySelector('[data-testid="settings-egress-private-switch"]').click())
    assert.equal(apply().disabled, true, 'failed reload -> discard -> edit must not reopen Save')
  } finally {
    if (root) await act(async () => root.unmount())
    await server.close()
  }
})

async function providerRender() {
  const server = await createServer({ configFile: false, server: { middlewareMode: true }, ssr: { noExternal: ['react-i18next'] }, plugins: [{
    name: 'provider-render-stubs', enforce: 'pre',
    resolveId(id) { if (id.endsWith('/UnsavedChangesGuard') || id === 'react-i18next' || id.endsWith('/utils/api')) return `\0provider-stub:${id}` },
    load(id) {
      if (!id.startsWith('\0provider-stub:')) return
      if (id.endsWith('/UnsavedChangesGuard')) return 'export default function Guard() { return null }'
      // The English words of the admin namespace for the add-on update switch; every other key stays its own name.
      if (id.endsWith('react-i18next')) return `const en = ${JSON.stringify(adminEnglish)}; const find = key => key.split('.').reduce((o, k) => (o && typeof o === 'object' ? o[k] : undefined), en); const fill = (text, opts) => Object.keys(opts || {}).reduce((out, name) => out.split('{{' + name + '}}').join(String(opts[name])), text); export const useTranslation = () => ({ t: (key, opts) => { const text = key.startsWith('settings.') ? find(key) : undefined; return typeof text === 'string' ? fill(text, opts) : key }, i18n: { language: "en" } })`
      return 'export const chatBuiltinToolsApi = { list: () => globalThis.providerCatalog() }; export const chatConnectorsApi = { kinds: () => globalThis.connectorKinds() }; export const settingsApi = { get: () => fetch("/api/settings"), save: () => fetch("/api/settings") }'
    },
  }] })
  const { default: Form } = await server.ssrLoadModule('/src/components/hostcontrols/HostControlsForm.jsx')
  await server.ssrLoadModule('/src/components/hostcontrols/ToolProvidersSettings.jsx')
  const { default: Skills } = await server.ssrLoadModule('/src/components/addons/AddonSkills.jsx')
  const { default: Upgrades } = await server.ssrLoadModule('/src/components/settings/AddonUpgradeSettings.jsx')
  const { default: ConnectorFiles } = await server.ssrLoadModule('/src/components/settings/ConnectorFilesSettings.jsx')
  const root = createRoot(document.getElementById('root'))
  return { Form, Skills, Upgrades, ConnectorFiles, async render(Component, props) { await act(async () => root.render(React.createElement(MemoryRouter, null, React.createElement(Component, props)))) }, async close() { await act(async () => root.unmount()); await server.close() } }
}
const buttonNamed = text => [...document.querySelectorAll('button')].find(b => b.textContent === text)
const skillSwitch = name => document.querySelector(`[data-testid="app-skill-${name}-switch"]`)
function providerWire() {
  let revision = 7, pins = { bash: 'sandbox', web_search: 'web', xlsx: 'office', pdf: 'other' }, mode = 'ok'
  const calls = [], forbidden = []
  const previous = globalThis.fetch
  globalThis.fetch = async (url, opts) => {
    if (url !== '/api/addons/dispatch') { forbidden.push(url); throw new Error(`Forbidden legacy route ${url}`) }
    const body = JSON.parse(opts.body); calls.push(body)
    if (mode === 'offline') throw new Error('offline')
    if (body.action === 'host-settings-update') {
      if (mode === 'conflict' || body.arguments.expectedRevision !== revision) return new Response(JSON.stringify({ payload: { error: 'revision_conflict' } }))
      assert.deepEqual(Object.keys(body.arguments.patch), ['chats_tool_providers'])
      for (const [key, value] of Object.entries(body.arguments.patch.chats_tool_providers)) { if (value === null) delete pins[key]; else pins[key] = value }
      revision++
    }
    return reply(dto(revision, { chats_tool_providers: pins }))
  }
  globalThis.providerCatalog = async () => ({ tools: ['bash', 'web_search'].map(id => ({ id, provider: 'legacy-wrong', candidates: id === 'bash' ? ['sandbox', 'other'] : ['web', 'other'], default: 'other' })), skills: ['xlsx', 'pdf'].map(id => ({ id, provider: 'legacy-wrong', candidates: ['office', 'other'], default: 'other' })) })
  return { calls, forbidden, get pins() { return pins }, set mode(v) { mode = v }, concurrent() { pins.web_fetch = 'remote'; revision++ }, close() { globalThis.fetch = previous; delete globalThis.providerCatalog } }
}
const officeApp = { id: 'office', skills: ['xlsx', 'pdf'].map(name => ({ name, shadows: name })) }

test('mounted skill switches use owner pins, preserve other pins, and read fresh across add-ons', async () => {
  const wire = providerWire(), h = await providerRender()
  try {
    await h.render(h.Skills, { app: officeApp, isAdmin: true })
    assert.equal(skillSwitch('xlsx').getAttribute('aria-checked'), 'true', 'owner pin overrides conflicting legacy catalog')
    await act(async () => skillSwitch('xlsx').click())
    await act(async () => skillSwitch('pdf').click())
    assert.deepEqual(wire.calls.filter(c => c.action === 'host-settings-update').map(c => c.arguments), [
      { expectedRevision: 7, patch: { chats_tool_providers: { xlsx: null } } },
      { expectedRevision: 8, patch: { chats_tool_providers: { pdf: 'office' } } },
    ])
    assert.deepEqual(wire.pins, { bash: 'sandbox', web_search: 'web', pdf: 'office' })
    await h.render(h.Skills, { app: { ...officeApp, id: 'other' }, isAdmin: true })
    assert.equal(skillSwitch('pdf').getAttribute('aria-checked'), 'false')
    assert.match(document.body.textContent, /revision 9/)
    assert.deepEqual(wire.forbidden, [])
  } finally { await h.close(); wire.close() }
})

test('mounted provider form sends per-key clear and never submits native runtime settings', async () => {
  const wire = providerWire(), h = await providerRender()
  try {
    await h.render(h.Form, { app: { id: 'sandbox', shadows: ['bash'], ui: { controls: ['tool-providers'] } } })
    assert.equal(document.querySelector('input[value="sandbox"]').checked, true)
    assert.equal(document.querySelector('[data-testid="settings-tools-bash-switch"]'), null, 'native execution mode is not owner policy')
    await act(async () => buttonNamed('Use default provider').click())
    await act(async () => buttonNamed('Apply').click())
    assert.deepEqual(wire.calls.find(c => c.action === 'host-settings-update').arguments, { expectedRevision: 7, patch: { chats_tool_providers: { bash: null } } })
    assert.deepEqual(wire.pins, { web_search: 'web', xlsx: 'office', pdf: 'other' })
    assert.deepEqual(wire.forbidden, [])
  } finally { await h.close(); wire.close() }
})

test('mounted skills retain conflict edits, require visible rebase and retry uncertain identity', async () => {
  const wire = providerWire(), h = await providerRender()
  try {
    await h.render(h.Skills, { app: officeApp, isAdmin: true })
    wire.concurrent(); wire.mode = 'conflict'
    await act(async () => skillSwitch('pdf').click())
    assert.match(document.body.textContent, /changed|conflict/i)
    assert.equal(skillSwitch('pdf').disabled, true)
    wire.mode = 'ok'
    await act(async () => buttonNamed('Reload and rebase edits').click())
    assert.equal(skillSwitch('pdf').getAttribute('aria-checked'), 'true', 'intended choice survives conflict')
    await act(async () => buttonNamed('Apply').click())
    assert.equal(wire.pins.web_fetch, 'remote')
    wire.mode = 'offline'
    await act(async () => skillSwitch('xlsx').click())
    assert.equal(skillSwitch('pdf').disabled, true)
    const failed = wire.calls.filter(c => c.action === 'host-settings-update').at(-1)
    await act(async () => buttonNamed('Reload and rebase edits').click())
    assert.match(document.body.textContent, /unavailable|uncertain/i)
    wire.mode = 'ok'
    await act(async () => buttonNamed('Retry same operation').click())
    assert.deepEqual(wire.calls.filter(c => c.action === 'host-settings-update').at(-1), failed)
    assert.deepEqual(wire.forbidden, [])
  } finally { await h.close(); wire.close() }
})

 test('mounted upgrade control writes owner CAS policy and blocks on outage', async () => {
  const previous = globalThis.fetch, calls = []
  let revision = 7, enabled = false, offline = false
  globalThis.fetch = async (url, opts) => {
    assert.equal(url, '/api/addons/dispatch')
    const body = JSON.parse(opts.body); calls.push(body)
    if (offline) throw new Error('offline')
    if (body.action === 'host-settings-update') {
      assert.deepEqual(body.arguments, { expectedRevision: 7, patch: { auto_upgrade_addons: true } })
      enabled = true; revision++
    }
    return reply(dto(revision, { auto_upgrade_addons: enabled }))
  }
  const h = await providerRender()
  try {
    await h.render(h.Upgrades, {})
    const box = () => document.querySelector('input[type="checkbox"]')
    await act(async () => box().click())
    await act(async () => buttonNamed('Apply update setting').click())
    assert.equal(enabled, true)
    offline = true
    await act(async () => box().click())
    await act(async () => buttonNamed('Apply update setting').click())
    assert.equal(buttonNamed('Apply update setting').disabled, true)
    assert.match(document.body.textContent, /unavailable|changed/i)
    assert.equal(calls.filter(c => c.action === 'host-settings-update').length, 2)
  } finally { await h.close(); globalThis.fetch = previous }
 })

test('mounted connector file ceiling states the size in effect, validates, and writes only its own owner key', async () => {
  const previous = globalThis.fetch, calls = []
  let revision = 3, ceiling = 0
  globalThis.fetch = async (url, opts) => {
    assert.equal(url, '/api/addons/dispatch')
    const body = JSON.parse(opts.body); calls.push(body)
    if (body.action === 'host-settings-update') {
      assert.deepEqual(body.arguments, { expectedRevision: 3, patch: { connector_files_ceiling_bytes: 5 * 1024 * 1024 } })
      ceiling = body.arguments.patch.connector_files_ceiling_bytes; revision++
    }
    return reply(dto(revision, { connector_files_ceiling_bytes: ceiling }))
  }
  globalThis.connectorKinds = async () => ({ kinds: [{ kind: 'x', files: { host_ceiling_bytes: ceiling || 100 * 1024 * 1024 } }] })
  const h = await providerRender()
  try {
    await h.render(h.ConnectorFiles, {})
    const input = () => document.querySelector('[data-testid="connector-files-ceiling"]')
    const setValue = async value => {
      const setter = Object.getOwnPropertyDescriptor(globalThis.window.HTMLInputElement.prototype, 'value').set
      await act(async () => { setter.call(input(), value); input().dispatchEvent(new globalThis.window.Event('input', { bubbles: true })) })
    }
    assert.match(document.body.textContent, /Now: 100 MiB\./)
    assert.equal(buttonNamed('Apply file ceiling').disabled, true, 'nothing changed')
    await setValue('0.0001')
    assert.match(document.body.textContent, /Use a size from 1 KiB to 1 TiB/)
    assert.equal(buttonNamed('Apply file ceiling').disabled, true)
    await setValue('5')
    await act(async () => buttonNamed('Apply file ceiling').click())
    assert.equal(ceiling, 5 * 1024 * 1024)
    assert.match(document.body.textContent, /Now: 5 MiB\./)
    assert.equal(input().value, '5')
  } finally { await h.close(); globalThis.fetch = previous; delete globalThis.connectorKinds }
})
