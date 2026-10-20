import test from 'node:test'
import assert from 'node:assert/strict'
import * as React from 'react'
import { renderToString } from 'react-dom/server'
import { createHostSDK } from '../../src/addons/sdk.js'
import { loadAddonUI, validateComponents } from '../../src/addons/loader.js'
import { createRuntimeManager } from '../../src/addons/runtime.js'
import { contributesUI, shouldPoll } from '../../src/addons/registryState.js'
import {
  SLOT_IDS, SLOTS, slotProps, declaredComponents, localizedLabel, slotContributions, rendererFor,
  resultText, proposalIdOf, actionArguments,
} from '../../src/addons/slots.js'
import { approvalState, confirmCall, cancelCall, rendererProps } from '../../src/addons/approval.js'

const DIGEST = 'ab'.repeat(32)
const asset = (id, p, d = DIGEST) => `/api/addons/${id}/assets/${d}/${p}`
const ORIGIN = 'https://host.example'

function ui(over = {}) {
  return {
    entry: asset('notes', 'ui.js'), css: [], sdk: { min: 1, max: 2 }, pages: ['home'], navigation: [], chatActions: ['summarize', 'inspect'],
    actions: [
      { id: 'summarize', label: 'Summarize', labels: { en: 'Summarize', it: 'Riassumi' }, icon: 'sparkles', placement: 'composer', tool: 'ping' },
      { id: 'inspect', label: 'Inspect', icon: 'eye', placement: 'message', component: 'InspectPanel' },
    ],
    slots: [
      { id: 'set', slot: 'settings.sections', component: 'SettingsBit' },
      { id: 'nav', slot: 'app.nav', component: 'NavBit' },
      { id: 'cx', slot: 'chat.composer.actions', component: 'ComposerBit' },
      { id: 'mx', slot: 'chat.message.actions', component: 'MessageBit' },
    ],
    renderers: [{ kind: 'approval-card', tool: 'ping', component: 'PingCard' }, { kind: 'tool-result', tool: 'ping', component: 'PingResult' }],
    ...over,
  }
}
const app = (over = {}) => ({ id: 'notes', title: 'Notes', titles: { en: 'Notes', it: 'Note' }, generation: 3, enabled: true, state: 'running', icon: '/api/addons/apps/notes/icon?generation=3', ui: ui(), tools: ['ping'], ...over })
const C = () => null
const instance = (over = {}) => ({
  id: 'notes', generation: 3, entry: asset('notes', 'ui.js'), status: 'ready',
  components: { SettingsBit: C, NavBit: C, ComposerBit: C, MessageBit: C, InspectPanel: C, PingCard: C, PingResult: C }, sdk: {}, ...over,
})

test('slots: the registry is closed and each slot has a frozen minimal props contract', () => {
  assert.deepEqual([...SLOT_IDS], ['chat.composer.actions', 'chat.message.actions', 'app.nav', 'settings.sections'])
  assert.equal(Object.isFrozen(SLOT_IDS), true)
  assert.equal(Object.isFrozen(SLOTS), true)
  assert.deepEqual([...SLOTS['chat.composer.actions'].props], ['conversationId', 'disabled'])
  assert.deepEqual([...SLOTS['chat.message.actions'].props], ['conversationId', 'messageId', 'role'])
  assert.deepEqual([...SLOTS['app.nav'].props], [])
  assert.deepEqual([...SLOTS['settings.sections'].props], [])
  assert.throws(() => { SLOTS['chat.footer'] = {} }, TypeError)
  assert.equal(slotProps('chat.footer', {}), null, 'an unknown slot has no props')
})

