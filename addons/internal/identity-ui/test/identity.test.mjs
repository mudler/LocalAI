import test from 'node:test'
import assert from 'node:assert/strict'
import * as identity from '../src/identity.js'

test('loadList returns an empty list for a missing or corrupt value', () => {
  assert.deepEqual(identity.loadList('localai_voice_enrollments', { getItem: () => '{not json' }), [])
  assert.deepEqual(identity.loadList('localai_voice_enrollments', { getItem: () => null }), [])
  assert.deepEqual(identity.loadList('localai_voice_enrollments', { getItem: () => '{"a":1}' }), [])
})

test('loadList keeps a stored list', () => {
  assert.deepEqual(identity.loadList('k', { getItem: () => JSON.stringify([{ id: 'a' }]) }), [{ id: 'a' }])
})

test('saveList keeps at most 50 people and never throws', () => {
  let saved
  identity.saveList('k', Array.from({ length: 60 }, (_, i) => ({ id: i })), { setItem: (_k, v) => { saved = v } })
  assert.equal(JSON.parse(saved).length, 50)
  assert.doesNotThrow(() => identity.saveList('k', [], { setItem: () => { throw new Error('quota') } }))
})

test('missingFromServer claims nothing from an incomplete search', () => {
  assert.equal(identity.missingFromServer([{ id: 'a' }], { complete: false, ids: [] }).size, 0)
  assert.deepEqual([...identity.missingFromServer([{ id: 'a' }, { id: 'b' }], { complete: true, ids: ['a'] })], ['b'])
})

test('the identity calls post JSON to the core voice and face routes', async () => {
  const { createIdentityApi } = await import('../src/registry.js')
  const seen = []
  const ctx = { core: { fetch: async (path, init) => { seen.push([path, init.method, JSON.parse(init.body)]); return { ok: true, json: async () => ({ ok: 1 }) } } } }
  const api = createIdentityApi(ctx)
  await api.voice.identify({ model: 'm', audio: 'x' })
  await api.face.forget({ id: 'a' })
  assert.deepEqual(seen, [['/v1/voice/identify', 'POST', { model: 'm', audio: 'x' }], ['/v1/face/forget', 'POST', { id: 'a' }]])
})

test('a failed identity call keeps its status and message, so a 404 on forget can be told apart', async () => {
  const { createIdentityApi } = await import('../src/registry.js')
  const ctx = { core: { fetch: async () => ({ ok: false, status: 404, json: async () => ({ error: { message: 'no such id' } }) }) } }
  await assert.rejects(createIdentityApi(ctx).voice.forget({ id: 'a' }), (err) => err.status === 404 && err.message === 'no such id')
  const plain = { core: { fetch: async () => ({ ok: false, status: 500, json: async () => { throw new Error('not json') } }) } }
  await assert.rejects(createIdentityApi(plain).voice.verify({}), (err) => err.status === 500 && err.message === 'HTTP 500')
})

test('an empty answer (forget replies 204) is not parsed as JSON', async () => {
  const { createIdentityApi } = await import('../src/registry.js')
  const ctx = { core: { fetch: async () => ({ ok: true, status: 204, json: async () => { throw new Error('empty body') } }) } }
  assert.equal(await createIdentityApi(ctx).voice.forget({ id: 'a' }), null)
})
