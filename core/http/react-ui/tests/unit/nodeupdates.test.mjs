import test from 'node:test'
import assert from 'node:assert/strict'
import {
  entriesFromPairs, entryOf, hasNodeView, isClusterRow, splitUpdate, nodeConfirmed, nodeStateOf, nodeView, planEntries, planTotals, stopKind,
} from '../../src/utils/nodeUpdates.js'
import { runNodeRow, confirmNodeUpdated } from '../../src/utils/updateRun.js'
import { beginRun, runSequential } from '../../src/utils/bulkUpgrade.js'

const item = (over = {}) => ({
  id: 'gallery:llama-cpp', kind: 'gallery', name: 'llama-cpp', state: 'update',
  installedVersion: '0.9.4', availableVersion: '0.9.7',
  nodes: [
    { nodeId: 'a', nodeName: 'gpu-a', status: 'healthy', version: '0.9.4', models: ['qwen3-coder', 'gemma-3'] },
    { nodeId: 'b', nodeName: 'gpu-b', status: 'healthy', version: '0.9.4', models: [] },
    { nodeId: 'c', nodeName: 'cpu-c', status: 'healthy', version: '0.9.7' },
  ],
  ...over,
})

test('node state: current, behind, differs, unreachable', () => {
  const it = item()
  assert.equal(nodeStateOf(it, it.nodes[0]), 'behind')
  assert.equal(nodeStateOf(it, it.nodes[2]), 'current')
  assert.equal(nodeStateOf(it, { ...it.nodes[0], status: 'unhealthy' }), 'unreachable')
  // The gallery and the majority agree, one node is off: it differs, it is not behind.
  const drift = item({ state: 'drift', installedVersion: '0.9.7', nodes: [{ nodeId: 'a', version: '0.9.9', drift: true }, { nodeId: 'b', version: '0.9.7' }] })
  assert.equal(nodeStateOf(drift, drift.nodes[0]), 'differs')
  assert.equal(nodeStateOf(drift, drift.nodes[1]), 'current')
  // A node with another version than the majority and than the gallery differs, even in an update.
  const mixed = item({ nodes: [{ nodeId: 'a', version: '0.9.9', drift: true }, { nodeId: 'b', version: '0.9.4' }] })
  assert.equal(nodeStateOf(mixed, mixed.nodes[0]), 'differs')
  assert.equal(nodeStateOf(mixed, mixed.nodes[1]), 'behind')
})

test('node state: a build is told by digest when the gallery names no version', () => {
  const it = item({ availableVersion: '', availableDigest: 'sha256:bb', nodes: [{ nodeId: 'a', digest: 'sha256:aa' }, { nodeId: 'b', digest: 'sha256:bb' }] })
  assert.equal(nodeStateOf(it, it.nodes[0]), 'behind')
  assert.equal(nodeStateOf(it, it.nodes[1]), 'current')
})

test('node view lists every node by name, and todo holds the ones to update', () => {
  const v = nodeView(item())
  assert.deepEqual(v.nodes.map(n => n.nodeName), ['cpu-c', 'gpu-a', 'gpu-b'])
  assert.deepEqual(v.todo.map(n => n.nodeId), ['a', 'b'])
  assert.equal(v.behind, 2)
  assert.equal(v.differs, 0)
  assert.deepEqual(nodeView(item({ kind: 'library' })).nodes, [], 'a library backend has no node view')
})

test('the Nodes action needs more than one node', () => {
  assert.equal(hasNodeView({ kind: 'gallery', nodes: [{}, {}] }), true)
  assert.equal(hasNodeView({ kind: 'gallery', nodes: [{}] }), false)
  assert.equal(hasNodeView({ kind: 'gallery', nodes: [] }), false)
  assert.equal(hasNodeView({ kind: 'library', nodes: [{}, {}] }), false)
  assert.equal(hasNodeView({ kind: 'gallery' }), false)
})

test('plan: entries come in backend and node order and can be narrowed', () => {
  const other = item({ id: 'gallery:vllm', name: 'vllm', nodes: [{ nodeId: 'a', nodeName: 'gpu-a', version: '0.9.4' }] })
  const all = planEntries([item(), other])
  assert.deepEqual(all.map(e => e.key), ['llama-cpp@a', 'llama-cpp@b', 'vllm@a'])
  assert.deepEqual(planEntries([item(), other], { backend: 'vllm' }).map(e => e.key), ['vllm@a'])
  assert.deepEqual(planEntries([item(), other], { nodeIds: new Set(['b']) }).map(e => e.key), ['llama-cpp@b'])
  const e = entryOf(item(), item().nodes[0])
  assert.equal(e.from, 'v0.9.4')
  assert.equal(e.to, 'v0.9.7')
  assert.deepEqual(e.models, ['qwen3-coder', 'gemma-3'])
})

