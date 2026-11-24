import test from 'node:test'
import assert from 'node:assert/strict'
import { addressProblem, checkItems, connectionProblem, fieldProblems, defaultName, emptyMail, extraBody, extraForm, extraValid, followName, foldersProblem, hostProblem, mailSecret, parseFolders, portAfterSecurity, portProblem, writeModeOf, WRITE_MODES } from '../src/settings.js'
import { proposalView } from '../src/proposal.js'
import { canSave, connectorPayload, emptyConnector } from '../../../internal/connector-ui/src/connector.js'
import en from '../src/i18n/mail.en.json' with { type: 'json' }
import manifest from '../../manifest.json' with { type: 'json' }

const filled = (over = {}) => ({
  ...emptyMail(), mail_address: 'ann@example.com', mail_user: 'ann@example.com', mail_pass: 'app-password',
  imap_host: 'imap.example.com', smtp_host: 'smtp.example.com', ...over,
})
const t = (key, o = {}) => key.split('.').reduce((n, k) => n?.[k], en)?.replace?.(/\{\{(\w+)\}\}/g, (_, k) => o[k])

test('mail: hosts, addresses and ports are checked for shape', () => {
  assert.equal(hostProblem('imap.example.com'), '')
  assert.equal(hostProblem('203.0.113.7'), '')
  for (const bad of ['', 'https://imap.example.com', 'imap.example.com:993', 'imap.example.com/x', 'a b', '-x.example.com']) assert.equal(hostProblem(bad), 'hostShape', bad)
  assert.equal(addressProblem('ann@example.com'), '')
  for (const bad of ['', 'ann', 'Ann <ann@example.com>', 'a@b', 'a@b.c\r\nBcc: x@y.z']) assert.equal(addressProblem(bad), 'addressShape', bad)
  assert.equal(portProblem('imap', 993, 'tls'), '')
  assert.equal(portProblem('imap', 143, 'starttls'), '')
  assert.equal(portProblem('smtp', 587, 'none'), '')
  assert.equal(portProblem('imap', 993, 'starttls'), 'portSecurityMismatch')
  assert.equal(portProblem('smtp', 587, 'tls'), 'portSecurityMismatch')
  assert.equal(portProblem('smtp', 25, 'starttls'), 'portShape')
  assert.equal(portProblem('imap', 'abc', 'tls'), 'portShape')
})

test('mail: the port follows the protection until the person types a port of their own', () => {
  assert.equal(portAfterSecurity('smtp', 'tls', 'starttls', 465), 587)
  assert.equal(portAfterSecurity('imap', 'starttls', 'tls', 143), 993)
  assert.equal(portAfterSecurity('imap', 'tls', 'starttls', 1234), 1234)
})

test('mail: the sealed record has the shape the add-on reads and leaves out what is empty', () => {
  const rec = JSON.parse(mailSecret(filled({ mail_display: 'Ann', folders_text: 'INBOX\n\nINBOX\nArchive', drafts_folder: 'Entwürfe' })))
  assert.deepEqual(rec, {
    v: 1, address: 'ann@example.com', username: 'ann@example.com', password: 'app-password', name: 'Ann',
    imap: { host: 'imap.example.com', port: 993, security: 'tls' }, smtp: { host: 'smtp.example.com', port: 465, security: 'tls' },
    folders: ['INBOX', 'Archive'], draftsFolder: 'Entwürfe',
  })
  const plain = JSON.parse(mailSecret(filled()))
  for (const k of ['name', 'folders', 'draftsFolder']) assert.equal(k in plain, false, k)
})

test('mail: no record while a field is wrong, and none for a saved connection that is not replaced', () => {
  assert.equal(mailSecret(filled({ mail_pass: '' })), '')
  assert.equal(connectionProblem(filled({ mail_pass: '' })), 'passMissing')
  assert.equal(connectionProblem(filled({ imap_host: 'https://x' })), 'imapHostShape')
  assert.equal(connectionProblem(filled({ smtp_port: 25 })), 'smtpPortShape')
  assert.equal(connectionProblem(filled({ smtp_sec: 'starttls', smtp_port: 465 })), 'smtpPortSecurityMismatch')
  assert.equal(connectionProblem(filled({ mail_user: 'a\nb' })), 'userShape')
  assert.equal(mailSecret({ ...filled(), replace: false }), '')
  assert.equal(connectionProblem({ ...filled({ mail_pass: '' }), replace: false }), '', 'nothing to check when nothing is replaced')
})

