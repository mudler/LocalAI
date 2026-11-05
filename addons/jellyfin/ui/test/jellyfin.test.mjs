import test from 'node:test'
import assert from 'node:assert/strict'
import { checkFailure, checkItem, checkLine, composeServerKey, defaultName, extraBody, extraForm, extraValid, followName, isPlainHttp, jellyfinSecret, normalizePin, serverIssue, splitServerKey, tokenProblem, WRITE_MODES, writeModeOf } from '../src/server.js'
import { canSave, connectorPayload, emptyConnector } from '../../../internal/connector-ui/src/connector.js'
import en from '../src/i18n/jellyfin.en.json' with { type: 'json' }

const PIN = 'ab'.repeat(32)

test('jellyfin: a key with a space or a quote is refused, and nothing typed keeps the saved one', () => {
  assert.equal(tokenProblem({}), '')
  assert.equal(tokenProblem({ token: 'abc123' }), '')
  assert.equal(tokenProblem({ token: 'a b' }), 'tokenSpace')
  assert.equal(tokenProblem({ token: 'a"b' }), 'tokenSpace')
  assert.equal(jellyfinSecret({ token: ' abc ' }), 'abc')
  assert.equal(jellyfinSecret({ token: 'a b' }), '')
})

test('jellyfin: the server address is http or https with a host, and nothing else', () => {
  assert.equal(serverIssue('http://192.168.1.10:8096', ''), '')
  assert.equal(serverIssue('https://media.example/jellyfin', ''), '')
  assert.equal(serverIssue('', ''), '')
  assert.equal(serverIssue('media.example', ''), 'serverShape')
  assert.equal(serverIssue('ftp://media.example', ''), 'serverScheme')
  assert.equal(serverIssue('file:///etc/passwd', ''), 'serverScheme')
  assert.equal(serverIssue('http://u:p@media.example', ''), 'serverUser')
  assert.equal(serverIssue('http://media.example/?api_key=x', ''), 'serverQuery')
  assert.equal(serverIssue('http://media.example/#x', ''), 'serverQuery')
  assert.equal(serverIssue('http://media example', ''), 'serverShape')
  assert.equal(serverIssue('https://' + 'a'.repeat(200) + '.example', ''), 'serverLong')
})

test('jellyfin: a certificate fingerprint goes with https, in any common writing', () => {
  assert.equal(normalizePin('AB:' .repeat(31) + 'AB'), PIN)
  assert.equal(normalizePin(' ' + PIN.toUpperCase() + ' '), PIN)
  assert.equal(normalizePin('abcd'), '')
  assert.equal(serverIssue('https://m.example', PIN), '')
  assert.equal(serverIssue('http://m.example', PIN), 'pinHttp')
  assert.equal(serverIssue('https://m.example', 'abcd'), 'pinShape')
  assert.equal(serverIssue('', PIN), 'serverShape')
})

test('jellyfin: the entry the host lists is the address plus the pin, and reads back', () => {
  assert.equal(composeServerKey('http://m.example:8096/', ''), 'http://m.example:8096')
  assert.equal(composeServerKey('https://m.example/', PIN.toUpperCase()), `https://m.example#sha256=${PIN}`)
  assert.deepEqual(splitServerKey(`https://m.example#sha256=${PIN}`), { address: 'https://m.example', pin: PIN })
  assert.deepEqual(splitServerKey('http://m.example'), { address: 'http://m.example', pin: '' })
  assert.equal(isPlainHttp('http://m.example'), true)
  assert.equal(isPlainHttp('https://m.example'), false)
})

test('jellyfin: the name follows the address until the person types one', () => {
  assert.equal(defaultName('http://192.168.1.10:8096'), 'Jellyfin: 192.168.1.10:8096')
  assert.equal(defaultName('nope'), '')
  assert.equal(followName('', '', 'http://a.example'), 'Jellyfin: a.example')
  assert.equal(followName('Jellyfin: a.example', 'http://a.example', 'http://b.example'), 'Jellyfin: b.example')
  assert.equal(followName('Living room', 'http://a.example', 'http://b.example'), 'Living room')
})

