import test from 'node:test'
import assert from 'node:assert/strict'
import { createHostSDK } from '../../src/addons/sdk.js'

const make = (host) => createHostSDK({
  addonId: 'calendar', generation: 1, signal: new AbortController().signal, pages: ['calendar'],
  host: { React: {}, ...host },
})

test('an add-on reads the reminders of its own items, asked under its own ID', async () => {
  const asked = []
  const sdk = make({ reminders: async (source) => { asked.push(source); return [{ id: 'r1', item_id: '42', watch_id: 'w1', due: '2026-11-26T14:45:00Z', item_time: '2026-11-26T15:00:00Z', skipped: false, extra: 'x' }] } })
  const rows = await sdk.reminders.list()
  assert.deepEqual(asked, ['calendar'])
  assert.deepEqual(rows, [{ id: 'r1', itemId: '42', watchId: 'w1', due: '2026-11-26T14:45:00Z', itemTime: '2026-11-26T15:00:00Z', skipped: false }])
})

test('a host without watches gives no reminders, and skip says it is unavailable', async () => {
  const sdk = make({})
  assert.deepEqual(await sdk.reminders.list(), [])
  await assert.rejects(() => sdk.reminders.skip('w1', 'r1'))
})

test('skip goes to the host with the own ID of the add-on, the watch and the reminder', async () => {
  const got = []
  const sdk = make({ skipReminder: async (source, w, r) => { got.push([source, w, r]); return { ok: true } } })
  await sdk.reminders.skip('w1', 'r1')
  assert.deepEqual(got, [['calendar', 'w1', 'r1']], 'the server checks the watch against this ID')
})
