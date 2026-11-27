import test from 'node:test'
import assert from 'node:assert/strict'
import { summarizeFleet, vramReading } from '../../src/utils/nodeFleet.js'
import { memoryOf } from '../../src/utils/swarm.js'

const GB = 1000 ** 3

// The worker of #13531: about 188 GiB reported, a 40 GB budget. The frontend
// already takes the reserve off available_vram, so a bar drawn against the raw
// total showed 148 GB in use on an idle node.
const budgeted = { id: 'w1', status: 'healthy', total_vram: 202325295104, available_vram: 40 * GB, vram_budget_bytes: 40 * GB }

test('vram: a node with a budget is measured against the budget, and keeps the detected total', () => {
  const reading = vramReading(budgeted)
  assert.equal(reading.total, 40 * GB)
  assert.equal(reading.used, 0)
  assert.equal(reading.detected, 202325295104)
})

test('vram: a node without a budget, or with a budget at or above its total, reads its total', () => {
  const plain = vramReading({ total_vram: 24 * GB, available_vram: 20 * GB })
  assert.equal(plain.total, 24 * GB)
  assert.equal(plain.detected, undefined)
  assert.equal(vramReading({ total_vram: 24 * GB, available_vram: 20 * GB, vram_budget_bytes: 24 * GB }).detected, undefined)
})

test('vram: the fleet summary, the low-memory flag and the node map use the budget', () => {
  const summary = summarizeFleet([budgeted, { id: 'w2', status: 'healthy', total_vram: 24 * GB, available_vram: 1 * GB }])
  assert.equal(summary.vram.total, 64 * GB)
  assert.equal(summary.vram.available, 41 * GB)
  assert.equal(summary.vram.detected, 202325295104 + 24 * GB)
  assert.deepEqual(summary.attention.lowVRAM, ['w2'])
  assert.equal(memoryOf(budgeted).total, 40 * GB)
})
