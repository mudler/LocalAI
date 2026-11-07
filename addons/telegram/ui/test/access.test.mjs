import test from 'node:test'
import assert from 'node:assert/strict'
import {
  toolOutcomeRows,  changedFields, fixAction, remainingChecks, sentenceCase, isProblem, previewBody, previewId, readinessRows, reachable, statusRows, unusable, visibleRows, forNewConnection, thingsToSet,
} from '../../../internal/connector-ui/src/access.js'
import { localizeAccess } from '../../../internal/connector-ui/src/connector.js'
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
  assert.deepEqual(b, { agent_id: 'a1', accept_any: false, allow: ['42'], allow_trust: {}, channels: [], approval_mode: 'ask', caps: ['memory.write'], grants: [{ role: 'r', caps: ['memory.write'] }], kind: 'telegram' })
  assert.equal(JSON.stringify(b).includes('secret'), false)
  assert.equal(previewId({ id: '' }), 'new')
  const saved = previewBody({ ...emptyConnector(), id: 'b1', accept_any: true, allow: ['x'] }, 'telegram')
  assert.equal('kind' in saved, false)
  assert.deepEqual(saved.allow, [], 'anyone sends no list')
})

test('access: the status line keeps the token, the agent and who is let in, and leaves the rest to the matrix', () => {
  const rows = readinessRows(base({ agent_id: 'a1', allow: ['42'], caps: ['memory.write'] }))
  assert.ok(rows.length > 3)
  assert.deepEqual(statusRows(rows).map((r) => r.id), ['token', 'agent', 'who'])
})

