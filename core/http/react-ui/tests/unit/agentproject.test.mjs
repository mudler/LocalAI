import test from 'node:test'
import assert from 'node:assert/strict'
import {
  CREATE_PROJECT, projectOptions, moveBody, moveReady, effectsOf, hardBlockers, needsRevoke, needsDisable,
  suggestedFolder, projectNameOf, moveRefusal,
} from '../../src/utils/agentProject.js'

const LIST = [
  { id: 'b', name: 'beta', slug: 'beta', status: 'active' },
  { id: 'a', name: 'Alpha', slug: 'alpha', status: 'active' },
  { id: 'z', name: 'Zeta', slug: 'zeta', status: 'archived' },
  { id: 'x', name: 'Broken', slug: 'broken', status: 'broken' },
]

test('agent project: the field lists active projects by name, ignoring case', () => {
  assert.deepEqual(projectOptions(LIST).map((p) => p.id), ['a', 'b'])
  assert.deepEqual(projectOptions(null), [])
})

test('agent project: the project the agent is in stays an option when it is archived', () => {
  assert.deepEqual(projectOptions(LIST, 'z').map((p) => p.id), ['z', 'a', 'b'])
  // a project that is not in the list has no option to show
  assert.deepEqual(projectOptions(LIST, 'gone').map((p) => p.id), ['a', 'b'])
  assert.notEqual(CREATE_PROJECT, '')
})

test('agent project: a move into a project sends the files mode and nothing about folders', () => {
  assert.deepEqual(moveBody('a', { filesMode: 'read', folders: [{ path: '/x', mode: 'write' }] }, true), { project_id: 'a', dry_run: true, files_mode: 'read' })
  assert.deepEqual(moveBody('a', {}), { project_id: 'a', dry_run: false, files_mode: 'write' })
})

test('agent project: leaving a project sends the named folders or the whole home, never both', () => {
  assert.deepEqual(moveBody('', { folders: [{ path: '/Docs', mode: 'write' }, { path: '', mode: 'read' }] }), { project_id: '', dry_run: false, folders: [{ path: '/Docs', mode: 'write' }] })
  assert.deepEqual(moveBody('', { wholeHome: true, folders: [{ path: '/Docs', mode: 'read' }] }), { project_id: '', dry_run: false, whole_home: true })
  assert.equal(moveBody('', { folders: [{ path: '/Docs' }] }).folders[0].mode, 'read')
})

test('agent project: the switches of the dialog are sent only when on', () => {
  const b = moveBody('a', { revokeShares: true, disableTriggers: true })
  assert.equal(b.revoke_shares, true)
  assert.equal(b.disable_triggers, true)
  const none = moveBody('a', {})
  assert.ok(!('revoke_shares' in none) && !('disable_triggers' in none))
})

test('agent project: leaving needs a folder or the whole home before the server is asked', () => {
  assert.equal(moveReady('a', {}), true)
  assert.equal(moveReady('', {}), false)
  assert.equal(moveReady('', { folders: [{ path: '', mode: 'read' }] }), false)
  assert.equal(moveReady('', { folders: [{ path: '/Docs', mode: 'read' }] }), true)
  assert.equal(moveReady('', { wholeHome: true }), true)
})

test('agent project: effectsOf names only what the move touches', () => {
  assert.deepEqual(effectsOf(null), [])
  assert.deepEqual(effectsOf({ chats: 0, loops: 0, shares: 0, proposals: 0, triggers_outside: [], dropped_tools: { skills: [], addons: [], mcp_servers: [], collections: [] } }), [])
  const out = effectsOf({
    chats: 2, loops: 1, shares: 3, proposals: 1, memory_grant_dropped: true,
    triggers_outside: [{ id: 't1', name: 'Arrivals' }],
    dropped_tools: { skills: ['brand'], addons: [], mcp_servers: ['crm'], collections: ['docs'] },
  })
  assert.deepEqual(out.map((e) => e.key), ['chats', 'loops', 'triggers', 'shares', 'proposals', 'dropped', 'grant'])
  assert.deepEqual(out.find((e) => e.key === 'triggers').names, ['Arrivals'])
  assert.deepEqual(out.find((e) => e.key === 'dropped').names, ['brand', 'crm', 'docs'])
  assert.equal(out.find((e) => e.key === 'dropped').count, 3)
})

test('agent project: a running turn blocks for good, shares and triggers have a switch', () => {
  const r = { blockers: ['busy', 'shared', 'triggers_outside'] }
  assert.deepEqual(hardBlockers(r), ['busy'])
  assert.equal(needsRevoke(r), true)
  assert.equal(needsDisable(r), true)
  assert.equal(needsRevoke({ blockers: [] }), false)
  assert.deepEqual(hardBlockers(null), [])
})

test('agent project: a leaving agent suggests the files folder of the project it leaves', () => {
  assert.deepEqual(suggestedFolder(LIST[1]), { path: '/Projects/alpha/files', mode: 'read' })
  assert.equal(suggestedFolder(null), null)
  assert.equal(suggestedFolder(LIST[3]), null)
})

test('agent project: the name of a project, and the refusal of a move', () => {
  assert.equal(projectNameOf(LIST, 'a'), 'Alpha')
  assert.equal(projectNameOf(LIST, 'nope'), '')
  assert.equal(moveRefusal({ status: 409, body: { error: { type: 'agent_move_blocked' } } }), 'blocked')
  assert.equal(moveRefusal({ status: 409, body: { error: { type: 'other' } } }), '')
  assert.equal(moveRefusal({ status: 500 }), '')
})
