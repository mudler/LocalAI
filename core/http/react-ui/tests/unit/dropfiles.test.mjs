import test from 'node:test'
import assert from 'node:assert/strict'
import { droppedFiles, hasFiles } from '../../src/utils/dropFiles.js'

const file = (name) => ({ name })
const item = (kind, isDirectory = false) => ({ kind, webkitGetAsEntry: () => ({ isDirectory }) })

test('drop files: a drag of text does not carry files', () => {
  assert.equal(hasFiles({ types: ['text/plain'] }), false)
  assert.equal(hasFiles({ types: ['text/plain', 'Files'] }), true)
})

test('drop files: every dropped file is returned in order', () => {
  const a = file('a.png'); const b = file('b.mp3'); const c = file('c.zip')
  const dt = { files: [a, b, c], items: [item('file'), item('file'), item('file')] }
  assert.deepEqual(droppedFiles(dt), [a, b, c])
})

test('drop files: a dropped folder is left out and the files stay', () => {
  const a = file('a.txt'); const dir = file('photos')
  const dt = { files: [dir, a], items: [item('file', true), item('file')] }
  assert.deepEqual(droppedFiles(dt), [a])
})

test('drop files: a browser without entries gives the file list as it is', () => {
  const a = file('a.txt')
  assert.deepEqual(droppedFiles({ files: [a], items: [] }), [a])
  assert.deepEqual(droppedFiles({ files: [a], items: [{ kind: 'file' }] }), [a])
  assert.deepEqual(droppedFiles(null), [])
})
