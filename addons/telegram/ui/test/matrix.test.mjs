import test from 'node:test'
import assert from 'node:assert/strict'
import { cellOf, matrixColumns, matrixRows } from '../../../internal/connector-ui/src/matrix.js'
import { previewBody } from '../../../internal/connector-ui/src/access.js'

const CAPS = [
  { name: 'memory.write', title: 'Save to memory' },
  { name: 'triggers.propose', title: 'Propose triggers' },
  { name: 'files.upload', title: 'Receive files' },
  { name: 'tools.write', title: 'Change things' },
]
const ok = (capability) => ({ capability, allowed: true })
const off = (capability) => ({ capability, allowed: false, blocked_by: 'connection', code: 'connection_permission_off', message: 'This connection does not allow it. Tick the permission.' })
const no = (capability) => ({ capability, allowed: false, blocked_by: 'sender', code: 'sender_needs_pairing', message: 'This person needs to be verified first.' })
const VIEW = {
  proposal_mode: 'ask',
  subjects: [
    { id: 'paired', decisions: [ok('memory.write'), ok('triggers.propose'), off('files.upload'), ok('tools.write')], note_save: 'now' },
    { id: 'listed', decisions: [ok('memory.write'), ok('triggers.propose'), off('files.upload'), no('tools.write')], note_save: 'ask' },
    { id: 'channel_acts', decisions: [ok('memory.write'), ok('triggers.propose'), off('files.upload'), no('tools.write')] },
    { id: 'anyone', decisions: [no('memory.write'), no('triggers.propose'), off('files.upload'), no('tools.write')] },
  ],
  addon_tools: [{ addon: 'tracker', capability: 'tools.write', tools: ['create-issue', 'comment-issue'], connections: [{ name: 'Work', write_mode: 'propose' }] }],
  approvals: [{ tool: 'comment-issue', skip: true, runs_without_asking: ['owner', 'paired'] }],
}

test('matrix: the Anyone column shows only when Anyone is chosen, and a subject the host did not send is left out', () => {
  assert.deepEqual(matrixColumns({ accept_any: false }, VIEW).map((c) => c.id), ['on', 'paired', 'listed', 'channel_acts', 'channel_member'])
  assert.deepEqual(matrixColumns({ accept_any: true }, VIEW).map((c) => c.id), ['on', 'paired', 'listed', 'channel_acts', 'channel_member', 'anyone'])
  assert.deepEqual(matrixColumns({}, { subjects: [{ id: 'listed' }] }).map((c) => c.id), ['on', 'listed'])
  assert.deepEqual(matrixColumns({}, null).map((c) => c.id), ['on'])
})

test('matrix: a refusal before the sender reads Off, a refusal for the sender reads No, and an allowed cell reads Yes', () => {
  assert.equal(cellOf(ok('x'), null, VIEW, 'x').state, 'yes')
  assert.equal(cellOf(off('x'), null, VIEW, 'x').state, 'off')
  for (const link of ['server', 'tools', 'agent', 'connection']) assert.equal(cellOf({ allowed: false, blocked_by: link }, null, VIEW, 'x').state, 'off', link)
  assert.equal(cellOf(no('x'), null, VIEW, 'x').state, 'no')
  assert.equal(cellOf({ allowed: false, blocked_by: 'role' }, null, VIEW, 'x').state, 'no')
  assert.equal(cellOf(undefined, null, VIEW, 'x').state, 'unknown')
})

