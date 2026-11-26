import test from 'node:test'
import assert from 'node:assert/strict'
import { normalizeSchema, withProfile } from '../../src/utils/engineSettings.js'
import { estimateBody, memoryChange, memoryNow, runtimeBytes, SAME_BYTES } from '../../src/utils/runtimeMemory.js'
// The answer of the real handler (pkg/enginesettings), saved for the specs.
import SCHEMA from '../../e2e/fixtures/engine-llamacpp-schema.json' with { type: 'json' }

const schema = normalizeSchema(SCHEMA)
const prof = (id) => schema.profiles.find(p => p.id === id)
const answer = (kv, compute, approximate = false) => ({ runtime: { kv_cache_bytes: kv, compute_bytes: compute, context_length: 8192, approximate } })
const GIB = 1024 ** 3

test('runtime memory: the estimate body of a value map', () => {
  // Unset batch sizes ask for the defaults, an unset cache type for f16.
  assert.deepEqual(estimateBody(schema, 'm', {}), { model: 'm', cache_type_k: 'f16', cache_type_v: 'f16', batch: 0, ubatch: 0 })
  assert.deepEqual(estimateBody(schema, 'm', {
    context_size: 16384, gpu_layers: 99, cache_type_k: 'q8_0', 'parameters.batch': 2048, options: ['n_ubatch:512', 'paged_kv:true'],
  }), { model: 'm', context_size: 16384, gpu_layers: 99, cache_type_k: 'q8_0', cache_type_v: 'f16', batch: 2048, ubatch: 512 })
  // Each profile gets its own body.
  const many = estimateBody(schema, 'm', withProfile(schema, {}, prof('concurrent')))
  assert.equal(many.cache_type_k, 'q8_0')
  assert.equal(many.cache_type_v, 'q8_0')
  assert.equal(estimateBody(schema, 'm', withProfile(schema, {}, prof('longPrompts'))).ubatch, 2048)
})

test('runtime memory: no body without a model or an estimate map', () => {
  assert.equal(estimateBody(schema, '', {}), null)
  assert.equal(estimateBody({ ...schema, estimate: {} }, 'm', {}), null)
})

test('runtime memory: the change between two answers', () => {
  assert.equal(runtimeBytes(answer(GIB, GIB / 2)), 1.5 * GIB)
  assert.equal(runtimeBytes({ message: 'no weight files' }), null)
  assert.deepEqual(memoryChange(answer(GIB, 0), answer(2 * GIB, 0)), { kind: 'more', bytes: GIB, text: '1 GB', approximate: false })
  const less = memoryChange(answer(GIB, 0), answer(GIB / 2, 0, true))
  assert.equal(less.kind, 'less')
  assert.equal(less.text, '512 MB')
  assert.equal(less.approximate, true)
  assert.equal(memoryChange(answer(GIB, 0), answer(GIB + SAME_BYTES - 1, 0)).kind, 'same')
  assert.equal(memoryChange(answer(GIB, 0), {}), null)
})

test('runtime memory: the figures of the current settings', () => {
  assert.deepEqual(memoryNow(answer(GIB, 256 * 1024 * 1024)), { kv: '1 GB', compute: '256 MB', context: 8192, approximate: false })
  assert.equal(memoryNow(null), null)
})
