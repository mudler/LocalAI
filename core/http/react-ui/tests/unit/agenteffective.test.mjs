import test from 'node:test'
import assert from 'node:assert/strict'
import { effectiveLines, flatRows, summariseAccess, blockedCount, fixOf } from '../../src/utils/agentEffective.js'
import { tryRequest, notApplied, tryLines } from '../../src/utils/agentTryIt.js'
import { emptyAgent, newAgent } from '../../src/utils/chatAgents.js'

const groups = [
  { key: 'builtins', rows: [
    { id: 'web', label: 'Look things up online', on: true, usable: true },
    { id: 'files', label: 'Your files', on: true, usable: true },
    { id: 'shell', label: 'Run commands', on: true, usable: false, reason: 'Turned off by the server.' },
    { id: 'triggers', label: 'Triggers', on: false, usable: true, options: [{ id: 'triggers.propose', label: 'Also propose', on: true, blocked: true }] },
  ] },
  { key: 'messaging', rows: [{ id: 'talkback.channels', label: 'Reply on known channels', on: true, notes: [{ key: 'c1', text: 'Slack Bender does not allow this yet.', link: { to: '/app/chats/connectors' } }] }] },
  { key: 'unattended', rows: [{ id: 'act.send', label: 'Send messages on its own', on: true }] },
]
const base = (over = {}) => ({ form: { ...newAgent(), ...over }, rows: flatRows(groups), skills: [], memory: { enabled: true, save_mode: 'ask' }, access: [], isEdit: false })
const byId = (lines, id) => lines.find((l) => l.id === id)

test('effective: tools that work are listed, one that cannot is blocked with a fix on this form', () => {
  const lines = effectiveLines(base())
  assert.equal(byId(lines, 'tools').state, 'allowed')
  assert.equal(byId(lines, 'tools').vars.count, 2)
  const shell = byId(lines, 'tool-shell')
  assert.equal(shell.state, 'blocked')
  assert.deepEqual(shell.fix, { kind: 'section', section: 'capabilities' })
  assert.equal(shell.vars.reason, 'Turned off by the server.')
})

test('effective: a right under a switch that is off is blocked, and a connection that lacks it is named', () => {
  const lines = effectiveLines(base())
  assert.equal(byId(lines, 'grant-triggers.propose').code, 'tools_switch_off')
  const conn = byId(lines, 'grant-talkback.channels-c1')
  assert.equal(conn.state, 'blocked')
  assert.deepEqual(conn.fix, { kind: 'link', to: '/app/chats/connectors' })
  assert.ok(blockedCount(lines) >= 3)
  assert.equal(lines[0].state, 'blocked')
})

test('effective: rights that act on their own are said apart', () => {
  const lines = effectiveLines(base())
  assert.equal(byId(lines, 'acts').vars.names, 'Send messages on its own')
})

test('effective: no tool on says so', () => {
  const lines = effectiveLines({ ...base(), rows: [] })
  assert.equal(byId(lines, 'tools').state, 'off')
})

test('effective: memory is off in Settings says so with the fix, whatever the choice', () => {
  const lines = effectiveLines({ ...base(), memory: { enabled: false, save_mode: 'ask' } })
  const m = byId(lines, 'memory-off')
  assert.equal(m.code, 'memory_off_user')
  assert.equal(m.fix.to, '/app/account?tab=memory')
  assert.equal(byId(lines, 'memory'), undefined)
})

test('effective: the save level is the stricter of the person and the agent', () => {
  const lines = effectiveLines(base({ memory_save: 'always' }))
  assert.equal(byId(lines, 'memory-level').text, 'memory.level.always')
  assert.equal(effectiveLines({ ...base({ memory_save: 'never' }) }).find((l) => l.id === 'memory-level').text, 'memory.level.never')
  assert.equal(byId(effectiveLines(base()), 'memory-level').text, 'memory.level.ask')
  assert.equal(byId(effectiveLines({ ...base(), memory: { enabled: true, save_mode: 'auto' } }), 'memory-level').state, 'allowed')
})

test('effective: a choice that saves without the right to remember is blocked', () => {
  const lines = effectiveLines(base({ builtins: [] }))
  assert.equal(byId(lines, 'memory-grant').state, 'blocked')
  assert.equal(effectiveLines(base({ memory: 'none' })).find((l) => l.id === 'memory').text, 'memory.none')
})

test('effective: knowledge collections and files scope', () => {
  const lines = effectiveLines(base({ collections: ['handbook', 'notes'], folders: [{ path: '/A' }] }))
  assert.equal(byId(lines, 'knowledge').vars.count, 2)
  assert.equal(byId(lines, 'files').text, 'files.folders')
  assert.equal(byId(effectiveLines(base()), 'knowledge').state, 'off')
})

test('effective: more than five names are cut and counted', () => {
  const rows = flatRows([{ key: 'builtins', rows: Array.from({ length: 8 }, (_, i) => ({ id: `g${i}`, label: `G${i}`, on: true, usable: true })) }])
  const t = byId(effectiveLines({ ...base(), rows }), 'tools')
  assert.equal(t.vars.count, 8)
  assert.equal(t.vars.more, 3)
})

