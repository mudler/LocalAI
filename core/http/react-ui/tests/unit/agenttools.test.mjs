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

const optionGrants = [
  { name: 'memory.write', title: 'Remember things', description: 'x', sensitive: false },
  { name: 'skills.write', title: 'Write skills', description: 'x', sensitive: true },
  { name: 'triggers.propose', title: 'Propose triggers', description: 'x', sensitive: false },
  { name: 'triggers.run', title: 'Run triggers', description: 'x', sensitive: false },
  { name: 'files.upload', title: 'Receive files', description: 'x', sensitive: false },
  { name: 'talkback.channels', title: 'Reply', description: 'x', sensitive: false },
  { name: 'act.send', title: 'Send', description: 'x', sensitive: true },
  { name: 'saved_agents', title: 'Call your saved agents', description: 'x', sensitive: false },
  { name: 'from.the.future', title: 'Future', description: 'x', sensitive: false, gone: true },
]

async function toolGroupsHarness() {
  const { hookHarness } = await import('./chatcommands-lifecycle.mjs')
  const imports = Object.assign({}, ...await Promise.all(['chatMCP', 'chatCommands', 'toolOptions'].map((n) => import(`../../src/utils/${n}.js`))))
  return hookHarness('../../src/hooks/useToolGroups.js', 'useToolGroups', {
    useTranslation: () => ({ t: (key, o) => (o?.defaultValue ?? key) }), ...imports,
  }, (source) => source.replace(/export const/g, 'const'))
}

test('the Tools list nests each grant under the switch it refines, shows every grant once and offers no bulk change', async () => {
  const { agentGrants } = await import('../../src/utils/agentTools.js')
  const { switchRows, switchCounts } = await import('../../src/utils/chatSwitchRows.js')
  const harness = await toolGroupsHarness()
  const toggled = []
  const mk = (group, off) => ({ group, off, usable: true, tools: [] })
  const base = { builtins: [mk('triggers', false), mk('changes', true), mk('files', false), mk('saved_agents', false)], addons: [], servers: [], connectors: [], commands: {}, setBuiltinsOn() {}, setAddonsOn() {}, setMCPOn() {}, toggleBuiltin() {} }
  const grants = { list: agentGrants(optionGrants.slice(0, 8), ['memory.write', 'triggers.propose', 'act.send', 'from.the.future']), editable: true, onToggle: (n, on) => toggled.push([n, on]) }
  const groups = harness.render({ ...base, grants }, {})
  const row = (id) => groups.find((x) => x.key === 'builtins').rows.find((r) => r.id === id)
  assert.deepEqual(row('triggers').options.map((o) => [o.id, o.on, o.blocked]), [['triggers.propose', true, false], ['triggers.run', false, false]])
  assert.deepEqual(row('files').options.map((o) => o.id), ['files.upload'], 'only the grants the server declares')
  // The changes switch is off: its options are greyed, with the reason, and keep their stored state.
  assert.deepEqual(row('changes').options.map((o) => [o.id, o.on, o.blocked]), [['memory.write', true, true], ['skills.write', false, true]])
  assert.equal(row('changes').options[0].reason, 'tools.optionNeeds')
  assert.equal(row('saved_agents').options, undefined, 'saved agents is one switch')
  row('triggers').options[1].onToggle(true)
  assert.deepEqual(toggled, [['triggers.run', true]])
  // A grant with no switch to hang from is in a section; an unknown held one stays visible.
  assert.deepEqual(groups.find((x) => x.key === 'messaging').rows.map((r) => r.id), ['talkback.channels'])
  assert.deepEqual(groups.find((x) => x.key === 'unattended').rows.map((r) => [r.id, !!r.warn, r.on]), [['act.send', true, true]])
  assert.deepEqual(groups.find((x) => x.key === 'grants').rows.map((r) => r.id), ['from.the.future'])
  const all = groups.flatMap((g) => g.rows.flatMap((r) => [r.id, ...(r.options || []).map((o) => o.id)]))
  assert.equal(new Set(all.filter((x) => x.includes('.'))).size, all.filter((x) => x.includes('.')).length, 'no grant twice')
  // The count and the bulk rows leave the grants out.
  assert.deepEqual(groups.filter((g) => !g.onSet).map((g) => g.key), ['messaging', 'unattended', 'grants'])
  assert.deepEqual(switchCounts(switchRows(groups)), { on: 3, total: 4 })
  // A chat shows the held grants read only.
  const chat = harness.render({ ...base, grants: { list: agentGrants(optionGrants, ['memory.write'], { heldOnly: true }), editable: false } }, {})
  assert.deepEqual(chat.find((x) => x.key === 'builtins').rows.find((r) => r.id === 'changes').options.map((o) => [o.id, o.readOnly]), [['memory.write', true]])
  // No grants prop, no content.
  assert.equal(harness.render(base, {}).filter((x) => x.key === 'grants' || x.key === 'messaging').every((g) => g.rows.length === 0), true)
})

