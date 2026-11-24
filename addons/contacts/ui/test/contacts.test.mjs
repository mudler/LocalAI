import test from 'node:test'
import assert from 'node:assert/strict'
import { load, React, renderToString } from './load.mjs'

const lib = () => import('../src/lib/contacts.js')

test('lib: the form becomes the arguments of the tool, and back', async () => {
  const { emptyForm, argsOf, formOf, problem } = await lib()
  const f = { ...emptyForm(), name: ' Marco Rossi ', phones: [{ label: 'mobile', value: ' +39 02 5550123 ' }, { label: '', value: '' }], emails: [{ label: '', value: 'marco@example.org' }] }
  assert.deepEqual(argsOf(f), { name: 'Marco Rossi', organisation: '', phones: [{ label: 'mobile', value: '+39 02 5550123' }], emails: [{ value: 'marco@example.org' }], birthday: '', address: '', notes: '' })
  assert.equal(argsOf({ ...f, id: 'ct-1' }).id, 'ct-1')
  const back = formOf({ id: 'ct-1', name: 'Anna Bianchi', organisation: 'Example Org', phones: [], emails: [{ label: 'work', value: 'anna@example.org' }], birthday: '--04-23' })
  assert.equal(back.phones.length, 1, 'an empty list keeps one blank row to type in')
  assert.deepEqual(back.emails, [{ label: 'work', value: 'anna@example.org' }])
  assert.equal(problem({ ...f, name: ' ' }), 'contacts.form.needName')
  assert.equal(problem({ ...f, birthday: '23/04/1990' }), 'contacts.form.badBirthday')
  assert.equal(problem({ ...f, birthday: '--04-23' }), '')
  assert.equal(problem(f), '')
})

test('lib: initials and birthdays read well', async () => {
  const { initials, birthdayText } = await lib()
  assert.equal(initials('marco rossi'), 'MR')
  assert.equal(initials('Anna'), 'A')
  assert.equal(initials('  '), '?')
  const fmt = (opts, y, m, d) => JSON.stringify([!!opts.year, y, m, d])
  assert.equal(birthdayText('1990-04-23', fmt), '[true,1990,4,23]')
  assert.equal(birthdayText('--04-23', fmt), '[false,2000,4,23]')
  assert.equal(birthdayText('', fmt), '')
})

const marco = { id: 'ct-1', name: 'Marco Rossi', organisation: 'Example Org', phones: [{ label: 'mobile', value: '+39 02 5550123' }], emails: [{ label: 'work', value: 'marco@example.org' }], birthday: '1990-04-23', address: 'Via Esempio 1, Milano', notes: 'Met at the fair.' }
const anna = { id: 'ct-2', name: 'Anna Bianchi', phones: [], emails: [{ value: 'anna@example.org' }] }

const make = async (extra) => {
  const { mod, sdk } = await load(extra)
  return mod.createAddon(sdk)
}
const draw = (addon, name, props) => renderToString(React.createElement(addon.components[name], props))

test('page: it draws nothing for a page it does not own, and a loading note for its own', async () => {
  const addon = await make()
  assert.equal(renderToString(React.createElement(addon.Component, { page: 'other' })), '')
  assert.match(renderToString(React.createElement(addon.Component, { page: 'contacts' })), /data-testid="contacts-loading"/)
})

test('page: the list shows each contact with a short line, and says when it is empty', async () => {
  const addon = await make()
  const html = draw(addon, 'ContactsList', { contacts: [marco, anna], current: 'ct-1', onOpen() {}, searching: false })
  assert.match(html, /Marco Rossi/)
  assert.match(html, /Example Org · \+39 02 5550123 · marco@example.org/)
  assert.match(html, /aria-current="true"/)
  assert.equal((html.match(/data-testid="contacts-item"/g) || []).length, 2)
  assert.match(draw(addon, 'ContactsList', { contacts: [], onOpen() {}, searching: false }), /No contacts yet/)
  assert.match(draw(addon, 'ContactsList', { contacts: [], onOpen() {}, searching: true }), /No contact matches/)
})

test('page: the detail shows what the contact has and nothing it lacks', async () => {
  const addon = await make()
  const html = draw(addon, 'ContactsDetail', { c: marco, onEdit() {}, onDeleted() {}, onClose() {} })
  for (const w of ['Example Org', 'mobile', 'marco@example.org', 'April 23, 1990', 'Via Esempio 1', 'Met at the fair.']) assert.ok(html.includes(w), w)
  assert.match(html, /data-testid="contacts-edit"/)
  assert.match(html, /data-testid="contacts-delete"/)
  const bare = draw(addon, 'ContactsDetail', { c: anna, onEdit() {}, onDeleted() {}, onClose() {} })
  assert.doesNotMatch(bare, /Birthday|Address|Notes|Phones/)
  assert.match(bare, /Emails/)
})

test('page: the form has every field', async () => {
  const addon = await make()
  const { emptyForm } = await lib()
  const form = draw(addon, 'ContactsForm', { initial: emptyForm(), onSaved() {}, onCancel() {} })
  for (const id of ['name', 'organisation', 'phones', 'emails', 'add-phone', 'add-email', 'birthday', 'address', 'notes', 'save', 'cancel']) {
    assert.match(form, new RegExp(`data-testid="contacts-${id}"`), id)
  }
  assert.match(form, /New contact/)
})

test('page: it reads in Italian, and in English for a language it has no text for', async () => {
  const it = await make({ language: () => 'it' })
  assert.match(draw(it, 'ContactsList', { contacts: [], onOpen() {}, searching: true }), /Nessun contatto corrisponde/)
  const xx = await make({ language: () => 'xx' })
  assert.match(draw(xx, 'ContactsList', { contacts: [], onOpen() {}, searching: true }), /No contact matches/)
})
