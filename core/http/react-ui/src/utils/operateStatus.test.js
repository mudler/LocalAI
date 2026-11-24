import assert from 'node:assert/strict'
import test from 'node:test'

import {
  MAX_SAMPLES,
  appendSample,
  backendDependents,
  capacityOf,
  capacityProblems,
  chartGeometry,
  isFirstRun,
  ledgerRows,
  recommendedBackend,
  statusHeadline,
} from './operateStatus.js'

const GB = 1024 ** 3

const gpuHost = {
  type: 'gpu',
  gpus: [{ name: 'RTX 4090', total_vram: 24 * GB, used_vram: 18 * GB }],
  aggregate: { total_memory: 24 * GB, used_memory: 18 * GB, usage_percent: 75, gpu_count: 1 },
  ram: { total: 64 * GB, used: 32 * GB },
  disk: { total: 1000 * GB, available: 600 * GB },
}

test('reads GPU memory on a host with a GPU and system memory without one', () => {
  const gpu = capacityOf({ resources: gpuHost })
  assert.equal(gpu.kind, 'gpu')
  assert.equal(gpu.used, 18 * GB)
  assert.equal(Math.round(gpu.pct), 75)
  assert.equal(gpu.device, 'RTX 4090')

  const ram = capacityOf({ resources: { type: 'ram', ram: { total: 16 * GB, used: 4 * GB }, aggregate: {} } })
  assert.equal(ram.kind, 'ram')
  assert.equal(ram.pct, 25)
})

test('sums the devices when the reading has no aggregate', () => {
  const pool = capacityOf({ resources: { type: 'gpu', gpus: [
    { total_vram: 10 * GB, used_vram: 4 * GB },
    { total_vram: 10 * GB, used_vram: 6 * GB },
  ], aggregate: { gpu_count: 2 } } })
  assert.equal(pool.total, 20 * GB)
  assert.equal(pool.used, 10 * GB)
})

test('has no capacity when the host reports none', () => {
  assert.equal(capacityOf({ resources: null }), null)
  assert.equal(capacityOf({ resources: {} }), null)
})

test('sums the workers of a cluster and ignores agent workers', () => {
  const nodes = [
    { id: 'a', total_vram: 24 * GB, available_vram: 12 * GB },
    { id: 'b', total_vram: 24 * GB, available_vram: 24 * GB },
    { id: 'c', node_type: 'agent', total_vram: 99 * GB, available_vram: 0 },
  ]
  const pool = capacityOf({ nodes, distributed: true })
  assert.equal(pool.kind, 'cluster-gpu')
  assert.equal(pool.total, 48 * GB)
  assert.equal(pool.used, 12 * GB)
  assert.equal(pool.nodes, 2)
})

test('falls back to this host when no worker reports memory', () => {
  const pool = capacityOf({ resources: gpuHost, distributed: true, nodes: [{ id: 'a', status: 'healthy' }] })
  assert.equal(pool.kind, 'gpu')
  assert.equal(pool.total, 24 * GB)
})

test('leaves out a node that is not answering', () => {
  const pool = capacityOf({
    distributed: true,
    nodes: [
      { id: 'a', status: 'healthy', total_vram: 24 * GB, available_vram: 12 * GB },
      { id: 'b', status: 'unhealthy', total_vram: 24 * GB, available_vram: 0 },
      { id: 'c', status: 'draining', total_vram: 24 * GB, available_vram: 24 * GB },
    ],
  })
  assert.equal(pool.nodes, 2)
  assert.equal(pool.total, 48 * GB)
})

test('flags a full memory pool and a low models disk', () => {
  assert.deepEqual(capacityProblems({ resources: gpuHost }), [])
  const full = { ...gpuHost, aggregate: { total_memory: 24 * GB, used_memory: 22 * GB } }
  const [memory] = capacityProblems({ resources: full })
  assert.equal(memory.key, 'memory')
  assert.equal(memory.level, 'warn')
  const brimming = { ...gpuHost, aggregate: { total_memory: 24 * GB, used_memory: 24 * GB } }
  assert.equal(capacityProblems({ resources: brimming })[0].level, 'error')
  const lowDisk = { ...gpuHost, disk: { total: 1000 * GB, available: 10 * GB } }
  assert.equal(capacityProblems({ resources: lowDisk }).find(p => p.key === 'disk').level, 'warn')
})

test('keeps the rolling buffer bounded and skips unreadable readings', () => {
  let samples = []
  samples = appendSample(samples, null, 1)
  assert.equal(samples.length, 0)
  for (let i = 1; i <= MAX_SAMPLES + 20; i += 1) {
    samples = appendSample(samples, { kind: 'gpu', used: i, total: 100 }, i * 1000)
  }
  assert.equal(samples.length, MAX_SAMPLES)
  assert.equal(samples[0].used, 21)
  assert.equal(samples[samples.length - 1].used, MAX_SAMPLES + 20)
})

