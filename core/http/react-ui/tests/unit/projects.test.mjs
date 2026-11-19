import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  MEMORY_INDEX, filterProjects, isProjectsOff, memoryFileError, projectCounts, projectFilesPath,
  defaultsCount, cleanDefaults, APPLIED_LISTS, DEFAULT_LISTS, addChip, startChatBody, whenAgo,
  PULLED_MAX, NO_PROJECT, composerProjects, pulledChoices, withPulled, projectNames, chatsInProject, homeProject, pulledProjects, pulledState, projectFolderLink, pickStartModel, isBusy, wrapKey,
} from '../../src/utils/projects.js'
import { navBuiltins, primaryDestinations } from '../../src/utils/navigationModel.js'
import { NAV_BUILTIN_IDS, readItems, sections } from '../../src/utils/nav.js'

const here = dirname(fileURLToPath(import.meta.url))
const goSchema = readFileSync(join(here, '../../../../services/studio/wellknown/schema.go'), 'utf8')

const P = [
  { id: 'a', name: 'Client site', description: 'Copy and assets', slug: 'client-site', status: 'active', chat_count: 3 },
  { id: 'b', name: 'Podcast', description: 'Show notes', slug: 'podcast', status: 'archived', chat_count: 1 },
  { id: 'c', name: 'Kitchen', description: '', slug: 'kitchen', status: 'broken', error: 'missing manifest', chat_count: 0 },
]

test('projects: memory file names follow the server rule', () => {
  assert.equal(MEMORY_INDEX, 'index.md')
  for (const ok of ['index.md', 'brand-voice.md', 'a.md', '0notes_1.v2.md']) assert.equal(memoryFileError(ok), '', ok)
  assert.equal(memoryFileError(''), 'empty')
  for (const bad of ['Index.md', '.hidden.md', 'a/b.md', '../x.md', 'notes.txt', 'notes', '-a.md', 'a b.md', `${'a'.repeat(65)}.md`]) {
    assert.equal(memoryFileError(bad), 'invalid', bad)
  }
  assert.equal(memoryFileError(`${'a'.repeat(64)}.md`), '')
})

test('projects: counts and the status filter', () => {
  assert.deepEqual(projectCounts(P), { all: 3, active: 1, archived: 1, broken: 1 })
  assert.deepEqual(filterProjects(P, { status: 'all', query: '' }).map(p => p.id), ['a', 'b', 'c'])
  assert.deepEqual(filterProjects(P, { status: 'archived', query: '' }).map(p => p.id), ['b'])
  assert.deepEqual(filterProjects(P, { status: 'broken', query: '' }).map(p => p.id), ['c'])
  assert.deepEqual(filterProjects(P, { status: 'all', query: ' SHOW ' }).map(p => p.id), ['b'])
  assert.deepEqual(filterProjects(P, { status: 'active', query: 'podcast' }), [])
  assert.deepEqual(filterProjects(undefined, { status: 'all', query: '' }), [])
})

test('projects: the off answer is a 409 projects_off', () => {
  assert.equal(isProjectsOff({ status: 409, body: { error: { type: 'projects_off', message: 'x' } } }), true)
  assert.equal(isProjectsOff({ status: 409, body: { error: { type: 'other' } } }), false)
  assert.equal(isProjectsOff({ status: 500 }), false)
  assert.equal(isProjectsOff(null), false)
})

test('projects: the files folder of a project and the chat start body', () => {
  assert.equal(projectFilesPath({ slug: 'client-site' }), '/Projects/client-site/files')
  assert.deepEqual(startChatBody({ id: 'p1' }), { project_id: 'p1' })
})

test('projects: tool defaults are cleaned, counted and chips are added once', () => {
  assert.deepEqual(cleanDefaults({ skills: [' a ', 'a', ''], addons: [], mcp_servers: ['m'], connectors: undefined }), { skills: ['a'], mcp_servers: ['m'] })
  assert.equal(defaultsCount({ skills: ['a', 'b'], mcp_servers: ['m'] }), 3)
  assert.equal(defaultsCount(undefined), 0)
  assert.deepEqual(addChip(['a'], ' b '), ['a', 'b'])
  assert.deepEqual(addChip(['a'], 'a'), ['a'])
  assert.deepEqual(addChip(['a'], '  '), ['a'])
})