test('slots: props carry only the contract and never message text or history', () => {
  const p = slotProps('chat.message.actions', {
    conversationId: 'c1', messageId: '4', role: 'assistant',
    content: 'secret reply', history: [{ role: 'user', content: 'secret' }], text: 'x', messages: [], model: 'llama', token: 't',
  })
  assert.deepEqual(p, { conversationId: 'c1', messageId: '4', role: 'assistant' })
  assert.equal(Object.isFrozen(p), true)
  assert.deepEqual(slotProps('chat.composer.actions', { conversationId: 'c1', disabled: true, draft: 'hello' }), { conversationId: 'c1', disabled: true })
  assert.deepEqual(slotProps('app.nav', { anything: 1 }), {})
  assert.deepEqual(slotProps('settings.sections', { user: { id: 1 } }), {})
  // A value of the wrong shape is refused rather than coerced.
  assert.equal(slotProps('chat.message.actions', { conversationId: 'c1', messageId: '4', role: 'wizard' }), null)
  assert.equal(slotProps('chat.message.actions', { conversationId: '', messageId: '4', role: 'user' }), null)
  assert.equal(slotProps('chat.message.actions', { conversationId: 'c'.repeat(200), messageId: '4', role: 'user' }), null)
  // Every prop name of every slot is free of anything that could carry a message.
  for (const slot of SLOT_IDS) for (const name of SLOTS[slot].props) assert.doesNotMatch(name, /text|content|history|messages|prompt|draft/i)
})

test('slots: declared component names come from the manifest only and the module cannot add more', () => {
  const names = declaredComponents(ui())
  assert.deepEqual([...names].sort(), ['ComposerBit', 'InspectPanel', 'MessageBit', 'NavBit', 'PingCard', 'PingResult', 'SettingsBit'])
  const warned = []
  const got = validateComponents({
    SettingsBit: C, InspectPanel: () => 'x', PingCard: 'not a component', Sneaky: C, __proto__: { Other: C },
  }, names, (m) => warned.push(m))
  assert.deepEqual(Object.keys(got).sort(), ['InspectPanel', 'SettingsBit'])
  assert.equal(Object.isFrozen(got), true)
  assert.equal('Sneaky' in got, false)
  assert.ok(warned.some(w => /Sneaky/.test(w)), 'an undeclared component is logged')
  assert.ok(warned.some(w => /PingCard/.test(w)), 'a declared name that is not a component is logged')
  assert.deepEqual(validateComponents(undefined, names, () => {}), {})
  assert.throws(() => validateComponents([C], names, () => {}), /components must be an object/)
})

test('slots: the loader hands the host only declared components and never an imperative registration', async () => {
  const styles = []
  const doc = { head: { appendChild: (el) => styles.push(el) }, createElement: () => ({ attrs: {}, setAttribute() {}, remove() {} }) }
  const registered = []
  const host = {
    React, components: {}, Icon: () => null, theme: {}, language: () => 'en', subscribeLanguage: () => () => {},
    api: { dispatch: async () => ({}), propose: async () => ({}) },
  }
  const out = await loadAddonUI({
    app: app(),
    createSDK: ({ signal }) => createHostSDK({ addonId: 'notes', generation: 3, signal, pages: ['home'], host }),
    importer: async () => ({
      createAddon: (sdk) => {
        for (const k of ['registerSlot', 'register', 'contribute', 'addSlot']) registered.push(k in sdk)
        return {
          components: { SettingsBit: C, Injected: C }, slots: [{ slot: 'app.nav', component: C }],
          registerSlot() {}, dispose() {},
        }
      },
    }),
    fetchImpl: async () => ({ ok: true, headers: { get: () => 'text/css' }, text: async () => '' }), document: doc, origin: ORIGIN,
  })
  assert.deepEqual(registered, [false, false, false, false], 'the SDK has no registration call')
  assert.deepEqual(Object.keys(out.components), ['SettingsBit'])
  assert.equal('slots' in out, false)
})

