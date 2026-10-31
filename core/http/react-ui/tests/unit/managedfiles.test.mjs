import test from 'node:test'
import assert from 'node:assert/strict'
import { splitEntries, isSettingsFolder, guardOf } from '../../src/utils/managedFiles.js'

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
  assert.equal(guardOf(file('/LocalAI/notes.txt', 'localai')), null, 'a file LocalAI does not read')
  assert.equal(guardOf(file('/persona.json')), null, 'the user\'s own file of the same name')
  assert.equal(guardOf(dir('Notes')), null)
  assert.equal(guardOf(null), null)
})
