import test from 'node:test'
import assert from 'node:assert/strict'
import {
  bindingState, triggerState, loopState, connectKinds, kindLabel, connectTarget, connectionHref, nextRunLabel,
} from '../../src/utils/projectLinks.js'

test('project links: an active trigger is paused while its project is archived, an off one stays off', () => {
  assert.equal(triggerState({ enabled: true }, false), 'active')
  assert.equal(triggerState({ enabled: true }, true), 'paused')
  assert.equal(triggerState({ enabled: false }, false), 'off')
  assert.equal(triggerState({ enabled: false }, true), 'off')
})

test('project links: an on connection is paused while its project is archived, an off one stays off', () => {
  assert.equal(bindingState({ enabled: true }, false), 'active')
  assert.equal(bindingState({ enabled: true }, true), 'paused')
  assert.equal(bindingState({ enabled: false }, false), 'off')
  assert.equal(bindingState({ enabled: false }, true), 'off')
  assert.equal(bindingState(null, true), 'off')
})

test('project links: an active loop is paused by an archived project, a paused or ended one is not changed', () => {
  assert.equal(loopState({ state: 'active' }, false), 'active')
  assert.equal(loopState({ state: 'active' }, true), 'paused-archived')
  assert.equal(loopState({ state: 'paused' }, true), 'paused')
  assert.equal(loopState({ state: 'ended' }, true), 'ended')
  assert.equal(loopState({ state: 'ended' }, false), 'ended')
  assert.equal(loopState({ state: 'weird' }, false), 'paused')
})

const KINDS = [
  { addon: 'slack', kind: 'slack', title: 'Slack', page: 'connections' },
  { addon: 'mail', kind: 'email', title: 'Email', page: 'accounts', agent_optional: true },
  { addon: 'tg', kind: 'telegram', title: 'Telegram', localized_title: 'Telegramma' },
]

test('project links: connectKinds keeps the kinds that answer as an agent', () => {
  const items = connectKinds(KINDS)
  assert.deepEqual(items.map((i) => i.kind), ['slack', 'telegram'])
  assert.deepEqual(connectKinds(null), [])
  assert.deepEqual(connectKinds(undefined), [])
})

test('project links: kindLabel falls back to the kind id', () => {
  assert.equal(kindLabel(KINDS, 'slack'), 'Slack')
  assert.equal(kindLabel(KINDS, 'telegram'), 'Telegramma')
  assert.equal(kindLabel(KINDS, 'matrix'), 'matrix')
  assert.equal(kindLabel(null, 'matrix'), 'matrix')
})

test('project links: connectTarget goes straight to the setup address with one agent and asks with several', () => {
  const item = connectKinds(KINDS)[0]
  assert.deepEqual(connectTarget(item, [{ id: 'a 1' }]), { href: '/app/addons/slack/connections?new=1&agent=a+1' })
  assert.deepEqual(connectTarget(item, [{ id: 'a1' }, { id: 'a2' }]), { choose: true })
  assert.deepEqual(connectTarget(item, []), { none: true })
  assert.deepEqual(connectTarget(item, null), { none: true })
})

test('project links: connectionHref opens the page of the kind on the connection, else the gallery', () => {
  assert.equal(connectionHref(KINDS, { id: 'b1', kind: 'slack' }), '/app/addons/slack/connections?binding=b1')
  assert.equal(connectionHref(KINDS, { id: 'b2', kind: 'matrix' }), '/app/addons?detail=matrix')
  assert.equal(connectionHref(null, { id: 'b2', kind: 'matrix' }), '/app/addons?detail=matrix')
})

test('project links: nextRunLabel is empty for a missing or bad time', () => {
  assert.equal(nextRunLabel(undefined, 'en'), '')
  assert.equal(nextRunLabel('nope', 'en'), '')
  assert.notEqual(nextRunLabel('2030-01-02T08:00:00Z', 'en'), '')
})
