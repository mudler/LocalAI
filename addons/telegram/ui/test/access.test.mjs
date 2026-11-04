import test from 'node:test'
import assert from 'node:assert/strict'
import {
  changedFields, fixAction, remainingChecks, sentenceCase, isProblem, pickSubject, previewBody, previewId, readinessRows, reachable, senderOptions, unusable, visibleRows,
} from '../../../internal/connector-ui/src/access.js'
import { connectorForm, emptyConnector } from '../../../internal/connector-ui/src/connector.js'

const ONE = [{ key: 'allow', testId: 'connector-allow' }]
const TWO = [{ key: 'people', testId: 'connector-allow', match: (x) => !x.startsWith('C') }, { key: 'channels', testId: 'connector-allow-channels', match: (x) => x.startsWith('C') }]
const dec = (capability, over = {}) => ({ capability, allowed: true, ...over })
const blocked = (capability, over = {}) => ({ capability, allowed: false, message: `blocked ${capability}`, blocked_by: 'connection', fix: { kind: 'open_connection', target: 'caps', field: capability }, ...over })
const view = (subjects) => ({ subjects: Object.entries(subjects).map(([id, decisions]) => ({ id, title: id, decisions })) })
const agents = [{ id: 'a1', name: 'Support', model: 'qwen' }, { id: 'a2', name: 'Bare', model: '' }]
const base = (form, over = {}) => ({
  form: { ...emptyConnector(), ...form }, saved: null, kindInfo: {}, capabilities: [{ name: 'memory.write', title: 'Save to memory' }, { name: 'triggers.propose', title: 'Propose triggers' }],
  agents, lists: ONE, tokenTyped: false, token: null, preview: { status: 'ready', view: view({ listed: [], paired: [], anyone: [] }) }, paired: [], ...over,
})
const row = (rows, id) => rows.find((r) => r.id === id)

test('access: the preview body holds the form values, no token, and the kind only for a new connection', () => {
  const b = previewBody({ ...emptyConnector(), agent_id: 'a1', allow: [' 42 ', '42', ''], caps: ['memory.write'], grants: [{ role: 'r', caps: ['memory.write'] }], token: 'secret' }, 'telegram')
  assert.deepEqual(b, { agent_id: 'a1', accept_any: false, allow: ['42'], caps: ['memory.write'], grants: [{ role: 'r', caps: ['memory.write'] }], kind: 'telegram' })
  assert.equal(JSON.stringify(b).includes('secret'), false)
  assert.equal(previewId({ id: '' }), 'new')
  const saved = previewBody({ ...emptyConnector(), id: 'b1', accept_any: true, allow: ['x'] }, 'telegram')
  assert.equal('kind' in saved, false)
  assert.deepEqual(saved.allow, [], 'anyone sends no list')
})

test('access: the sender selector offers channels only for a kind with channels, anyone only with accept_any, and each role', () => {
  const ids = (f, lists) => senderOptions(f, { roles: [{ id: 'admin', title: 'Admin' }] }, lists).map((o) => o.id)
  assert.deepEqual(ids({}, ONE), ['listed', 'paired', 'role:admin'])
  assert.deepEqual(ids({}, TWO), ['listed', 'channel_member', 'paired', 'role:admin'])
  assert.deepEqual(ids({ accept_any: true }, TWO), ['anyone', 'paired', 'role:admin'])
  const v = view({ listed: [], anyone: [] })
  assert.equal(pickSubject(v, 'channel_member').id, 'anyone')
  assert.equal(pickSubject(v, 'paired'), null)
})

