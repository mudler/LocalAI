import test from 'node:test'
import assert from 'node:assert/strict'
import { addonsAfter, agentAddons, agentBuiltins, agentConnectors, agentSkills, builtinsAfter, connectorsAfter, skillsAfter } from '../../src/utils/agentTools.js'
import { agentPayload, emptyAgent } from '../../src/utils/chatAgents.js'

const groups = [{ group: 'web' }, { group: 'triggers' }, { group: 'saved_agents', opt_in: true }]

test('agent tools: a built-in group is off when the agent lists it, and an opt-in group is on only when listed', () => {
  const form = { builtins_off: ['triggers'], builtins_on: [] }
  assert.deepEqual(agentBuiltins(groups, form).map(b => [b.group, b.off]), [['web', false], ['triggers', true], ['saved_agents', true]])
  assert.deepEqual(agentBuiltins(groups, { builtins_off: [], builtins_on: ['saved_agents'] }).map(b => b.off), [false, false, false])
})

test('agent tools: switching a group changes builtins_off, an opt-in group changes builtins_on, and unknown groups stay', () => {
  const form = { builtins_off: ['files', 'web'], builtins_on: [] }
  assert.deepEqual(builtinsAfter(form, groups, ['web'], true), { builtins_off: ['files'], builtins_on: [] })
  assert.deepEqual(builtinsAfter(form, groups, ['triggers', 'web'], false), { builtins_off: ['files', 'triggers', 'web'], builtins_on: [] })
  assert.deepEqual(builtinsAfter(form, groups, ['saved_agents'], true), { builtins_off: ['files', 'web'], builtins_on: ['saved_agents'] })
  assert.deepEqual(builtinsAfter({ ...form, builtins_on: ['saved_agents'] }, groups, ['saved_agents'], false), { builtins_off: ['files', 'web'], builtins_on: [] })
})

test('agent tools: add-ons and connectors are on when granted, and a granted one that is gone stays to be removed', () => {
  const addons = agentAddons([{ id: 'a', tools: 2 }, { id: 'b', tools: 1 }], { addons: ['a', 'old'] })
  assert.deepEqual(addons.map(a => [a.id, a.off, !!a.gone]), [['a', false, false], ['b', true, false], ['old', false, true]])
  assert.deepEqual(addonsAfter({ addons: ['a', 'old'] }, ['old'], false), ['a'])
  assert.deepEqual(addonsAfter({ addons: ['a'] }, ['b', 'a'], true), ['b', 'a'])

  const form = { mcp_servers: ['crm', 'connector:m1', 'connector:x'] }
  assert.deepEqual(agentConnectors([{ id: 'm1', name: 'Docs' }, { id: 'm2', name: 'Parked' }], form).map(c => [c.id, c.off, !!c.gone]),
    [['m1', false, false], ['m2', true, false], ['x', false, true]])
  assert.equal(agentConnectors([{ id: 'm2', name: 'Parked', usable: false, reason: 'off' }], form)[0].usable, true, 'an off connector can still be granted')
  assert.deepEqual(connectorsAfter(form, ['connector:m2'], true), ['crm', 'connector:m1', 'connector:x', 'connector:m2'])
  assert.deepEqual(connectorsAfter(form, ['connector:x', 'connector:m1'], false), ['crm'])
})

test('agent tools: skills are granted by name, and the keys of the rows map back to names', () => {
  const skills = [{ id: 'user:review', name: 'review' }, { id: 'builtin:review', name: 'review' }, { id: 'user:cite', name: 'cite' }]
  const rows = agentSkills(skills, { skills: ['cite', 'missing'] })
  assert.deepEqual(rows.map(s => [s.name, s.enabled, !!s.gone]), [['review', false, false], ['review', false, false], ['cite', true, false], ['missing', true, true]])
  assert.deepEqual(skillsAfter({ skills: ['cite'] }, skills, new Set(['user:review']), true), ['cite', 'review'])
  assert.deepEqual(skillsAfter({ skills: ['cite', 'missing'] }, rows, new Set(['gone:missing']), false), ['cite'])
})

test('agent tools: the request carries the switches, empty when there are none', () => {
  const body = agentPayload({ ...emptyAgent(), name: 'A', builtins_off: ['triggers', ' triggers'], builtins_on: undefined })
  assert.deepEqual(body.builtins_off, ['triggers'])
  assert.deepEqual(body.builtins_on, [])
})
