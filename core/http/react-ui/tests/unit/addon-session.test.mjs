// SPDX-License-Identifier: MIT
import test from 'node:test'
import assert from 'node:assert/strict'
import { hookHarness } from './chatcommands-lifecycle.mjs'
import { createActionBook, effectOutcome } from '../../src/components/addons/actionBook.js'
import { runLifecycle, settleOperation } from '../../src/components/addons/lifecycleFlow.js'
import { newOperationId } from '../../src/components/catalog/catalogState.js'
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r }); return { promise, resolve } }

for (const verb of ['start', 'stop']) test(`pending ${verb} cannot verify or publish after actual auth epoch changes before cleanup`, async () => {
  let auth = { user: { id: 'admin' }, isAdmin: true, sessionEpoch: 1, captureSessionAuthority: () => () => true }
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
  let auth = { user: { id: 'admin' }, isAdmin: true, sessionEpoch: 1, captureSessionAuthority: () => () => true }
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
  auth = { ...auth, user: { id: 'other' }, isAdmin: true, sessionEpoch: 2 }
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
  const api = { apps: { read: async () => ({ apps: [{ id: 'notes', generation: 1 }] }) }, installed: () => { reads++; return pending.promise } }
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

for (const phase of ['completed', 'failed', 'intent', 'unreadable']) test(`observer drops ${phase} after auth epoch changes during status read`, async () => {
  const { createUpgradeStore } = await import('../../src/utils/addonUpgrades.js')
  const status = deferred(), reading = deferred()
  let epoch = 1, verified = 0, reads = 0
  const store = createUpgradeStore({ waits: [] })
  const api = {
    updates: { upgrade: async () => ({ phase: 'accepted' }) },
    operationStatus: () => { reads++; reading.resolve(); return status.promise.then(value => { if (phase === 'unreadable') throw new Error('offline'); return value }) },
    installed: async () => { verified++; return [] },
  }
  const addon = { id: 'notes', digest: 'old', generation: 1, update: { state: 'update-available', digest: 'new' } }
  const work = store.start(api, addon, { current: () => epoch === 1 })
  await reading.promise
  assert.equal(store.cancel(store.forAddon('notes').jobID), false, 'accepted owner work cannot be cancelled')
  epoch = 2
  const publications = []
  store.subscribe(() => publications.push(store.getSnapshot()))
  status.resolve({ phase })
  assert.equal(await work, null)
  assert.equal(reads, 1)
  assert.equal(verified, 0)
  assert.deepEqual(store.getSnapshot(), { live: [], records: [] })
  assert.ok(publications.every(s => s.records.length === 0 && s.live.length === 0))
})

test('observer does not read status when auth expires before acceptance returns', async () => {
  const { createUpgradeStore } = await import('../../src/utils/addonUpgrades.js')
  const store = createUpgradeStore({ waits: [] }), accepted = deferred()
  let epoch = 1, reads = 0
  const api = {
    updates: { upgrade: () => accepted.promise },
    operationStatus: async () => { reads++; return { phase: 'completed' } },
  }
  const addon = { id: 'notes', digest: 'old', generation: 1, update: { state: 'update-available', digest: 'new' } }
  const work = store.start(api, addon, { current: () => epoch === 1 })
  assert.equal(store.cancel(store.forAddon('notes').jobID), false, 'disconnect cannot prove preacceptance')
  epoch = 2
  accepted.resolve({ phase: 'accepted' })
  assert.equal(await work, null)
  assert.equal(reads, 0)
  assert.deepEqual(store.getSnapshot(), { live: [], records: [] })
})

test('expired observer cleanup preserves a newer attempt with the same action key', async () => {
  const { createUpgradeStore } = await import('../../src/utils/addonUpgrades.js')
  let id = 0, epoch = 1
  const book = createActionBook(() => `attempt-${++id}`)
  const store = createUpgradeStore({ book, waits: [] })
  const status = deferred(), reading = deferred(), nextSubmit = deferred()
  const addon = { id: 'notes', digest: 'old', generation: 1, update: { state: 'update-available', digest: 'new' } }
  const api = {
    updates: { upgrade: async () => ({ phase: 'accepted' }) },
    operationStatus: () => { reading.resolve(); return status.promise },
  }
  const oldWork = store.start(api, addon, { current: () => epoch === 1 })
  const old = store.forAddon(addon.id)
  await reading.promise
  // Another follower concludes the old entry, allowing a new authorized action.
  store.conclude(old.jobID, addon, 'upgrade:notes:1', 'failed', old.operationId)
  epoch = 2
  const nextWork = store.start({
    updates: { upgrade: () => nextSubmit.promise },
    installed: async () => [{ ...addon, generation: 2 }],
  }, addon, { current: () => epoch === 2 })
  const next = store.forAddon(addon.id), held = book.pending(addon.id)
  assert.notEqual(next.operationId, old.operationId)
  status.resolve({ phase: 'completed' })
  assert.equal(await oldWork, null)
  assert.deepEqual(store.forAddon(addon.id), next)
  assert.deepEqual(book.pending(addon.id), held)
  assert.deepEqual(store.getSnapshot().records, [])
  nextSubmit.resolve({ phase: 'completed' })
  assert.equal((await nextWork).ok, true)
  assert.equal(store.getSnapshot().records[0].operationId, next.operationId)
})

// Execute the actual AuthProvider state transitions, not a test-owned epoch getter.
// Only its JSX boundary is replaced; fetch is the controlled network boundary.
async function providerAuthority() {
  const ts = (await import('typescript')).default
  let status = { authEnabled: true, user: { id: 'admin', role: 'admin' } }
  const logoutReply = deferred()
  const h = hookHarness('../../src/context/AuthContext.jsx', 'AuthProvider', {
    apiUrl: x => x, addonUpgrades: (await import('../../src/utils/addonUpgrades.js')).createUpgradeStore(),
    fetch: url => url.endsWith('/logout') ? logoutReply.promise : Promise.resolve({ json: async () => status }),
    document: { cookie: '' }, window: { location: {} },
  }, source => ts.transpileModule(source.replace(/return \(\s*<AuthContext.Provider[\s\S]*?\n  \)/, 'return value'), {
    compilerOptions: { target: ts.ScriptTarget.ES2022 },
  }).outputText)
  let auth = h.render({ children: null }); h.flush()
  await new Promise(r => setImmediate(r))
  auth = h.render({ children: null })
  return {
    get auth() { return auth },
    async change(kind) {
      if (kind === 'unmount') { h.unmount(); return }
      if (kind === 'logout') { void auth.logout(); return } // network remains pending
      if (kind === 'account') status = { ...status, user: { id: 'other', role: 'admin' } }
      if (kind === 'feature') status = { ...status, user: { id: 'admin', role: 'user', permissions: {} } }
      await auth.refresh()
      // Do not render the provider or any consumer: expiry must be synchronous.
    },
  }
}

for (const path of ['observer', 'completion', 'verification']) {
  for (const change of ['same-auth', 'refresh', 'account', 'feature', 'logout', 'unmount']) {
    test(`background ${path} after origin unmount uses actual provider authority on ${change}`, async () => {
      const { createUpgradeStore } = await import('../../src/utils/addonUpgrades.js')
      const provider = await providerAuthority()
      const store = createUpgradeStore({ waits: [] })
      const reply = deferred(), reading = deferred(), verification = deferred(), verifying = deferred()
      let verified = 0, changed = 0
      const addon = { id: 'notes', digest: 'old', generation: 1, update: { state: 'update-available', digest: 'new' } }
      const api = {
        updates: { upgrade: () => path === 'observer' ? Promise.resolve({ phase: 'accepted' }) : reply.promise },
        operationStatus: () => { reading.resolve(); return reply.promise },
        installed: () => { verified++; verifying.resolve(); return verification.promise },
      }
      const view = hookHarness('../../src/components/addons/useAddonUpgrade.js', 'useAddonUpgrade', {
        useAuth: () => provider.auth, newOperationId, addonUpgrades: store,
        useAddonUpgrades: () => store.getSnapshot(),
      })
      const origin = view.render(api, { onChanged: () => changed++ }); view.flush()
      const work = origin.upgrade(addon)
      view.unmount() // navigation must not revoke a valid background upgrade
      if (path === 'observer') {
        await reading.promise
        assert.equal(store.cancel(store.forAddon(addon.id).jobID), false)
      }
      if (path === 'verification') {
        reply.resolve({ phase: 'completed' }); await verifying.promise
      }
      if (change !== 'same-auth') await provider.change(change)
      reply.resolve({ phase: 'completed' })
      verification.resolve([{ ...addon, generation: 2 }])
      await work
      assert.equal(changed, 0, 'unmounted view must not receive callbacks')
      assert.equal(verified, path === 'verification' || change === 'same-auth' ? 1 : 0)
      assert.equal(store.getSnapshot().live.length, 0)
      assert.equal(store.getSnapshot().records.length, change === 'same-auth' ? 1 : 0)
    })
  }
}
