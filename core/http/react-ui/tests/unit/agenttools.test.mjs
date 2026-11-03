import test from 'node:test'
import assert from 'node:assert/strict'
import { addonsAfter, agentAddons, agentBuiltins, agentConnectors, agentSkills, builtinsAfter, connectionsAfter, connectorsAfter, skillsAfter } from '../../src/utils/agentTools.js'
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

// ---- builtin grants in the shared Tools list ----------------------------

const grantCatalog = [
  { name: 'memory.write', title: 'Remember things', description: 'The agent can propose a note in your memory.', sensitive: false },
  { name: 'skills.write', title: 'Write skills', description: 'The agent can propose a skill.', sensitive: true },
]

test('agentGrants lists every declared grant, on when the agent holds it, and keeps an unknown held one', async () => {
  const { agentGrants, grantsAfter } = await import('../../src/utils/agentTools.js')
  const rows = agentGrants(grantCatalog, ['skills.write', 'from.the.future'])
  assert.deepEqual(rows.map((r) => [r.name, r.on]), [['memory.write', false], ['skills.write', true], ['from.the.future', true]])
  assert.equal(rows[2].gone, true)
  assert.deepEqual(agentGrants(grantCatalog, ['skills.write'], { heldOnly: true }).map((r) => r.name), ['skills.write'])
  assert.deepEqual(agentGrants(grantCatalog, undefined).map((r) => r.on), [false, false])
  assert.deepEqual(grantsAfter({ builtins: ['memory.write'] }, ['skills.write'], true), ['memory.write', 'skills.write'])
  assert.deepEqual(grantsAfter({ builtins: ['memory.write', 'skills.write'] }, ['memory.write'], false), ['skills.write'])
  assert.deepEqual(grantsAfter({}, ['memory.write'], true), ['memory.write'])
})

test('the Tools list shows each grant with its plain words, marks the sensitive ones and offers no bulk change', async () => {
  const { hookHarness } = await import('./chatcommands-lifecycle.mjs')
  const { agentGrants } = await import('../../src/utils/agentTools.js')
  const { switchRows, switchCounts } = await import('../../src/utils/chatSwitchRows.js')
  const imports = Object.assign({}, ...await Promise.all(['chatMCP', 'chatCommands'].map((n) => import(`../../src/utils/${n}.js`))))
  const harness = hookHarness('../../src/hooks/useToolGroups.js', 'useToolGroups', {
    useTranslation: () => ({ t: (key, o) => (o?.defaultValue ?? key) }), connectorRef: imports.connectorRef, unknownTextKey: imports.unknownTextKey,
  }, (source) => source.replace(/export const/g, 'const'))
  const toggled = []
  const base = { builtins: [], addons: [], servers: [], connectors: [], commands: {}, setBuiltinsOn() {}, setAddonsOn() {}, setMCPOn() {} }
  const editable = { list: agentGrants(grantCatalog, ['memory.write']), editable: true, onToggle: (n, on) => toggled.push([n, on]) }
  const groups = harness.render({ ...base, grants: editable }, {})
  const g = groups.find((x) => x.key === 'grants')
  assert.ok(g, 'a grants group')
  assert.equal(g.onSet, undefined, 'no group setter: Select all never grants a right')
  assert.deepEqual(g.rows.map((r) => [r.label, r.description, r.on, !!r.warn, !!r.readOnly]), [
    ['Remember things', 'The agent can propose a note in your memory.', true, false, false],
    ['Write skills', 'The agent can propose a skill.', false, true, false],
  ])
  assert.equal(g.rows[1].note, 'tools.grantSensitive')
  g.rows[1].onToggle(true)
  assert.deepEqual(toggled, [['skills.write', true]])
  // The count and the bulk rows leave the grants out.
  assert.deepEqual(switchCounts(switchRows(groups)), { on: 0, total: 0 })
  // A chat shows the held grants read only.
  const chat = harness.render({ ...base, grants: { list: agentGrants(grantCatalog, ['memory.write'], { heldOnly: true }), editable: false } }, {})
  assert.deepEqual(chat.find((x) => x.key === 'grants').rows.map((r) => [r.label, r.readOnly]), [['Remember things', true]])
  // No grants prop, no section content.
  assert.equal(harness.render(base, {}).find((x) => x.key === 'grants').rows.length, 0)
})

test('agent tools: an add-on with a credential shows the connections the agent names, and ones the person no longer has are left out', () => {
  const rows = [{ id: 'tracker', credentialed: true, enabled_connections: ['b-new-chat-default'], connection_by: 'only', connections: [{ id: 'b1' }, { id: 'b2' }, { id: 'b3' }] }, { id: 'office' }]
  const got = agentAddons(rows, { addons: ['tracker'], connections: { tracker: ['b3', 'b1', 'deleted'] } })
  assert.deepEqual([got[0].enabled_connections, got[0].connection_by, got[0].needs_connection], [['b1', 'b3'], '', false])
  assert.equal(got[1].enabled_connections, undefined)
  assert.deepEqual(agentAddons(rows, { addons: [], connections: { tracker: ['deleted'] } })[0].enabled_connections, [])
  assert.deepEqual(agentAddons(rows, { addons: [] })[0].enabled_connections, [])
  assert.deepEqual(agentAddons(rows, { addons: [], connections: { tracker: 'b2' } })[0].enabled_connections, ['b2'], 'a single string is a set of one')
})

test('agent tools: naming and clearing the connections of an add-on changes only that add-on', () => {
  const form = { connections: { tracker: ['b1'], ledger: ['s1'] } }
  assert.deepEqual(connectionsAfter(form, 'tracker', ['b2', 'b3']), { tracker: ['b2', 'b3'], ledger: ['s1'] })
  assert.deepEqual(connectionsAfter(form, 'tracker', []), { ledger: ['s1'] })
  assert.deepEqual(connectionsAfter({}, 'tracker', ['b1']), { tracker: ['b1'] })
  assert.deepEqual(form, { connections: { tracker: ['b1'], ledger: ['s1'] } }, 'the form is not changed')
})

test('agent payload: the connections are sent once the agent has them or the person cleared them, never invented', () => {
  assert.equal('connections' in agentPayload(emptyAgent()), false, 'a write that does not mention them keeps the stored ones')
  assert.deepEqual(agentPayload({ ...emptyAgent(), connections: { tracker: [' b1 ', 'b1', 'b2'], ledger: [''], older: 'b3' } }).connections, { tracker: ['b1', 'b2'], older: ['b3'] })
  assert.deepEqual(agentPayload({ ...emptyAgent(), connections: {} }).connections, {}, 'an empty object clears')
})
