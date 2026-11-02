// SPDX-License-Identifier: MIT
import test from 'node:test'
import assert from 'node:assert/strict'
import { hookHarness } from './chatcommands-lifecycle.mjs'
import { createActionBook, effectOutcome } from '../../src/components/addons/actionBook.js'
import { runLifecycle, settleOperation } from '../../src/components/addons/lifecycleFlow.js'
import { newOperationId } from '../../src/components/catalog/catalogState.js'
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r }); return { promise, resolve } }

for (const verb of ['start', 'stop']) test(`pending ${verb} cannot verify or publish after actual auth epoch changes before cleanup`, async () => {
  let auth = { user: { id: 'admin' }, isAdmin: true, sessionEpoch: 1 }
  const pending = deferred()
  let submitted = 0, verified = 0, settled = 0
  const h = hookHarness('../../src/components/addons/useAddonActions.js', 'useAddonActions', {
    useAuth: () => auth, createActionBook, effectOutcome, runLifecycle, settleOperation, runInstallAndTurnOn() {},
  })
  const render = () => h.render({}, { onSettled: () => settled++ })
  const old = render(); h.flush()
  const args = { addonId: 'notes', submit: () => { submitted++; return pending.promise }, verify: async () => { verified++; return true } }
  const work = old.run(`${verb}:notes:1`, args)
  auth = { ...auth, sessionEpoch: 2 }
  const next = render() // deliberately do not flush cleanup
  assert.deepEqual(next.state, {})
  assert.equal(await old.run('late', args), null)
  pending.resolve({ phase: 'completed' }); await work
  assert.equal(submitted, 1)
  assert.equal(verified, 0)
  assert.equal(settled, 0)
  assert.deepEqual(render().state, {})
})

test('pending update check and upgrade cannot publish to the next actor before cleanup', async () => {
  let auth = { user: { id: 'admin' }, isAdmin: true, sessionEpoch: 1 }
  const pending = deferred(), updating = deferred()
  let checked = 0, changed = 0, started = 0
  const api = { updates: { check: () => { checked++; return pending.promise } } }
  const h = hookHarness('../../src/components/addons/useAddonUpgrade.js', 'useAddonUpgrade', {
    useAuth: () => auth, newOperationId, useAddonUpgrades: () => ({ live: [], records: [] }),
    addonUpgrades: { start: () => { started++; return updating.promise } },
  })
  const render = () => h.render(api, { onChanged: () => changed++ })
  const old = render(); h.flush()
  const work = old.check({ id: 'notes' }), upgrade = old.upgrade({ id: 'notes' })
  auth = { user: { id: 'other' }, isAdmin: true, sessionEpoch: 2 }
  assert.deepEqual(render().checks, {})
  await old.check({ id: 'notes' }); await old.upgrade({ id: 'notes' })
  pending.resolve({ state: 'update-available' }); updating.resolve({ ok: true })
  await Promise.all([work, upgrade])
  assert.equal(checked, 1); assert.equal(started, 1); assert.equal(changed, 0)
  assert.deepEqual(render().checks, {})
})

test('upgrade store drops an expired completion without installed verification or cached record', async () => {
  const { createUpgradeStore } = await import('../../src/utils/addonUpgrades.js')
  const pending = deferred()
  let live = true, verified = 0, submitted = 0
  const store = createUpgradeStore()
  const api = { updates: { upgrade: () => { submitted++; return pending.promise } }, installed: async () => { verified++; return [] } }
  const addon = { id: 'notes', digest: 'old', generation: 1, update: { state: 'update-available', digest: 'new' } }
  const work = store.start(api, addon, { current: () => live })
  assert.equal(submitted, 1)
  live = false
  assert.equal(await store.start(api, addon, { current: () => live }), null)
  pending.resolve({ phase: 'completed' }); await work
  assert.equal(verified, 0)
  assert.deepEqual(store.getSnapshot(), { live: [], records: [] })
})

for (const switchUser of [false, true]) test(`installed records reject late admin read on ${switchUser ? 'account change' : 'actual auth refresh'} before cleanup`, async () => {
  const { readFileSync } = await import('node:fs')
  const ts = (await import('typescript')).default
  let auth = { user: { id: 'admin' }, isAdmin: true, sessionEpoch: 1, hasFeature: () => true }
  const pending = deferred()
  let reads = 0
  const api = { apps: { list: async () => [{ id: 'notes', generation: 1 }] }, installed: () => { reads++; return pending.promise } }
  const text = readFileSync(new URL('../../src/components/addons/AppsView.jsx', import.meta.url), 'utf8')
  // Full Apps body/effects; JSX is compiled, leaf hooks are external boundaries.
  const names = [...text.matchAll(/^import \{ ([^}]+) \} from/gm)].flatMap(m => m[1].split(',').map(n => n.trim()))
  const defaults = [...text.matchAll(/^import (\w+) from/gm)].map(m => m[1])
  const h = hookHarness('../../src/components/addons/AppsView.jsx', 'AppsView', {
    ...Object.fromEntries([...names, ...defaults].filter(n => !n.startsWith('use')).map(n => [n, () => null])),
    useAuth: () => auth, useContext: () => null, useTranslation: () => ({ t: k => k, i18n: {} }),
    useAddonActions: () => ({}), useAddonUpgrade: () => ({ checks: {}, records: [] }), useBulkUpgrade: () => ({}),
    useOutletContext: () => ({}), useNavigation: () => ({ pins: [] }), useHubAuth: () => ({ features: {} }),
    useSearchParams: () => [new URLSearchParams(), () => {}],
    buildTiles: () => [], resolvePins: () => [], filterTiles: () => [], filterByState: () => [], sortByAttention: () => [],
    updateView: () => ({ state: '' }),
    FEATURE_ADDONS: 'addons', RegistryContext: {}, setTimeout: () => 1, clearTimeout() {},
  }, source => ts.transpileModule(source.replace('export default function', 'function').split("  if (state.status === 'loading')")[0] + 'return { installedInfo }\n}', { compilerOptions: { jsx: ts.JsxEmit.React, target: ts.ScriptTarget.ES2022 } }).outputText)
  h.render({ api }); h.flush()
  await new Promise(r => setImmediate(r))
  h.render({ api }); h.flush()
  assert.equal(reads, 1)
  auth = { ...auth, user: { id: switchUser ? 'other' : 'admin' }, sessionEpoch: 2 }
  assert.deepEqual(h.render({ api }).installedInfo, {})
  pending.resolve([{ id: 'notes', privateValue: 'old-admin-record' }])
  await new Promise(r => setImmediate(r))
  assert.deepEqual(h.render({ api }).installedInfo, {})
})