test('totals and the words: an empty model list is "none recorded", not "none stop"', () => {
  const entries = planEntries([item()])
  const t = planTotals(entries)
  assert.deepEqual(t, { updates: 2, nodes: 2, backends: 1, models: 2, unknown: 1 })
  assert.equal(stopKind(t), 'partial')
  assert.equal(stopKind(planTotals([entries[0]])), 'known')
  assert.equal(stopKind(planTotals([entries[1]])), 'none')
  assert.equal(stopKind(planTotals([])), 'none')
})

test('pairs of the Nodes page get the version and the models the read model knows', () => {
  const nodes = [{ id: 'a', name: 'alpha' }, { id: 'z', name: 'zeta' }]
  const out = entriesFromPairs({
    pairs: [{ backend: 'llama-cpp', nodeId: 'a' }, { backend: 'llama-cpp', nodeId: 'z' }],
    items: [item()],
    nodes,
    upgrades: { 'llama-cpp': { available_version: '0.9.7' } },
  })
  assert.equal(out[0].nodeName, 'alpha')
  assert.deepEqual(out[0].models, ['qwen3-coder', 'gemma-3'])
  assert.equal(out[0].from, 'v0.9.4')
  // Unknown to the read model: a line with no models named and the target version.
  assert.equal(out[1].nodeName, 'zeta')
  assert.deepEqual(out[1].models, [])
  assert.equal(out[1].to, 'v0.9.7')
  assert.equal(out[1].itemId, 'gallery:llama-cpp')
})

const ok = { gallery: { state: 'ok' } }
test('confirmation: the node must show the new build in a fresh check', () => {
  const e = planEntries([item()])[0]
  assert.equal(nodeConfirmed(e, { sources: ok, items: [] }), 'done', 'the backend left the list')
  const updated = item({ nodes: [{ nodeId: 'a', version: '0.9.7' }, { nodeId: 'b', version: '0.9.4' }] })
  assert.equal(nodeConfirmed(e, { sources: ok, items: [updated] }), 'done', 'other nodes may still be behind')
  assert.equal(nodeConfirmed(e, { sources: ok, items: [item()] }), 'unchanged')
  assert.equal(nodeConfirmed(e, { sources: { gallery: { state: 'failed' } }, items: [] }), 'unconfirmed')
  assert.equal(nodeConfirmed(e, null), 'unconfirmed')
  assert.equal(nodeConfirmed(e, { sources: ok, items: [item({ nodes: [{ nodeId: 'x', version: '0.9.7' }] })] }), 'unconfirmed', 'a node that is not reported cannot be confirmed')
})

function clock() {
  let t = 0
  return { now: () => t, sleep: async (ms) => { t += ms } }
}

test('a throttled check that still shows the node behind is asked again', async () => {
  const e = planEntries([item()])[0]
  const answers = [
    { throttled: true, sources: ok, items: [item()] },
    { sources: ok, items: [] },
  ]
  let n = 0
  const v = await confirmNodeUpdated({ entry: e, recheck: async () => answers[n++], ...clock() })
  assert.equal(v, 'done')
  assert.equal(n, 2)
})

test('node run: the start is not the end; done needs the operation to end and the check to agree', async () => {
  const e = planEntries([item()])[0]
  const events = []
  let polls = 0
  const out = await runNodeRow({
    entry: e,
    start: async () => { events.push('start'); return 'j1' },
    getOperations: () => { polls += 1; return polls <= 2 ? [{ jobID: 'j1' }] : [] },
    recheck: async () => { events.push('recheck'); return { sources: ok, items: [] } },
    ...clock(),
  })
  assert.deepEqual(out, { ok: true })
  assert.deepEqual(events, ['start', 'recheck'])
})

test('node run: a failed operation, a refused start and an unchanged node are failures with a code', async () => {
  const e = planEntries([item()])[0]
  const base = { entry: e, ...clock() }
  assert.deepEqual(
    await runNodeRow({ ...base, start: async () => 'j', getOperations: () => [{ jobID: 'j', error: 'boom' }], recheck: async () => ({}) }),
    { ok: false, code: 'failed', error: 'boom' },
  )
  assert.deepEqual(
    await runNodeRow({ ...base, start: async () => { throw new Error('node is down') }, getOperations: () => [], recheck: async () => ({}) }),
    { ok: false, code: 'start', error: 'node is down' },
  )
  assert.deepEqual(
    await runNodeRow({ ...base, start: async () => 'j', getOperations: () => [], recheck: async () => ({ sources: ok, items: [item()] }), maxTries: 0 }),
    { ok: false, code: 'unchanged' },
  )
})

