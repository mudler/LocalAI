import test from 'node:test'
import assert from 'node:assert/strict'
import { OFFERED_MODES, extraBody, extraForm, extraValid, folderId, foldersIssue, foldersProblem, parseFolders, WRITE_MODES, writeModeOf } from '../src/folders.js'
import { canSave, connectorPayload, emptyConnector } from '../../../internal/connector-ui/src/connector.js'
import en from '../src/i18n/gdrive.en.json' with { type: 'json' }
import manifest from '../../manifest.json' with { type: 'json' }

test('gdrive: an address copied from the browser gives its folder ID, anything else stays as typed', () => {
  assert.equal(folderId('https://drive.google.com/drive/folders/1AbC_d-9?usp=sharing'), '1AbC_d-9')
  assert.equal(folderId('https://drive.google.com/drive/u/2/folders/1AbC_d-9'), '1AbC_d-9')
  assert.equal(folderId('  1AbC_d-9 '), '1AbC_d-9')
  assert.equal(folderId('https://evil.example/drive/folders/1AbC'), 'https://evil.example/drive/folders/1AbC')
  assert.equal(folderId('root'), 'root')
})

test('gdrive: the folder list is read from lines and commas, keeps the case, and drops repeats', () => {
  assert.deepEqual(parseFolders('abc\n ABC, abc\r\nx_y-1'), ['abc', 'ABC', 'x_y-1'])
  assert.deepEqual(parseFolders(''), [])
  assert.equal(foldersProblem('abc\nroot'), '')
  assert.equal(foldersProblem('not a folder'), 'foldersShape')
  assert.equal(foldersProblem('a/b'), 'foldersShape')
  assert.equal(foldersProblem(Array.from({ length: 51 }, (_, i) => `f${i}`).join('\n')), 'foldersMany')
  assert.deepEqual(foldersIssue('a\nnot a folder\nc'), { key: 'foldersShape', entry: 'not a folder' })
  assert.ok(foldersIssue('x/'.repeat(100)).entry.length <= 60)
})

test('gdrive: the list and the write mode ride in the update, a saved one reads back, and no token is sent', () => {
  const saved = { id: 'b1', name: 'Work', resources: ['abc', 'def'], write_mode: 'propose', credentials_only: true }
  const form = { ...emptyConnector(), id: 'b1', name: 'Work', ...extraForm(saved) }
  assert.equal(form.folders_text, 'abc\ndef')
  assert.equal(form.write_mode, 'propose')
  const body = connectorPayload(form, 'gdrive', '', { enabled: false, accept_any: false, ...extraBody({ ...form, folders_text: 'abc' }) })
  assert.equal(body.kind, 'gdrive')
  assert.equal('token' in body, false)
  assert.deepEqual(body.resources, ['abc'])
  assert.equal(body.write_mode, 'propose')
  assert.equal(body.enabled, false)
  assert.equal(canSave(form, false, false, null, true), true, 'a saved connection needs no token and no agent')
  assert.equal(extraValid({ folders_text: 'nope nope' }), false)
  assert.deepEqual(extraBody({ folders_text: '' }).resources, [], 'an empty list clears the list')
})

test('gdrive: an unknown write mode is read only', () => {
  assert.deepEqual(WRITE_MODES, ['off', 'propose', 'auto'])
  assert.equal(writeModeOf('x'), 'off')
  assert.equal(writeModeOf(undefined), 'off')
})

test('gdrive: every key the shared page and this page use has words', () => {
  const need = ['title', 'intro', 'limits', 'edit', 'name', 'folders', 'foldersHelp', 'foldersShape', 'foldersMany', 'agent', 'agentNone', 'test', 'testOk', 'save', 'cancel', 'editButton', 'delete', 'empty', 'noFolders', 'foldersOf', 'agentOf', 'inactive']
  for (const k of need) assert.ok(en[k], k)
  for (const k of ['title', 'message', 'confirm']) assert.ok(en.confirmDelete[k], k)
  for (const m of OFFERED_MODES) {
    assert.ok(en.writeMode[m], m)
    for (const part of ['line', 'note', 'row']) assert.ok(en.writeMode[part][m], `${part}.${m}`)
  }
  assert.ok(en.writeMode.row.auto, 'a connection that has auto reads as asking first')
})

test('gdrive: the page is declared for the connector kind', () => {
  const ui = manifest.capabilities.ui
  assert.deepEqual(ui.pages, ['apps'])
  assert.equal(ui.mainPage, 'apps')
  assert.equal(manifest.capabilities.connector.kinds[0].page, 'apps')
})
