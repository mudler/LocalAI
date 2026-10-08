import assert from 'node:assert/strict'
import test from 'node:test'

import {
  attentionOf, eligibleNodes, leavePreview, matchesSelector, memoryOf, nodeState, p2pCommand,
  ruleKind, rulePlan, selectorOf, singleReplicaModels, updateTargets, workerCommand,
} from './swarm.js'

const GB = 1024 ** 3
const node = (id, extra = {}) => ({ id, name: id, node_type: 'backend', status: 'healthy', labels: {}, ...extra })

test('names the state of a node in the words the pages use', () => {
  assert.deepEqual(nodeState({ status: 'healthy' }), { key: 'healthy', level: 'ok' })
  assert.equal(nodeState({ status: 'unhealthy' }).key, 'down')
  assert.equal(nodeState({ status: 'offline' }).key, 'offline')
  assert.equal(nodeState({ status: 'pending' }).level, 'warn')
  assert.equal(nodeState({ status: 'something-new' }).key, 'unknown')
})

test('flags pending, lost and low-memory nodes, and never a node that does not report', () => {
  const reasons = attentionOf([
    node('a'),
    node('b', { status: 'pending' }),
    node('c', { status: 'offline' }),
    node('d', { total_vram: 10 * GB, available_vram: 0.5 * GB }),
    node('e', { total_vram: 10 * GB }),
  ])
  assert.deepEqual([...reasons.keys()].sort(), ['b', 'c', 'd'])
  assert.deepEqual(reasons.get('d'), ['lowVram'])
})

test('reads GPU memory when a node has it and system memory otherwise', () => {
  assert.equal(memoryOf(node('a', { total_vram: 24 * GB, available_vram: 12 * GB })).kind, 'gpu')
  assert.equal(memoryOf(node('b', { total_ram: 8 * GB, available_ram: 4 * GB })).kind, 'ram')
  assert.equal(memoryOf(node('c')), null)
})

test('matches a selector the way the scheduler does: every pair, exactly', () => {
  const n = node('a', { labels: { zone: 'b', gpu: '4090' } })
  assert.equal(matchesSelector(n, { zone: 'b' }), true)
  assert.equal(matchesSelector(n, { zone: 'b', gpu: '3090' }), false)
  assert.equal(matchesSelector(n, {}), true)
  assert.deepEqual(selectorOf({ node_selector: '{"zone":"b"}' }), { zone: 'b' })
  assert.deepEqual(selectorOf({ node_selector: 'not json' }), {})
})

test('only healthy backend workers are eligible', () => {
  const nodes = [node('a'), node('b', { status: 'draining' }), node('c', { node_type: 'agent' }), node('d', { status: 'pending' })]
  assert.deepEqual(eligibleNodes(nodes, {}).map(n => n.id), ['a'])
})

test('tells the four kinds of rule apart', () => {
  assert.equal(ruleKind({ spread_all: true }), 'spread')
  assert.equal(ruleKind({ min_replicas: 2 }), 'autoscale')
  assert.equal(ruleKind({ node_selector: { zone: 'a' } }), 'placement')
  assert.equal(ruleKind({}), 'inactive')
})

test('plans an auto-scaling rule across the roomiest eligible nodes and reports a shortfall', () => {
  const nodes = [
    node('small', { available_vram: 4 * GB, labels: { gpu: 'x' } }),
    node('big', { available_vram: 20 * GB, labels: { gpu: 'x' } }),
    node('other', { available_vram: 30 * GB, labels: { gpu: 'y' } }),
  ]
  const plan = rulePlan({ min_replicas: 3, node_selector: { gpu: 'x' } }, nodes)
  assert.deepEqual(plan.planned.map(p => p.node.id), ['big', 'small'])
  assert.equal(plan.wanted, 3)
  assert.equal(plan.shortfall, 1)
})

test('lets a node with a higher replica cap take several replicas', () => {
  const plan = rulePlan({ min_replicas: 3 }, [node('a', { max_replicas_per_model: 3 })])
  assert.equal(plan.planned[0].replicas, 3)
  assert.equal(plan.shortfall, 0)
})

test('a spread rule puts one replica on every matching node', () => {
  const plan = rulePlan({ spread_all: true, node_selector: { role: 'edge' } }, [
    node('a', { labels: { role: 'edge' } }), node('b', { labels: { role: 'edge' } }), node('c'),
  ])
  assert.deepEqual(plan.planned.map(p => p.node.id), ['a', 'b'])
})