test('slots: contributions come only from enabled, usable, current-generation add-ons that are ready', () => {
  const ctx = { apps: [app()], instances: [instance()], language: 'en', allowed: true }
  const ids = (slot, over = {}) => slotContributions(slot, { ...ctx, ...over }).map(c => c.id)
  assert.deepEqual(ids('chat.composer.actions'), ['summarize', 'cx'])
  assert.deepEqual(ids('chat.message.actions'), ['inspect', 'mx'])
  assert.deepEqual(ids('app.nav'), ['nav'])
  assert.deepEqual(ids('settings.sections'), ['set'])
  assert.deepEqual(slotContributions('chat.footer', ctx), [])
  // Nothing without the feature.
  assert.deepEqual(ids('settings.sections', { allowed: false }), [])
  // Disabled, crashed, incompatible, no UI, stopped listing: no contribution.
  for (const next of [app({ enabled: false, state: 'stopped' }), app({ state: 'failed' }), app({ ui: ui({ sdk: { min: 3, max: 4 } }) }), app({ ui: undefined })]) {
    assert.deepEqual(ids('settings.sections', { apps: [next] }), [])
    assert.deepEqual(ids('chat.composer.actions', { apps: [next] }), [])
  }
  assert.deepEqual(ids('settings.sections', { apps: [] }), [], 'not listed any more')
  // A newer generation or package: the loaded instance is stale.
  assert.deepEqual(ids('settings.sections', { apps: [app({ generation: 4 })] }), [])
  assert.deepEqual(ids('settings.sections', { apps: [app({ ui: ui({ entry: asset('notes', 'ui.js', 'cd'.repeat(32)) }) })] }), [])
  // An instance that is loading, withdrawn or failed draws nothing.
  for (const status of ['loading', 'unavailable', 'stale', 'error']) assert.deepEqual(ids('settings.sections', { instances: [instance({ status })] }), [], status)
  assert.deepEqual(ids('settings.sections', { instances: [] }), [])
  // A declared component the module did not supply is not drawn; a tool action needs only a ready instance.
  assert.deepEqual(ids('settings.sections', { instances: [instance({ components: {} })] }), [])
  assert.deepEqual(ids('chat.message.actions', { instances: [instance({ components: {} })] }), [])
  assert.deepEqual(ids('chat.composer.actions', { instances: [instance({ components: {} })] }), ['summarize'])
})

test('slots: an action appears only in its declared placement and carries its own target', () => {
  const ctx = { apps: [app()], instances: [instance()], language: 'it', allowed: true }
  const composer = slotContributions('chat.composer.actions', ctx).find(c => c.id === 'summarize')
  assert.equal(composer.kind, 'action')
  assert.equal(composer.label, 'Riassumi', 'the label follows the interface language')
  assert.equal(composer.icon, 'sparkles')
  assert.deepEqual(composer.target, { tool: 'ping' })
  assert.equal(composer.addonId, 'notes')
  assert.equal(composer.generation, 3)
  const message = slotContributions('chat.message.actions', ctx).find(c => c.id === 'inspect')
  assert.deepEqual(message.target, { component: 'InspectPanel' })
  assert.equal(slotContributions('chat.composer.actions', ctx).some(c => c.id === 'inspect'), false)
  assert.equal(slotContributions('chat.message.actions', ctx).some(c => c.id === 'summarize'), false)
  // A packaged icon resolves to the verified app icon URL, never to a path of the module.
  const packaged = app({ ui: ui({ actions: [{ id: 'p', label: 'P', packagedIcon: true, placement: 'composer', tool: 'ping' }] }) })
  const c = slotContributions('chat.composer.actions', { ...ctx, apps: [packaged] })[0]
  assert.equal(c.iconUrl, '/api/addons/apps/notes/icon?generation=3')
  assert.equal(c.icon, '')
  // An icon name outside the host list falls back to the neutral glyph.
  const odd = app({ ui: ui({ actions: [{ id: 'o', label: 'O', icon: 'https://evil.example/i.png', placement: 'composer', tool: 'ping' }] }) })
  assert.equal(slotContributions('chat.composer.actions', { ...ctx, apps: [odd] })[0].icon, 'puzzle')
  // Server data is not trusted blindly: a target tool outside the app's declared tools or two targets is dropped.
  const bad = app({ ui: ui({ actions: [
    { id: 'a', label: 'A', icon: 'star', placement: 'composer', tool: 'ping', component: 'InspectPanel' },
    { id: 'b', label: 'B', icon: 'star', placement: 'sidebar', tool: 'ping' },
    { id: 'c', label: '<img onerror=x>', icon: 'star', placement: 'composer', tool: 'ping' },
  ] }) })
  assert.deepEqual(slotContributions('chat.composer.actions', { ...ctx, apps: [bad] }).filter(x => x.kind === 'action'), [])
})

