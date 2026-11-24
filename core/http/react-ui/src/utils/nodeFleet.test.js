import assert from 'node:assert/strict'
import test from 'node:test'

import {
  capacityReading,
  filterModels,
  filterNodes,
  groupModels,
  groupNodes,
  paginateModels,
  paginateNodes,
  runBounded,
  sortModels,
  sortNodes,
  summarizeFleet,
  nodeLifecycleAction,
} from './nodeFleet.js'

const modelRows = [
  { id: 'r1', node_id: 'n1', model_name: 'Llama 3.2', replica_index: 0, address: '10.0.0.1:50051', in_flight: 2, backend_type: 'llama-cpp', last_used: '2026-09-14T10:00:00Z' },
  { id: 'r2', node_id: 'n1', model_name: 'Llama 3.2', replica_index: 1, address: '10.0.0.1:50052', in_flight: -4, backend_type: 'llama-cpp', last_used: 'invalid' },
  { id: 'r3', node_id: 'n2', model_name: 'Llama 3.2', replica_index: 0, address: '10.0.0.2:50051', in_flight: 3.8, backend_type: ' vllm ', last_used: '2026-09-14T11:00:00Z' },
  { id: 'r4', node_id: '', model_name: 'Whisper', replica_index: 0, address: '', in_flight: Number.NaN, backend_type: '', last_used: null },
]

test('groups model replicas across nodes and normalizes defensive aggregate values', () => {
  const grouped = groupModels(modelRows)

  assert.equal(grouped.length, 2)
  assert.deepEqual(grouped[0], {
    model_name: 'Llama 3.2',
    replicas: modelRows.slice(0, 3),
    replica_count: 3,
    node_count: 2,
    in_flight: 5,
    backend_types: ['llama-cpp', 'vllm'],
    last_used: '2026-09-14T11:00:00Z',
  })
  assert.deepEqual(grouped[1], {
    model_name: 'Whisper',
    replicas: [modelRows[3]],
    replica_count: 1,
    node_count: 0,
    in_flight: 0,
    backend_types: [],
    last_used: null,
  })
})

test('ignores malformed model rows while preserving valid replicas and input order', () => {
  const input = [null, {}, { model_name: '  ' }, ...modelRows]
  const original = [...input]

  assert.deepEqual(groupModels(input).flatMap(model => model.replicas), modelRows)
  assert.deepEqual(input, original)
  assert.deepEqual(groupModels(null), [])
})

test('filters grouped models by name and backend type case-insensitively', () => {
  const grouped = groupModels(modelRows)

  assert.deepEqual(filterModels(grouped, 'LLAMA').map(model => model.model_name), ['Llama 3.2'])
  assert.deepEqual(filterModels(grouped, 'VLLM').map(model => model.model_name), ['Llama 3.2'])
  assert.equal(filterModels(grouped, '').length, 2)
})

test('sorts grouped models stably across every roster column without mutation', () => {
  const input = [
    { model_name: 'Zulu', replica_count: 2, node_count: 1, in_flight: 4, last_used: null },
    { model_name: 'Alpha', replica_count: 2, node_count: 2, in_flight: 1, last_used: '2026-09-14T09:00:00Z' },
    { model_name: 'Beta', replica_count: 1, node_count: 3, in_flight: 1, last_used: '2026-09-14T10:00:00Z' },
  ]
  const original = [...input]

  assert.deepEqual(sortModels(input, { key: 'replica_count', direction: 'desc' }).map(model => model.model_name), ['Alpha', 'Zulu', 'Beta'])
  assert.deepEqual(sortModels(input, { key: 'node_count', direction: 'desc' }).map(model => model.model_name), ['Beta', 'Alpha', 'Zulu'])
  assert.deepEqual(sortModels(input, { key: 'in_flight', direction: 'asc' }).map(model => model.model_name), ['Alpha', 'Beta', 'Zulu'])
  assert.deepEqual(sortModels(input, { key: 'last_used', direction: 'desc' }).map(model => model.model_name), ['Beta', 'Alpha', 'Zulu'])
  assert.deepEqual(input, original)
})

test('paginates grouped models at 50 rows and clamps after filtering', () => {
  const input = Array.from({ length: 1000 }, (_, index) => ({ model_name: `model-${String(index).padStart(4, '0')}` }))
  const page = paginateModels(input, 20)

  assert.equal(page.pageSize, 50)
  assert.equal(page.totalPages, 20)
  assert.equal(page.items.length, 50)
  assert.equal(page.items[0].model_name, 'model-0950')
  assert.equal(paginateModels(input.slice(0, 7), 20).page, 1)
})

