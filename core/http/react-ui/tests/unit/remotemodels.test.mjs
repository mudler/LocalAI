import test from 'node:test'
import assert from 'node:assert/strict'
import {
  remoteKey, parseRemoteKey, remoteSearchParams, canLoadMore, nextLimit, remoteSubline, remoteRow,
  remoteVariantData, defaultVariantFile, estimateOfVariant, importBody, installBlock, remoteFitsVisible,
  remoteErrorKind, REMOTE_PAGE_STEP, REMOTE_MAX_RESULTS,
} from '../../src/utils/remoteModels.js'

const GB = 1024 ** 3
const budget = { totalMemory: 8 * GB, hasGpu: true, scope: 'node' }
const labels = { downloads: 'downloads', seeds: n => `${n} ${n === 1 ? 'seed' : 'seeds'}`, fits: 'Fits', spill: 'Spills', over: 'Too big' }

test('remote models: a row is keyed by provider and repository', () => {
  assert.equal(remoteKey({ provider: 'huggingface', id: 'acme/Tiny-GGUF' }), 'huggingface:acme/Tiny-GGUF')
  assert.deepEqual(parseRemoteKey('pirateface:acme/Tiny'), { provider: 'pirateface', id: 'acme/Tiny' })
  assert.equal(parseRemoteKey('acme/Tiny'), null)
  assert.equal(parseRemoteKey('other:acme/Tiny'), null)
  assert.equal(parseRemoteKey(''), null)
})

test('remote models: a search asks one provider, gguf by default', () => {
  assert.deepEqual(remoteSearchParams({ provider: 'huggingface', term: ' qwen ', limit: 40, ggufOnly: true }), { q: 'qwen', providers: 'huggingface', limit: 40, format: 'gguf' })
  assert.deepEqual(remoteSearchParams({ provider: 'pirateface', term: 'x', ggufOnly: false }), { q: 'x', providers: 'pirateface', limit: REMOTE_PAGE_STEP, format: 'any' })
})

test('remote models: load more stops when the provider ran dry or the cap is reached', () => {
  const rows = n => Array.from({ length: n }, () => ({}))
  assert.equal(canLoadMore(rows(20), 20), true)
  assert.equal(canLoadMore(rows(11), 20), false)
  assert.equal(canLoadMore(rows(50), 50), false)
  assert.equal(nextLimit(20), 40)
  assert.equal(nextLimit(40), REMOTE_MAX_RESULTS)
  assert.equal(nextLimit(REMOTE_MAX_RESULTS), REMOTE_MAX_RESULTS)
})

test('remote models: the sub-line is author, licence and popularity, only what is known', () => {
  assert.equal(remoteSubline({ id: 'acme/Tiny', license: 'apache-2.0', downloads: 3400, provider: 'huggingface' }, labels), 'acme · apache-2.0 · 3.4k downloads')
  assert.equal(remoteSubline({ id: 'acme/Tiny', provider: 'pirateface', seeders: 6 }, labels), 'acme · 6 seeds')
  assert.equal(remoteSubline({ id: 'acme/Tiny', provider: 'pirateface', seeders: 1 }, labels), 'acme · 1 seed')
  assert.equal(remoteSubline({ id: 'acme/Tiny', provider: 'huggingface' }, labels), 'acme')
})

test('remote models: a result becomes a table row with its key as the name', () => {
  const row = remoteRow(
    { provider: 'huggingface', id: 'acme/Tiny-GGUF', uri: 'huggingface://acme/Tiny-GGUF', backend: 'llama-cpp', license: 'mit', has_variants: true, gated: true, tags: ['gguf', 'license:mit', 'base_model:x/y', 'chat'], url: 'https://huggingface.co/acme/Tiny-GGUF' },
    { installedName: '', sourceLabel: 'Hugging Face', labels },
  )
  assert.equal(row.name, 'huggingface:acme/Tiny-GGUF')
  assert.equal(row.title, 'acme/Tiny-GGUF')
  assert.equal(row.remote, true)
  assert.equal(row.installed, false)
  assert.equal(row.gated, true)
  assert.deepEqual(row.tags, ['gguf', 'chat'])
  assert.equal(row.gallery, 'Hugging Face')
  assert.equal(remoteRow({ provider: 'huggingface', id: 'a/b', installed: true, installed_name: 'b' }, { sourceLabel: '', labels }).installedName, 'b')
  assert.equal(remoteRow({ provider: 'huggingface', id: 'a/b' }, { installedName: 'b', sourceLabel: '', labels }).installed, true)
})

const files = {
  variants: [
    { quantization: 'Q4_K_M', file: 'a-q4.gguf', files: ['a-q4.gguf'], size_bytes: 4 * GB, preference: 'q4_k_m', recommended: true },
    { quantization: 'Q8_0', file: 'a-q8.gguf', files: ['a-q8.gguf'], size_bytes: 8 * GB, preference: 'q8_0' },
  ],
  backend: 'llama-cpp',
}
const estimate = {
  file: 'a-q4.gguf',
  estimates: { 8192: { vramBytes: 5 * GB } },
  variants: [
    { file: 'a-q4.gguf', quantization: 'Q4_K_M', sizeBytes: 4 * GB, sizeDisplay: '4.3 GB', estimates: { 8192: { vramBytes: 5 * GB } } },
    { file: 'a-q8.gguf', quantization: 'Q8_0', sizeBytes: 8 * GB, sizeDisplay: '8.6 GB', estimates: { 8192: { vramBytes: 9 * GB } } },
  ],
}

