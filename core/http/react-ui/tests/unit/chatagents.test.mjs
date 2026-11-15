import test from 'node:test'
import assert from 'node:assert/strict'
import { addEntry, agentFolderMarks, agentPayload, emptyAgent, effectiveModes, folderName, folderPickable, folderRows, folderState, foldersPayload, foldersUnchosen, MAX_FOLDERS, grantSummary, isFolderMissingError, removeEntry, missingConnectorGrants, setGrant, CONNECTOR_GRANTS, filesChoiceOf, filesOff, withFilesChoice } from '../../src/utils/chatAgents.js'

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

test('chat agents: folders omitted when unchanged', () => {
  const form = { ...emptyAgent(), name: 'a', folders: [ACME] }
  assert.equal('folders' in agentPayload(form, { initialFolders: [ACME] }), false)
  assert.equal('folders' in agentPayload(form, { initialFolders: [{ ...ACME }] }), false)
  assert.equal('folders' in agentPayload({ ...emptyAgent(), name: 'a' }, { initialFolders: [] }), false)
  // Without the saved rows the body never names folders: the dirty check and
  // the preview baseline use it.
  assert.equal('folders' in agentPayload(form), false)
  // A missing or unset row the person did not touch is kept.
  const gone = [{ mode: 'read', state: 'missing' }]
  assert.equal('folders' in agentPayload({ ...form, folders: gone }, { initialFolders: gone }), false)
})

test('chat agents: removing every row clears, and a row without a path is not sent', () => {
  assert.deepEqual(agentPayload({ ...emptyAgent(), name: 'a', folders: [] }, { initialFolders: [ACME] }).folders, [])
  assert.deepEqual(foldersPayload([{ mode: 'read', state: 'missing' }], []), [])
  assert.deepEqual(foldersPayload([ACME], [ACME, { mode: 'read' }]), [{ path: '/Projects/Acme', mode: 'read' }])
  assert.equal(foldersUnchosen([ACME], [ACME, { mode: 'read' }]), true)
  assert.equal(foldersUnchosen([ACME], [ACME]), false)
  assert.equal(foldersUnchosen([{ mode: 'read', state: 'unset' }], [{ mode: 'read', state: 'unset' }]), false)
})

test('chat agents: each row keeps its own mode, and a nonsense mode is read', () => {
  assert.deepEqual(foldersPayload([], [{ path: '/A', mode: 'write' }, { path: '/B', mode: 'read' }]), [{ path: '/A', mode: 'write' }, { path: '/B', mode: 'read' }])
  assert.deepEqual(foldersPayload([ACME], [{ ...ACME, mode: 'write' }]), [{ path: '/Projects/Acme', mode: 'write' }])
  assert.deepEqual(foldersPayload(null, [{ path: '/', mode: 'nonsense' }]), [{ path: '/', mode: 'read' }])
  assert.equal(MAX_FOLDERS, 8)
})

test('chat agents: a folder inside another takes the outer mode', () => {
  assert.deepEqual(effectiveModes([{ path: '/P', mode: 'write' }, { path: '/P/A', mode: 'read' }]), ['write', 'write'])
  assert.deepEqual(effectiveModes([{ path: '/P', mode: 'read' }, { path: '/P/A', mode: 'write' }]), ['read', 'read'])
  assert.deepEqual(effectiveModes([{ path: '/P', mode: 'read' }, { path: '/Pq', mode: 'write' }]), ['read', 'write'])
  assert.deepEqual(effectiveModes([{ path: '/', mode: 'read' }, { path: '/A', mode: 'write' }]), ['read', 'read'])
})

test('chat agents: rows come from folders, or from the first folder', () => {
  assert.deepEqual(folderRows({ folder: ACME }), [ACME])
  assert.deepEqual(folderRows({ folder: ACME, folders: [ACME, { ...ACME, path: '/B' }] }).length, 2)
  assert.deepEqual(folderRows({}), [])
})

