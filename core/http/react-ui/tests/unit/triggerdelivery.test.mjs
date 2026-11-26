import test from 'node:test'
import assert from 'node:assert/strict'
import { channelKey, deliveryChoice, deliveryOf, deliveryOptions, deliveryPayload, unusableConnections } from '../../src/utils/triggerDelivery.js'

test('trigger delivery: a trigger with no delivery, or the inbox, reports to the Inbox with no problem', () => {
  assert.equal(deliveryOf({}).kind, 'inbox')
  assert.equal(deliveryOf({ delivery: { target: 'inbox', mode: 'always' } }).kind, 'inbox')
  assert.equal(deliveryOf({ delivery: { target: 'inbox' } }).problem, '')
})

test('trigger delivery: a channel names the place, the connection and the service', () => {
  const d = deliveryOf({ delivery: { target: 'channel', channel_label: 'General', channel_ref: 'G1', connection_name: 'Team bot', connection_kind: 'telegram', connection_kind_title: 'Telegram', connection_addon: 'telegram-addon', state: 'ok', mode: 'on_change' } })
  assert.deepEqual([d.kind, d.place, d.connection, d.service, d.addon, d.problem, d.mode], ['channel', 'General', 'Team bot', 'Telegram', 'telegram-addon', '', 'on_change'])
})

test('trigger delivery: a channel without a label falls back to the short channel id', () => {
  assert.equal(deliveryOf({ delivery: { target: 'channel', channel_ref: 'C01…XYZ' } }).place, 'C01…XYZ')
})

test('trigger delivery: the chat of a proposal is a chat, or a thread of it', () => {
  const d = deliveryOf({ delivery: { target: 'origin', chat_ref: '123…789', in_thread: true, connection_name: 'Bot', state: 'ok' } })
  assert.deepEqual([d.kind, d.place, d.thread], ['chat', '123…789', true])
})

test('trigger delivery: each failing state is a problem, and an unknown one is not', () => {
  for (const state of ['off', 'gone', 'unavailable', 'no_channel', 'blocked']) {
    assert.equal(deliveryOf({ delivery: { target: 'channel', state } }).problem, state)
  }
  assert.equal(deliveryOf({ delivery: { target: 'channel', state: 'ok' } }).problem, '')
  assert.equal(deliveryOf({ delivery: { target: 'channel', state: 'weird' } }).problem, '')
})

test('trigger delivery: always is the default mode and is not shown', () => {
  assert.equal(deliveryOf({ delivery: { target: 'channel', mode: 'always' } }).mode, '')
})

const dest = {
  connections: [
    { id: 'c1', kind: 'telegram', kind_title: 'Telegram', addon: 'telegram', name: 'Team bot', enabled: true, can_receive_reports: true, talkback_allowed: true,
      channels: [{ id: '-100', label: 'General', current: true }, { id: '-200', label: 'Ops' }] },
    { id: 'c2', kind: 'slack', kind_title: 'Slack', name: 'Work', enabled: true, can_receive_reports: true, talkback_allowed: true,
      channels: [{ id: 'room:with:colons' }] },
    { id: 'c3', kind: 'code', name: 'Code host', enabled: true, can_receive_reports: false, talkback_allowed: false, channels: [], reason: 'tool_only' },
    { id: 'c4', kind: 'telegram', name: 'Old bot', enabled: false, can_receive_reports: true, talkback_allowed: true, channels: [], reason: 'off' },
  ],
}

test('trigger delivery form: the choice starts from where the trigger reports', () => {
  assert.deepEqual(deliveryChoice({}), { key: 'inbox', mode: 'always' })
  assert.deepEqual(deliveryChoice({ delivery: { target: 'channel', mode: 'on_change' } }), { key: 'keep', mode: 'on_change' })
  assert.deepEqual(deliveryChoice({ delivery: { target: 'origin', mode: 'weird' } }), { key: 'origin', mode: 'always' })
})

test('trigger delivery form: the picker offers the Inbox and the approved channels of every service, never the current one twice', () => {
  const keys = deliveryOptions(dest, { keepable: true }).map((o) => o.key)
  assert.deepEqual(keys, ['keep', 'inbox', channelKey('c1', '-200'), channelKey('c2', 'room:with:colons')])
  const fresh = deliveryOptions(dest).map((o) => o.key)
  assert.deepEqual(fresh, ['inbox', channelKey('c1', '-100'), channelKey('c1', '-200'), channelKey('c2', 'room:with:colons')])
  const ops = deliveryOptions(dest).find((o) => o.key === channelKey('c1', '-200'))
  assert.deepEqual([ops.label, ops.connection, ops.service, ops.addon], ['Ops', 'Team bot', 'Telegram', 'telegram'])
  assert.equal(deliveryOptions(dest).find((o) => o.key === channelKey('c2', 'room:with:colons')).label, 'room:with:colons', 'a channel without a label shows its id')
})

test('trigger delivery form: a trigger from a chat can go back to its chat', () => {
  assert.deepEqual(deliveryOptions({ from_chat: true, connections: [] }).map((o) => o.key), ['inbox', 'origin'])
  assert.deepEqual(deliveryOptions(null).map((o) => o.key), ['inbox'])
})

test('trigger delivery form: connections without a channel are named with the reason', () => {
  assert.deepEqual(unusableConnections(dest).map((c) => [c.id, c.reason]), [['c3', 'tool_only'], ['c4', 'off']])
})

test('trigger delivery form: an unchanged choice sends nothing, so the trigger keeps its place', () => {
  assert.equal(deliveryPayload({ key: 'keep', mode: 'always' }, { key: 'keep', mode: 'always' }), undefined)
  assert.equal(deliveryPayload({ key: 'inbox', mode: 'always' }, { key: 'inbox', mode: 'always' }), undefined)
  assert.equal(deliveryPayload(null, null), undefined)
})

test('trigger delivery form: a new channel sends the connection and the channel, a new mode alone keeps the place', () => {
  assert.deepEqual(deliveryPayload({ key: channelKey('c2', 'room:with:colons'), mode: 'on_problem' }, { key: 'inbox', mode: 'always' }),
    { target: 'channel', connection: 'c2', channel: 'room:with:colons', mode: 'on_problem' })
  assert.deepEqual(deliveryPayload({ key: 'keep', mode: 'on_change' }, { key: 'keep', mode: 'always' }), { target: 'channel', mode: 'on_change' })
})

test('trigger delivery form: clearing sends the Inbox, and a trigger from a chat never names a connection', () => {
  assert.deepEqual(deliveryPayload({ key: 'inbox', mode: 'always' }, { key: 'keep', mode: 'always' }), { target: 'inbox', mode: 'always' })
  assert.deepEqual(deliveryPayload({ key: channelKey('c1', '-200'), mode: 'always' }, { key: 'origin', mode: 'always' }, { fromChat: true }),
    { target: 'channel', channel: '-200', mode: 'always' })
  assert.deepEqual(deliveryPayload({ key: 'origin', mode: 'always' }, { key: 'inbox', mode: 'always' }), { target: 'origin', mode: 'always' })
})
