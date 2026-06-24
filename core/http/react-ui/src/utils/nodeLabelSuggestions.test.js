import assert from 'node:assert/strict'
import test from 'node:test'

import { labelIndex, suggestKeys, suggestValues } from './nodeLabelSuggestions.js'

const NODES = [
  { id: 'n1', name: 'Falcon GPU', labels: { 'gpu.vendor': 'NVIDIA', zone: 'east' } },
  { id: 'n2', name: 'Worker 2', labels: { 'gpu.vendor': 'amd', zone: 'west' } },
  { id: 'n3', name: 'Worker 3', labels: {} },
  { id: 'n4', name: 'Worker 4' },
  { id: 'n5', name: 'Worker 5', labels: { 'gpu.vram': '24GB' } },
]

test('collects every distinct label key across the cluster', () => {
  assert.deepEqual(labelIndex(NODES).keys, ['gpu.vendor', 'gpu.vram', 'zone'])
})

test('survives a node list that has not loaded yet', () => {
  assert.deepEqual(labelIndex(null), { keys: [], values: {} })
  assert.deepEqual(labelIndex([]), { keys: [], values: {} })
})

test('collects the values a key actually takes, deduplicated', () => {
  const index = labelIndex([...NODES, { id: 'n6', labels: { zone: 'east' } }])
  assert.deepEqual(index.values.zone, ['east', 'west'])
})

test('offers every key before the user has typed anything', () => {
  assert.deepEqual(suggestKeys(labelIndex(NODES), ''), ['gpu.vendor', 'gpu.vram', 'zone'])
})

test('matches a key anywhere in the string, ignoring case', () => {
  assert.deepEqual(suggestKeys(labelIndex(NODES), 'VEND'), ['gpu.vendor'])
})

test('ranks keys that start with the query above keys that merely contain it', () => {
  const index = labelIndex([{ id: 'n1', labels: { 'node.zone': 'a', zone: 'b' } }])
  assert.deepEqual(suggestKeys(index, 'zone'), ['zone', 'node.zone'])
})

// A key already in the selector is not a suggestion: adding it again would
// silently overwrite the pair the user just built.
test('drops keys the selector already carries', () => {
  assert.deepEqual(suggestKeys(labelIndex(NODES), '', ['gpu.vendor']), ['gpu.vram', 'zone'])
})

test('offers only the values that belong to the key being filled', () => {
  assert.deepEqual(suggestValues(labelIndex(NODES), 'gpu.vendor', ''), ['NVIDIA', 'amd'])
})

test('matches a value ignoring case, so the chip keeps the cluster spelling', () => {
  assert.deepEqual(suggestValues(labelIndex(NODES), 'gpu.vendor', 'nvi'), ['NVIDIA'])
})

test('offers nothing for a key the cluster has never reported', () => {
  assert.deepEqual(suggestValues(labelIndex(NODES), 'made.up', ''), [])
})