test('a connection that does not allow a grant the agent holds is named under that option, with a link', async () => {
  const { agentGrants } = await import('../../src/utils/agentTools.js')
  const { agentConnections } = await import('../../src/utils/toolOptions.js')
  const harness = await toolGroupsHarness()
  const connections = agentConnections(
    [{ id: 'b1', name: 'Bender', kind: 'slack', agent_id: 'a1', caps: [] }, { id: 'b2', name: 'Other', kind: 'slack', agent_id: 'a2', caps: [] }, { id: 'b3', name: 'Ok', kind: 'telegram', agent_id: 'a1', caps: ['triggers.propose'] }],
    [{ kind: 'slack', addon: 'slack', title: 'Slack' }], 'a1')
  assert.deepEqual(connections.map((c) => [c.name, c.to]), [['Slack Bender', '/app/addons/slack/apps?binding=b1'], ['telegram Ok', '/app/chats/connectors']])
  const base = { builtins: [{ group: 'triggers', off: false, usable: true }], addons: [], servers: [], connectors: [], commands: {}, toggleBuiltin() {}, connections }
  const grants = { list: agentGrants(optionGrants, ['triggers.propose', 'act.send']), editable: true, onToggle() {} }
  const groups = harness.render({ ...base, grants }, {})
  const rows = groups.find((x) => x.key === 'builtins').rows[0].options
  assert.deepEqual(rows[0].notes.map((n) => [n.key, n.link.to]), [['b1', '/app/addons/slack/apps?binding=b1']])
  assert.deepEqual(rows[1].notes, [], 'an option that is off has no note')
  assert.deepEqual(groups.find((x) => x.key === 'unattended').rows[0].notes, [], 'the act grants are the agent\'s alone')
  // The agent not saved yet has no connections.
  assert.deepEqual(agentConnections([{ agent_id: '' }], [], ''), [])
})

test('saved agents is one switch: either stored value turns it on, off clears both, on adds only the group', async () => {
  const { agentBuiltins, savedAgentsGrantAfter, builtinsAfter } = await import('../../src/utils/agentTools.js')
  const groups = [{ group: 'saved_agents', opt_in: true }]
  assert.equal(agentBuiltins(groups, { builtins_on: [], builtins: ['saved_agents'] })[0].off, false)
  assert.equal(agentBuiltins(groups, { builtins_on: ['saved_agents'], builtins: [] })[0].off, false)
  assert.equal(agentBuiltins(groups, { builtins_on: [], builtins: [] })[0].off, true)
  const held = { builtins_on: ['saved_agents'], builtins: ['saved_agents', 'memory.write'] }
  assert.deepEqual(savedAgentsGrantAfter(held, ['saved_agents'], false), ['memory.write'])
  assert.deepEqual(builtinsAfter(held, groups, ['saved_agents'], false).builtins_on, [])
  assert.deepEqual(savedAgentsGrantAfter({ builtins: [] }, ['saved_agents'], true), [], 'switching on never adds the first grant')
  assert.deepEqual(savedAgentsGrantAfter(held, ['web'], false), held.builtins)
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
