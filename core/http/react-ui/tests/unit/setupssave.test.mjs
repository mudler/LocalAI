import test from 'node:test'
import assert from 'node:assert/strict'
import { saveWithRetry } from '../../src/utils/setupsSave.js'

const add = (s) => (d) => ({ ...d, setups: [...d.setups, s] })
const conflict = (data, version) => ({ conflict: true, current: { data, version } })

test('saveWithRetry saves once without a conflict', async () => {
  const calls = []
  const out = await saveWithRetry(async (v, o) => { calls.push([v, o]) }, { setups: [] }, add({ id: 'a' }))
  assert.deepEqual(out, { setups: [{ id: 'a' }] })
  assert.equal(calls.length, 1)
  assert.equal(calls[0][1].replace, true)
  assert.equal(calls[0][1].version, undefined)
})

test('saveWithRetry reapplies the change on conflict', async () => {
  const calls = []
  const save = async (v, o) => {
    calls.push([v, o])
    if (calls.length === 1) throw conflict({ setups: [{ id: 'other' }] }, 7)
  }
  const out = await saveWithRetry(save, { setups: [] }, add({ id: 'mine' }))
  assert.deepEqual(out.setups.map((s) => s.id), ['other', 'mine'])
  assert.equal(calls.length, 2)
  assert.deepEqual(calls[1][0], out)
  assert.deepEqual(calls[1][1], { replace: true, version: 7 })
})

test('saveWithRetry gives up after a second conflict', async () => {
  let n = 0
  const err = conflict({ setups: [] }, 9)
  await assert.rejects(saveWithRetry(async () => { n++; throw err }, { setups: [] }, add({ id: 'a' })), (e) => e === err)
  assert.equal(n, 2)
})

test('saveWithRetry rethrows other errors at once', async () => {
  let n = 0
  const err = new Error('boom')
  await assert.rejects(saveWithRetry(async () => { n++; throw err }, { setups: [] }, add({ id: 'a' })), (e) => e === err)
  assert.equal(n, 1)
})

test('saveWithRetry gives change a fresh copy each time', async () => {
  const base = { setups: [] }
  const seen = []
  let n = 0
  await saveWithRetry(async () => { if (n++ === 0) throw conflict({ setups: [] }, 2) }, base, (d) => {
    seen.push(d)
    d.setups.push({ id: 'x' })
    return d
  })
  assert.deepEqual(base, { setups: [] })
  assert.notEqual(seen[0], seen[1])
  assert.equal(seen[1].setups.length, 1)
})

test('saveWithRetry sends the version of the base on the first save', async () => {
  const calls = []
  await saveWithRetry(async (v, o) => { calls.push(o) }, { setups: [] }, add({ id: 'a' }), 3)
  assert.deepEqual(calls, [{ replace: true, version: 3 }])
})

test('saveWithRetry keeps the other change when the base is stale', async () => {
  // the file is at version 2 with "first"; the caller still holds version 1
  let stored = { setups: [{ id: 'first' }] }
  let version = 2
  const save = async (v, o) => {
    if (o.version !== version) throw conflict(stored, version)
    stored = v; version++
  }
  await saveWithRetry(save, { setups: [] }, add({ id: 'second' }), 1)
  assert.deepEqual(stored.setups.map((s) => s.id), ['first', 'second'])
})

test('saveWithRetry tells change whether the file exists, and again after a conflict', async () => {
  const seen = []
  let n = 0
  await saveWithRetry(async () => { if (n++ === 0) throw { conflict: true, current: { data: { setups: [{ id: 'o' }] }, version: 2, exists: true } } }, { setups: [] }, (d, meta) => {
    seen.push(meta.exists)
    return d
  }, 0, false)
  assert.deepEqual(seen, [false, true])
})

test('saveWithRetry saves nothing when change answers null', async () => {
  let saved = 0
  const out = await saveWithRetry(async () => { saved++ }, { setups: [] }, () => null, 0, true)
  assert.equal(out, null)
  assert.equal(saved, 0)
})