test('chat agents: the folder state, name and what the picker may choose', () => {
  assert.equal(folderState({}), 'none')
  assert.equal(folderState({ folder: ACME }), 'ok')
  assert.equal(folderState({ folder: { mode: 'read', state: 'missing' } }), 'missing')
  assert.equal(folderState({ folder: { mode: 'read', state: 'unset' } }), 'unset')
  assert.equal(folderState({ folders: [ACME, { mode: 'read', state: 'missing' }] }), 'missing')
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

test('chat agents: the first grant on an agent without a list names the connector grants that turn off', () => {
  assert.deepEqual(missingConnectorGrants([], ['memory.write']), CONNECTOR_GRANTS)
  assert.deepEqual(missingConnectorGrants(undefined, ['memory.write', 'triggers.run']), ['triggers.propose', 'talkback.channels', 'files.upload'])
  assert.deepEqual(missingConnectorGrants([], CONNECTOR_GRANTS), [])
})

test('chat agents: no notice when the agent had a list, or the form still has none', () => {
  assert.deepEqual(missingConnectorGrants(['memory.write'], ['memory.write', 'skills.write']), [])
  assert.deepEqual(missingConnectorGrants([], []), [])
  assert.deepEqual(missingConnectorGrants([], undefined), [])
})

test('chat agents: a grant is switched on once and off again', () => {
  assert.deepEqual(setGrant([], 'memory.write', true), ['memory.write'])
  assert.deepEqual(setGrant(['memory.write'], 'memory.write', true), ['memory.write'])
  assert.deepEqual(setGrant(['memory.write', 'files.upload'], 'memory.write', false), ['files.upload'])
  assert.deepEqual(agentPayload({ ...emptyAgent(), name: 'a', builtins: ['memory.write', ' memory.write'] }).builtins, ['memory.write'])
})

test('files choice: saved agents map as they always behaved', () => {
  assert.equal(filesChoiceOf({ builtins_off: [], folders: [] }), 'all')
  assert.equal(filesChoiceOf({}), 'all')
  assert.equal(filesChoiceOf({ builtins_off: ['web'] }), 'all')
  assert.equal(filesChoiceOf({ builtins_off: ['files'] }), 'none')
  assert.equal(filesChoiceOf({ folders: [{ path: '/Work', mode: 'read' }] }), 'folders')
  assert.equal(filesChoiceOf({ folder: { path: '/Work', mode: 'read' } }), 'folders')
  assert.equal(filesChoiceOf({ folders: [], folder: { path: '/Work' } }), 'all', 'a form whose rows were cleared')
  // the switch wins: the server ignores folders of an agent with Files off
  assert.equal(filesChoiceOf({ builtins_off: ['files'], folders: [{ path: '/Work' }] }), 'none')
  assert.equal(filesOff({ builtins_off: ['files'] }), true)
})

test('files choice: each choice sets the switch and the rows together', () => {
  const rows = [{ path: '/Work', mode: 'write' }]
  const form = { builtins_off: ['web'], folders: rows }
  const none = withFilesChoice(form, 'none', [])
  assert.deepEqual(none.builtins_off, ['web', 'files'])
  assert.deepEqual(none.folders, [])
  assert.deepEqual(withFilesChoice(none, 'none', []).builtins_off, ['web', 'files'], 'idempotent')
  const all = withFilesChoice(none, 'all', rows)
  assert.deepEqual(all.builtins_off, ['web'])
  assert.deepEqual(all.folders, [])
  assert.deepEqual(withFilesChoice(all, 'folders', rows).folders, rows, 'brings the saved rows back')
  assert.deepEqual(withFilesChoice(all, 'folders', []).folders, [{ mode: 'read' }], 'a blank row to fill in')
  assert.deepEqual(withFilesChoice({ ...form }, 'folders', []).folders, rows, 'keeps the rows it has')
  assert.equal(filesChoiceOf(withFilesChoice(none, 'folders', rows)), 'folders')
})

test('files choice: No files on a saved agent sends the switch and clears the folders', () => {
  const saved = [{ path: '/Work', mode: 'read' }]
  const form = withFilesChoice({ ...emptyAgent(), name: 'a', folders: saved }, 'none', saved)
  const body = agentPayload(form, { initialFolders: saved })
  assert.deepEqual(body.builtins_off, ['files'])
  assert.deepEqual(body.folders, [])
  // an untouched agent sends no folders
  assert.equal(agentPayload({ ...emptyAgent(), name: 'a', folders: saved }, { initialFolders: saved }).folders, undefined)
})