test('mail: no protection needs a confirmation, and says so', () => {
  const f = filled({ imap_sec: 'none', imap_port: 143 })
  assert.equal(connectionProblem(f), 'plainNotConfirmed')
  assert.equal(mailSecret(f), '')
  assert.equal(connectionProblem({ ...f, plain_ok: true }), '')
  assert.equal(JSON.parse(mailSecret({ ...f, plain_ok: true })).imap.security, 'none')
  assert.match(t('plainNotConfirmed'), /without protection/)
})

test('mail: a record over 4096 bytes is not made', () => {
  const folders = Array.from({ length: 20 }, (_, i) => `${i}`.padEnd(199, 'x')).join('\n')
  assert.equal(foldersProblem(folders), '')
  assert.equal(connectionProblem(filled({ folders_text: folders })), 'tooLong')
  assert.equal(foldersProblem(Array.from({ length: 21 }, (_, i) => `f${i}`).join('\n')), 'foldersMany')
  assert.deepEqual(parseFolders(' A \n\nA\nB'), ['A', 'B'])
})

test('mail: the address rides in the list, the secret in the token, and a saved one reads back', () => {
  const form = { ...emptyConnector(), name: defaultName('ann@example.com'), ...filled(), write_mode: 'propose' }
  const body = connectorPayload(form, 'mail', mailSecret(form), { enabled: false, accept_any: false, ...extraBody(form) })
  assert.equal(body.kind, 'mail')
  assert.deepEqual(body.resources, ['ann@example.com'])
  assert.equal(body.write_mode, 'propose')
  assert.equal(JSON.parse(body.token).password, 'app-password')
  assert.equal(canSave(form, false, Boolean(mailSecret(form)), null, true), true)
  assert.equal(canSave({ ...form, mail_pass: '' }, false, Boolean(mailSecret({ ...form, mail_pass: '' })), null, true), false)

  const saved = { ...emptyConnector(), id: 'b1', name: 'Work', ...extraForm({ id: 'b1', resources: ['ann@example.com'], write_mode: 'propose' }) }
  assert.equal(saved.replace, false)
  assert.equal(saved.mail_address, 'ann@example.com')
  assert.equal(writeModeOf(saved.write_mode), 'propose')
  const update = connectorPayload(saved, 'mail', mailSecret(saved), extraBody(saved))
  assert.equal('token' in update, false, 'an update without new details keeps the sealed connection')
  assert.deepEqual(update.resources, ['ann@example.com'])
  assert.equal(extraValid(saved), true)
  assert.equal(extraValid(filled({ mail_pass: '' })), false)
})

test('mail: Allow all is offered, kept on save and read back, and never the default', () => {
  assert.deepEqual(WRITE_MODES, ['off', 'propose', 'auto'], 'auto is offered')
  assert.equal(writeModeOf('auto'), 'auto')
  assert.equal(writeModeOf('x'), 'off')
  assert.equal(writeModeOf(undefined), 'off')
  assert.equal(emptyMail().write_mode, 'off', 'a new connection starts read only')
  assert.equal(extraForm({}).write_mode, 'off')
  assert.equal(extraBody({ mail_address: 'ann@example.com', write_mode: 'auto' }).write_mode, 'auto', 'choosing Allow all is sent')
  const reloaded = extraForm({ id: 'b1', resources: ['ann@example.com'], write_mode: 'auto' })
  assert.equal(reloaded.write_mode, 'auto', 'a saved auto shows as Allow all after a reload')
  assert.equal(extraForm({ id: 'b1', resources: ['ann@example.com'], write_mode: 'propose' }).write_mode, 'propose')
  assert.equal(t('writeMode.auto'), 'Allow all')
  assert.match(t('writeMode.note.auto'), /cannot be taken back/)
})

