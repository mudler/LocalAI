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
