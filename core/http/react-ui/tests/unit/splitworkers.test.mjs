import test from 'node:test'
import assert from 'node:assert/strict'
import {
  arrivedSince, backendNodes, cutLimits, defaultSplitServer, limitsRelay, nodeChoices, pathOf, relayOf, slowAction, splitCommand,
  stateOf, summarize, topCallout, unattached,
} from '../../src/components/swarm/splitWorkersModel.js'

const worker = (extra = {}) => ({ id: 'w', state: 'online', path: { kind: 'direct' }, allow_slow_path: false, ...extra })

test('split workers: a state has a tone, an unknown state reads as offline', () => {
  assert.deepEqual(stateOf(worker()), { key: 'online', tone: 'ok' })
  assert.deepEqual(stateOf(worker({ state: 'slow' })), { key: 'slow', tone: 'warn' })
  assert.equal(stateOf(worker({ state: 'limited' })).tone, 'error')
  assert.deepEqual(stateOf(worker({ state: 'new-thing' })), { key: 'offline', tone: 'idle' })
})

test('split workers: Use anyway only for a slow worker, Stop using only for one used over a relay', () => {
  assert.equal(slowAction(worker({ state: 'slow', path: { kind: 'relayed' } })), 'useAnyway')
  assert.equal(slowAction(worker({ allow_slow_path: true, path: { kind: 'relayed' } })), 'stopUsing')
  // A worker that found a direct path needs no choice, even with Use anyway on.
  assert.equal(slowAction(worker({ allow_slow_path: true })), null)
  // A relay with a data limit can never carry model layers: no choice helps.
  assert.equal(slowAction(worker({ state: 'limited', path: { kind: 'relayed', limited: true } })), null)
  assert.equal(slowAction(worker({ state: 'offline' })), null)
})

test('split workers: the path names the relay and the round trip', () => {
  assert.deepEqual(pathOf(worker({ path: { kind: 'direct', rtt_ms: 3 } })), { key: 'direct', rtt: 3 })
  assert.deepEqual(pathOf(worker({ path: { kind: 'relayed', relay_label: 'home', rtt_ms: 52 } })), { key: 'relayed', relay: 'home', rtt: 52 })
  assert.equal(pathOf(worker({ path: { kind: 'relayed' } })).key, 'relayedUnknown')
  assert.deepEqual(pathOf(worker({ path: { kind: 'none' } })), { key: 'none', rtt: null })
})

test('split workers: the summary counts the online ones and lists the slow ones', () => {
  const s = summarize([worker(), worker({ id: 'b', state: 'slow' }), worker({ id: 'c', state: 'offline' })])
  assert.equal(s.total, 3)
  assert.equal(s.online, 1)
  assert.deepEqual(s.slow.map(w => w.id), ['b'])
  assert.equal(summarize(null).total, 0)
})

test('split workers: the add flow sees only workers that paired after it opened', () => {
  const since = Date.parse('2026-11-18T10:00:00Z')
  const list = [
    worker({ id: 'old', created_at: '2026-11-18T09:00:00Z' }),
    worker({ id: 'new', created_at: '2026-11-18T10:01:00Z' }),
    worker({ id: 'newer', created_at: '2026-11-18T10:02:00Z' }),
  ]
  assert.deepEqual(arrivedSince(list, since).map(w => w.id), ['newer', 'new'])
})

test('split workers: the command joins with the link and keeps its state', () => {
  const link = 'localai://pair?v=1&k=split&p=12D3KooW&c=ABCDE23456&e=1'
  assert.equal(splitCommand({ engine: 'llama', link, flavor: 'cli' }), `local-ai worker llama-cpp-rpc \\\n  --join "${link}"`)
  const docker = splitCommand({ engine: 'llama', link, flavor: 'docker', option: { dockerFlags: '--gpus all', tag: 'latest-gpu', devTag: 'master-gpu' }, dev: false })
  assert.match(docker, /--gpus all/)
  assert.match(docker, /-v localai-llama-cpp-rpc:\/p2p/)
  assert.match(docker, /LOCALAI_P2P_STATE_DIR=\/p2p/)
  assert.match(docker, /localai\/localai:latest-gpu worker llama-cpp-rpc/)
  // MLX runs on Apple Silicon outside a container.
  assert.equal(splitCommand({ engine: 'mlx', link, flavor: 'docker' }), `local-ai worker mlx-distributed \\\n  --join "${link}"`)
  assert.match(splitCommand({ engine: 'llama', flavor: 'cli' }), /<invite link>/)
})

