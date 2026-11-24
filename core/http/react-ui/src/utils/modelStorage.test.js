import assert from 'node:assert/strict'
import test from 'node:test'

import { diskEntry, filesOf, freedBy, indexStorage, ownBytes, sharedWith } from './modelStorage.js'
import { buildCleanupPlan, totalSize } from './cleanupPlan.js'

const GB = 1024 * 1024 * 1024

// Two builds share a tokenizer file; one model names a file that is gone.
const report = {
  models: [
    { name: 'big', size_bytes: 15 * GB, shared_bytes: 0, files: ['big.gguf'] },
    { name: 'q8', size_bytes: 9 * GB, shared_bytes: 1 * GB, files: ['q8.gguf', 'tok.json'] },
    { name: 'q4', size_bytes: 5 * GB, shared_bytes: 1 * GB, files: ['q4.gguf', 'tok.json'] },
    { name: 'broken', size_bytes: 2 * GB, shared_bytes: 0, files: ['broken.gguf'], missing: ['broken.mmproj'] },
    { name: 'empty', size_bytes: 0, shared_bytes: 0, files: [] },
  ],
  files: [
    { path: 'big.gguf', size_bytes: 15 * GB, models: ['big'] },
    { path: 'q8.gguf', size_bytes: 8 * GB, models: ['q8'] },
    { path: 'q4.gguf', size_bytes: 4 * GB, models: ['q4'] },
    { path: 'tok.json', size_bytes: 1 * GB, models: ['q4', 'q8'] },
    { path: 'broken.gguf', size_bytes: 2 * GB, models: ['broken'] },
    { path: 'broken.mmproj', size_bytes: 0, missing: true, models: ['broken'] },
  ],
  total_bytes: 30 * GB,
}
const index = indexStorage(report)

test('a body that is not a storage report reads as no report', () => {
  assert.equal(indexStorage(null), null)
  assert.equal(indexStorage({}), null)
  assert.equal(indexStorage({ error: 'nope' }), null)
})

test('a model with no files and no missing references has no disk entry', () => {
  assert.equal(diskEntry(index, 'empty'), null)
  assert.equal(diskEntry(index, 'unknown'), null)
  assert.equal(diskEntry(index, 'broken').missing[0], 'broken.mmproj')
})

test('a model gives back its size minus the shared part', () => {
  assert.equal(ownBytes(diskEntry(index, 'q8')), 8 * GB)
  assert.equal(ownBytes(diskEntry(index, 'big')), 15 * GB)
})

test('shared files name the other model and the bytes', () => {
  assert.deepEqual(sharedWith(index, 'q8'), [{ model: 'q4', bytes: 1 * GB }])
  assert.deepEqual(sharedWith(index, 'big'), [])
})

test('files are listed largest first and missing references last', () => {
  const rows = filesOf(index, 'broken')
  assert.deepEqual(rows.map(r => [r.path, r.missing]), [['broken.gguf', false], ['broken.mmproj', true]])
  assert.deepEqual(filesOf(index, 'q8').map(r => r.path), ['q8.gguf', 'tok.json'])
})

test('removing one of two models that share a file does not free the shared file', () => {
  assert.equal(freedBy(index, ['q8']), 8 * GB)
})

test('removing both models that share a file frees it', () => {
  assert.equal(freedBy(index, ['q8', 'q4']), 13 * GB)
})

test('freedBy cannot say when the report does not know a model', () => {
  assert.equal(freedBy(index, ['q8', 'ghost']), null)
  assert.equal(freedBy(null, ['q8']), null)
})

test('the plan sizes a model by its own bytes and says who it shares with', () => {
  const plan = buildCleanupPlan({
    models: [{ id: 'q8' }, { id: 'q4' }, { id: 'broken' }, { id: 'local' }],
    sizes: { local: 3 * GB, q8: 99 * GB },
    storage: index,
  })
  const all = plan.groups.call
  const q8 = all.find(i => i.id === 'q8')
  assert.equal(q8.size, 8 * GB)
  assert.equal(q8.sizeSource, 'disk')
  assert.deepEqual(q8.sharedWith, [{ model: 'q4', bytes: 1 * GB }])
  assert.deepEqual(all.find(i => i.id === 'broken').missing, ['broken.mmproj'])
  // A model the report has nothing on keeps the gallery's estimate.
  const local = all.find(i => i.id === 'local')
  assert.equal(local.size, 3 * GB)
  assert.equal(local.sizeSource, 'estimate')
})

test('a batch counts a shared file once it holds every model that uses it', () => {
  const plan = buildCleanupPlan({ models: [{ id: 'q8' }, { id: 'q4' }, { id: 'local' }], sizes: { local: 2 * GB }, storage: index })
  const by = Object.fromEntries(plan.groups.call.map(i => [i.id, i]))
  assert.deepEqual(totalSize([by.q8], index), { bytes: 8 * GB, unknown: 0 })
  assert.deepEqual(totalSize([by.q8, by.q4], index), { bytes: 13 * GB, unknown: 0 })
  assert.deepEqual(totalSize([by.q8, by.local], index), { bytes: 10 * GB, unknown: 0 })
})

test('a model whose files are all shared frees nothing alone and is not unknown', () => {
  const only = indexStorage({
    models: [
      { name: 'a', size_bytes: 1 * GB, shared_bytes: 1 * GB, files: ['x'] },
      { name: 'b', size_bytes: 1 * GB, shared_bytes: 1 * GB, files: ['x'] },
    ],
    files: [{ path: 'x', size_bytes: 1 * GB, models: ['a', 'b'] }],
    total_bytes: 1 * GB,
  })
  const plan = buildCleanupPlan({ models: [{ id: 'a' }, { id: 'b' }], storage: only })
  const a = plan.groups.call.find(i => i.id === 'a')
  assert.equal(a.size, 0)
  assert.deepEqual(totalSize([a], only), { bytes: 0, unknown: 0 })
})