test('matrix: asks first comes only from what the host said: a note that waits, and triggers that are not set to run at once', () => {
  assert.equal(cellOf(ok('memory.write'), { note_save: 'ask' }, VIEW, 'memory.write').state, 'asks')
  assert.equal(cellOf(ok('memory.write'), { note_save: 'now' }, VIEW, 'memory.write').state, 'yes')
  assert.equal(cellOf(ok('memory.write'), {}, VIEW, 'memory.write').state, 'yes', 'no rule of the agent: no claim')
  assert.equal(cellOf(ok('triggers.propose'), {}, { proposal_mode: 'ask' }, 'triggers.propose').state, 'asks')
  assert.equal(cellOf(ok('triggers.propose'), {}, { proposal_mode: 'auto_readonly' }, 'triggers.propose').state, 'asks')
  assert.equal(cellOf(ok('triggers.propose'), {}, { proposal_mode: 'auto' }, 'triggers.propose').state, 'yes')
  assert.equal(cellOf(ok('triggers.propose'), {}, {}, 'triggers.propose').state, 'yes', 'an older host sends no mode: no claim')
})

test('matrix: the rows follow the host list, the switch column follows the form, and tools are sub-rows', () => {
  const form = { accept_any: false, caps: ['memory.write', 'triggers.propose', 'tools.write'] }
  const columns = matrixColumns(form, VIEW)
  const rows = matrixRows(VIEW, CAPS, form, columns)
  assert.deepEqual(rows.map((r) => r.key), ['memory.write', 'triggers.propose', 'files.upload', 'tools.write', 'tools.write:tracker:create-issue', 'tools.write:tracker:comment-issue'])
  const by = Object.fromEntries(rows.map((r) => [r.key, r]))
  assert.equal(by['memory.write'].cells.on.state, 'yes')
  assert.equal(by['files.upload'].cells.on.state, 'off')
  assert.equal(by['memory.write'].cells.paired.state, 'yes')
  assert.equal(by['memory.write'].cells.listed.state, 'asks')
  assert.equal(by['triggers.propose'].cells.paired.state, 'asks')
  assert.equal(by['files.upload'].cells.paired.state, 'off')
  assert.equal(by['tools.write'].cells.listed.state, 'no')
  // The tool that waits for a person, and the tool the owner relaxed (runs at once for a verified person only).
  assert.equal(by['tools.write:tracker:create-issue'].cells.paired.state, 'waiting')
  assert.equal(by['tools.write:tracker:comment-issue'].cells.paired.state, 'yes')
  assert.equal(by['tools.write:tracker:create-issue'].cells.listed.state, 'no', 'a refused cell stays refused in a sub-row')
  assert.equal(by['tools.write:tracker:create-issue'].parent, 'tools.write')
})

test('matrix: the preview request carries the places the bot may post and the approval mode of the unsaved form', () => {
  const form = { id: '', agent_id: 'a1', allow: ['C1'], allow_trust: { C1: 'act' }, caps: [], grants: [], channels: [{ id: ' C9 ', label: ' ops ', approved: true }], approval: { mode: 'auto' } }
  const b = previewBody(form, 'slack')
  assert.deepEqual(b.channels, [{ id: 'C9', label: 'ops', approved: true }])
  assert.equal(b.approval_mode, 'auto')
  assert.deepEqual(b.allow_trust, { C1: 'act' })
  assert.deepEqual(previewBody({ ...form, channels: [] }, 'slack').channels, [], 'an empty list clears')
  assert.equal('approval_mode' in previewBody({ ...form, approval: {} }, 'slack'), false)
})

test('matrix: a kind with roles gets one column for each role the host sent, after the kinds of sender', () => {
  const roles = [{ id: 'admin', title: 'Admin' }, { id: 'member', title: 'Member' }, { id: 'gone', title: 'Gone' }]
  const view = { subjects: [{ id: 'listed' }, { id: 'role:admin' }, { id: 'role:member' }] }
  const cols = matrixColumns({}, view, roles)
  assert.deepEqual(cols.map((c) => c.id), ['on', 'listed', 'role:admin', 'role:member'], 'a role the host did not send is left out')
  assert.equal(cols[2].role, 'admin')
  assert.equal(cols[2].title, 'Admin')
  assert.deepEqual(matrixColumns({}, view, []).map((c) => c.id), ['on', 'listed'], 'a kind without roles has no role column')
})
