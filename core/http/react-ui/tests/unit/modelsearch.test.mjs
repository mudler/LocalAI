import test from 'node:test'
import assert from 'node:assert/strict'
import { searchParams, compactCount, resultFacts, failedProviders, resultKey, SEARCH_PROVIDERS } from '../../src/utils/modelSearch.js'

const all = { huggingface: true, pirateface: true }

test('model search: all providers on sends no providers list, gguf by default', () => {
  assert.deepEqual(searchParams({ query: '  qwen ', providers: all, ggufOnly: true, sort: 'downloads' }), { q: 'qwen', format: 'gguf' })
})

test('model search: one provider, any format and a sort are sent', () => {
  assert.deepEqual(
    searchParams({ query: 'x', providers: { huggingface: false, pirateface: true }, ggufOnly: false, sort: 'likes' }),
    { q: 'x', providers: 'pirateface', format: 'any', sort: 'likes' },
  )
})

test('model search: the panel lists the providers the server knows', () => {
  assert.deepEqual(SEARCH_PROVIDERS, ['huggingface', 'pirateface'])
})

test('model search: counts are compact', () => {
  assert.equal(compactCount(0), '')
  assert.equal(compactCount(undefined), '')
  assert.equal(compactCount(950), '950')
  assert.equal(compactCount(1234), '1.2k')
  assert.equal(compactCount(3400000), '3.4M')
  assert.equal(compactCount(2e9), '2B')
})

test('model search: a Hugging Face result shows downloads, likes, license and task', () => {
  const facts = resultFacts({ provider: 'huggingface', downloads: 3400, likes: 12, license: 'apache-2.0', pipeline_tag: 'text-generation' })
  assert.deepEqual(facts.map(f => f.key), ['downloads', 'likes', 'license', 'task'])
  assert.equal(facts[0].value, '3.4k')
})

test('model search: a Pirate Face result shows size and seeders, even with zero seeders', () => {
  const facts = resultFacts({ provider: 'pirateface', size_bytes: 1.5e9, seeders: 0 })
  assert.deepEqual(facts.map(f => `${f.key}=${f.value}`), ['size=1.4 GB', 'seeders=0'])
})

test('model search: failed providers are the ones with ok false', () => {
  const res = { providers: [{ name: 'a', ok: true }, { name: 'b', ok: false, error: 'down' }] }
  assert.deepEqual(failedProviders(res).map(p => p.name), ['b'])
  assert.deepEqual(failedProviders(null), [])
})

test('model search: the same repository from two providers has two keys', () => {
  assert.notEqual(resultKey({ provider: 'a', id: 'o/r' }), resultKey({ provider: 'b', id: 'o/r' }))
})

test('import source: a magnet link is recognised and shows its info hash', async () => {
  const { detectSource } = await import('../../src/utils/tools.js')
  const s = detectSource('magnet:?xt=urn:btih:7158ba0f294cdd7da7f4d4f7a496c6162a63a622&tr=udp%3A%2F%2Ft.example%3A6969')
  assert.equal(s.kind, 'magnet')
  assert.equal(s.ok, true)
  assert.equal(s.ref, '7158ba0f294cdd7da7f4d4f7a496c6162a63a622')
  assert.equal(detectSource('magnet:?dn=nothing').ok, false)
})
