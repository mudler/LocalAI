import assert from 'node:assert/strict'
import test from 'node:test'

import { modelBudget } from './modelBudget.js'

const GB = 1024 * 1024 * 1024

test('reports nothing to size against before the first reading arrives', () => {
  assert.deepEqual(modelBudget(null), {
    totalMemory: 0, hasGpu: false, nodeName: '', nodeCount: 0, scope: 'local',
  })
})

test('reports the local aggregate on a single-node host', () => {
  assert.deepEqual(
    modelBudget({ aggregate: { total_memory: 12 * GB, gpu_count: 1 }, gpus: [{ index: 0 }] }),
    { totalMemory: 12 * GB, hasGpu: true, nodeName: '', nodeCount: 0, scope: 'local' },
  )
})

test('treats a CPU-only host as having no GPU', () => {
  const budget = modelBudget({ aggregate: { total_memory: 8 * GB, gpu_count: 0 }, gpus: [] })
  assert.equal(budget.hasGpu, false)
  assert.equal(budget.totalMemory, 8 * GB)
})

// The defect: the controller's own 8GB must not decide what a fleet of A100s
// can run.
test('prefers the cluster reading over the controller it is served from', () => {
  assert.deepEqual(
    modelBudget({
      aggregate: { total_memory: 8 * GB, gpu_count: 0 },
      gpus: [],
      cluster: { enabled: true, node_name: 'dgx-01', total_memory: 80 * GB, is_gpu: true, node_count: 4 },
    }),
    { totalMemory: 80 * GB, hasGpu: true, nodeName: 'dgx-01', nodeCount: 4, scope: 'cluster' },
  )
})

test('falls back to the local reading when the cluster reports no usable memory', () => {
  const budget = modelBudget({
    aggregate: { total_memory: 8 * GB, gpu_count: 0 },
    cluster: { enabled: true, node_name: 'dgx-01', total_memory: 0, is_gpu: false, node_count: 0 },
  })
  assert.equal(budget.scope, 'local')
  assert.equal(budget.totalMemory, 8 * GB)
})

test('ignores a cluster block that says distributed mode is off', () => {
  const budget = modelBudget({
    aggregate: { total_memory: 8 * GB },
    cluster: { enabled: false, total_memory: 80 * GB },
  })
  assert.equal(budget.scope, 'local')
  assert.equal(budget.totalMemory, 8 * GB)
})
