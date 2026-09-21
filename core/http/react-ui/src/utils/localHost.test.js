import assert from 'node:assert/strict'
import test from 'node:test'

import { filterLocalModels, hostAsNode, localModelRows, sortLocalModels, uptime } from './localHost.js'
import { summarizeFleet } from './nodeFleet.js'

const GB = 1024 ** 3

test('a GPU host becomes one healthy node the fleet gauges can read', () => {
  const node = hostAsNode({
    type: 'gpu',
    gpus: [{ total_vram: 24 * GB, free_vram: 20 * GB }, { total_vram: 24 * GB, free_vram: 4 * GB }],
    ram: { total: 64 * GB, available: 48 * GB, free: 8 * GB },
    cpu: { logical_cores: 16, usage_percent: 25, load_1: 3.5 },
    disk: { total: 1000 * GB, available: 250 * GB },
  })
  const summary = summarizeFleet([node])
  assert.equal(summary.health.healthy, 1)
  assert.equal(summary.vram.total, 48 * GB)
  assert.equal(summary.vram.available, 24 * GB)
  assert.equal(summary.ram.available, 48 * GB, 'uses available, not free, like the worker heartbeat')
  assert.equal(summary.cpu.totalLogicalCores, 16)
  assert.equal(summary.cpu.busyCoreEquivalents, 4)
  assert.equal(summary.disk.used, 750 * GB)
})

test('a CPU-only host reports no VRAM instead of zero VRAM', () => {
  const summary = summarizeFleet([hostAsNode({ type: 'ram', gpus: [], ram: { total: 16 * GB, available: 8 * GB } })])
  assert.equal(summary.vram.reportingCount, 0)
  assert.equal(summary.ram.reportingCount, 1)
  assert.equal(summary.cpu.reportingCount, 0, 'no cpu block means no data, not an idle CPU')
})

test('no resources means no node', () => {
  assert.equal(hostAsNode(null), null)
})

test('loaded models keep unknown process readings as null', () => {
  const rows = localModelRows({
    loaded_models: [
      { id: 'qwen', backend: 'llama-cpp', process: { pid: 42, rss_bytes: 2 * GB, memory_percent: 3.1, cpu_percent: 12.5, started_at: '2026-09-21T10:00:00Z' } },
      { id: 'whisper' },
      { id: '' },
    ],
  })
  assert.equal(rows.length, 2)
  assert.deepEqual(rows[0], { model_name: 'qwen', backend: 'llama-cpp', pid: 42, rss_bytes: 2 * GB, memory_percent: 3.1, cpu_percent: 12.5, started_at: '2026-09-21T10:00:00Z' })
  assert.equal(rows[1].rss_bytes, null)
  assert.equal(rows[1].cpu_percent, null)
  assert.equal(rows[1].backend, '')
})

test('sorting puts models without a reading last in both directions', () => {
  const rows = [
    { model_name: 'a', backend: '', rss_bytes: 1 },
    { model_name: 'b', backend: '', rss_bytes: null },
    { model_name: 'c', backend: '', rss_bytes: 3 },
  ]
  assert.deepEqual(sortLocalModels(rows, { key: 'rss_bytes', direction: 'desc' }).map(r => r.model_name), ['c', 'a', 'b'])
  assert.deepEqual(sortLocalModels(rows, { key: 'rss_bytes', direction: 'asc' }).map(r => r.model_name), ['a', 'c', 'b'])
})

test('filtering matches model name or backend', () => {
  const rows = [{ model_name: 'Qwen3', backend: 'llama-cpp' }, { model_name: 'kokoro', backend: 'kokoro' }]
  assert.deepEqual(filterLocalModels(rows, 'LLAMA').map(r => r.model_name), ['Qwen3'])
  assert.equal(filterLocalModels(rows, '  ').length, 2)
})

test('uptime reads at the right grain', () => {
  const now = Date.parse('2026-09-21T12:00:00Z')
  assert.equal(uptime('2026-09-21T11:59:30Z', now), '30s')
  assert.equal(uptime('2026-09-21T09:15:00Z', now), '2h 45m')
  assert.equal(uptime('2026-09-19T11:00:00Z', now), '2d 1h')
  assert.equal(uptime(null, now), null)
})