test('projects: the sidebar lists Projects after Files and the ids match the server', () => {
  const ids = primaryDestinations.map(d => d.id)
  assert.equal(ids[ids.indexOf('files') + 1], 'projects')
  const dest = primaryDestinations.find(d => d.id === 'projects')
  assert.equal(dest.path, '/app/projects')
  assert.equal(dest.feature, 'chats')
  assert.equal(dest.labelKey, 'items.projects')
  const m = goSchema.match(/var NavBuiltinIDs = \[\]string\{([^}]*)\}/)
  const goIds = [...m[1].matchAll(/"([^"]+)"/g)].map(x => x[1])
  assert.deepEqual(goIds, navBuiltins.map(b => b.id))
})

test('projects: a stored nav layout without projects still shows it, after the last item', () => {
  const items = readItems({ items: [{ id: 'home' }, { id: 'files' }, { id: 'agents' }] })
  const got = sections(items, { available: new Set(NAV_BUILTIN_IDS) }).flatMap(s => s.entries.map(e => e.id))
  assert.deepEqual(got.slice(0, 3), ['home', 'files', 'agents'])
  assert.ok(got.includes('projects'))
  // And a layout that hides it keeps it hidden.
  const hidden = readItems({ items: [{ id: 'projects', hidden: true }] })
  assert.ok(!sections(hidden, { available: new Set(NAV_BUILTIN_IDS) }).flatMap(s => s.entries.map(e => e.id)).includes('projects'))
})

test('projects: whenAgo speaks relative time and falls back to a date', () => {
  const now = Date.parse('2026-11-14T12:00:00Z')
  assert.equal(whenAgo('2026-11-14T10:00:00Z', 'en', now), '2 hours ago')
  assert.equal(whenAgo('2026-11-13T10:00:00Z', 'en', now), 'yesterday')
  assert.equal(whenAgo('', 'en', now), '')
  assert.equal(whenAgo('nope', 'en', now), '')
  assert.match(whenAgo('2026-08-01T10:00:00Z', 'en', now), /Aug/)
})

const CP = [
  { id: 'a', name: 'Client site', status: 'active', chat_count: 12 },
  { id: 'b', name: 'Podcast', status: 'archived', chat_count: 1 },
  { id: 'c', name: 'Kitchen', status: 'broken', chat_count: 0 },
  { id: 'd', name: 'Home lab notes', status: 'active', chat_count: 5 },
]

test('composerProjects lists only active projects and searches by name', () => {
  assert.deepEqual(composerProjects(CP).map(p => p.id), ['a', 'd'])
  assert.deepEqual(composerProjects(CP, 'LAB').map(p => p.id), ['d'])
  assert.deepEqual(composerProjects(CP, '  ').map(p => p.id), ['a', 'd'])
  assert.deepEqual(composerProjects(null), [])
})

test('pulledChoices leaves out the home project, pulled ones and inactive projects', () => {
  assert.deepEqual(pulledChoices(CP, { project_id: 'a', pulled_projects: [] }).map(p => p.id), ['d'])
  assert.deepEqual(pulledChoices(CP, { project_id: '', pulled_projects: ['d'] }).map(p => p.id), ['a'])
  assert.deepEqual(pulledChoices(CP, null).map(p => p.id), ['a', 'd'])
})

test('withPulled adds and removes one id, never twice and never past the maximum', () => {
  assert.equal(PULLED_MAX, 8)
  assert.deepEqual(withPulled(['a'], 'd', true), ['a', 'd'])
  assert.deepEqual(withPulled(['a'], 'a', true), ['a'])
  assert.deepEqual(withPulled(['a', 'd'], 'a', false), ['d'])
  const full = ['1', '2', '3', '4', '5', '6', '7', '8']
  assert.deepEqual(withPulled(full, '9', true), full)
  assert.deepEqual(withPulled(undefined, 'a', true), ['a'])
})

test('projectNames maps ids to names and tolerates a missing list', () => {
  assert.deepEqual(projectNames(CP), { a: 'Client site', b: 'Podcast', c: 'Kitchen', d: 'Home lab notes' })
  assert.deepEqual(projectNames(null), {})
})

test('chatsInProject filters by project, none, or everything', () => {
  const chats = [{ id: '1', projectId: 'a' }, { id: '2', projectId: '' }, { id: '3', projectId: 'd' }]
  assert.equal(chatsInProject(chats, '').length, 3)
  assert.deepEqual(chatsInProject(chats, 'a').map(c => c.id), ['1'])
  assert.deepEqual(chatsInProject(chats, NO_PROJECT).map(c => c.id), ['2'])
  assert.equal(chatsInProject(null, 'a').length, 0)
})

