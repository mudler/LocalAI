import test from 'node:test'
import assert from 'node:assert/strict'
import { ALL, extraBody, extraForm, extraValid, hasAll, labelKey, labelsIssue, labelsProblem, parseLabels, WRITE_MODES, writeModeOf } from '../src/labels.js'
import { canSave, connectorPayload, emptyConnector } from '../../../internal/connector-ui/src/connector.js'
import en from '../src/i18n/gmail.en.json' with { type: 'json' }
import manifest from '../../manifest.json' with { type: 'json' }

test('gmail: a label name with spaces is written with dashes, the case is kept', () => {
  assert.equal(labelKey('  Private  notes '), 'Private-notes')
  assert.equal(labelKey('INBOX'), 'INBOX')
  assert.equal(labelKey(null), '')
})

test('gmail: the label list is read from lines and commas, and drops repeats without regard to case', () => {
  assert.deepEqual(parseLabels('Work\n work, Label_12\r\nPrivate notes'), ['Work', 'Label_12', 'Private-notes'])
  assert.deepEqual(parseLabels(''), [])
  assert.equal(labelsProblem('Work\nINBOX'), '')
  assert.equal(labelsProblem('bad<label'), 'labelsShape')
  assert.equal(labelsProblem('a"b'), 'labelsShape')
  assert.equal(labelsProblem(Array.from({ length: 51 }, (_, i) => `l${i}`).join('\n')), 'labelsMany')
  assert.deepEqual(labelsIssue('a\nx<y\nc'), { key: 'labelsShape', entry: 'x<y' })
  assert.ok(labelsIssue('<'.repeat(100)).entry.length <= 60)
})

test('gmail: the word ALL is found without regard to case', () => {
  assert.equal(ALL, 'ALL')
  assert.equal(hasAll(['Work', 'all']), true)
  assert.equal(hasAll(['Work']), false)
  assert.equal(hasAll(undefined), false)
})

test('gmail: the list and the write mode ride in the update, a saved one reads back, and no token is sent', () => {
  const saved = { id: 'b1', name: 'Work', resources: ['Work', 'INBOX'], write_mode: 'propose', credentials_only: true }
  const form = { ...emptyConnector(), id: 'b1', name: 'Work', ...extraForm(saved) }
  assert.equal(form.labels_text, 'Work\nINBOX')
  assert.equal(form.write_mode, 'propose')
  const body = connectorPayload(form, 'gmail', '', { enabled: false, accept_any: false, ...extraBody({ ...form, labels_text: 'Work' }) })
  assert.equal(body.kind, 'gmail')
  assert.equal('token' in body, false)
  assert.deepEqual(body.resources, ['Work'])
  assert.equal(body.write_mode, 'propose')
  assert.equal(canSave(form, false, false, null, true), true, 'a saved connection needs no token and no agent')
  assert.equal(extraValid({ labels_text: 'a<b' }), false)
  assert.deepEqual(extraBody({ labels_text: '' }).resources, [], 'an empty list clears the list')
})

test('gmail: an unknown write mode is read only, and Allow all is offered and kept', () => {
  assert.deepEqual(WRITE_MODES, ['off', 'propose', 'auto'], 'auto is offered')
  assert.equal(writeModeOf('x'), 'off')
  assert.equal(writeModeOf(undefined), 'off')
  assert.equal(writeModeOf('auto'), 'auto')
  assert.equal(extraBody({ labels_text: 'Work', write_mode: 'auto' }).write_mode, 'auto', 'a saved auto round-trips to the request')
  assert.equal(extraForm({ resources: ['Work'], write_mode: 'auto' }).write_mode, 'auto', 'and reads back after a reload')
  assert.equal(extraForm({ resources: ['Work'] }).write_mode, 'off', 'a connection without a mode stays read only')
})

test('gmail: every key the shared page and this page use has words', () => {
  const need = ['title', 'intro', 'limits', 'edit', 'name', 'labels', 'labelsHelp', 'labelsShape', 'labelsMany', 'allNote', 'allMailbox', 'agent', 'agentNone', 'test', 'testOk', 'save', 'cancel', 'editButton', 'delete', 'empty', 'noLabels', 'labelsOf', 'agentOf', 'inactive']
  for (const k of need) assert.ok(en[k], k)
  for (const k of ['title', 'message', 'confirm']) assert.ok(en.confirmDelete[k], k)
  for (const m of WRITE_MODES) {
    assert.ok(en.writeMode[m], m)
    for (const part of ['line', 'note', 'row']) assert.ok(en.writeMode[part][m], `${part}.${m}`)
  }
  assert.equal(en.writeMode.auto, 'Allow all')
  assert.notEqual(en.writeMode.row.auto, en.writeMode.row.propose)
  assert.match(en.writeMode.note.auto, /cannot be taken back/, 'the warning is in the note')
})

test('gmail: the page is declared for the connector kind', () => {
  const ui = manifest.capabilities.ui
  assert.deepEqual(ui.pages, ['apps'])
  assert.equal(ui.mainPage, 'apps')
  assert.equal(manifest.capabilities.connector.kinds[0].page, 'apps')
})

test('gmail: Allow all has words in every language the add-on ships', async () => {
  const { readFileSync } = await import('node:fs')
  for (const loc of ['en','it','es','de','fr','zh-CN','id','ko','pt-BR']) {
    const w = JSON.parse(readFileSync(new URL(`../src/i18n/gmail.${loc}.json`, import.meta.url), 'utf8')).writeMode
    for (const path of [['auto'], ['line', 'auto'], ['note', 'auto'], ['row', 'auto']]) {
      const v = path.reduce((o, k) => o?.[k], w)
      assert.equal(typeof v, 'string', `${loc}: writeMode.${path.join('.')}`)
      assert.ok(v.length > 0)
    }
    assert.notEqual(w.row.auto, w.row.propose, `${loc}: Allow all must not read as Asks you first`)
  }
})