test('a second reading at the same instant replaces the first', () => {
  let samples = appendSample([], { kind: 'gpu', used: 1, total: 10 }, 5000)
  samples = appendSample(samples, { kind: 'gpu', used: 2, total: 10 }, 5000)
  assert.equal(samples.length, 1)
  assert.equal(samples[0].used, 2)
})

test('draws nothing for fewer than two samples', () => {
  assert.equal(chartGeometry([]), null)
  assert.equal(chartGeometry([{ t: 1, used: 1, total: 2 }]), null)
})

test('draws the axis from zero to the capacity and marks the capacity line', () => {
  const samples = [
    { t: 0, used: 6 * GB, total: 24 * GB },
    { t: 60_000, used: 12 * GB, total: 24 * GB },
    { t: 120_000, used: 18 * GB, total: 24 * GB },
  ]
  const g = chartGeometry(samples)
  assert.equal(g.peak, 24 * GB)
  assert.equal(g.capacityY, g.top)
  assert.ok(g.points[0].y > g.points[2].y, 'more memory sits higher')
  assert.ok(g.points[2].y > g.capacityY, 'below the capacity line')
  assert.equal(g.end.used, 18 * GB)
  assert.equal(g.xTicks.length, 4)
  assert.equal(chartGeometry(samples, { width: 300, right: 92, left: 36 }).xTicks.length, 3)
})

test('the axis grows past the capacity rather than clipping a reading above it', () => {
  const g = chartGeometry([
    { t: 0, used: 10, total: 20 },
    { t: 10, used: 30, total: 20 },
  ])
  assert.equal(g.peak, 30)
  assert.ok(g.capacityY > g.top)
})

test('the headline names what needs a person, then what is only worth a look', () => {
  assert.deepEqual(statusHeadline({ needs: 3 }), { kind: 'needs', count: 3 })
  assert.deepEqual(statusHeadline({ needs: 0, looking: 2 }), { kind: 'looking', count: 2 })
  assert.deepEqual(statusHeadline({}), { kind: 'ok', count: 0 })
  assert.deepEqual(statusHeadline({ needs: 4, firstRun: true }), { kind: 'firstRun', count: 0 })
})

test('says first run only when every answer is in and all of them are empty', () => {
  assert.equal(isFirstRun({ backends: 0, models: 0 }), true)
  assert.equal(isFirstRun({ backends: null, models: 0 }), false)
  assert.equal(isFirstRun({ backends: 0, models: 0, operations: [{}] }), false)
  assert.equal(isFirstRun({ backends: 0, models: 0, loaded: 1 }), false)
  assert.equal(isFirstRun({ backends: 1, models: 0 }), false)
})

test('only a row with a problem opens', () => {
  const quiet = ledgerRows({})
  assert.deepEqual(quiet.map(r => r.open), [false, false, false, false])
  const rows = ledgerRows({
    attention: [{ kind: 'backend-update' }],
    memoryProblems: [{ level: 'warn' }],
    requestErrors: 2,
    running: 3,
  })
  const byId = Object.fromEntries(rows.map(r => [r.id, r]))
  assert.equal(byId.needs.open, true)
  assert.equal(byId.needs.level, 'warn')
  assert.equal(byId.capacity.open, true)
  assert.equal(byId.failures.open, true)
  assert.equal(byId.running.open, false)
  assert.equal(byId.running.level, 'active')
})

test('a failed operation or an unhealthy node is an error, an update only a warning', () => {
  assert.equal(ledgerRows({ attention: [{ kind: 'operation-failed' }] })[0].level, 'error')
  assert.equal(ledgerRows({ attention: [{ kind: 'node-unhealthy' }] })[0].level, 'error')
  assert.equal(ledgerRows({ attention: [{ kind: 'backend-update' }] })[0].level, 'warn')
})

test('finds the models and meta backends that depend on a backend', () => {
  const installed = [
    { Name: 'llama-cpp', Metadata: { meta_backend_for: 'cuda12-llama-cpp' } },
    { Name: 'cuda12-llama-cpp', Metadata: {} },
    { Name: 'whisper', Metadata: {} },
  ]
  const models = [
    { id: 'qwen', backend: 'llama-cpp' },
    { id: 'phi', backend: 'cuda12-llama-cpp' },
    { id: 'tiny', backend: 'whisper' },
    { id: 'none' },
  ]
  assert.deepEqual(backendDependents('cuda12-llama-cpp', { models, installed }), {
    models: ['phi', 'qwen'],
    metas: ['llama-cpp'],
  })
  assert.deepEqual(backendDependents('whisper', { models, installed }), { models: ['tiny'], metas: [] })
  assert.deepEqual(backendDependents('', { models, installed }), { models: [], metas: [] })
})

test('recommends llama-cpp, then a meta backend, then the first one', () => {
  assert.equal(recommendedBackend([{ name: 'vllm' }, { name: 'llama-cpp' }]).name, 'llama-cpp')
  assert.equal(recommendedBackend([{ name: 'a' }, { name: 'b', isMeta: true }]).name, 'b')
  assert.equal(recommendedBackend([{ name: 'a' }]).name, 'a')
  assert.equal(recommendedBackend([{ name: 'llama-cpp', installed: true }]), null)
})
