import test from 'node:test'
import assert from 'node:assert/strict'
import { connectionsFor, proposalView } from '../src/proposal.js'

test('home-assistant proposal: the card shows the entities with their names and states from the preview', () => {
  const v = proposalView('call-service', { instance: 'https://a.example', domain: 'light', service: 'turn_off', entityIds: ['light.kitchen'] },
    { action: 'light.turn_off', instance: 'https://a.example', physical: false, targets: [{ entityId: 'light.kitchen', name: 'Kitchen Light', state: 'on' }] })
  assert.equal(v.action, 'light.turn_off')
  assert.equal(v.physical, false)
  assert.deepEqual(v.targets, [{ entityId: 'light.kitchen', name: 'Kitchen Light', state: 'on' }])
  assert.equal(v.instance, 'https://a.example')
})

test('home-assistant proposal: the confirming tool is drawn as a physical effect', () => {
  assert.equal(proposalView('call-physical-service', {}, {}).physical, true)
  assert.equal(proposalView('call-service', {}, { physical: true }).physical, true)
})

test('home-assistant proposal: without a preview the card still names the entities from the stored arguments', () => {
  const v = proposalView('call-service', { domain: 'light', service: 'toggle', entityIds: ['light.a', 7, 'light.b'], data: { brightness: 10 } }, null)
  assert.equal(v.action, 'light.toggle')
  assert.deepEqual(v.targets.map((t) => t.entityId), ['light.a', 'light.b'])
  assert.equal(v.data, '{"brightness":10}')
  assert.equal(v.dataCut, false)
})

test('home-assistant proposal: a cut preview of the data says so only when the stored data is missing', () => {
  assert.equal(proposalView('call-service', {}, { data: '{"a":', dataCut: true }).dataCut, true)
  assert.equal(proposalView('call-service', { data: { a: 1 } }, { data: '{"a"', dataCut: true }).dataCut, false)
})

test('home-assistant proposal: nothing but strings is drawn from a hostile preview', () => {
  const v = proposalView(5, { entityIds: 'x', data: 3 }, { action: {}, targets: [null, { entityId: 1, name: {}, state: [] }], instance: 9 })
  assert.equal(v.tool, '')
  assert.equal(v.action, '')
  assert.equal(v.data, '')
  assert.deepEqual(v.targets, [{ entityId: '', name: '', state: '' }])
})

test('home-assistant proposal: the connections that list an address are found by name', () => {
  const list = [
    { kind: 'home-assistant', name: 'Home', resources: ['https://A.example'] },
    { kind: 'home-assistant', name: 'Cabin', resources: ['https://b.example'] },
    { kind: 'github', name: 'Other', resources: ['https://a.example'] },
  ]
  assert.deepEqual(connectionsFor(list, 'https://a.example'), ['Home'])
  assert.deepEqual(connectionsFor(list, ''), [])
  assert.deepEqual(connectionsFor(null, 'x'), [])
})