test('mail: the name follows the address until the person types one', () => {
  assert.equal(followName('', '', 'ann@example.com'), 'Mail: ann@example.com')
  assert.equal(followName('Mail: ann@example.com', 'ann@example.com', 'bob@example.com'), 'Mail: bob@example.com')
  assert.equal(followName('Work', 'ann@example.com', 'bob@example.com'), 'Work')
})

test('mail: the Test names which of the two logins failed', () => {
  const items = checkItems({ imap: { ok: true }, smtp: { ok: false, error: 'The mail server refused the username or password for sending.' } })
  assert.deepEqual(items.map((i) => [i.part, i.tone]), [['imap', 'ok'], ['smtp', 'warn']])
  assert.match(t(items[0].key), /IMAP.*works/)
  assert.match(t(items[1].key, items[1].vars), /SMTP.*failed.*refused the username/)
  assert.deepEqual(checkItems(null).map((i) => i.tone), ['warn', 'warn'])
})

test('mail: the approval card reads the stored text, the addresses and the kind of write', () => {
  const v = proposalView('send-message', { mailbox: 'ann@example.com', to: ['bob@example.org'], cc: ['c@example.org'], subject: 'Hi', body: 'Full text', inReplyTo: 'x@y.z' }, { from: 'ann@example.com', bodyCut: true, bodyBytes: 9 })
  assert.deepEqual([v.tool, v.from, v.to, v.cc, v.subject, v.body, v.reply, v.cut], ['send-message', 'ann@example.com', ['bob@example.org'], ['c@example.org'], 'Hi', 'Full text', true, false])
  assert.equal(proposalView('other', null, { bodyCut: true, bodyBytes: 3 }).tool, 'send-message')
  assert.equal(proposalView('save-draft', null, { to: ['a@b.c'] }).tool, 'save-draft')
  assert.equal(proposalView('save-draft', null, { body: 'cut', bodyCut: true, bodyBytes: 99 }).cut, true)
})

test('mail: every word the pages use exists, and the manifest renderers name the card', () => {
  for (const key of ['title', 'imapOk', 'plainNotConfirmed', 'imapPortSecurityMismatch']) assert.ok(t(key) || t(`check.${key}`) || key, key)
  for (const k of ['approval.action.send-message', 'approval.action.save-draft', 'writeMode.note.off', 'writeMode.note.propose', 'security.plainWarning', 'check.smtpFailed']) assert.ok(t(k), k)
  const renderers = manifest.capabilities.ui.renderers
  assert.deepEqual(renderers.map((r) => r.tool).sort(), ['save-draft', 'send-message'])
  assert.ok(renderers.every((r) => r.component === 'ProposalCard' && r.kind === 'approval-card'))
})

test('mail: every wrong field is reported at once, so one error never hides another', () => {
  const p = fieldProblems(filled({ mail_pass: '', imap_host: 'https://x', smtp_port: 25 }))
  assert.deepEqual(Object.keys(p).sort(), ['imapHost', 'pass', 'smtpPort'])
  assert.deepEqual(fieldProblems({ ...filled({ mail_pass: '' }), replace: false }), {})
  assert.deepEqual(fieldProblems(filled()), {})
})

test('mail: Allow all has words in every language the add-on ships', async () => {
  const { readFileSync } = await import('node:fs')
  for (const loc of ['en','it','es','de','fr','zh-CN','id','ko','pt-BR']) {
    const w = JSON.parse(readFileSync(new URL(`../src/i18n/mail.${loc}.json`, import.meta.url), 'utf8')).writeMode
    for (const path of [['auto'], ['line', 'auto'], ['note', 'auto'], ['row', 'auto']]) {
      const v = path.reduce((o, k) => o?.[k], w)
      assert.equal(typeof v, 'string', `${loc}: writeMode.${path.join('.')}`)
      assert.ok(v.length > 0)
    }
    assert.notEqual(w.row.auto, w.row.propose, `${loc}: Allow all must not read as Asks you first`)
  }
})
