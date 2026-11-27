import test from 'node:test'
import assert from 'node:assert/strict'
import { normalizeSchema, resolveProfile, withProfile } from '../../src/utils/engineSettings.js'
import { estimateBody, memoryChange, memoryKind, memoryNow, profileFit, runtimeBytes, SAME_BYTES, tokensPerStream } from '../../src/utils/runtimeMemory.js'
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
  const tier = schema.concurrency.find(p => p.id === 'streams32')
  const many = estimateBody(schema, 'm', withProfile(schema, {}, resolveProfile(tier, 'discrete')))
  assert.equal(many.cache_type_k, 'q8_0')
  assert.equal(many.cache_type_v, 'q8_0')
  assert.equal(estimateBody(schema, 'm', withProfile(schema, {}, resolveProfile(prof('longPrompts'), 'unified'))).ubatch, 2048)
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

test('runtime memory: the memory kind is the override, else the detected one, else the fallback', () => {
  assert.deepEqual(memoryKind(schema, { kind: 'unified' }, ''), { kind: 'unified', source: 'detected' })
  assert.deepEqual(memoryKind(schema, { kind: 'unified' }, 'discrete'), { kind: 'discrete', source: 'override' })
  assert.deepEqual(memoryKind(schema, null, ''), { kind: 'discrete', source: 'fallback' })
  assert.deepEqual(memoryKind(schema, { kind: 'nope' }, 'nope'), { kind: 'discrete', source: 'fallback' })
})

test('runtime memory: whether a concurrency profile fits the memory', () => {
  const tier = (id) => schema.concurrency.find(p => p.id === id)
  const total = (bytes) => ({ vram_bytes: bytes })
  // Unknown memory: no size check.
  assert.deepEqual(profileFit(tier('streams32'), 'discrete', 0, 0.9, {}), { state: 'ok', tight: false })
  // Not offered without a GPU, and below the floor of the kind.
  assert.equal(profileFit(tier('streams32'), 'system', 64 * GIB, 0.9, {}).state, 'unsupported')
  const small = profileFit(tier('streams32'), 'discrete', 6 * GIB, 0.9, {})
  assert.equal(small.state, 'small')
  assert.equal(small.need, 8 * GIB)
  assert.equal(profileFit(tier('streams8'), 'discrete', 16 * GIB, 0.9, { plain: total(10 * GIB), now: total(10 * GIB) }).state, 'ok')
  // Unified memory: f16 when it fits, the 8-bit cache when it does not.
  assert.deepEqual(profileFit(tier('streams32'), 'unified', 128 * GIB, 0.9, { plain: total(100 * GIB), tight: total(80 * GIB), now: total(90 * GIB) }), { state: 'ok', tight: false })
  assert.deepEqual(profileFit(tier('streams32'), 'unified', 128 * GIB, 0.9, { plain: total(120 * GIB), tight: total(100 * GIB), now: total(90 * GIB) }), { state: 'ok', tight: true })
  const nofit = profileFit(tier('streams32'), 'unified', 128 * GIB, 0.9, { plain: total(140 * GIB), tight: total(130 * GIB), now: total(90 * GIB) })
  assert.equal(nofit.state, 'nofit')
  assert.equal(nofit.need, 130 * GIB)
  // The f16 cache of the current values already overflows: 32+ uses the 8-bit one.
  assert.deepEqual(profileFit(tier('streams32'), 'unified', 80 * GIB, 0.9, { plain: total(75 * GIB), tight: total(60 * GIB), now: total(75 * GIB) }), { state: 'ok', tight: true })
  // A model that already does not fit keeps its choices.
  assert.equal(profileFit(tier('streams16'), 'discrete', 16 * GIB, 0.9, { plain: total(20 * GIB), now: total(20 * GIB) }).state, 'ok')
})

test('runtime memory: the context tokens per stream', () => {
  assert.equal(tokensPerStream(32768, 8), 4096)
  assert.equal(tokensPerStream(8192, 32), 256)
  assert.equal(tokensPerStream(undefined, 8), null)
  assert.equal(tokensPerStream(8192, 0), null)
})
