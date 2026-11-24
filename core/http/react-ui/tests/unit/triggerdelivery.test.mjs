import test from 'node:test'
import assert from 'node:assert/strict'
import { deliveryOf } from '../../src/utils/triggerDelivery.js'

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