const nodes = [
  {
    id: 'new',
    name: 'Atlas',
    address: '10.0.0.1:50051',
    node_type: 'backend',
    status: 'healthy',
    total_vram: 100,
    available_vram: 5,
    total_ram: 200,
    available_ram: 250,
    total_disk: 1_000,
    available_disk: -20,
    cpu_logical_cores: 8,
    cpu_usage_percent: 25,
    cpu_load_1: 1.5,
    model_count: 3,
    gpu_vendor: 'NVIDIA',
    capability: 'nvidia-cuda-13',
    labels: { zone: 'East', team: 'inference' },
  },
  {
    id: 'old',
    name: 'Birch',
    address: 'worker-old.internal',
    node_type: 'agent',
    status: 'offline',
    total_vram: 0,
    available_vram: 0,
    total_ram: 100,
    available_ram: 10,
    model_count: 0,
    gpu_vendor: 'unknown',
    labels: { zone: 'West' },
  },
  {
    id: 'pending',
    name: 'Cedar',
    node_type: 'backend',
    status: 'pending',
    total_vram: 100,
    available_vram: 80,
    total_ram: Number.NaN,
    available_ram: 10,
    total_disk: Infinity,
    available_disk: 2,
    cpu_logical_cores: 4,
    cpu_usage_percent: 150,
    cpu_load_1: -3,
    labels: {},
  },
]

test('summarizes mixed worker generations and clamps malformed capacity readings', () => {
  const summary = summarizeFleet(nodes)

  assert.deepEqual(summary.health, {
    total: 3,
    healthy: 1,
    draining: 0,
    pending: 1,
    offline: 1,
    unhealthy: 0,
    other: 0,
  })
  const { usagePercent: vramUsagePercent, ...vram } = summary.vram
  assert.deepEqual(vram, {
    total: 200,
    used: 115,
    available: 85,
    reportingCount: 2,
    unknownCount: 1,
  })
  assert.ok(Math.abs(vramUsagePercent - 57.5) < Number.EPSILON * 100)
  assert.deepEqual(summary.ram, {
    total: 300,
    used: 90,
    available: 210,
    usagePercent: 30,
    reportingCount: 2,
    unknownCount: 1,
  })
  assert.deepEqual(summary.disk, {
    total: 1_000,
    used: 1_000,
    available: 0,
    usagePercent: 100,
    reportingCount: 1,
    unknownCount: 2,
  })
  assert.deepEqual(summary.cpu, {
    totalLogicalCores: 12,
    busyCoreEquivalents: 6,
    idleCoreEquivalents: 6,
    usagePercent: 50,
    load1: 1.5,
    reportingCount: 2,
    unknownCount: 1,
  })
})

test('deduplicates headline attention while retaining every matching category', () => {
  const { attention, attentionNodeCount } = summarizeFleet(nodes)

  assert.equal(attentionNodeCount, 3)
  assert.deepEqual(attention, {
    pending: ['pending'],
    offlineOrUnhealthy: ['old'],
    lowVRAM: ['new'],
    lowRAM: ['old'],
    lowDisk: ['new'],
  })
})

test('does not flag zero-total or invalid-total capacity as exhausted', () => {
  const summary = summarizeFleet([
    { id: 'zero', status: 'healthy', total_vram: 0, available_vram: 0 },
    { id: 'bad', status: 'healthy', total_ram: -10, available_ram: 0, total_vram: 8, available_vram: Number.NaN },
  ])

  assert.equal(summary.attentionNodeCount, 0)
  assert.deepEqual(summary.attention.lowVRAM, [])
  assert.equal(summary.vram.unknownCount, 2)
  assert.equal(summary.ram.unknownCount, 2)
})

test('requires finite total and available values before reporting capacity', () => {
  const summary = summarizeFleet([
    { id: 'complete', status: 'healthy', total_vram: 100, available_vram: 120 },
    { id: 'missing', status: 'healthy', total_vram: 200 },
    { id: 'malformed', status: 'healthy', total_vram: 300, available_vram: '30' },
    { id: 'non-finite', status: 'healthy', total_vram: 400, available_vram: Infinity },
  ])

  assert.deepEqual(summary.vram, {
    total: 100,
    used: 0,
    available: 100,
    usagePercent: 0,
    reportingCount: 1,
    unknownCount: 3,
  })
  assert.deepEqual(summary.attention.lowVRAM, [])
  assert.deepEqual(capacityReading(100, -20), { total: 100, used: 100, available: 0, usagePercent: 100 })
  assert.equal(capacityReading(100, undefined), null)
  assert.equal(capacityReading(100, Number.NaN), null)
})

test('maps only server-accepted lifecycle states to controls', () => {
  assert.equal(nodeLifecycleAction('healthy'), 'drain')
  assert.equal(nodeLifecycleAction('draining'), 'resume')
  assert.equal(nodeLifecycleAction('pending'), 'approve')
  for (const status of ['unhealthy', 'offline', 'unknown', '', null, 'HEALTHY']) {
    assert.equal(nodeLifecycleAction(status), null)
  }
})