test('access: fix routing maps each kind to a jump, a link, words or nothing', () => {
  const ctx = { agentId: 'a1', isAdmin: true, lists: TWO, form: {} }
  assert.deepEqual(fixAction({ kind: 'open_connection', target: 'caps', field: 'memory.write' }, ctx).selectors[0], '[data-testid="connector-cap-switch-memory.write"]')
  assert.match(fixAction({ kind: 'open_connection', target: 'grants' }, ctx).selectors[0], /connector-roles/)
  assert.match(fixAction({ kind: 'open_connection', target: 'agent_id' }, ctx).selectors[0], /connector-agent/)
  assert.deepEqual(fixAction({ kind: 'open_agent', target: 'a 1' }, ctx), { type: 'link', to: '/app/agents/a%201/edit' })
  assert.deepEqual(fixAction({ kind: 'open_agent', target: '' }, ctx), { type: 'link', to: '/app/agents/a1/edit' })
  assert.deepEqual(fixAction({ kind: 'open_server_setting', target: 'disable_triggers' }, ctx), { type: 'link', to: '/app/settings' })
  assert.deepEqual(fixAction({ kind: 'open_server_setting', target: 'x' }, { ...ctx, isAdmin: false }), { type: 'text', key: 'askAdmin' })
  assert.deepEqual(fixAction({ kind: 'open_server_setting', target: 'x' }, { ...ctx, isAdmin: null }), { type: 'text', key: 'askAdmin' })
  assert.match(fixAction({ kind: 'pair_sender', target: 'pairing' }, ctx).selectors[0], /connector-pair-start/)
  assert.match(fixAction({ kind: 'add_person', target: 'allow' }, ctx).selectors[0], /connector-allow"\] input/, 'the first list holds people')
  assert.match(fixAction({ kind: 'add_person', target: 'allow' }, { ...ctx, form: { accept_any: true } }).selectors[0], /connector-who/)
  assert.deepEqual(fixAction({ kind: 'save' }, ctx), { type: 'save', selectors: ['[data-testid="connector-save"]'] })
  assert.equal(fixAction(null, ctx), null)
  assert.equal(fixAction({ kind: 'open_agent', target: '' }, {}), null)
  assert.equal(fixAction({ kind: 'nope' }, ctx), null)
})

test('access: the readiness token row follows the form, the saved binding and the last test', () => {
  const token = (over) => row(readinessRows(base({}, over)), 'token')
  assert.equal(token().state, 'attention')
  assert.equal(token({ tokenTyped: true }).state, 'after_save')
  assert.equal(token({ tokenTyped: true }).fix.kind, 'save')
  const saved = { form: { ...emptyConnector(), id: 'b1' } }
  assert.equal(row(readinessRows(base({ id: 'b1' })), 'token').state, 'ok')
  assert.equal(row(readinessRows(base({ id: 'b1' }, { token: { ok: false, reason: 'bad token' } })), 'token').state, 'broken')
  assert.equal(row(readinessRows(base({ id: 'b1' }, { token: { ok: false, reason: 'bad token' } })), 'token').params.reason, 'bad token')
  assert.equal(row(readinessRows(base({ id: 'b1' }, { token: { ok: true, bot: 'x' } })), 'token').state, 'ok')
  assert.equal(row(readinessRows(base({ id: 'b1' }, { tokenTyped: true })), 'token').state, 'after_save')
  assert.ok(saved)
})

test('access: the agent row names a missing agent, one without a model, and a ready one', () => {
  const a = (f, o) => row(readinessRows(base(f, o)), 'agent')
  assert.equal(a({}).state, 'attention')
  assert.equal(a({}, { agentOptional: true }).state, 'off')
  assert.equal(a({ agent_id: 'zz' }).state, 'attention')
  assert.equal(a({ agent_id: 'a2' }).state, 'broken')
  assert.deepEqual(a({ agent_id: 'a2' }).fix, { kind: 'open_agent', target: 'a2' })
  assert.equal(a({ agent_id: 'a1' }).state, 'after_save', 'a new connection is not saved yet')
  const saved = connectorForm({ id: 'b1', agent_id: 'a1' })
  assert.equal(a({ id: 'b1', agent_id: 'a1' }, { saved }).state, 'ok')
})

test('access: the who row catches nobody, and the channel-only trap', () => {
  const w = (f, lists = TWO) => row(readinessRows(base(f, { lists })), 'who')
  assert.equal(w({ allow: [] }).state, 'attention')
  assert.equal(w({ allow: [] }).key, 'ready.who.nobody')
  const trap = w({ allow: ['C001', 'C002'] })
  assert.equal(trap.state, 'attention')
  assert.equal(trap.key, 'ready.who.channelsOnly')
  assert.equal(trap.fix.kind, 'add_person')
  assert.equal(w({ allow: ['U1', 'C001'] }, TWO).key, 'ready.who.mixed')
  assert.equal(w({ accept_any: true }).state, 'after_save')
  assert.equal(w({ allow: ['U1'] }, ONE).params.count, 1)
})

test('access: which senders can reach the bot with the form as it is', () => {
  const v = view({ listed: [], anyone: [], paired: [], 'role:admin': [] })
  assert.deepEqual(reachable({ allow: ['U1'] }, TWO, v), ['listed', 'paired', 'role:admin'])
  assert.deepEqual(reachable({ allow: ['C1'] }, TWO, v), ['anyone', 'role:admin'])
  assert.deepEqual(reachable({ allow: ['U1', 'C1'] }, TWO, v), ['listed', 'paired', 'anyone', 'role:admin'])
  assert.deepEqual(reachable({ allow: [] }, TWO, v), [])
  assert.deepEqual(reachable({ accept_any: true }, TWO, v), ['anyone', 'paired', 'role:admin'])
})

test('access: a ticked permission that nobody can use is named with the first blocker of the host', () => {
  const v = view({ listed: [blocked('memory.write', { blocked_by: 'agent', fix: { kind: 'open_agent', target: 'a1', field: 'memory.write' } })], paired: [blocked('memory.write', { blocked_by: 'agent' })] })
  const d = unusable('memory.write', v, { allow: ['U1'] }, ONE, [], Date.now())
  assert.equal(d.blocked_by, 'agent')
  const ok = view({ listed: [dec('memory.write', { via: 'allowlist' })] })
  assert.equal(unusable('memory.write', ok, { allow: ['U1'] }, ONE, [], Date.now()), null)
  assert.equal(unusable('memory.write', ok, { allow: [] }, ONE, [], Date.now()).code, 'nobody_let_in')
  // Only a paired sender holds it, and nobody is paired.
  const sens = view({ listed: [blocked('profile.write', { code: 'sender_needs_pairing', blocked_by: 'sender' })], paired: [dec('profile.write', { via: 'paired' })] })
  const stuck = unusable('profile.write', sens, { allow: ['U1'] }, ONE, [], Date.now())
  assert.equal(stuck.code, 'nobody_paired')
  assert.equal(stuck.fix.kind, 'pair_sender')
  assert.equal(unusable('profile.write', sens, { allow: ['U1'] }, ONE, [{ sender_id: 'U1', expires_at: new Date(Date.now() + 1e6).toISOString() }], Date.now()), null)
  // The channel-only trap: a listed person would be allowed, but none is listed.
  const trap = view({ listed: [dec('memory.write')], anyone: [blocked('memory.write', { code: 'channel_not_trusted', blocked_by: 'sender', fix: { kind: 'add_person', target: 'allow' } })] })
  assert.equal(unusable('memory.write', trap, { allow: ['C1'] }, TWO, [], Date.now()).code, 'channel_not_trusted')
})

test('access: the permissions row has no rows to show with none ticked, and names dead ones', () => {
  const v = view({ listed: [blocked('memory.write')], paired: [blocked('memory.write')] })
  const rows = readinessRows(base({ id: 'b1', agent_id: 'a1', allow: ['U1'], caps: ['memory.write'] }, { preview: { status: 'ready', view: v } }))
  const p = row(rows, 'permissions')
  assert.equal(p.state, 'attention')
  assert.equal(p.params.message, 'blocked memory.write')
  assert.equal(p.params.names, 'Save to memory')
  assert.deepEqual(p.fix, { kind: 'open_connection', target: 'caps', field: 'memory.write' })
  assert.equal(row(readinessRows(base({})), 'permissions').state, 'off')
  assert.equal(row(readinessRows(base({ id: 'b1' }, { saved: connectorForm({ id: 'b1' }) })), 'permissions').state, 'attention')
})

test('access: the triggers row is off by choice, shows the host blocker, or names the approval mode', () => {
  const t = (f, o) => row(readinessRows(base({ allow: ['U1'], ...f }, o)), 'triggers')
  assert.equal(t({}).state, 'off')
  assert.equal(t({}).fix.field, 'triggers.propose')
  const v = view({ listed: [blocked('triggers.propose', { blocked_by: 'server', fix: { kind: 'open_server_setting', target: 'disable_triggers' } })] })
  const b = t({ caps: ['triggers.propose'] }, { preview: { status: 'ready', view: v } })
  assert.equal(b.state, 'attention')
  assert.equal(b.fix.kind, 'open_server_setting')
  assert.equal(b.params.message, 'blocked triggers.propose')
  const ok = view({ listed: [dec('triggers.propose'), ], paired: [dec('triggers.propose')] })
  const saved = connectorForm({ id: 'b1', caps: ['triggers.propose'], allow: ['U1'], approval: { mode: 'auto' } })
  const r = t({ id: 'b1', caps: ['triggers.propose'], approval: { mode: 'auto', acts: [] } }, { preview: { status: 'ready', view: ok }, saved })
  assert.equal(r.state, 'ok')
  assert.equal(r.key, 'ready.triggers.ok.auto')
  // The same form, changed from the saved one: it works after a save.
  assert.equal(t({ id: 'b1', caps: ['triggers.propose'], approval: { mode: 'ask', acts: [] } }, { preview: { status: 'ready', view: ok }, saved }).state, 'after_save')
})

test('access: preview states give checking and unknown rows, which are not hidden as fine', () => {
  const f = { allow: ['U1'], caps: ['triggers.propose', 'memory.write'] }
  const loading = readinessRows(base(f, { preview: { status: 'loading' } }))
  assert.equal(row(loading, 'triggers').state, 'checking')
  assert.equal(row(loading, 'permissions').state, 'checking')
  const failed = readinessRows(base(f, { preview: { status: 'error' } }))
  assert.equal(row(failed, 'triggers').state, 'unknown')
  assert.equal(isProblem('unknown'), true)
  assert.equal(isProblem('checking'), false)
})

test('access: files and notifications rows', () => {
  const r = (f, o) => readinessRows(base({ allow: ['U1'], ...f }, o))
  assert.equal(row(r({}), 'files').state, 'off')
  assert.equal(row(r({ send_files: false }), 'files').key, 'ready.files.offBoth')
  assert.equal(row(r({}, { kindInfo: { files: { receive: {}, send: {} } } }), 'files'), undefined, 'a kind that moves no file has no row')
  const v = view({ listed: [blocked('files.upload')], paired: [blocked('files.upload')] })
  assert.equal(row(r({ caps: ['files.upload'] }, { preview: { status: 'ready', view: v } }), 'files').state, 'attention')
  assert.equal(row(r({}), 'notify').state, 'off')
  assert.equal(row(r({ notify_chat: '99' }), 'notify').params.chat, '99')
})

test('access: collapsed mode keeps the rows that need the owner and counts the hidden ones', () => {
  const rows = readinessRows(base({ allow: ['U1'] }))
  const { shown, hidden } = visibleRows(rows, false)
  assert.ok(shown.every((x) => isProblem(x.state)))
  assert.equal(shown.length + hidden, rows.length)
  assert.equal(visibleRows(rows, true).hidden, 0)
  assert.equal(visibleRows(rows, true).shown.length, rows.length)
})

test('access: changed fields compare the form with the saved binding', () => {
  const saved = connectorForm({ id: 'b1', agent_id: 'a1', allow: ['U1', 'U2'], caps: ['a', 'b'] })
  assert.equal(changedFields({ ...saved }, saved).size, 0)
  assert.deepEqual([...changedFields({ ...saved, allow: ['U2', 'U1'], caps: ['b', 'a'] }, saved)], [], 'order does not count')
  assert.deepEqual([...changedFields({ ...saved, agent_id: 'a2', notify_chat: '1' }, saved)].sort(), ['agent_id', 'notify_chat'])
  assert.ok(changedFields(saved, null).has('caps'))
})

test('access: the unchecked links never list the sender, which the selector already names', () => {
  assert.deepEqual(remainingChecks(['connection', 'sender']), ['connection'])
  assert.deepEqual(remainingChecks(['sender']), [])
  assert.deepEqual(remainingChecks(['sender', 'approval']), ['approval'])
  assert.deepEqual(remainingChecks(undefined), [])
})

test('access: a title is a sentence with the product name right', () => {
  assert.equal(sentenceCase('change things outside localai'), 'Change things outside LocalAI')
  assert.equal(sentenceCase('Post to approved channels'), 'Post to approved channels')
  assert.equal(sentenceCase(''), '')
})