test('slots: labels follow the language with a safe fallback', () => {
  assert.equal(localizedLabel({ label: 'Summarize', labels: { en: 'Summarize', it: 'Riassumi' } }, 'it-IT'), 'Riassumi')
  assert.equal(localizedLabel({ label: 'Summarize', labels: { en: 'Summarize', it: 'Riassumi' } }, 'de'), 'Summarize')
  assert.equal(localizedLabel({ label: 'Plain' }, 'it'), 'Plain')
  assert.equal(localizedLabel({}, 'it'), '')
})

test('slots: a renderer is found only for the addon own declared tool and kind', () => {
  const a = app()
  const inst = instance({ components: { PingCard: C, PingResult: () => 'r' } })
  assert.equal(rendererFor(a, inst, 'approval-card', 'ping'), C)
  assert.equal(typeof rendererFor(a, inst, 'tool-result', 'ping'), 'function')
  assert.equal(rendererFor(a, inst, 'approval-card', 'other'), null)
  assert.equal(rendererFor(a, inst, 'banner', 'ping'), null)
  assert.equal(rendererFor(a, instance({ components: {} }), 'approval-card', 'ping'), null, 'a missing export falls back to the generic card')
  assert.equal(rendererFor(a, instance({ status: 'unavailable' }), 'approval-card', 'ping'), null)
})

test('slots: an action sends its declared tool and the sanitized context, and results are bounded text', () => {
  const args = actionArguments({ target: { tool: 'ping' } }, { conversationId: 'c1', messageId: '2', role: 'user', content: 'secret', history: [] })
  assert.deepEqual(args, { tool: 'ping', context: { conversationId: 'c1', messageId: '2', role: 'user' } })
  assert.equal(actionArguments({ target: { component: 'X' } }, { conversationId: 'c1' }), null, 'a component action calls no tool')
  assert.equal(resultText({ a: 1 }), JSON.stringify({ a: 1 }, null, 2))
  assert.equal(resultText('x'.repeat(10000)).length <= 4001, true)
  assert.equal(resultText(undefined), '')
  assert.equal(proposalIdOf({ proposalId: 'op-1' }), 'op-1')
  assert.equal(proposalIdOf({ proposalId: '../x' }), '')
  assert.equal(proposalIdOf({ proposalId: 5 }), '')
  assert.equal(proposalIdOf(null), '')
})

test('slots: disable, crash and update unmount every contribution through the runtime', async () => {
  const log = { disposed: 0 }
  const manager = createRuntimeManager({
    load: async () => ({
      component: null, chatActions: [], components: { SettingsBit: C }, sdk: { id: 'sdk' },
      dispose: () => { log.disposed++ }, abort: () => {},
    }),
  })
  const hold = manager.hold(app())
  await new Promise(r => setTimeout(r, 0))
  let inst = manager.get('notes')
  assert.deepEqual(Object.keys(inst.components), ['SettingsBit'])
  assert.deepEqual(slotContributions('settings.sections', { apps: [app()], instances: manager.snapshot(), language: 'en', allowed: true }).map(c => c.id), ['set'])
  for (const next of [app({ enabled: false, state: 'stopped' }), app({ state: 'failed' }), app({ generation: 4 })]) {
    const m = createRuntimeManager({ load: async () => ({ component: null, chatActions: [], components: { SettingsBit: C }, sdk: {}, dispose() {}, abort() {} }) })
    m.hold(app())
    await new Promise(r => setTimeout(r, 0))
    m.revalidate([next])
    inst = m.get('notes')
    assert.deepEqual(inst.components, {}, 'the withdrawn instance supplies no component')
    assert.deepEqual(slotContributions('settings.sections', { apps: [next], instances: m.snapshot(), language: 'en', allowed: true }), [])
  }
  hold.release()
})