test('access: fix routing maps each kind to a jump, a link, words or nothing', () => {
  const ctx = { agentId: 'a1', isAdmin: true, lists: TWO, form: {} }
  assert.deepEqual(fixAction({ kind: 'open_connection', target: 'caps', field: 'memory.write' }, ctx).selectors[0], '[data-testid="connector-cap-switch-memory.write"]')
  assert.match(fixAction({ kind: 'open_connection', target: 'grants' }, ctx).selectors[0], /connector-matrix/)
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
  assert.equal(a({}, { toolOnly: true }), undefined, 'a tool-only kind has no agent row')
  assert.equal(a({ agent_id: 'zz' }, { toolOnly: true }), undefined)
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

test('access: a new connection has no save fix, and a row that only waits for the save is not a thing to set', () => {
  // Nothing typed: the token, the agent and who is let in each need the person.
  const empty = forNewConnection(statusRows(readinessRows(base({}))))
  assert.equal(thingsToSet(empty), 3)
  assert.ok(empty.every((r) => r.fix?.kind !== 'save'))
  // A token and an agent chosen: both wait for the save, which is not something to set; only the list of people is left.
  const typed = forNewConnection(statusRows(readinessRows(base({ agent_id: 'a1' }, { tokenTyped: true }))))
  assert.equal(row(typed, 'token').state, 'after_save')
  assert.equal(row(typed, 'agent').state, 'after_save')
  assert.equal(thingsToSet(typed), 1)
  assert.ok(typed.every((r) => r.fix?.kind !== 'save'), 'no Keep these changes on a connection that does not exist yet')
  // A saved connection keeps the fix.
  const saved = statusRows(readinessRows(base({ id: 'b1', agent_id: 'a2x' }, { tokenTyped: true })))
  assert.equal(row(saved, 'token').fix?.kind, 'save')
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
  assert.equal(sentenceCase('Post to other channels'), 'Post to other channels')
  assert.equal(sentenceCase(''), '')
})

test('access: a permission that holds back the tools of an add-on gets a row of its own, with the fix', () => {
  const hold = [{ addon: 'tracker', capability: 'tools.write', tools: ['create-issue', 'comment-issue'] }]
  const caps = [{ name: 'tools.write', title: 'Change things outside LocalAI' }]
  const withView = (form, subjects) => base(form, { capabilities: caps, preview: { status: 'ready', view: { ...view(subjects), addon_tools: hold } } })
  // Not ticked: the row says which permission to turn on, and jumps to it.
  const off = row(readinessRows(withView({ allow: ['42'], caps: [] }, { listed: [blocked('tools.write')], paired: [blocked('tools.write')] })), 'addons:tracker')
  assert.equal(off.state, 'attention')
  assert.equal(off.key, 'ready.addons.off')
  assert.equal(off.labelKey, 'ready.row.addons')
  assert.deepEqual(off.params, { addon: 'tracker', tools: 'create-issue, comment-issue', permission: 'Change things outside LocalAI' })
  assert.deepEqual(fixAction(off.fix, { lists: ONE, form: {} }).selectors[0], '[data-testid="connector-cap-switch-tools.write"]')
  // Ticked and usable by a listed sender: fine.
  const saved = connectorForm({ id: 'b1', agent_id: 'a1', allow: ['42'], caps: ['tools.write'] })
  const okInput = withView({ id: 'b1', agent_id: 'a1', allow: ['42'], caps: ['tools.write'] }, { listed: [dec('tools.write', { via: 'allowlist' })], paired: [dec('tools.write', { via: 'paired' })] })
  const ok = row(readinessRows({ ...okInput, saved }), 'addons:tracker')
  assert.equal(ok.state, 'ok')
  // Ticked and still blocked for everyone: the host's sentence and fix.
  const dead = row(readinessRows(withView({ allow: ['42'], caps: ['tools.write'] }, { listed: [blocked('tools.write', { blocked_by: 'sender', fix: { kind: 'pair_sender', target: 'pairing' } })], paired: [blocked('tools.write')] })), 'addons:tracker')
  assert.equal(dead.state, 'attention')
  assert.equal(dead.key, 'ready.addons.blocked')
  assert.match(dead.params.message, /blocked tools.write/)
  // No hold from the host: no row.
  assert.equal(readinessRows(base({ caps: ['tools.write'] })).some((r) => r.id.startsWith('addons:')), false)
})

test('access: not ticked, a paired person has the write tools of an add-on the agent is connected to, and the row says so once', () => {
  const caps = [{ name: 'tools.write', title: 'Let people who are not paired change things' }]
  const holdOf = (extra) => [{ addon: 'tracker', capability: 'tools.write', tools: ['create-issue'], connections: [{ name: 'Work tracker', write_mode: 'propose' }], without_tick: ['paired'], ...extra }]
  const saved = connectorForm({ id: 'b1', agent_id: 'a1', allow: ['42'], caps: [] })
  const input = (hold, over = {}) => base({ id: 'b1', agent_id: 'a1', allow: ['42'], caps: [] }, { saved, capabilities: caps, preview: { status: 'ready', view: { ...view({ listed: [blocked('tools.write')], paired: [dec('tools.write', { via: 'agent-connection' })] }), addon_tools: hold } }, ...over })
  const rowsOf = (hold, over) => readinessRows(input(hold, over)).filter((r) => r.id === 'addons:tracker')
  const paired = [{ sender_id: 'p', at: new Date().toISOString() }]
  // Covered: one info row, with the connection and the mode, no fix.
  const covered = rowsOf(holdOf(), { paired })
  assert.equal(covered.length, 1)
  assert.equal(covered[0].state, 'ok')
  assert.equal(covered[0].key, 'ready.addons.paired')
  assert.deepEqual(covered[0].params, { addon: 'tracker', tools: 'create-issue', permission: 'Let people who are not paired change things', connection: 'Work tracker', modeKey: 'ready.addons.mode.propose' })
  assert.equal(covered[0].fix, undefined)
  // Nobody paired yet: the pairing is what is missing.
  const none = rowsOf(holdOf(), { paired: [] })[0]
  assert.equal(none.state, 'attention')
  assert.equal(none.key, 'ready.addons.blocked')
  assert.equal(none.fix.kind, 'pair_sender')
  // The owner took the tick away: nobody has them, and the row says it was the owner's choice.
  const removed = rowsOf(holdOf({ without_tick: [], tick_removed: true }), { paired })[0]
  assert.equal(removed.key, 'ready.addons.removed')
  assert.equal(removed.state, 'attention')
  // Every connection is in write mode off: the row names the connection, not the tick.
  const off = rowsOf(holdOf({ without_tick: [], connections: [{ name: 'Work tracker', write_mode: 'off' }] }), { paired })[0]
  assert.equal(off.key, 'ready.addons.modeOff')
  assert.equal(off.params.connection, 'Work tracker')
  // The agent has no connection of the add-on: the tick is the way, as before.
  const none2 = rowsOf(holdOf({ without_tick: [], connections: undefined }), { paired })[0]
  assert.equal(none2.key, 'ready.addons.off')
  assert.equal(none2.fix.field, 'tools.write')
})

test('access: the readiness list says what the people of a channel may do, and where the bot may post, in one sentence each', () => {
  const isChannel = (x) => x.startsWith('C')
  const form = { ...base().form, allow: ['U1', 'C1', 'C2'], allow_trust: { C1: 'act' }, caps: ['files.upload', 'talkback.channels'], channels: [{ id: 'C7', label: '', approved: true }] }
  const rows = readinessRows({ ...base({}, {}), form, lists: TWO, isChannel })
  const trust = row(rows, 'channelTrust')
  assert.equal(trust.key, 'ready.channelTrust.answer', 'one channel is still answer only')
  assert.equal(trust.params.channels, 'C2')
  assert.equal(row(rows, 'postTo').key, 'ready.postTo')
  assert.equal(row(rows, 'postTo').params.count, 1)
  const ok = row(readinessRows({ ...base({}, {}), form: { ...form, allow: ['U1', 'C1'] }, lists: TWO, isChannel }), 'channelTrust')
  assert.equal(ok.key, 'ready.channelTrust.act')
  const none = row(readinessRows({ ...base({}, {}), form: { ...form, allow: ['U1', 'C1'], caps: [] }, lists: TWO, isChannel }), 'channelTrust')
  assert.equal(none.key, 'ready.channelTrust.noCaps', 'nothing is ticked: still refused')
  assert.deepEqual(previewBodyTrust(), { C1: 'act' })
})
function previewBodyTrust() { return previewBody({ allow: ['C1', 'U1'], allow_trust: { C1: 'act', C9: 'act' } }, 'slack').allow_trust }

test('access: the add-on tool calls are listed newest first, at most 20, with the words of the host and no content', () => {
  const ev = (i, over = {}) => ({ at: `2026-11-05T10:${String(59 - i).padStart(2, '0')}:00Z`, addon: 'tracker', tool: 'create-issue', outcome: 'held', code: 'approval_always', message: 'The change is waiting for the owner.', senders: ['U1'], ...over })
  const many = Array.from({ length: 25 }, (_, i) => ev(i))
  const rows = toolOutcomeRows({ tool_outcomes: many })
  assert.equal(rows.length, 20)
  assert.equal(rows[0].at, many[0].at, 'the host sends newest first and the page keeps the order')
  assert.deepEqual([rows[0].tool, rows[0].outcome, rows[0].tone, rows[0].code], ['tracker / create-issue', 'held', 'skipped', 'approval_always'])
  const tones = toolOutcomeRows({ tool_outcomes: ['ok', 'held', 'denied', 'error', 'confirmed', 'cancelled', 'expired'].map((outcome) => ev(0, { outcome })) }).map((r) => r.tone)
  assert.deepEqual(tones, ['done', 'skipped', 'failed', 'failed', 'done', 'skipped', 'skipped'])
  const answered = toolOutcomeRows({ tool_outcomes: [ev(0, { outcome: 'confirmed', code: 'chat_confirmed', senders: ['P2'], asker: 'U1' })] })[0]
  assert.deepEqual([answered.outcome, answered.senders, answered.asker], ['confirmed', ['P2'], 'U1'], 'who answered and who asked are both kept')
  const odd = toolOutcomeRows({ tool_outcomes: [ev(0, { outcome: 'surprise', addon: '' })] })[0]
  assert.deepEqual([odd.outcome, odd.tone, odd.tool], ['error', 'failed', 'create-issue'], 'an outcome the page does not know reads as a failure')
  assert.deepEqual(toolOutcomeRows({}), [])
  assert.deepEqual(toolOutcomeRows(null), [])
})

test('access: a tool call reads in the language of the page when the host translated it', () => {
  const v = localizeAccess({ subjects: [], tool_outcomes: [
    { tool: 'a', outcome: 'held', message: 'The change is waiting.', localized_message: 'La modifica attende.' },
    { tool: 'b', outcome: 'ok' },
  ] })
  assert.equal(v.tool_outcomes[0].message, 'La modifica attende.')
  assert.equal(v.tool_outcomes[1].message, undefined)
  assert.deepEqual(localizeAccess({ subjects: [] }).tool_outcomes, [])
})

test('access: a connection that asks first and a ticked confirm permission say a paired person confirms the writes in the chat', () => {
  const caps = [{ name: 'tools.write', title: 'Change things' }, { name: 'proposals.confirm', title: 'Confirm proposals' }]
  const hold = (mode) => [{ addon: 'tracker', capability: 'tools.write', tools: ['create-issue'], connections: [{ name: 'Work', write_mode: mode }], without_tick: ['paired'] }]
  const paired = [{ sender_id: 'p', at: new Date().toISOString() }]
  const rows = (mode, ticked, pairs = paired) => readinessRows(base({ id: 'b1', agent_id: 'a1', allow: ['42'], caps: ticked },
    { saved: connectorForm({ id: 'b1', agent_id: 'a1', allow: ['42'], caps: ticked }), capabilities: caps, paired: pairs,
      preview: { status: 'ready', view: { ...view({ listed: [], paired: [dec('tools.write'), dec('proposals.confirm')], anyone: [] }), addon_tools: hold(mode) } } }))
  const confirm = (list) => list.find((r) => r.id === 'addons:tracker:confirm')
  assert.equal(confirm(rows('propose', ['proposals.confirm']))?.key, 'ready.addons.confirmChat')
  assert.equal(confirm(rows('propose', ['proposals.confirm', 'tools.write']))?.state, 'ok')
  assert.equal(confirm(rows('auto', ['proposals.confirm'])), undefined, 'a connection that writes at once has nothing to confirm')
  assert.equal(confirm(rows('propose', [])), undefined, 'without the confirm permission the bot does not ask in the chat')
  assert.equal(confirm(rows('propose', ['proposals.confirm'], [])), undefined, 'nobody is paired')
})
