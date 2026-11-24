import test from 'node:test'
import assert from 'node:assert/strict'
import { addressProblem, checkFailure, checkItem, defaultName, extraBody, extraForm, extraValid, followName, haSecret, parseAddress, tokenProblem, writeModeOf, WRITE_MODES } from '../src/ha.js'
import { canSave, connectorPayload } from '../../../internal/connector-ui/src/connector.js'
import en from '../src/i18n/home-assistant.en.json' with { type: 'json' }

const LONG = 'eyJhbGciOiJIUzI1NiJ9.abcdefghijklmnop'

test('home-assistant: the token has no spaces and is long enough, and nothing typed keeps the saved one', () => {
  assert.equal(tokenProblem({}), '')
  assert.equal(tokenProblem({ token: LONG }), '')
  assert.equal(tokenProblem({ token: 'abc def' + 'x'.repeat(30) }), 'tokenSpace')
  assert.equal(tokenProblem({ token: 'short' }), 'tokenShort')
  assert.equal(haSecret({ token: ` ${LONG} ` }), LONG)
  assert.equal(haSecret({ token: 'short' }), '')
})

test('home-assistant: an address is https, or http on a local network only', () => {
  assert.equal(parseAddress('https://Home.Example.org:8123/').url, 'https://home.example.org:8123')
  assert.equal(parseAddress('https://ha.example.org/').url, 'https://ha.example.org')
  assert.equal(parseAddress('https://ha.example.org/ha/').url, 'https://ha.example.org/ha')
  assert.equal(parseAddress('http://192.168.1.20:8123').url, 'http://192.168.1.20:8123')
  assert.equal(parseAddress('http://homeassistant.local:8123').url, 'http://homeassistant.local:8123')
  assert.equal(parseAddress('http://homeassistant:8123').url, 'http://homeassistant:8123')
  assert.equal(parseAddress('').problem, '')
  assert.equal(addressProblem('http://ha.example.org:8123'), 'addressHttp')
  assert.equal(addressProblem('http://8.8.8.8'), 'addressHttp')
  assert.equal(addressProblem('http://172.32.0.1'), 'addressHttp', '172.32 is not private')
  assert.equal(addressProblem('ftp://x'), 'addressShape')
  assert.equal(addressProblem('ha.example.org:8123'), 'addressShape')
  assert.equal(addressProblem('https://user:pass@ha.example.org'), 'addressShape')
  assert.equal(addressProblem('https://ha.example.org/?x=1'), 'addressShape')
  assert.equal(addressProblem('https://ha.example.org/#x'), 'addressShape')
})

test('home-assistant: the name follows the address until the person types one', () => {
  assert.equal(defaultName('https://ha.example.org:8123'), 'Home Assistant: ha.example.org:8123')
  assert.equal(defaultName('nope'), '')
  assert.equal(followName('', '', 'https://a.example'), 'Home Assistant: a.example')
  assert.equal(followName('Home Assistant: a.example', 'https://a.example', 'https://b.example'), 'Home Assistant: b.example')
  assert.equal(followName('Cabin', 'https://a.example', 'https://b.example'), 'Cabin', 'a typed name stays')
})

test('home-assistant: the connection body carries one normalized address and the write mode', () => {
  assert.deepEqual(extraBody({ address_text: ' https://HA.example.org:8123/ ', write_mode: 'propose' }), { resources: ['https://ha.example.org:8123'], write_mode: 'propose' })
  assert.deepEqual(extraBody({ address_text: '', write_mode: 'weird' }), { resources: [], write_mode: 'off' })
  assert.deepEqual(extraForm({ resources: ['https://a.example', 'https://b.example'], write_mode: 'auto' }), { address_text: 'https://a.example', write_mode: 'auto' })
  assert.deepEqual(extraForm({}), { address_text: '', write_mode: 'off' })
  assert.equal(extraValid({ address_text: '' }), false, 'a connection without an address reaches nothing')
  assert.equal(extraValid({ address_text: 'https://a.example' }), true)
  assert.equal(extraValid({ address_text: 'http://a.example' }), false)
})

test('home-assistant: the write mode is off unless it is one of the three', () => {
  assert.deepEqual(WRITE_MODES, ['off', 'propose', 'auto'])
  assert.equal(writeModeOf(undefined), 'off')
  assert.equal(writeModeOf('auto'), 'auto')
  assert.equal(writeModeOf('everything'), 'off')
})

test('home-assistant: the connection saves as credentials only, with the token, the address and the mode', () => {
  const form = { name: 'Home', agent_id: '', token: LONG, address_text: 'https://a.example', write_mode: 'propose' }
  assert.equal(canSave(form, false, true, null, true), true)
  const body = connectorPayload(form, 'home-assistant', haSecret(form), { enabled: false, accept_any: false, ...extraBody(form) })
  assert.equal(body.kind, 'home-assistant')
  assert.equal(body.token, LONG)
  assert.deepEqual(body.resources, ['https://a.example'])
  assert.equal(body.write_mode, 'propose')
})

test('home-assistant: the Test line says what happened without the details of the failure', () => {
  assert.deepEqual(checkItem({ ok: true, version: '2026.11.0', name: 'Home', integrations: 12 }), { tone: 'ok', key: 'check.ok', vars: { version: '2026.11.0', name: 'Home', count: 12 } })
  assert.equal(checkItem({ failed: 'refused' }).key, 'check.refused')
  assert.equal(checkItem({ failed: 'certificate' }).key, 'check.certificate')
  assert.equal(checkItem({ failed: 'other' }).key, 'check.failed')
  assert.equal(checkItem(null).tone, 'warn')
  assert.equal(checkFailure({ status: 401 }), 'refused')
  assert.equal(checkFailure({ message: 'home assistant refused the token' }), 'refused')
  assert.equal(checkFailure({ message: 'the certificate of home assistant is not trusted' }), 'certificate')
  assert.equal(checkFailure({ message: 'timeout' }), 'other')
})

test('home-assistant: every word the page asks for exists', () => {
  const need = ['title', 'intro', 'new', 'edit', 'name', 'token', 'tokenHelp', 'tokenSaved', 'tokenSpace', 'tokenShort', 'address', 'addressHelp', 'addressShape', 'addressHttp',
    'agent', 'test', 'testOk', 'checkFailed', 'save', 'cancel', 'editButton', 'delete', 'empty', 'inactive', 'confirmDelete.title', 'confirmDelete.message', 'confirmDelete.confirm',
    'writeMode.label', 'writeMode.off', 'writeMode.propose', 'writeMode.auto', 'writeMode.line.auto', 'writeMode.note.auto', 'writeMode.row.propose',
    'check.ok', 'check.refused', 'check.certificate', 'check.failed', 'setup.title', 'setup.tls', 'policy.title', 'policy.physical', 'policy.hardIntro',
    'approval.title', 'approval.physical', 'approval.quiet']
  for (const key of need) {
    const v = key.split('.').reduce((o, k) => o?.[k], en)
    assert.equal(typeof v, 'string', `missing words: ${key}`)
  }
  assert.ok(!/—/.test(JSON.stringify(en)), 'no em dash in the words')
})
