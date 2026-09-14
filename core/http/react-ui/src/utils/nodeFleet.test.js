import assert from 'node:assert/strict'
import test from 'node:test'

import {
  filterNodes,
  groupNodes,
  paginateNodes,
  runBounded,
  sortNodes,
  summarizeFleet,
} from './nodeFleet.js'

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
  assert.equal(summary.vram.unknownCount, 1)
  assert.equal(summary.ram.unknownCount, 2)
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