test('chatsInProject counts a chat whose project is gone as having no project', () => {
  const chats = [{ id: '1', projectId: 'a' }, { id: '2', projectId: '' }, { id: '3', projectId: 'deleted' }]
  assert.deepEqual(chatsInProject(chats, NO_PROJECT, CP).map(c => c.id), ['2', '3'])
  assert.deepEqual(chatsInProject(chats, 'a', CP).map(c => c.id), ['1'])
  assert.deepEqual(chatsInProject(chats, '', CP).map(c => c.id), ['1', '2', '3'])
  // Not loaded yet: the id alone decides, as before.
  assert.deepEqual(chatsInProject(chats, NO_PROJECT, null).map(c => c.id), ['2'])
  assert.deepEqual(chatsInProject(chats, NO_PROJECT, undefined).map(c => c.id), ['2'])
})

test('a pulled project says whether it is mounted, archived or unavailable', () => {
  assert.equal(pulledState({ status: 'active' }), 'mounted')
  assert.equal(pulledState({ status: 'archived' }), 'archived')
  assert.equal(pulledState({ status: 'broken' }), 'unavailable')
  assert.equal(pulledState(undefined), 'unavailable')
})

test('a broken project folder opens in Files by its slug', () => {
  assert.equal(projectFolderLink({ slug: 'kitchen' }), '/app/files?path=/Projects/kitchen')
})

test('homeProject and pulledProjects resolve names and drop projects that are gone', () => {
  assert.deepEqual(homeProject({ project_id: 'a' }, CP), { id: 'a', name: 'Client site' })
  assert.equal(homeProject({ project_id: 'zz' }, CP), null)
  assert.equal(homeProject({}, CP), null)
  assert.equal(homeProject({ project_id: 'a' }, null), null)
  assert.deepEqual(pulledProjects({ pulled_projects: ['d', 'zz', 'a'] }, CP), [{ id: 'd', name: 'Home lab notes', state: 'mounted' }, { id: 'a', name: 'Client site', state: 'mounted' }])
  assert.deepEqual(pulledProjects({ pulled_projects: ['b', 'c'] }, CP).map(p => p.state), ['archived', 'unavailable'])
  assert.deepEqual(pulledProjects({}, CP), [])
})

test('pickStartModel follows the defaults, then the last model, then the first one', () => {
  const models = [{ id: 'm1' }, { id: 'm2' }, { id: 'off', disabled: true }, { id: 'bad', alias_error: 'x' }]
  assert.equal(pickStartModel(models, null, 'm2'), 'm2')
  assert.equal(pickStartModel(models, null, 'gone'), 'm1')
  assert.equal(pickStartModel(models, { mode: 'model', model: 'm2', from: 'defaults' }, 'm1'), 'm2')
  assert.equal(pickStartModel(models, { mode: 'ask' }, 'm2'), 'm2')
  assert.equal(pickStartModel(models, { mode: 'model', model: 'gone', from: 'defaults' }, 'm2'), 'm2')
  assert.equal(pickStartModel([], null, 'm2'), '')
  assert.equal(pickStartModel(undefined, null, null), '')
})

test('isBusy reads the busy refusal of a running turn', () => {
  assert.equal(isBusy({ status: 409 }), true)
  assert.equal(isBusy({ status: 409, body: { error: { type: 'projects_off' } } }), false)
  assert.equal(isBusy({ status: 500 }), false)
  assert.equal(isBusy(null), false)
})

test('wrapKey moves a tab index with the arrow, Home and End keys', () => {
  assert.equal(wrapKey('ArrowRight', 4, 5), 0)
  assert.equal(wrapKey('ArrowLeft', 0, 5), 4)
  assert.equal(wrapKey('Home', 3, 5), 0)
  assert.equal(wrapKey('End', 1, 5), 4)
  assert.equal(wrapKey('x', 1, 5), -1)
})

test('APPLIED_LISTS covers all five default lists, since chats apply them all', () => {
  assert.deepEqual([...APPLIED_LISTS], ['skills', 'addons', 'mcp_servers', 'connectors', 'collections'])
  assert.deepEqual([...APPLIED_LISTS], [...DEFAULT_LISTS])
})
