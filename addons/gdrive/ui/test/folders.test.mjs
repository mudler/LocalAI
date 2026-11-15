import test from 'node:test'
import assert from 'node:assert/strict'
import { ALL_FOLDERS, ALL_FOLDERS_CONFIRM, extraBody, extraForm, extraValid, folderId, foldersIssue, foldersProblem, parseFolders, WRITE_MODES, writeModeOf } from '../src/folders.js'
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

test('gdrive: an unknown write mode is read only, and Allow all is offered and kept', () => {
  assert.deepEqual(WRITE_MODES, ['off', 'propose', 'auto'], 'auto is offered')
  assert.equal(writeModeOf('x'), 'off')
  assert.equal(writeModeOf(undefined), 'off')
  assert.equal(writeModeOf('auto'), 'auto')
  assert.equal(extraBody({ folders_text: 'a', write_mode: 'auto' }).write_mode, 'auto', 'a saved auto round-trips to the request')
  assert.equal(extraForm({ resources: ['a'], write_mode: 'auto' }).write_mode, 'auto', 'and reads back after a reload')
  assert.equal(extraForm({ resources: ['a'] }).write_mode, 'off', 'a connection without a mode stays read only')
})

test('gdrive: every key the shared page and this page use has words', () => {
  const need = ['title', 'intro', 'limits', 'edit', 'name', 'folders', 'foldersHelp', 'foldersShape', 'foldersMany', 'agent', 'agentNone', 'test', 'testOk', 'save', 'cancel', 'editButton', 'delete', 'empty', 'noFolders', 'foldersOf', 'agentOf', 'inactive']
  for (const k of need) assert.ok(en[k], k)
  for (const k of ['title', 'message', 'confirm']) assert.ok(en.confirmDelete[k], k)
  for (const m of WRITE_MODES) {
    assert.ok(en.writeMode[m], m)
    for (const part of ['line', 'note', 'row']) assert.ok(en.writeMode[part][m], `${part}.${m}`)
  }
  assert.equal(en.writeMode.auto, 'Allow all')
  assert.notEqual(en.writeMode.row.auto, en.writeMode.row.propose, 'auto no longer reads as asking first')
  assert.match(en.writeMode.note.auto, /cannot undo/, 'the warning is in the note')
})

test('gdrive: the page is declared for the connector kind', () => {
  const ui = manifest.capabilities.ui
  assert.deepEqual(ui.pages, ['apps'])
  assert.equal(ui.mainPage, 'apps')
  assert.equal(manifest.capabilities.connector.kinds[0].page, 'apps')
})

test('gdrive: All folders is the list * and needs the confirmation of a new choice', () => {
  assert.equal(ALL_FOLDERS, '*')
  assert.equal(ALL_FOLDERS_CONFIRM, 'allow any resource')
  const saved = { id: 'b1', name: 'Work', resources: ['*'], write_mode: 'off' }
  const form = { ...emptyConnector(), id: 'b1', ...extraForm(saved) }
  assert.equal(form.all_folders, true)
  assert.equal(form.all_was, true)
  assert.equal(form.folders_text, '', 'the * is not a line of the folder list')
  assert.deepEqual(extraBody(form).resources, ['*'])
  assert.equal(extraBody(form).confirm_any_resource, undefined, 'a choice that was already saved is not confirmed again')
  assert.equal(extraValid(form), true)
  // A new choice: the list is *, whatever the box still holds, and it waits for the tick.
  const fresh = { ...emptyConnector(), ...extraForm({}), all_folders: true, folders_text: 'abc' }
  assert.deepEqual(extraBody(fresh).resources, ['*'])
  assert.equal(extraValid(fresh), false)
  const ticked = { ...fresh, all_ack: true }
  assert.equal(extraValid(ticked), true)
  assert.equal(extraBody(ticked).confirm_any_resource, 'allow any resource')
})

test('gdrive: turning All folders off gives back the typed list, and an empty list still means no access', () => {
  const form = { ...extraForm({ resources: ['abc'] }), all_folders: false, folders_text: 'abc' }
  assert.deepEqual(extraBody(form).resources, ['abc'])
  assert.deepEqual(extraBody({ ...extraForm({}) }).resources, [])
  assert.equal(extraForm({ resources: ['abc'] }).all_folders, false)
  assert.equal(extraValid({ ...extraForm({}), all_folders: true, all_ack: true, folders_text: 'not a folder' }), true, 'the box is ignored while All folders is on')
  assert.equal(extraValid({ ...extraForm({}), all_folders: false, folders_text: 'not a folder' }), false)
})

test('gdrive: a connection with no agent and no trigger saves, and its approval is the default', () => {
  const form = { ...emptyConnector(), id: 'b1', name: 'Work', ...extraForm({}) }
  assert.equal(canSave(form, false, false, null, true), true)
  const body = connectorPayload(form, 'gdrive', '', { enabled: false, accept_any: false, ...extraBody(form) })
  assert.equal(body.agent_id, '')
  assert.equal(body.enabled, false)
  assert.deepEqual(body.approval, { mode: 'ask', acts: [], max_pending: 0, min_gap_minutes: 0, max_runs_per_day: 0, max_lifetime_days: 0 })
  assert.deepEqual(body.caps, [])
})

test('gdrive: the manifest lets the host honor * and the words for All folders exist', () => {
  assert.equal(manifest.capabilities.connector.kinds[0].wildcardResources, true)
  for (const k of ['label', 'hint', 'warning', 'confirm', 'row']) assert.ok(en.allFolders[k], k)
})

test('gdrive: Allow all has words in every language the add-on ships', async () => {
  const { readFileSync } = await import('node:fs')
  for (const loc of ['en','it','es','de','fr','zh-CN','id','ko','pt-BR']) {
    const w = JSON.parse(readFileSync(new URL(`../src/i18n/gdrive.${loc}.json`, import.meta.url), 'utf8')).writeMode
    for (const path of [['auto'], ['line', 'auto'], ['note', 'auto'], ['row', 'auto']]) {
      const v = path.reduce((o, k) => o?.[k], w)
      assert.equal(typeof v, 'string', `${loc}: writeMode.${path.join('.')}`)
      assert.ok(v.length > 0)
    }
    assert.notEqual(w.row.auto, w.row.propose, `${loc}: Allow all must not read as Asks you first`)
  }
})
