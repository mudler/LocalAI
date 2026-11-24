import test from 'node:test'
import assert from 'node:assert/strict'
import {
  projectOfAgent, projectOfTrigger, agentLabel, allowedFolders, defaultFolder, baseOf, subOf, joinFolder,
  folderProblem, filterByProject, projectsWithTriggers, firstProjectAgent,
} from '../../src/utils/triggerProjects.js'

const projects = [{ id: 'p1', name: 'Kitchen', status: 'active' }, { id: 'p2', name: 'Atrium', status: 'archived' }]
const files = { path: '/Projects/kitchen/files', mode: 'write', state: 'ok' }
const memory = { path: '/Projects/kitchen/memory', mode: 'read', state: 'ok' }
const agents = [
  { id: 'a1', name: 'Planner', project_id: 'p1', folders: [memory, files] },
  { id: 'a2', name: 'Loner' },
  { id: 'a3', name: 'Archivist', project_id: 'p2', folders: [{ path: '/Projects/atrium/files', mode: 'read', state: 'ok' }] },
  { id: 'a4', name: 'Scoped', folders: [{ path: '/Docs', mode: 'read', state: 'ok' }, { path: '/Reports', mode: 'write', state: 'missing' }] },
  { id: 'a5', name: 'Orphan', project_id: 'gone', folders: [] },
]

test('trigger projects: an agent of a project says which, an agent of none or of a deleted project says nothing', () => {
  assert.deepEqual(projectOfAgent(agents[0], projects), { id: 'p1', name: 'Kitchen', status: 'active' })
  assert.equal(projectOfAgent(agents[1], projects), null)
  assert.equal(projectOfAgent(agents[4], projects), null)
  assert.equal(projectOfAgent(null, projects), null)
  assert.equal(projectOfTrigger({ agent_id: 'a1' }, agents, projects).name, 'Kitchen')
  assert.equal(projectOfTrigger({ agent_id: 'none' }, agents, projects), null)
  assert.equal(projectOfTrigger({}, agents, projects), null)
})

test('trigger projects: the agent label carries the project name of a project agent only', () => {
  const w = (a, p) => `${a} (${p})`
  assert.equal(agentLabel(agents[0], projects, w), 'Planner (Kitchen)')
  assert.equal(agentLabel(agents[1], projects, w), 'Loner')
})

test('trigger projects: the folders an agent allows are its own rows that are ok, null for an agent with no fence', () => {
  assert.deepEqual(allowedFolders(agents[0]), ['/Projects/kitchen/memory', '/Projects/kitchen/files'])
  assert.equal(allowedFolders(agents[1]), null)
  assert.deepEqual(allowedFolders(agents[3]), ['/Docs'], 'a missing folder is not offered')
  assert.deepEqual(allowedFolders(agents[4]), [], 'a project agent with no usable folder allows none')
  assert.equal(allowedFolders(undefined), null)
})

test('trigger projects: the default folder is the project files, not its memory', () => {
  assert.equal(defaultFolder(agents[0]), '/Projects/kitchen/files')
  assert.equal(defaultFolder(agents[3]), '/Docs')
  assert.equal(defaultFolder(agents[1]), '')
  assert.equal(defaultFolder(agents[4]), '')
})

test('trigger projects: a path splits into the folder that holds it and the rest, and joins back', () => {
  const allowed = ['/Docs', '/Docs/Reports', '/']
  assert.equal(baseOf('/Docs/Reports/2026', allowed), '/Docs/Reports', 'the innermost folder holds it')
  assert.equal(baseOf('/Other', ['/Docs']), '')
  assert.equal(baseOf('/Docsy', ['/Docs']), '', 'a name that only starts the same is not inside')
  assert.equal(subOf('/Docs/Reports/2026', '/Docs/Reports'), '2026')
  assert.equal(subOf('/Docs', '/Docs'), '')
  assert.equal(subOf('/a/b', '/'), 'a/b')
  assert.equal(joinFolder('/Docs', 'inbox/'), '/Docs/inbox')
  assert.equal(joinFolder('/Docs', ' '), '/Docs')
  assert.equal(joinFolder('/', 'a'), '/a')
})

test('trigger projects: a folder outside the fence or with ".." is refused before Save, an empty one too', () => {
  const allowed = ['/Projects/kitchen/files']
  assert.equal(folderProblem('/Projects/kitchen/files/incoming', allowed), '')
  assert.equal(folderProblem('/Contracts', allowed), 'outside')
  assert.equal(folderProblem('/Projects/kitchen/files/../../other', allowed), 'dots')
  assert.equal(folderProblem('', allowed), 'empty')
  assert.equal(folderProblem('/Contracts', null), '', 'an agent with no fence may watch any folder')
  assert.equal(folderProblem('/Contracts', []), 'outside', 'an agent that allows none refuses all')
})

test('trigger projects: the filter keeps the triggers of one project, of none, or all', () => {
  const triggers = [{ id: 't1', agent_id: 'a1' }, { id: 't2', agent_id: 'a2' }, { id: 't3', agent_id: 'a3' }, { id: 't4' }]
  assert.deepEqual(filterByProject(triggers, agents, 'all').map((t) => t.id), ['t1', 't2', 't3', 't4'])
  assert.deepEqual(filterByProject(triggers, agents, 'p1').map((t) => t.id), ['t1'])
  assert.deepEqual(filterByProject(triggers, agents, 'none').map((t) => t.id), ['t2', 't4'])
  assert.deepEqual(filterByProject(triggers, agents).map((t) => t.id), ['t1', 't2', 't3', 't4'])
  assert.deepEqual(projectsWithTriggers(triggers, agents, projects).map((p) => p.name), ['Atrium', 'Kitchen'])
})

test('trigger projects: a new trigger of a project starts with its first agent that can work, by name', () => {
  const rows = [{ id: 'x', name: 'Zed' }, { id: 'y', name: 'alpha', unavailable: true }, { id: 'z', name: 'Beta' }]
  assert.equal(firstProjectAgent(rows).id, 'z')
  assert.equal(firstProjectAgent([{ id: 'y', name: 'a', unavailable: true }]), null)
  assert.equal(firstProjectAgent([]), null)
  assert.equal(firstProjectAgent(undefined), null)
})
