import test from 'node:test'
import assert from 'node:assert/strict'
import {
  chipCounts, matchesChip, matchesQuery, nodeConnections, rosterSummary, rowHealth, splitAsRow, splitConnections,
} from '../../src/utils/workerRoster.js'

const live = route => ({ route, state: 'live' })

test('connections: each route of the route state is a connection, in NATS, WebSocket, P2P order', () => {
  const chips = nodeConnections({
    route_state: [live('heartbeat'), { route: 'tunnel', transport: 'websocket', state: 'live' }, live('nats')],
  })
  assert.deepEqual(chips.map(c => [c.kind, c.state, c.tone]), [['nats', 'live', 'ok'], ['websocket', 'live', 'ok']])
})

test('connections: a failing route keeps its reason, and a lost heartbeat shows', () => {
  const chips = nodeConnections({
    route_state: [{ route: 'heartbeat', state: 'down', reason: 'the heartbeat is stale' }, { route: 'nats', state: 'down', reason: 'no recent report' }],
  })
  assert.deepEqual(chips.map(c => [c.kind, c.tone, c.reason]), [['nats', 'error', 'no recent report'], ['heartbeat', 'error', 'the heartbeat is stale']])
})

test('connections: a P2P tunnel carries its path and whether a relay limits it', () => {
  const [chip] = nodeConnections({
    route_state: [live('heartbeat'), { route: 'tunnel', transport: 'p2p', state: 'live' }],
    tunnel_path: 'relayed', tunnel_limited: true,
  })
  assert.deepEqual([chip.kind, chip.path, chip.limited], ['p2p', 'relayed', true])
  assert.equal(nodeConnections({ route_state: [{ route: 'tunnel', transport: 'p2p', state: 'live' }], tunnel_path: 'relayed' })[0].pathKey, 'relayedUnknown')
})

test('connections: a reconnecting tunnel is a warning, an unknown state is down', () => {
  const chips = nodeConnections({ route_state: [{ route: 'tunnel', state: 'reconnecting' }, { route: 'nats', state: 'strange' }] })
  assert.deepEqual(chips.map(c => [c.kind, c.state, c.tone]), [['nats', 'down', 'error'], ['websocket', 'reconnecting', 'warn']])
})

test('connections: without route state the report of the node decides, and nothing reported shows nothing', () => {
  assert.deepEqual(nodeConnections({ attached: 'nats,tunnel' }).map(c => c.kind), ['nats'])
  assert.deepEqual(nodeConnections({ attached: 'tunnel' }).map(c => [c.kind, c.state]), [['websocket', 'live']])
  assert.deepEqual(nodeConnections({ peer_id: 'abc', tunnel_path: 'direct' }).map(c => [c.kind, c.path]), [['p2p', 'direct']])
  assert.deepEqual(nodeConnections({}), [])
  assert.deepEqual(nodeConnections({ route_state: [live('heartbeat')] }), [])
})

test('connections: a model-split worker has one P2P connection with its path and round trip', () => {
  const [chip] = splitConnections({ state: 'online', path: { kind: 'hole_punched', rtt_ms: 41 } })
  assert.deepEqual([chip.kind, chip.state, chip.pathKey, chip.rtt], ['p2p', 'live', 'holePunched', 41])
  const [off] = splitConnections({ state: 'offline' })
  assert.deepEqual([off.state, off.tone], ['down', 'idle'])
})

const nodes = [
  { id: 'a', name: 'worker-a', status: 'healthy', node_type: 'backend', labels: { gpu: 'nvidia' }, in_flight_count: 2, model_count: 2 },
  { id: 'b', name: 'worker-b', status: 'pending', node_type: 'backend', labels: {}, in_flight_count: 0, model_count: 0 },
  { id: 'c', name: 'worker-c', status: 'offline', node_type: 'agent', labels: {}, in_flight_count: 0, model_count: 0 },
]
const reasons = new Map([['b', ['pending']], ['c', ['down']]])

test('roster: a model-split worker is a row that the filters and the sort read like a node', () => {
  const row = splitAsRow({ id: 'w1', label: 'split-1', state: 'online', peer_id: 'peerid', memory_bytes: 8 })
  assert.equal(row.id, 'split:w1')
  assert.equal(row.name, 'split-1')
  assert.equal(row.node_type, 'model-split')
  assert.equal(row.total_ram, 8)
  assert.deepEqual(rowHealth(row), ['healthy'])
  assert.deepEqual(rowHealth(splitAsRow({ id: 'w2', state: 'slow' })), ['attention'])
  assert.deepEqual(rowHealth(splitAsRow({ id: 'w3', state: 'offline' })), ['offline'])
  assert.deepEqual(rowHealth(splitAsRow({ id: 'w4', state: 'refused' })), ['attention'])
})

test('roster: chips count the rows of each health, a node can be in two', () => {
  const rows = [...nodes, splitAsRow({ id: 'w1', state: 'online' })]
  assert.deepEqual(chipCounts(rows, reasons), { all: 4, healthy: 2, attention: 2, offline: 1, pending: 1 })
  assert.equal(matchesChip(nodes[1], 'attention', reasons), true)
  assert.equal(matchesChip(nodes[1], 'healthy', reasons), false)
  assert.equal(matchesChip(nodes[0], 'all', reasons), true)
})

test('roster: the search reads the name, address, labels and the models of a row', () => {
  assert.equal(matchesQuery(nodes[0], 'nvidia', null), true)
  assert.equal(matchesQuery(nodes[0], 'gpu=nvidia', null), true)
  assert.equal(matchesQuery(nodes[0], 'llama', ['llama-3.1-8b']), true)
  assert.equal(matchesQuery(nodes[0], 'qwen', ['llama-3.1-8b']), false)
  assert.equal(matchesQuery(nodes[0], '  ', null), true)
})

test('roster: the summary counts online, models, requests in flight and what needs attention', () => {
  const rows = [...nodes, splitAsRow({ id: 'w1', state: 'slow' })]
  const models = new Map([['a', ['m1', 'm2']]])
  const sum = rosterSummary(rows, reasons, models)
  assert.deepEqual(
    [sum.total, sum.online, sum.loaded, sum.hosts, sum.inFlight, sum.attention, sum.offline, sum.degraded],
    [4, 1, 2, 1, 2, 3, 1, 2],
  )
  const fallback = rosterSummary(rows, reasons, null)
  assert.deepEqual([fallback.loaded, fallback.hosts], [2, 1])
})

test('update: one worker offers an update only for a backend that drifted on it', async () => {
  const { updateTargetsForNode } = await import('../../src/utils/swarm.js')
  const nodes = [
    { id: 'a', status: 'healthy', node_type: 'backend' },
    { id: 'b', status: 'healthy', node_type: 'backend' },
  ]
  const upgrades = {
    llama: { backend_name: 'llama', available_version: '2', node_drift: [{ node_id: 'a' }] },
    whisper: { backend_name: 'whisper', available_version: '3' },
  }
  assert.deepEqual(updateTargetsForNode({ upgrades, nodes, nodeId: 'a' }).pairs, [{ backend: 'llama', nodeId: 'a' }, { backend: 'whisper', nodeId: 'a' }])
  assert.deepEqual(updateTargetsForNode({ upgrades, nodes, nodeId: 'b' }).pairs, [{ backend: 'whisper', nodeId: 'b' }])
  assert.deepEqual(updateTargetsForNode({ upgrades: {}, nodes, nodeId: 'b' }).pairs, [])
})
