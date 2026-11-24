import assert from 'node:assert/strict'
import test from 'node:test'

import { modelBudget } from './modelBudget.js'
import { fitFor, diskState, leavesFree, gbNumber } from './modelLedger.js'
import { buildCleanupPlan, collectReferences, findDuplicates, totalSize } from './cleanupPlan.js'

const GB = 1024 * 1024 * 1024

const gpu24 = modelBudget({ type: 'gpu', gpus: [{}], aggregate: { total_memory: 24 * GB, gpu_count: 1 } })
const cpu16 = modelBudget({ type: 'ram', aggregate: { total_memory: 16 * GB, gpu_count: 0 } })

test('a model under the limit fits and reports the room left', () => {
  const fit = fitFor(10 * GB, gpu24, 32 * GB)
  assert.equal(fit.state, 'fits')
  assert.ok(Math.abs(fit.amount - (24 * 0.95 - 10) * GB) < 1)
})

test('a model over the GPU but inside GPU plus RAM spills to the CPU', () => {
  const fit = fitFor(24 * GB, gpu24, 32 * GB)
  assert.equal(fit.state, 'spill')
  assert.ok(Math.abs(fit.amount - (24 - 22.8) * GB) < 1)
})

test('a model over GPU and RAM together is over by the shortfall', () => {
  const fit = fitFor(60 * GB, gpu24, 8 * GB)
  assert.equal(fit.state, 'over')
  assert.ok(Math.abs(fit.amount - (60 - 22.8 - 8) * GB) < 1)
})

test('a host with no GPU never spills: RAM is the whole budget', () => {
  assert.equal(fitFor(10 * GB, cpu16, 12 * GB).state, 'fits')
  const fit = fitFor(17 * GB, cpu16, 12 * GB)
  assert.equal(fit.state, 'over')
  assert.ok(Math.abs(fit.amount - (17 - 15.2) * GB) < 1)
})

test('a cluster reading has no RAM figure, so it cannot spill', () => {
  const cluster = modelBudget({ cluster: { enabled: true, total_memory: 24 * GB, is_gpu: true, node_name: 'w1', node_count: 2 } })
  assert.equal(fitFor(24 * GB, cluster, 64 * GB).state, 'over')
})

test('no estimate or no budget gives no verdict', () => {
  assert.equal(fitFor(0, gpu24), null)
  assert.equal(fitFor(4 * GB, modelBudget(null)), null)
})

test('disk is amber under 10 percent or under 20 GB free, and not otherwise', () => {
  const at = (total, free) => diskState({ disk: { total: total * GB, available: free * GB }, storage_size: 10 * GB })
  assert.equal(at(1000, 171).low, false)
  assert.equal(at(1000, 99).low, true)
  assert.equal(at(100, 19).low, true)
  assert.equal(at(100, 21).low, false)
  assert.equal(at(4000, 450).low, false)
})

test('disk is hidden when the server reports none, and on a cluster controller', () => {
  assert.equal(diskState(null), null)
  assert.equal(diskState({}), null)
  assert.equal(diskState({ disk: { total: 0, available: 0 } }), null)
  assert.equal(diskState({ disk: { total: GB, available: GB }, cluster: { enabled: true } }), null)
})

test('an install leaves the free space minus its size, negative when it does not fit', () => {
  const disk = diskState({ disk: { total: 500 * GB, available: 100 * GB } })
  assert.equal(leavesFree(disk, 19.4 * GB) > 80 * GB, true)
  assert.equal(leavesFree(disk, 120 * GB) < 0, true)
  assert.equal(leavesFree(disk, 0), null)
  assert.equal(gbNumber(0.01 * GB), '<0.1')
})

test('protected models are never suggested, and each says why', () => {
  const refs = collectReferences({
    agents: [{ name: 'helper', models: ['m-agent'] }],
    tasks: [{ name: 'nightly', model: 'm-task' }],
    chains: [{ name: 'chat', targets: [{ model: 'm-chain' }] }],
    aliases: { gpt: 'm-alias' },
  })
  const models = ['m-run', 'm-pin', 'm-agent', 'm-task', 'm-chain', 'm-alias', 'free']
    .map(id => ({ id, running: id === 'm-run', pinned: id === 'm-pin' }))
  const plan = buildCleanupPlan({ models, references: refs })
  assert.deepEqual(plan.protected.map(p => p.id).sort(), ['m-agent', 'm-alias', 'm-chain', 'm-pin', 'm-run', 'm-task'])
  assert.deepEqual(plan.groups.call.map(p => p.id), ['free'])
})

test('a duplicate build is safe, the preferred one stays', () => {
  const dups = findDuplicates(['a', 'a-q4', 'solo'], { a: { variants: [{ model: 'a' }, { model: 'a-q4' }], auto_selected: 'a-q4' } })
  assert.deepEqual([...dups.keys()], ['a'])
  assert.equal(dups.get('a').keep, 'a-q4')
  const plan = buildCleanupPlan({ models: [{ id: 'a' }, { id: 'a-q4' }], duplicates: dups })
  assert.deepEqual(plan.groups.safe.map(p => p.id), ['a'])
  assert.deepEqual(plan.groups.call.map(p => p.id), ['a-q4'])
})

test('with no usage history the tiers come from structure and size orders each', () => {
  const models = [
    { id: 'disabled-gal', disabled: true },
    { id: 'disabled-local', disabled: true },
    { id: 'big' },
    { id: 'small' },
  ]
  const plan = buildCleanupPlan({
    models,
    sizes: { big: 20 * GB, small: 1 * GB },
    galleryIds: new Set(['disabled-gal', 'big', 'small']),
  })
  assert.deepEqual(plan.groups.probably.map(p => p.id), ['disabled-gal'])
  assert.deepEqual(plan.groups.call.map(p => p.id), ['big', 'small', 'disabled-local'])
  assert.deepEqual(totalSize(plan.groups.call), { bytes: 21 * GB, unknown: 1 })
})

test('when a lookup failed nothing is called safe', () => {
  const dups = new Map([['a', { keep: 'b' }]])
  const plan = buildCleanupPlan({ models: [{ id: 'a' }], duplicates: dups, verified: false })
  assert.equal(plan.groups.safe.length, 0)
  assert.equal(plan.groups.call[0].reason.unverified, true)
})

test('a model only the cluster knows is left out', () => {
  const plan = buildCleanupPlan({ models: [{ id: 'ghost', source: 'registry-only' }] })
  assert.equal(plan.groups.call.length + plan.protected.length, 0)
})