test('split workers: a relay of the user that limits each connection is slow, with its cut and its limits dialog', () => {
  const own = worker({
    state: 'slow', own_relay: true, relay_id: 'r1',
    path: { kind: 'relayed', relay_label: 'home relay', limited: true, limit_bytes: 1024 ** 3, limit_seconds: 1800 },
  })
  assert.equal(slowAction(own), 'useAnyway')
  assert.deepEqual(cutLimits(own), ['1 GiB', '30 min'])
  assert.deepEqual(limitsRelay(own), { id: 'r1', label: 'home relay', mode: 'gated', limits: { circuit_bytes: 1024 ** 3, circuit_seconds: 1800 } })
  // Used anyway, it can still be stopped.
  assert.equal(slowAction({ ...own, state: 'online', allow_slow_path: true }), 'stopUsing')
  // A relay without limits cuts nothing; a public relay has no dialog.
  assert.deepEqual(cutLimits(worker({ state: 'slow', own_relay: true, relay_id: 'r1', path: { kind: 'relayed' } })), [])
  const pub = worker({ state: 'limited', path: { kind: 'relayed', limited: true, limit_bytes: 131072 } })
  assert.deepEqual(cutLimits(pub), [])
  assert.equal(limitsRelay(pub), null)
  assert.equal(slowAction(pub), null)
})

test('split workers: only split servers can hold a worker, by name', () => {
  const nodes = [
    { id: 'b', name: 'gpu-b', node_type: 'backend', split_server: true, status: 'healthy' },
    { id: 'a', name: 'agent-1', node_type: 'agent', split_server: true },
    { id: 'c', name: 'gpu-a', split_server: true, status: 'offline' },
    { id: 'd', name: 'plain', node_type: 'backend', status: 'healthy' },
  ]
  assert.deepEqual(nodeChoices(nodes), [{ id: 'c', name: 'gpu-a', online: false }, { id: 'b', name: 'gpu-b', online: true }])
  assert.deepEqual(nodeChoices(null), [])
  // The API answers an object with a nodes list on some versions.
  assert.deepEqual(nodeChoices({ nodes: [{ id: 'x', name: 'x', split_server: true, status: 'healthy' }] }), [{ id: 'x', name: 'x', online: true }])
  // Every backend node can be made a split server.
  assert.deepEqual(backendNodes(nodes).map(n => [n.id, n.splitServer]), [['c', true], ['b', true], ['d', false]])
})

test('split workers: a new worker goes to the only online split server, else the admin picks', () => {
  const s = (id, status = 'healthy') => ({ id, name: id, split_server: true, status })
  assert.equal(defaultSplitServer([]), '')
  assert.equal(defaultSplitServer([s('a'), { id: 'p', name: 'p', status: 'healthy' }]), 'a')
  assert.equal(defaultSplitServer([s('a'), s('b')]), '', 'two split servers: the admin picks')
  assert.equal(defaultSplitServer([s('a'), s('b', 'offline')]), 'a', 'an offline one does not count')
})

test('split workers: the relay admission of the node has a tone', () => {
  assert.deepEqual(relayOf({ node_id: 'n', node_relay: { state: 'admitted' } }), { key: 'admitted', tone: 'ok', reason: '' })
  assert.deepEqual(relayOf({ node_id: 'n', node_relay: { state: 'refused', reason: 'full' } }), { key: 'refused', tone: 'error', reason: 'full' })
  assert.equal(relayOf({ node_id: 'n', node_relay: { state: 'pending' } }).tone, 'warn')
  assert.equal(relayOf({ node_id: 'n', node_relay: { state: 'not_needed' } }).key, 'notNeeded')
  assert.equal(relayOf({ node_id: '' }), null, 'a worker attached to no node has no node relay')
  assert.equal(relayOf({ node_id: 'n' }), null)
})

test('split workers: in a cluster a worker attached to no node is not used', () => {
  const list = [worker({ id: 'a', node_id: 'n1' }), worker({ id: 'b' }), worker({ id: 'c', node_id: '' })]
  assert.deepEqual(unattached(list, true).map(w => w.id), ['b', 'c'])
  assert.deepEqual(unattached(list, false), [], 'a standalone instance runs the models itself')
})

test('split workers: the group shows one callout, the most important of those that apply', () => {
  assert.equal(topCallout({}), null)
  assert.equal(topCallout({ slow: true }), 'slow')
  assert.equal(topCallout({ slow: true, unattached: true, offline: true }), 'offline')
  assert.equal(topCallout({ offline: true, refused: true }), 'refused')
  assert.equal(topCallout({ cut: true, limited: true, slow: true }), 'limited')
  assert.equal(topCallout({ noServer: true, refused: true, slow: true }), 'noServer')
})
