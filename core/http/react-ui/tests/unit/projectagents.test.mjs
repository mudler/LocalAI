import test from 'node:test'
import assert from 'node:assert/strict'
import {
  ceilingOf, withinCeiling, limitedTools, unavailableReason, agentIsUnavailable, importParams, projectRequiredOf,
  startRefusal, projectFolderRows, filesModeOf, projectAgentFolders, isProjectAgent,
} from '../../src/utils/projectAgents.js'

const P = { id: 'p1', name: 'Atlas', slug: 'atlas', status: 'active', defaults: { skills: ['brand'], addons: [], mcp_servers: ['docs'], connectors: [], collections: ['papers'] } }

test('project agents: a non-empty project list is a ceiling, an empty one is none', () => {
  assert.deepEqual(ceilingOf(P, 'skills'), ['brand'])
  assert.equal(ceilingOf(P, 'addons'), null)
  assert.equal(ceilingOf(P, 'connectors'), null)
  assert.equal(ceilingOf(null, 'skills'), null)
  assert.equal(ceilingOf({}, 'skills'), null)
})

test('project agents: withinCeiling keeps only listed items and every item without a ceiling', () => {
  const items = [{ name: 'brand' }, { name: 'other' }]
  assert.deepEqual(withinCeiling(items, ['brand'], (i) => i.name), [{ name: 'brand' }])
  assert.deepEqual(withinCeiling(items, null, (i) => i.name), items)
  assert.deepEqual(withinCeiling(items, [], (i) => i.name), items)
  // an item the agent holds but nothing defines stays, so it can be removed
  assert.deepEqual(withinCeiling([{ name: 'x', gone: true }], ['brand'], (i) => i.name), [{ name: 'x', gone: true }])
})

test('project agents: limitedTools filters add-ons, connectors, skills and model servers by their own list', () => {
  const out = limitedTools(P, {
    addons: [{ id: 'web' }, { id: 'mail' }],
    connectors: [{ id: 'c1', name: 'Team chat' }],
    skills: [{ name: 'brand' }, { name: 'seo' }],
    servers: ['docs', 'git'],
    collections: ['papers', 'faq'],
  })
  assert.deepEqual(out.addons.map(a => a.id), ['web', 'mail'])
  assert.deepEqual(out.connectors.map(a => a.id), ['c1'])
  assert.deepEqual(out.skills.map(s => s.name), ['brand'])
  assert.deepEqual(out.servers, ['docs'])
  assert.deepEqual(out.collections, ['papers'])
  assert.deepEqual(out.limited, { skills: true, addons: false, mcp_servers: true, connectors: false, collections: true })
})

test('project agents: limitedTools without a project changes nothing', () => {
  const rows = { addons: [{ id: 'a' }], skills: [{ name: 's' }], connectors: [], servers: ['x'], collections: ['c'] }
  const out = limitedTools(null, rows)
  assert.deepEqual(out.skills, rows.skills)
  assert.deepEqual(out.servers, rows.servers)
  assert.equal(Object.values(out.limited).some(Boolean), false)
})

test('project agents: the reason an agent is paused', () => {
  assert.equal(unavailableReason(P, false), '')
  assert.equal(unavailableReason({ ...P, status: 'archived' }, true), 'archived')
  assert.equal(unavailableReason(null, true), 'deleted')
  assert.equal(unavailableReason({ ...P, status: 'broken' }, true), 'deleted')
  assert.equal(unavailableReason(P, true), 'changed')
  assert.equal(unavailableReason(undefined, false, true), 'deleted')
})

test('project agents: agentIsUnavailable follows the project list, then the flag', () => {
  const byId = { p1: P, p3: { ...P, id: 'p3', status: 'archived' } }
  assert.equal(agentIsUnavailable({ project_id: 'p1' }, byId, {}), '')
  assert.equal(agentIsUnavailable({ project_id: 'p3' }, byId, {}), 'archived')
  assert.equal(agentIsUnavailable({ project_id: 'gone' }, byId, {}), 'deleted')
  assert.equal(agentIsUnavailable({ id: 'a', project_id: 'p1' }, byId, { a: true }), 'changed')
  assert.equal(agentIsUnavailable({ id: 'b' }, byId, {}), '')
  // projects not loaded (feature off or read failed): no claim
  assert.equal(agentIsUnavailable({ project_id: 'p1' }, null, {}), '')
})

test('project agents: import mapping goes in query parameters, never in the file', () => {
  assert.deepEqual(importParams({ projectId: 'p1' }), { project_id: 'p1' })
  assert.deepEqual(importParams({ create: true }), { create_project: true })
  assert.equal(importParams({}), null)
  assert.equal(importParams(null), null)
  assert.equal(importParams({ projectId: '' }), null)
})

test('project agents: a project_required answer is read from the 409 body', () => {
  const err = { status: 409, body: { error: { type: 'project_required', message: 'm', project_name: 'Triage' } } }
  assert.deepEqual(projectRequiredOf(err), { name: 'Triage', message: 'm' })
  assert.equal(projectRequiredOf({ status: 409, body: { error: { type: 'other' } } }), null)
  assert.equal(projectRequiredOf({ status: 400, body: { error: { type: 'project_required' } } }), null)
  assert.equal(projectRequiredOf(null), null)
})

test('project agents: a refused start names the paused reason', () => {
  assert.equal(startRefusal({ status: 409, body: { error: { type: 'project_unavailable' } } }), 'unavailable')
  assert.equal(startRefusal({ status: 409, body: { error: { type: 'project_folders_changed' } } }), 'changed')
  assert.equal(startRefusal({ status: 409, body: { error: { type: 'agent_folder_missing' } } }), '')
  assert.equal(startRefusal(new Error('x')), '')
})

test('project agents: the two fixed folders of a project', () => {
  assert.deepEqual(projectFolderRows(P, 'read'), [{ path: '/Projects/atlas/files', mode: 'read' }, { path: '/Projects/atlas/memory', mode: 'read' }])
  assert.deepEqual(projectFolderRows(P, 'bogus')[0].mode, 'write')
})

test('project agents: the files mode is read from the stored rows, write when unknown', () => {
  assert.equal(filesModeOf({ folders: [{ path: '/Projects/atlas/files', mode: 'read' }, { path: '/Projects/atlas/memory', mode: 'read' }] }), 'read')
  assert.equal(filesModeOf({ folders: [{ path: '/Projects/atlas/files', mode: 'write' }, { path: '/Projects/atlas/memory', mode: 'read' }] }), 'write')
  assert.equal(filesModeOf({}), 'write')
})

test('project agents: folders are sent only when the mode differs from the stored one', () => {
  // create: write is the server default, so nothing is sent
  assert.equal(projectAgentFolders(P, 'write', 'write'), undefined)
  assert.deepEqual(projectAgentFolders(P, 'read', 'write'), projectFolderRows(P, 'read'))
  // edit: unchanged mode sends nothing
  assert.equal(projectAgentFolders(P, 'read', 'read'), undefined)
  assert.deepEqual(projectAgentFolders(P, 'write', 'read'), projectFolderRows(P, 'write'))
})

test('project agents: isProjectAgent reads project_id', () => {
  assert.equal(isProjectAgent({ project_id: 'p1' }), true)
  assert.equal(isProjectAgent({}), false)
  assert.equal(isProjectAgent(null), false)
})
