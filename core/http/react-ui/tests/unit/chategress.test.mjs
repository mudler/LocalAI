import test from 'node:test'
import assert from 'node:assert/strict'
import { aboveCeiling, cardAnswers, cardStateKey, cardsByTool, networkMode, presetChoices, sortedCards } from '../../src/utils/chatEgress.js'
import { payloadFor, runChecks, EGRESS_CONFIRM_KEY, EGRESS_PRIVATE_PHRASE } from '../../src/utils/settingsPending.js'
import { FIELD_BY_KEY } from '../../src/utils/settingsSchema.js'

test('chat egress: the presets above the limit are not offered', () => {
  assert.equal(aboveCeiling('all', 'registries'), true)
  assert.equal(aboveCeiling('registries', 'registries'), false)
  assert.equal(aboveCeiling('registries', 'off'), true)
  assert.equal(aboveCeiling('off', undefined), false)
  assert.deepEqual(presetChoices({ preset: 'off', ceiling: 'registries' }), [
    { id: 'off', chosen: true, limited: false },
    { id: 'registries', chosen: false, limited: false },
    { id: 'all', chosen: false, limited: true },
  ])
})

test('chat egress: the Tools menu shows the row only where the Sandbox serves, and says so on this machine', () => {
  assert.equal(networkMode({ loaded: false, applies: true }), 'hidden')
  assert.equal(networkMode({ loaded: true, applies: true }), 'row')
  assert.equal(networkMode({ loaded: true, applies: false, applies_reason: 'host' }), 'host')
  assert.equal(networkMode({ loaded: true, applies: false, applies_reason: 'none' }), 'hidden')
})

test('chat egress: cards sort pending first, newest first, and group by tool line', () => {
  const cards = [
    { id: 'a', state: 'denied', asked_at: '2026-10-30T10:00:00Z', tool_seq: 4 },
    { id: 'b', state: 'pending', asked_at: '2026-10-30T09:00:00Z', tool_seq: 4 },
    { id: 'c', state: 'chat', asked_at: '2026-10-30T11:00:00Z', tool_seq: 7 },
  ]
  assert.deepEqual(sortedCards(cards).map(c => c.id), ['b', 'c', 'a'])
  const by = cardsByTool(cards)
  assert.deepEqual(by.get(4).map(c => c.id), ['a', 'b'])
  assert.deepEqual(by.get(7).map(c => c.id), ['c'])
})

test('chat egress: a card offers once, chat and deny; a private one only deny to a user', () => {
  assert.deepEqual(cardAnswers({ state: 'pending' }, false), ['once', 'chat', 'deny'])
  assert.deepEqual(cardAnswers({ state: 'pending', private: true }, false), ['deny'])
  assert.deepEqual(cardAnswers({ state: 'pending', private: true }, true), ['once', 'chat', 'deny'])
  assert.deepEqual(cardAnswers({ state: 'chat' }, true), [])
  assert.deepEqual(cardAnswers({ state: 'expired' }, false), ['once', 'chat', 'deny'])
  assert.deepEqual(cardAnswers({ state: 'expired', private: true }, false), ['deny'])
  assert.deepEqual(cardAnswers({ state: 'denied' }, true), [])
  assert.equal(cardStateKey({ state: 'once' }), 'allowedOnce')
  assert.equal(cardStateKey({ state: 'refused' }), 'refused')
  assert.equal(cardStateKey({ state: 'pending' }), 'pending')
})

test('settings: the private network switch sends the typed phrase and needs it to turn on', () => {
  const field = FIELD_BY_KEY.chats_egress_allow_private
  const initial = { chats_egress_allow_private: false }
  const draft = { chats_egress_allow_private: true }
  const changes = [{ field, from: false, to: true }]
  const without = runChecks(changes, initial, draft).filter(c => c.level === 'error')
  assert.equal(without.length, 1)
  const typed = { ...draft, [EGRESS_CONFIRM_KEY]: EGRESS_PRIVATE_PHRASE }
  assert.equal(runChecks(changes, initial, typed).filter(c => c.level === 'error').length, 0)
  assert.deepEqual(payloadFor(changes, typed), { chats_egress_allow_private: true, [EGRESS_CONFIRM_KEY]: EGRESS_PRIVATE_PHRASE })
  assert.deepEqual(payloadFor([{ field, from: true, to: false }], { chats_egress_allow_private: false }), { chats_egress_allow_private: false })
})