test('remote models: variants carry size and a fit verdict at the chosen context', () => {
  const data = remoteVariantData(files, estimate, { contextSize: 8192, budget, ramAvailable: null, backend: '', labels })
  assert.equal(data.auto_selected, '')
  assert.equal(data.variants[0].recommended, true)
  assert.equal(data.variants.length, 2)
  assert.deepEqual(
    data.variants.map(v => [v.model, v.label, v.quantization, v.memory_bytes, v.fits, v.fitNote]),
    [['a-q4.gguf', 'Q4_K_M', 'GGUF', 4 * GB, true, ''], ['a-q8.gguf', 'Q8_0', 'GGUF', 8 * GB, false, 'Too big']],
  )
  assert.equal(data.variants[0].backend, 'llama-cpp')
})

test('remote models: a variant with no estimate yet is not marked as not fitting', () => {
  const data = remoteVariantData(files, null, { contextSize: 8192, budget, ramAvailable: null, backend: '', labels })
  assert.ok(data.variants.every(v => v.fits === true && v.fitNote === ''))
  assert.equal(remoteVariantData(null, null, { contextSize: 8192, budget, labels }), null)
})

test('remote models: the default variant is the recommended one, else the first', () => {
  assert.equal(defaultVariantFile(files), 'a-q4.gguf')
  assert.equal(defaultVariantFile({ variants: [{ file: 'x.gguf' }, { file: 'y.gguf' }] }), 'x.gguf')
  assert.equal(defaultVariantFile({ variants: [] }), '')
  assert.equal(defaultVariantFile(undefined), '')
})

test('remote models: the estimate of the chosen variant reads like a gallery estimate', () => {
  assert.equal(estimateOfVariant(estimate, 'a-q4.gguf'), estimate)
  const big = estimateOfVariant(estimate, 'a-q8.gguf')
  assert.equal(big.sizeBytes, 8 * GB)
  assert.equal(big.estimates[8192].vramBytes, 9 * GB)
  assert.equal(estimateOfVariant(null, 'a'), null)
})

test('remote models: the import carries the quantization of the chosen variant', () => {
  const row = { uri: 'huggingface://acme/Tiny-GGUF' }
  assert.deepEqual(importBody(row, files, 'a-q8.gguf'), { uri: 'huggingface://acme/Tiny-GGUF', preferences: { quantizations: 'q8_0' } })
  assert.deepEqual(importBody(row, { variants: [] }, ''), { uri: 'huggingface://acme/Tiny-GGUF' })
  assert.deepEqual(importBody(row, files, 'unknown.gguf'), { uri: 'huggingface://acme/Tiny-GGUF' })
})

test('remote models: only a gated Hugging Face model is blocked, a gated Pirate Face one installs', () => {
  assert.equal(installBlock({ provider: 'pirateface' }, { gated: true }), '')
  assert.equal(installBlock({ provider: 'pirateface', gated: true }, undefined), '')
  assert.equal(installBlock({ provider: 'huggingface', gated: true }, { gated: true }), 'gatedHfInstall')
  assert.equal(installBlock({ provider: 'huggingface', gated: true }, undefined), 'gatedHfInstall')
  assert.equal(installBlock({ provider: 'huggingface' }, { gated: true }), 'gatedHfInstall')
  assert.equal(installBlock({ provider: 'huggingface' }, { gated: false }), '')
  assert.equal(installBlock({ provider: 'pirateface' }, { gated: false }), '')
  const row = (provider, gated) => remoteRow({ provider, id: 'a/b', gated }, { sourceLabel: '', labels })
  assert.equal(row('huggingface', true).installBlocked, true)
  assert.equal(row('huggingface', false).installBlocked, false)
  assert.equal(row('pirateface', true).installBlocked, false)
})

test('remote models: the fits filter hides only a row known not to fit', () => {
  assert.equal(remoteFitsVisible(undefined, 8192, budget, null), true, 'still sizing')
  assert.equal(remoteFitsVisible({ error: 'failed', estimates: {} }, 8192, budget, null), true, 'no estimate could be made')
  assert.equal(remoteFitsVisible({ estimates: { 8192: { vramBytes: 2 * GB } } }, 8192, budget, null), true)
  assert.equal(remoteFitsVisible({ estimates: { 8192: { vramBytes: 9 * GB } } }, 8192, budget, null), false)
})

test('remote models: errors are told apart by status', () => {
  assert.equal(remoteErrorKind({ status: 429 }), 'rateLimited')
  assert.equal(remoteErrorKind({ status: 404 }), 'notFound')
  assert.equal(remoteErrorKind({ status: 502 }), 'failed')
  assert.equal(remoteErrorKind(new Error('x')), 'failed')
})