const view = { subjects: [
  { id: 'anyone', decisions: [
    { capability: 'triggers.propose', allowed: false, code: 'sender_needs_pairing', message: 'Pair the sender.', fix: { kind: 'pair_sender' } },
    { capability: 'memory.write', allowed: false, code: 'agent_permission_off', message: 'The agent has no such right.', fix: { kind: 'open_agent', target: 'a1' } },
    { capability: 'files.upload', allowed: true },
  ] },
  { id: 'paired', decisions: [
    { capability: 'triggers.propose', allowed: true },
    { capability: 'memory.write', allowed: false, code: 'agent_permission_off', message: 'The agent has no such right.', fix: { kind: 'open_agent', target: 'a1' } },
  ] },
] }

test('access: a capability some sender may use is allowed; refusals come nearest the agent first', () => {
  const s = summariseAccess(view)
  assert.equal(s.allowed, 2)
  assert.deepEqual(s.blocked.map((b) => b.capability), ['memory.write'])
  assert.deepEqual(fixOf({ kind: 'open_agent' }), { kind: 'section', section: 'capabilities' })
  assert.deepEqual(fixOf({ kind: 'open_server_setting' }), { kind: 'link', to: '/app/settings' })
  assert.deepEqual(fixOf({ kind: 'open_connection' }, { to: '/app/x' }), { kind: 'link', to: '/app/x' })
  assert.equal(fixOf(null), null)
})

test('effective: connection lines come from the preview and say plainly what is blocked', () => {
  const lines = effectiveLines({ ...base(), isEdit: true, access: [{ id: 'b1', name: 'Slack Bender', to: '/app/x', status: 'ok', view }] })
  assert.equal(byId(lines, 'conn-b1').vars.allowed, 2)
  const blocked = byId(lines, 'conn-b1-memory.write')
  assert.equal(blocked.code, 'agent_permission_off')
  assert.equal(blocked.vars.message, 'The agent has no such right.')
  assert.equal(byId(effectiveLines({ ...base(), isEdit: true, rows: [{ id: 'memory.write', label: 'Remember things', kind: 'grant' }], access: [{ id: 'b1', name: 'S', to: '/x', status: 'ok', view }] }), 'conn-b1-memory.write').vars.capability, 'Remember things')
  assert.deepEqual(blocked.fix, { kind: 'section', section: 'capabilities' })
  assert.equal(byId(effectiveLines({ ...base(), isEdit: true, access: [{ id: 'b1', name: 'S', to: '/x', status: 'error' }] }), 'conn-b1').text, 'connections.error')
  assert.equal(byId(effectiveLines(base()), 'connections').text, 'connections.unsaved')
  assert.equal(byId(effectiveLines({ ...base(), isEdit: true }), 'connections').text, 'connections.none')
})

test('try it: the request uses the form model, the switches and the instructions', () => {
  const tools = {
    addons: [{ id: 'office', off: false }, { id: 'tracker', off: true }, { id: 'old', off: false, gone: true }],
    connectors: [{ id: 'm1', off: true }, { id: 'm2', off: false }],
    builtins: [{ group: 'files', off: true }, { group: 'saved_agents', off: false, opt_in: true }],
    skills: [],
  }
  const r = tryRequest({ form: { ...emptyAgent(), model: 'qwen3-8b', instructions: 'Be brief.' }, tools, models: [{ id: 'other' }] })
  assert.equal(r.model, 'qwen3-8b')
  assert.equal(r.usedDefaultModel, false)
  assert.deepEqual(r.switches, { addons_off: ['tracker'], mcp_off: ['connector:m1'], builtins_off: ['files'], builtins_on: ['saved_agents'] })
  assert.equal(r.settings.system_prompt, 'Be brief.')
})

test('try it: no model set uses the first chat model and says so; none at all gives no request', () => {
  const tools = { addons: [], connectors: [], builtins: [], skills: [] }
  const r = tryRequest({ form: emptyAgent(), tools, models: [{ id: 'first' }, { id: 'second' }] })
  assert.equal(r.model, 'first')
  assert.equal(r.usedDefaultModel, true)
  assert.equal(r.switches, null)
  assert.equal(tryRequest({ form: emptyAgent(), tools, models: [] }), null)
})

test('try it: names what the test does not use, and keeps only readable lines', () => {
  assert.deepEqual(notApplied(emptyAgent()), [])
  assert.deepEqual(notApplied(newAgent()), ['grants', 'memory'])
  assert.deepEqual(notApplied({ ...newAgent(), collections: ['a'], folders: [{}] }), ['grants', 'memory', 'knowledge', 'folders'])
  const lines = tryLines([{ seq: 1, role: 'user', text: 'hi' }, { seq: 2, role: 'reasoning', text: 'x' }, { seq: 3, role: 'tool', text: 'web_search\n{...}' }, { seq: 4, role: 'assistant', text: ' ' }, { seq: 5, role: 'assistant', text: 'ok' }])
  assert.deepEqual(lines.map((l) => [l.role, l.text]), [['user', 'hi'], ['tool', 'web_search'], ['assistant', 'ok']])
})