test('requires a complete finite CPU reading before including a node', () => {
  const summary = summarizeFleet([
    { id: 'complete', cpu_logical_cores: 4, cpu_usage_percent: 50, cpu_load_1: 0.5 },
    { id: 'missing-load', cpu_logical_cores: 8, cpu_usage_percent: 25 },
    { id: 'bad-usage', cpu_logical_cores: 2, cpu_usage_percent: Number.NaN, cpu_load_1: 1 },
  ])

  assert.deepEqual(summary.cpu, {
    totalLogicalCores: 4,
    busyCoreEquivalents: 2,
    idleCoreEquivalents: 2,
    usagePercent: 50,
    load1: 0.5,
    reportingCount: 1,
    unknownCount: 2,
  })
})

test('filters without mutation across roster fields and labels case-insensitively', () => {
  const original = [...nodes]

  assert.deepEqual(filterNodes(nodes, { query: 'CUDA-13', statuses: [], types: [] }).map(node => node.id), ['new'])
  assert.deepEqual(filterNodes(nodes, { query: 'east', statuses: [], types: [] }).map(node => node.id), ['new'])
  assert.deepEqual(filterNodes(nodes, { query: '3', statuses: [], types: [] }).map(node => node.id), ['new'])
  assert.deepEqual(filterNodes(nodes, { query: '', statuses: ['OFFLINE'], types: ['AGENT'] }).map(node => node.id), ['old'])
  assert.deepEqual(nodes, original)
})

test('sorts stably with node name as the final tie-breaker and leaves input untouched', () => {
  const input = [
    { id: 'z1', name: 'Zulu', model_count: 2 },
    { id: 'a1', name: 'Alpha', model_count: 2 },
    { id: 'a2', name: 'Alpha', model_count: 2 },
    { id: 'b1', name: 'Beta', model_count: 1 },
  ]

  assert.deepEqual(sortNodes(input, { key: 'model_count', direction: 'asc' }).map(node => node.id), ['b1', 'a1', 'a2', 'z1'])
  assert.deepEqual(sortNodes(input, { key: 'model_count', direction: 'desc' }).map(node => node.id), ['a1', 'a2', 'z1', 'b1'])
  assert.deepEqual(input.map(node => node.id), ['z1', 'a1', 'a2', 'b1'])
})

test('groups missing labels separately from a real label value equal to unlabelled', () => {
  const grouped = groupNodes([
    { id: 'missing', name: 'Missing', labels: {} },
    { id: 'literal', name: 'Literal', labels: { zone: 'unlabelled' } },
    { id: 'east', name: 'East', labels: { zone: 'east' } },
  ], 'label:zone')

  assert.equal(grouped.length, 3)
  assert.equal(new Set(grouped.map(group => group.key)).size, 3)
  assert.deepEqual(grouped.map(group => ({ label: group.label, ids: group.nodes.map(node => node.id) })), [
    { label: 'east', ids: ['east'] },
    { label: 'unlabelled', ids: ['literal'] },
    { label: 'Unlabelled', ids: ['missing'] },
  ])
})

test('groups by node type and treats none as one group', () => {
  assert.deepEqual(groupNodes(nodes, 'node_type').map(group => group.label), ['agent', 'backend'])
  assert.deepEqual(groupNodes(nodes, 'none'), [{ key: 'all', label: 'All nodes', nodes }])
})

test('paginates with a default of 50 rows and clamps the requested page', () => {
  const input = Array.from({ length: 61 }, (_, index) => ({ id: index + 1 }))

  assert.deepEqual(paginateNodes(input, 99), {
    page: 2,
    pageSize: 50,
    totalItems: 61,
    totalPages: 2,
    items: input.slice(50),
  })
  assert.deepEqual(paginateNodes([], -4), {
    page: 1,
    pageSize: 50,
    totalItems: 0,
    totalPages: 1,
    items: [],
  })
})

test('runs at most eight operations concurrently and preserves settled result order', async () => {
  let active = 0
  let peak = 0
  const input = Array.from({ length: 25 }, (_, index) => index)

  const results = await runBounded(input, 8, async item => {
    active += 1
    peak = Math.max(peak, active)
    await new Promise(resolve => setTimeout(resolve, (25 - item) % 4))
    active -= 1
    if (item === 7) throw new Error('seven failed')
    return item * 2
  })

  assert.equal(peak, 8)
  assert.equal(results.length, 25)
  assert.deepEqual(results[0], { status: 'fulfilled', value: 0 })
  assert.equal(results[7].status, 'rejected')
  assert.equal(results[7].reason.message, 'seven failed')
  assert.deepEqual(results[24], { status: 'fulfilled', value: 48 })
})

test('rejects a non-positive concurrency limit', async () => {
  await assert.rejects(runBounded([1], 0, async value => value), RangeError)
})