test('slots: contributions keep the apps list polled while they exist', () => {
  const only = (u) => [app({ ui: { entry: ui().entry, sdk: { min: 1, max: 2 }, ...u } })]
  assert.equal(contributesUI(only({ slots: [{ id: 'a', slot: 'app.nav', component: 'X' }] })), true)
  assert.equal(contributesUI(only({ actions: [{ id: 'a', label: 'A', icon: 'star', placement: 'composer', tool: 'ping' }] })), true)
  assert.equal(contributesUI(only({})), false)
})

test('slots: the approval frame decides from the stored proposal and never from the renderer', () => {
  const now = Date.parse('2026-10-13T10:00:00Z')
  const proposal = {
    id: 'op-1', addonId: 'notes', state: 'pending', digest: 'sha256:aa', arguments: { tool: 'ping', rows: 3 },
    effects: { count: 3, sample: [{ a: 1 }] }, expiresAt: '2026-10-13T10:05:00Z',
  }
  const s = approvalState(proposal, now)
  assert.equal(s.canDecide, true)
  assert.equal(s.count, 3)
  assert.equal(s.tool, 'ping')
  assert.equal(approvalState({ ...proposal, state: 'applied' }, now).canDecide, false)
  assert.equal(approvalState({ ...proposal, state: 'executing' }, now).canDecide, false)
  assert.equal(approvalState({ ...proposal, blocked: true }, now).canDecide, false)
  assert.equal(approvalState({ ...proposal, expiresAt: '2026-10-13T09:59:00Z' }, now).expired, true)
  assert.equal(approvalState({ ...proposal, expiresAt: '2026-10-13T09:59:00Z' }, now).canDecide, false)
  assert.equal(approvalState(null, now).canDecide, false)
  const confirm = confirmCall(proposal)
  assert.equal(confirm.path, '/api/addons/proposals/op-1/confirm')
  assert.equal(confirm.method, 'POST')
  assert.deepEqual(confirm.headers, { 'X-LocalAI-Confirm': 'op-1' })
  assert.deepEqual(confirm.body, { digest: 'sha256:aa' }, 'the confirmation binds the digest that was shown')
  assert.equal(cancelCall(proposal).path, '/api/addons/proposals/op-1/cancel')
  assert.equal(confirmCall({ ...proposal, id: '../x' }), null)
  // The renderer gets data only: no function, no digest and no way to decide.
  const props = rendererProps(proposal)
  assert.deepEqual(Object.keys(props).sort(), ['arguments', 'effects', 'proposalId', 'tool'])
  assert.equal(Object.isFrozen(props), true)
  assert.equal(Object.values(props).some(v => typeof v === 'function'), false)
  assert.equal(JSON.stringify(props).includes('sha256:aa'), false)
})

test('slots: the SDK can present an approval of its own addon only through the host', () => {
  const controller = new AbortController()
  const presented = []
  const host = {
    React, components: {}, Icon: () => null, theme: {}, language: () => 'en', subscribeLanguage: () => () => {},
    api: { dispatch: async () => ({}), propose: async () => ({}) },
  }
  const sdk = createHostSDK({
    addonId: 'notes', generation: 3, signal: controller.signal, host,
    bindings: { presentApproval: (id) => presented.push(id) },
  })
  assert.equal(typeof sdk.approvals.present, 'function')
  assert.deepEqual(Object.keys(sdk.approvals), ['present'])
  sdk.approvals.present('op-9')
  assert.deepEqual(presented, ['op-9'])
  assert.throws(() => sdk.approvals.present('../etc'), /proposal/)
  assert.throws(() => sdk.approvals.present(''), /proposal/)
  controller.abort()
  assert.throws(() => sdk.approvals.present('op-10'), /withdrawn/)
  assert.equal(renderToString(React.createElement('i')), '<i></i>')
})

test('slots: a page that only needs the list reads it once and never retries a failed read', () => {
  const base = { allowed: true, onAddonsPage: false, apps: [], consumers: 3 }
  assert.equal(shouldPoll({ ...base, status: 'idle' }), true)
  assert.equal(shouldPoll({ ...base, status: 'ready' }), false)
  assert.equal(shouldPoll({ ...base, status: 'error' }), false, 'no loop of failing reads for a page that only draws slots')
  assert.equal(shouldPoll({ ...base, status: 'idle', allowed: false }), false)
})