test('sequence: nodes run one at a time, a failure does not stop the others, retry runs only the failed', async () => {
  const entries = planEntries([item({ nodes: [
    { nodeId: 'a', nodeName: 'a', version: '0.9.4' }, { nodeId: 'b', nodeName: 'b', version: '0.9.4' }, { nodeId: 'c', nodeName: 'c', version: '0.9.4' },
  ] })]).map(e => ({ ...e, id: e.key, startable: true }))
  let active = 0
  let maxActive = 0
  const order = []
  const failing = new Set(['llama-cpp@b'])
  const runOne = async (it) => {
    active += 1; maxActive = Math.max(maxActive, active)
    order.push(it.id)
    await new Promise(r => setTimeout(r, 1))
    active -= 1
    return failing.has(it.id) ? { ok: false, message: 'download interrupted' } : { ok: true }
  }
  const seen = []
  const run = beginRun(null, entries, new Set(entries.map(e => e.id)))
  const done = await runSequential(run, runOne, { onUpdate: r => seen.push(Object.fromEntries(Object.entries(r.status).map(([k, v]) => [k, v.phase]))) })
  assert.equal(maxActive, 1)
  assert.deepEqual(order, ['llama-cpp@a', 'llama-cpp@b', 'llama-cpp@c'])
  assert.deepEqual(Object.fromEntries(Object.entries(done.status).map(([k, v]) => [k, v.phase])), { 'llama-cpp@a': 'done', 'llama-cpp@b': 'failed', 'llama-cpp@c': 'done' })
  assert.equal(done.status['llama-cpp@b'].message, 'download interrupted')
  // The result of a node is visible before the next one starts.
  assert.ok(seen.some(s => s['llama-cpp@a'] === 'done' && s['llama-cpp@b'] === 'upgrading'))
  failing.clear()
  order.length = 0
  const retry = beginRun(done, done.items, new Set(['llama-cpp@b']))
  const again = await runSequential(retry, runOne)
  assert.deepEqual(order, ['llama-cpp@b'])
  assert.equal(again.status['llama-cpp@b'].phase, 'done')
})

test('sequence: a node that is skipped after a stop is not counted as done', async () => {
  const items = [{ id: 'x', startable: true }, { id: 'y', startable: true }]
  let stop = false
  const run = beginRun(null, items, new Set(['x', 'y']))
  const done = await runSequential(run, async (it) => { if (stop) return { ok: false, skipped: true }; stop = true; return { ok: true } })
  assert.equal(done.status.x.phase, 'done')
  assert.equal(done.status.y.phase, 'skipped')
})

test('a cluster row is a gallery backend that any node reports', () => {
  assert.equal(isClusterRow({ kind: 'gallery', nodes: [{}] }), true, 'one worker is enough: it still stops its models')
  assert.equal(isClusterRow({ kind: 'gallery', nodes: [] }), false)
  assert.equal(isClusterRow({ kind: 'gallery' }), false)
  assert.equal(isClusterRow({ kind: 'library', nodes: [{}, {}] }), false)
  assert.equal(isClusterRow({ kind: 'addon', nodes: [{}] }), false)
})

test('split: cluster rows become one combined plan, everything else keeps its own way', () => {
  const vllm = item({ id: 'gallery:vllm', name: 'vllm', nodes: [{ nodeId: 'a', nodeName: 'gpu-a', version: '0.9.4', models: ['m1'] }] })
  const items = [item(), vllm, { id: 'gallery:whisper', kind: 'gallery', name: 'whisper' }]
  const rows = [
    { id: 'gallery:llama-cpp', kind: 'gallery', nodes: item().nodes },
    { id: 'gallery:vllm', kind: 'gallery', nodes: vllm.nodes },
    { id: 'gallery:whisper', kind: 'gallery' },
    { id: 'library:official/piper', kind: 'library' },
    { id: 'addon:notes', kind: 'addon' },
  ]
  const all = splitUpdate({ rows, selected: new Set(rows.map(r => r.id)), items })
  assert.deepEqual(all.entries.map(e => e.key), ['llama-cpp@a', 'llama-cpp@b', 'vllm@a'])
  assert.deepEqual([...all.restIds].sort(), ['addon:notes', 'gallery:whisper', 'library:official/piper'])
  const onlyOne = splitUpdate({ rows, selected: new Set(['gallery:vllm', 'addon:notes']), items })
  assert.deepEqual(onlyOne.entries.map(e => e.key), ['vllm@a'])
  assert.deepEqual([...onlyOne.restIds], ['addon:notes'])
  // Nothing selected on a cluster: no entry and no plain update for it.
  const none = splitUpdate({ rows, selected: new Set(['gallery:whisper']), items })
  assert.deepEqual(none.entries, [])
  assert.deepEqual([...none.restIds], ['gallery:whisper'])
  // A cluster row whose nodes all are current or unreachable has nothing to confirm and does not fall back.
  const current = item({ nodes: [{ nodeId: 'c', nodeName: 'cpu-c', status: 'healthy', version: '0.9.7' }] })
  const stuck = splitUpdate({ rows: [{ id: 'gallery:llama-cpp', kind: 'gallery', nodes: current.nodes }], selected: new Set(['gallery:llama-cpp']), items: [current] })
  assert.deepEqual(stuck.entries, [])
  assert.equal(stuck.restIds.size, 0)
})
