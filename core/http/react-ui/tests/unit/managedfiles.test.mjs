import test from 'node:test'
import assert from 'node:assert/strict'
import { splitEntries, isSettingsFolder, guardOf, pageOf, projectMarks } from '../../src/utils/managedFiles.js'

const dir = (name, managed) => ({ path: `/${name}`, name, kind: 'dir', ...(managed ? { managed } : {}) })
const file = (path, managed) => ({ path, name: path.split('/').pop(), kind: 'file', ...(managed ? { managed } : {}) })

test('managed files: the user\'s own files come first, LocalAI/ and Chats/ last, in that order', () => {
  const entries = [dir('Chats', 'chats'), dir('LocalAI', 'localai'), dir('Notes'), file('/a.txt'), dir('pages')]
  const { own, managed } = splitEntries(entries, '/')
  assert.deepEqual(own.map(e => e.name), ['Notes', 'a.txt', 'pages'], 'the server order of the own files stays')
  assert.deepEqual(managed.map(e => e.name), ['LocalAI', 'Chats'])
})

test('managed files: only the top of the home is split, and a marker the server did not set is not guessed', () => {
  const inside = [file('/LocalAI/ui.json', 'localai'), file('/LocalAI/persona.json', 'localai')]
  assert.deepEqual(splitEntries(inside, '/LocalAI'), { own: inside, managed: [] })
  const unmarked = [dir('LocalAI'), dir('Chats')]
  assert.equal(splitEntries(unmarked, '/').managed.length, 0)
  assert.deepEqual(splitEntries(undefined, '/'), { own: [], managed: [] })
})

test('managed files: the header line shows in LocalAI/ only', () => {
  assert.equal(isSettingsFolder('/LocalAI'), true)
  assert.equal(isSettingsFolder('/'), false)
  assert.equal(isSettingsFolder('/LocalAI/sub'), false)
})

test('managed files: a delete or rename of a managed folder or settings file asks first and says what stops working', () => {
  assert.deepEqual(guardOf(dir('LocalAI', 'localai')), { what: 'settings', name: 'LocalAI' })
  assert.deepEqual(guardOf(dir('Chats', 'chats')), { what: 'chats', name: 'Chats' })
  assert.deepEqual(guardOf(file('/LocalAI/persona.json', 'localai')), { what: 'persona', name: 'LocalAI/persona.json' })
  assert.deepEqual(guardOf(file('/LocalAI/user.json', 'localai')), { what: 'user', name: 'LocalAI/user.json' })
  assert.deepEqual(guardOf(file('/LocalAI/ui.json', 'localai')), { what: 'ui', name: 'LocalAI/ui.json' })
  assert.deepEqual(guardOf(file('/LocalAI/defaults.json', 'localai')), { what: 'defaults', name: 'LocalAI/defaults.json' })
  assert.equal(guardOf(file('/LocalAI/notes.txt', 'localai')), null, 'a file LocalAI does not read')
  assert.equal(guardOf(file('/persona.json')), null, 'the user\'s own file of the same name')
  assert.equal(guardOf(dir('Notes')), null)
  assert.equal(guardOf(null), null)
})

test('managed files: the folders LocalAI creates go last in a fixed order, and a value nobody heard of is grouped by the marker', () => {
  const entries = [dir('.skills', 'skills'), dir('Zed', 'future'), dir('pages', 'pages'), dir('Chats', 'chats'), dir('Memory', 'memory'), dir('LocalAI', 'localai'), dir('Notes'), dir('Alpha', 'other-future')]
  const { own, managed } = splitEntries(entries, '/')
  assert.deepEqual(own.map(e => e.name), ['Notes'])
  assert.deepEqual(managed.map(e => e.name), ['LocalAI', 'Chats', 'Memory', 'pages', '.skills', 'Zed', 'Alpha'], 'unknown values last, in the server order')
})

test('managed files: a folder is never managed by its name, only by the marker of the listing', () => {
  const entries = [dir('Memory'), dir('pages'), dir('.skills')]
  assert.deepEqual(splitEntries(entries, '/'), { own: entries, managed: [] })
})

test('managed files: the folders with a page of their own say which, and the others do not', () => {
  assert.deepEqual(pageOf(dir('Memory', 'memory')), { to: '/app/account?tab=memory', features: ['chats'] })
  assert.equal(pageOf(dir('pages', 'pages')).to, '/app/pages')
  assert.equal(pageOf(dir('.skills', 'skills')).to, '/app/skills')
  assert.equal(pageOf(dir('Chats', 'chats')), null)
  assert.equal(pageOf(dir('LocalAI', 'localai')), null)
  assert.equal(pageOf(dir('Zed', 'future')), null)
  assert.equal(pageOf(file('/Memory/index.md', 'memory')), null, 'a file')
  assert.equal(pageOf(dir('Memory')), null, 'unmarked')
})

test('managed files: Projects/ sits after Chats/ in the managed group, and has its own page', () => {
  const entries = [dir('Memory', 'memory'), dir('Projects', 'projects'), dir('LocalAI', 'localai'), dir('Chats', 'chats'), dir('Notes')]
  const { own, managed } = splitEntries(entries, '/')
  assert.deepEqual(own.map(e => e.name), ['Notes'])
  assert.deepEqual(managed.map(e => e.name), ['LocalAI', 'Chats', 'Projects', 'Memory'])
  assert.deepEqual(pageOf(dir('Projects', 'projects')), { to: '/app/projects', features: ['chats'] })
})

test('managed files: deleting or renaming the Projects root asks first, a project folder inside it does not', () => {
  assert.deepEqual(guardOf(dir('Projects', 'projects')), { what: 'projects', name: 'Projects' })
  assert.equal(guardOf({ path: '/Projects/alpha', name: 'alpha', kind: 'dir' }), null, 'a project folder carries no marker')
  assert.equal(guardOf(dir('Projects')), null, 'a folder is never guarded by its name')
})

test('managed files: a project folder maps to its name and page, a project without a slug or name to nothing', () => {
  const marks = projectMarks([
    { id: 'p1', slug: 'client-site', name: 'Client site' },
    { id: 'p 2', slug: 'home-lab', name: 'Home lab' },
    { id: 'p3', slug: '', name: 'No slug' },
    { id: 'p4', slug: 'no-name' },
  ])
  assert.deepEqual([...marks.keys()], ['/Projects/client-site', '/Projects/home-lab'])
  assert.deepEqual(marks.get('/Projects/client-site'), { name: 'Client site', to: '/app/projects/p1' })
  assert.equal(marks.get('/Projects/home-lab').to, '/app/projects/p%202')
  assert.equal(projectMarks(undefined).size, 0)
})
