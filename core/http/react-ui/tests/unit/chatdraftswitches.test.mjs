import test from 'node:test'
import assert from 'node:assert/strict'
import { applyChoices, draftSwitches, emptyChoices, withChoice } from '../../src/utils/chatDraftSwitches.js'

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