test('jellyfin: one server rides in the request, the key only when typed, and a saved one reads back', () => {
  const form = { ...emptyConnector(), name: 'Home', agent_id: 'a', token: 'k', ...extraForm({ resources: [`https://m.example#sha256=${PIN}`], write_mode: 'propose' }) }
  assert.equal(form.server_text, 'https://m.example')
  assert.equal(form.pin_text, PIN)
  assert.equal(form.write_mode, 'propose')
  const body = connectorPayload(form, 'jellyfin', jellyfinSecret(form), { enabled: false, accept_any: false, ...extraBody(form) })
  assert.equal(body.kind, 'jellyfin')
  assert.equal(body.token, 'k')
  assert.deepEqual(body.resources, [`https://m.example#sha256=${PIN}`])
  assert.equal(body.write_mode, 'propose')
  const update = connectorPayload({ ...emptyConnector(), id: 'b1', name: 'Home', agent_id: 'a' }, 'jellyfin', '', extraBody({ server_text: '' }))
  assert.equal('token' in update, false)
  assert.deepEqual(update.resources, [])
  assert.equal(extraValid({ server_text: '' }), false)
  assert.equal(extraValid({ server_text: 'nope' }), false)
  assert.equal(extraValid({ server_text: 'http://m.example' }), true)
  assert.equal(canSave(form, false, true, null, true), true)
})

test('jellyfin: the write mode defaults to off and an unknown value is never a permission', () => {
  assert.deepEqual(WRITE_MODES, ['off', 'propose', 'auto'])
  assert.equal(writeModeOf(undefined), 'off')
  assert.equal(writeModeOf('always'), 'off')
  assert.equal(extraForm({}).write_mode, 'off')
  assert.equal(extraBody({ server_text: 'http://m.example' }).write_mode, 'off')
  for (const m of WRITE_MODES) {
    assert.ok(en.writeMode[m] && en.writeMode.note[m] && en.writeMode.row[m] && en.writeMode.line[m], m)
  }
  assert.match(en.writeMode.note.auto, /watching/)
})

test('jellyfin: the Test says accepted, key refused, certificate, refused or unfinished', () => {
  const t = (key, o = {}) => key.split('.').reduce((n, k) => n[k], en).replace(/\{\{(\w+)\}\}/g, (_, k) => o[k])
  assert.match(checkLine({ reachable: true, name: 'Home', version: '10.10', keyWorks: true }, t), /Home 10.10 answers.*accepted/)
  assert.match(checkLine({ reachable: true, name: 'Home', version: '10.10', keyWorks: false }, t), /refused the key/)
  assert.match(checkLine({ failed: 'certificate' }, t), /fingerprint/)
  assert.equal(checkItem({ reachable: true, keyWorks: true }).tone, 'ok')
  assert.equal(checkItem({ reachable: true, keyWorks: false }).tone, 'warn')
  assert.equal(checkItem({ failed: 'other' }).tone, 'warn')
  assert.equal(checkItem(null).tone, 'warn')
  assert.equal(checkFailure({ status: 401 }), 'refused')
  assert.equal(checkFailure(new Error('the certificate of the server is not trusted')), 'certificate')
  assert.equal(checkFailure(new Error('timeout')), 'other')
})

test('jellyfin: every word the page asks for exists', () => {
  for (const key of ['title', 'intro', 'new', 'token', 'tokenHelp', 'tokenSpace', 'server', 'serverHelp', 'serverShape', 'serverScheme', 'pin', 'pinHelp', 'pinShape', 'pinHttp', 'httpNote', 'writeMode', 'test', 'save', 'cancel', 'empty', 'noServer', 'serverOf', 'confirmDelete', 'setup', 'limits', 'approval', 'check', 'checkSeveral']) {
    assert.ok(en[key], key)
  }
  assert.match(en.limits, /cannot delete/)
})