test('previews a drain: requests, models that stay, models that wait, models that reload', () => {
  const a = node('a', { in_flight_count: 3 })
  const b = node('b')
  const c = node('c', { labels: { gpu: '4090' } })
  const rows = [
    { node_id: 'a', model_name: 'qwen', state: 'loaded' },
    { node_id: 'b', model_name: 'qwen', state: 'loaded' },
    { node_id: 'a', model_name: 'llama', state: 'loaded' },
    { node_id: 'a', model_name: 'flux', state: 'loaded' },
  ]
  const rules = [{ model_name: 'llama', node_selector: { gpu: '4090' } }, { model_name: 'flux', node_selector: { gpu: 'h100' } }]
  const items = leavePreview({ node: a, rows, nodes: [a, b, c], rules })
  assert.deepEqual(items[0], { kind: 'inflight', count: 3 })
  assert.deepEqual(items.find(i => i.model === 'qwen'), { kind: 'stays', model: 'qwen', on: ['b'] })
  assert.deepEqual(items.find(i => i.model === 'llama'), { kind: 'reload', model: 'llama', candidates: 1 })
  assert.deepEqual(items.find(i => i.model === 'flux'), { kind: 'blocked', model: 'flux', selector: { gpu: 'h100' } })
})

test('a model on a draining or lost node does not count as staying elsewhere', () => {
  const a = node('a')
  const b = node('b', { status: 'draining' })
  const rows = [{ node_id: 'a', model_name: 'm' }, { node_id: 'b', model_name: 'm' }]
  assert.equal(leavePreview({ node: a, rows, nodes: [a, b], rules: [] })[0].kind, 'reload')
})

test('finds the models that run on a single replica', () => {
  const nodes = [node('a'), node('b'), node('gone', { status: 'offline' })]
  const rows = [
    { node_id: 'a', model_name: 'one', state: 'loaded' },
    { node_id: 'a', model_name: 'two', state: 'loaded' },
    { node_id: 'b', model_name: 'two', state: 'loaded' },
    { node_id: 'gone', model_name: 'three', state: 'loaded' },
  ]
  assert.deepEqual(singleReplicaModels(rows, nodes), ['one'])
})

test('a bulk update goes to the nodes that drifted, or to every healthy backend node', () => {
  const nodes = [node('a'), node('b'), node('c', { node_type: 'agent' }), node('d', { status: 'offline' })]
  const withDrift = { x: { backend_name: 'x', available_version: '2', node_drift: [{ node_id: 'b' }] } }
  assert.deepEqual(updateTargets({ upgrades: withDrift, nodes }).nodeIds, ['b'])
  const noDrift = { x: { backend_name: 'x', available_version: '2' } }
  assert.deepEqual(updateTargets({ upgrades: noDrift, nodes }).nodeIds, ['a', 'b'])
  assert.deepEqual(updateTargets({ upgrades: noDrift, nodes, selectedIds: new Set(['a']) }).nodeIds, ['a'])
  assert.deepEqual(updateTargets({ upgrades: { y: { backend_name: 'y' } }, nodes }).pairs, [])
})

test('builds join commands that carry no secret', () => {
  const option = { dockerFlags: '--gpus all', tag: 'latest-gpu-nvidia-cuda-12', devTag: 'master-gpu-nvidia-cuda-12' }
  const docker = workerCommand({ flavor: 'docker', nodeType: 'backend', option, dev: false, frontend: 'https://x.test' })
  assert.match(docker, /--gpus all/)
  assert.match(docker, /LOCALAI_REGISTER_TO="https:\/\/x.test"/)
  assert.match(docker, /\$TOKEN/)
  assert.match(docker, / worker$/)
  const cli = workerCommand({ flavor: 'cli', nodeType: 'agent', option, dev: false, frontend: 'https://x.test' })
  assert.match(cli, /^local-ai agent-worker/)
  assert.match(cli, /\$LOCALAI_REGISTRATION_TOKEN/)
  assert.match(p2pCommand({ method: 'peer', option, dev: false, token: 'abc', flavor: 'docker' }), /run --federated --p2p$/)
  assert.match(p2pCommand({ method: 'mlx', option, dev: true, token: '', flavor: 'docker' }), /your-token-here/)
  assert.match(p2pCommand({ method: 'server', option, dev: false, token: 't', flavor: 'docker' }), /--name local-ai-federated[\s\S]*federated$/)
  assert.match(p2pCommand({ method: 'shard', option, dev: false, token: 't', flavor: 'cli' }), /^TOKEN="t" local-ai worker p2p-llama-cpp-rpc$/)
})
