import test from 'node:test'
import assert from 'node:assert/strict'
import { addEntry, agentFolderMarks, agentPayload, emptyAgent, folderName, folderPayload, folderPickable, folderState, grantSummary, isFolderMissingError, removeEntry } from '../../src/utils/chatAgents.js'

test('chat agents: an entry is trimmed and added once', () => {
  assert.deepEqual(addEntry([], ' office '), ['office'])
  assert.deepEqual(addEntry(['office'], 'office'), ['office'])
  assert.deepEqual(addEntry(['office'], '   '), ['office'])
  assert.deepEqual(removeEntry(['a', 'b'], 'a'), ['b'])
})

test('chat agents: the request body is trimmed, and its lists have no blanks or repeats', () => {
  const body = agentPayload({
    ...emptyAgent(), name: '  Reviewer ', description: ' d ', instructions: 'Be brief.\n', model: ' m ',
    addons: ['office', ' office', ''], skills: ['review'], mcp_servers: undefined,
  })
  assert.equal(body.name, 'Reviewer')
  assert.equal(body.description, 'd')
  assert.equal(body.instructions, 'Be brief.\n')
  assert.equal(body.model, 'm')
  assert.deepEqual(body.addons, ['office'])
  assert.deepEqual(body.mcp_servers, [])
  assert.deepEqual(body.collections, [])
})

test('chat agents: the list line says what an agent grants, and nothing for a bare agent', () => {
  assert.equal(grantSummary({ addons: ['a'], skills: ['x', 'y'] }), '1 add-on, 2 skills')
  assert.equal(grantSummary({ mcp_servers: ['crm'], collections: ['docs'] }), '1 MCP server, 1 collection')
  assert.equal(grantSummary(emptyAgent()), '')
})

test('chat agents: the network of unattended runs is off unless chosen, and only a known preset is sent', () => {
  assert.equal(emptyAgent().network, 'off')
  assert.equal(agentPayload({ ...emptyAgent(), name: 'a', network: 'registries' }).network, 'registries')
  assert.equal(agentPayload({ ...emptyAgent(), name: 'a', network: 'everything' }).network, 'off')
  assert.equal(agentPayload({ name: 'a' }).network, 'off')
})

const ACME = { path: '/Projects/Acme', name: 'Acme', mode: 'read', state: 'ok' }

test('chat agents: folder omitted when unchanged', () => {
  const form = { ...emptyAgent(), name: 'a', folder: ACME }
  assert.equal('folder' in agentPayload(form, { initialFolder: ACME }), false)
  assert.equal('folder' in agentPayload(form, { initialFolder: { ...ACME } }), false)
  assert.equal('folder' in agentPayload({ ...emptyAgent(), name: 'a' }, { initialFolder: null }), false)
  // Without the saved folder the body never names one: the dirty check and
  // the preview baseline use it.
  assert.equal('folder' in agentPayload(form), false)
  // A missing or unset folder the person did not replace is kept.
  const gone = { mode: 'read', state: 'missing' }
  assert.equal('folder' in agentPayload({ ...form, folder: gone }, { initialFolder: gone }), false)
  assert.equal(folderPayload({ mode: 'write', state: 'unset' }, { mode: 'read', state: 'unset' }), undefined)
})

test('chat agents: empty path clears', () => {
  assert.deepEqual(agentPayload({ ...emptyAgent(), name: 'a', folder: null }, { initialFolder: ACME }).folder, { path: '' })
  assert.deepEqual(folderPayload({ mode: 'read', state: 'missing' }, null), { path: '' })
})

test('chat agents: mode read maps to "read"', () => {
  assert.deepEqual(folderPayload(null, { path: '/Projects/Acme', mode: 'read' }), { path: '/Projects/Acme', mode: 'read' })
  assert.deepEqual(folderPayload(ACME, { ...ACME, mode: 'write' }), { path: '/Projects/Acme', mode: 'write' })
  assert.deepEqual(folderPayload(null, { path: '/', mode: 'nonsense' }), { path: '/', mode: 'read' })
  assert.deepEqual(folderPayload({ mode: 'read', state: 'missing' }, { path: '/Other', mode: 'read' }), { path: '/Other', mode: 'read' })
  assert.deepEqual(folderPayload(ACME, { path: '/Other', mode: 'read' }), { path: '/Other', mode: 'read' })
})

test('chat agents: the folder state, name and what the picker may choose', () => {
  assert.equal(folderState({}), 'none')
  assert.equal(folderState({ folder: ACME }), 'ok')
  assert.equal(folderState({ folder: { mode: 'read', state: 'missing' } }), 'missing')
  assert.equal(folderState({ folder: { mode: 'read', state: 'unset' } }), 'unset')
  assert.equal(folderName('/Projects/Acme'), 'Acme')
  assert.equal(folderName('/'), '')
  assert.equal(folderPickable({ kind: 'dir', path: '/Projects' }), true)
  assert.equal(folderPickable({ kind: 'file', path: '/a.txt' }), false)
  assert.equal(folderPickable({ kind: 'dir', path: '/Chats', managed: 'chats' }), false)
  assert.equal(folderPickable({ kind: 'dir', path: '/LocalAI', managed: 'localai' }), false)
  assert.equal(folderPickable({ kind: 'dir', path: '/shared/team' }), false)
  assert.equal(folderPickable({ kind: 'dir', path: '/x', shared: true }), false)
})

test('chat agents: the Files marks list the agents of each live folder', () => {
  const marks = agentFolderMarks([
    { id: '1', name: 'zeta', folder: { ...ACME, mode: 'write' } },
    { id: '2', name: 'alpha', folder: ACME },
    { id: '3', name: 'gone', folder: { mode: 'read', state: 'missing' } },
    { id: '4', name: 'plain' },
  ])
  assert.deepEqual([...marks.keys()], ['/Projects/Acme'])
  assert.deepEqual(marks.get('/Projects/Acme').map(m => `${m.name}:${m.mode}`), ['alpha:read', 'zeta:write'])
})

test('chat agents: a 409 agent_folder_missing is told apart from other errors', () => {
  assert.equal(isFolderMissingError({ status: 409, body: { error: { type: 'agent_folder_missing' } } }), true)
  assert.equal(isFolderMissingError({ status: 409, body: { error: { type: 'conflict' } } }), false)
  assert.equal(isFolderMissingError(new Error('x')), false)
  assert.equal(isFolderMissingError(null), false)
})
