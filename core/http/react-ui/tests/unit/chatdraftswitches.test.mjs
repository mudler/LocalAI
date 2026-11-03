import test from 'node:test'
import assert from 'node:assert/strict'
import { applyChoices, draftListSwitches, draftSwitches, emptyChoices, withChoice } from '../../src/utils/chatDraftSwitches.js'

const ref = (id) => `connector:${id}`

test('draft switches: a plain draft sends no switches', () => {
  assert.equal(draftSwitches({
    addons: [{ id: 'office', off: false }], mcpOff: [],
    builtins: [{ group: 'web', off: false }, { group: 'saved_agents', off: true, opt_in: true }],
    skills: [{ id: 'builtin:pdf', enabled: true }],
  }), null)
})

test('draft switches: each list takes the form of its own PUT', () => {
  assert.deepEqual(draftSwitches({
    addons: [{ id: 'sandbox', off: true }, { id: 'office', off: true }, { id: 'tables', off: false }],
    mcpOff: ['connector:k2', 'docs'],
    builtins: [{ group: 'web', off: true }, { group: 'saved_agents', off: false, opt_in: true }],
    skills: [{ id: 'builtin:pdf', enabled: false }, { name: 'notes', enabled: true }],
  }), {
    addons_off: ['office', 'sandbox'],
    mcp_off: ['connector:k2', 'docs'],
    builtins_off: ['web'],
    builtins_on: ['saved_agents'],
    skills_off: ['builtin:pdf'],
  })
})

test('draft switches: the choices survive a list read again for another model', () => {
  let c = emptyChoices()
  c = withChoice(c, 'addons', 'office', true)
  c = withChoice(c, 'mcp', 'connector:k1', true)
  c = withChoice(c, 'mcp', 'docs', false)
  c = withChoice(c, 'builtins', 'web', true)
  c = withChoice(c, 'skills', 'builtin:pdf', false)
  const got = applyChoices({
    addons: [{ id: 'office', off: false }, { id: 'sandbox', off: false }],
    servers: [{ name: 'docs', off: true }],
    connectors: [{ id: 'k1', name: 'Mail', off: false }],
    mcpOff: ['docs'],
    builtins: [{ group: 'web', off: false }],
    skills: [{ id: 'builtin:pdf', enabled: true }, { id: 'personal:x', enabled: true }],
  }, c, ref)
  assert.deepEqual(got.addons.map((a) => a.off), [true, false])
  assert.equal(got.servers[0].off, false)
  assert.equal(got.connectors[0].off, true)
  assert.deepEqual(got.mcpOff, ['connector:k1'])
  assert.equal(got.builtins[0].off, true)
  assert.deepEqual(got.skills.map((s) => s.enabled), [false, true])
})

test('draft switches: a choice for a row the list no longer has is ignored', () => {
  const c = withChoice(emptyChoices(), 'addons', 'gone', true)
  assert.deepEqual(applyChoices({ addons: [{ id: 'office', off: false }] }, c).addons, [{ id: 'office', off: false }])
})

test('draft list switches: a plain draft asks the plain question', () => {
  assert.equal(draftListSwitches(emptyChoices(), [{ group: 'shell', off: false }]), null)
})

test('draft list switches: the choices go as the names the server checks', () => {
  let choices = withChoice(emptyChoices(), 'builtins', 'shell', true)
  choices = withChoice(choices, 'builtins', 'saved_agents', false)
  choices = withChoice(choices, 'builtins', 'web', false)
  choices = withChoice(choices, 'addons', 'sandbox', true)
  choices = withChoice(choices, 'addons', 'office', false)
  const builtins = [{ group: 'shell' }, { group: 'web' }, { group: 'saved_agents', opt_in: true }]
  assert.deepEqual(draftListSwitches(choices, builtins), { addons_off: ['sandbox'], builtins_off: ['shell'], builtins_on: ['saved_agents'] })
})

test('draft list switches: a group the list does not know is not sent', () => {
  const choices = withChoice(emptyChoices(), 'builtins', 'gone', true)
  assert.equal(draftListSwitches(choices, [{ group: 'shell' }]), null)
  assert.equal(draftListSwitches(choices), null)
})

test('draft switches: a connection chosen in the composer goes with the create request, and an agent keeps its own', () => {
  const rows = [
    { id: 'github', off: false, credentialed: true, connection: 'b2', connection_by: 'chat', connections: [{ id: 'b1' }, { id: 'b2' }] },
    { id: 'slack', off: false, credentialed: true, connection: 'b9', connection_by: 'agent', connections: [{ id: 'b9' }] },
    { id: 'other', off: false, credentialed: true, connection: 'b3', connection_by: 'only', connections: [{ id: 'b3' }] },
  ]
  assert.deepEqual(draftSwitches({ addons: rows }), { connections: { github: 'b2' } }, 'only a choice of the chat is sent: the agent and the lone connection need none')
  assert.equal(draftSwitches({ addons: [{ id: 'github', off: false, credentialed: true, connection: '', needs_connection: true }] }), null)
})

test('draft switches: a connection choice rides in the draft list query and is applied to the rows the list still has', () => {
  let choices = withChoice(emptyChoices(), 'connections', 'github', 'b2')
  assert.deepEqual(draftListSwitches(choices), { connections: { github: 'b2' } })
  choices = withChoice(choices, 'connections', 'github', '')
  assert.equal(draftListSwitches(choices), null, 'a cleared choice sends nothing')
  choices = withChoice(emptyChoices(), 'connections', 'github', 'b2')
  const list = { addons: [
    { id: 'github', off: false, credentialed: true, needs_connection: true, connections: [{ id: 'b1' }, { id: 'b2' }] },
    { id: 'gone', off: false, credentialed: true, needs_connection: true, connections: [{ id: 'b1' }] },
  ] }
  const got = applyChoices(list, withChoice(choices, 'connections', 'gone', 'b-deleted'), ref).addons
  assert.deepEqual([got[0].connection, got[0].connection_by, got[0].needs_connection], ['b2', 'chat', false])
  assert.equal(got[1].connection, undefined, 'a choice for a connection the row does not list is ignored')
  const agent = applyChoices({ addons: [{ id: 'github', credentialed: true, connection: 'b1', connection_by: 'agent', connections: [{ id: 'b1' }, { id: 'b2' }] }] }, choices, ref).addons
  assert.equal(agent[0].connection, 'b1', 'the choice of the agent is not changed from the composer')
})
