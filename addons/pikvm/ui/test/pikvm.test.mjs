import test from 'node:test'
import assert from 'node:assert/strict'
import { addressProblem, checkFailure, checkItem, credentialProblem, defaultName, deviceKey, extraBody, extraForm, extraValid, followName, needsPinHint, parseAddress, pikvmSecret, writeModeOf, WRITE_MODES } from '../src/pikvm.js'
import { canSave, connectorPayload } from '../../../internal/connector-ui/src/connector.js'
import en from '../src/i18n/pikvm.en.json' with { type: 'json' }

const PIN = 'ab'.repeat(32)

test('pikvm: the user and the password are saved as one user:password, split at the first colon', () => {
  assert.equal(credentialProblem({}), '')
  assert.equal(credentialProblem({ user: 'kvm', passwd: 'a:b:c' }), '')
  assert.equal(pikvmSecret({ user: 'kvm', passwd: 'a:b:c' }), 'kvm:a:b:c')
  assert.equal(pikvmSecret({}), '')
  assert.equal(credentialProblem({ user: 'kvm' }), 'passMissing')
  assert.equal(credentialProblem({ passwd: 'x' }), 'userMissing')
  assert.equal(credentialProblem({ user: 'k:vm', passwd: 'x' }), 'userColon')
  assert.equal(credentialProblem({ user: 'kvm', passwd: 'a\nb' }), 'credControl')
  assert.equal(credentialProblem({ user: 'kvm', passwd: ' x' }), 'credSpace')
  assert.equal(pikvmSecret({ user: 'k:vm', passwd: 'x' }), '', 'a problem makes no secret')
})

test('pikvm: an address is https, or http on a local network only, with an optional pin', () => {
  assert.equal(parseAddress('https://PiKVM.local/').url, 'https://pikvm.local')
  assert.equal(parseAddress('https://pikvm.local:8443/kvm/').url, 'https://pikvm.local:8443/kvm')
  assert.equal(parseAddress('http://192.168.1.50').url, 'http://192.168.1.50')
  assert.equal(parseAddress('').problem, '')
  assert.equal(addressProblem('http://kvm.example.org'), 'addressHttp')
  assert.equal(addressProblem('ftp://x'), 'addressShape')
  assert.equal(addressProblem('pikvm.local'), 'addressShape')
  assert.equal(addressProblem('https://u:p@pikvm.local'), 'addressShape')
  assert.equal(addressProblem('https://pikvm.local/?x=1'), 'addressShape')
})

test('pikvm: the certificate pin is 64 hex digits and goes with https', () => {
  const ok = parseAddress(`https://PiKVM.local#sha256=${PIN.toUpperCase()}`)
  assert.equal(ok.url, `https://pikvm.local#sha256=${PIN}`)
  assert.equal(ok.pin, PIN)
  assert.equal(parseAddress(`https://pikvm.local#sha256=${PIN.match(/../g).join(':')}`).pin, PIN)
  assert.equal(addressProblem('https://pikvm.local#sha256=abcd'), 'pinShape')
  assert.equal(addressProblem('https://pikvm.local#other'), 'pinShape')
  assert.equal(addressProblem(`http://pikvm.local#sha256=${PIN}`), 'pinHttp')
  assert.equal(deviceKey(`https://pikvm.local#sha256=${PIN}`), 'https://pikvm.local')
  assert.equal(needsPinHint('https://pikvm.local'), true)
  assert.equal(needsPinHint(`https://pikvm.local#sha256=${PIN}`), false)
  assert.equal(needsPinHint('http://pikvm.local'), false)
})

test('pikvm: the name follows the address until the person types one', () => {
  assert.equal(defaultName('https://pikvm.local'), 'PiKVM: pikvm.local')
  assert.equal(defaultName('nope'), '')
  assert.equal(followName('', '', 'https://a.example'), 'PiKVM: a.example')
  assert.equal(followName('PiKVM: a.example', 'https://a.example', 'https://b.example'), 'PiKVM: b.example')
  assert.equal(followName('Lab', 'https://a.example', 'https://b.example'), 'Lab')
})

test('pikvm: the connection body carries one normalized address, with its pin, and the write mode', () => {
  assert.deepEqual(extraBody({ address_text: ` https://KVM.example/#sha256=${PIN} `, write_mode: 'propose' }), { resources: [`https://kvm.example#sha256=${PIN}`], write_mode: 'propose' })
  assert.deepEqual(extraBody({ address_text: '', write_mode: 'weird' }), { resources: [], write_mode: 'off' })
  assert.deepEqual(extraForm({ resources: ['https://a.example'], write_mode: 'auto' }), { address_text: 'https://a.example', write_mode: 'auto' }, 'a saved auto reads back as Allow all')
  assert.equal(extraBody({ address_text: 'https://a.example', write_mode: 'auto' }).write_mode, 'auto')
  assert.equal(extraValid({ address_text: '' }), false)
  assert.equal(extraValid({ address_text: 'https://a.example' }), true)
  assert.equal(extraValid({ address_text: 'http://a.example' }), false)
})

test('pikvm: the write mode is off unless it is one of the three, and auto is offered', () => {
  assert.deepEqual(WRITE_MODES, ['off', 'propose', 'auto'])
  assert.equal(writeModeOf(undefined), 'off')
  assert.equal(writeModeOf('propose'), 'propose')
  assert.equal(writeModeOf('auto'), 'auto')
  assert.equal(writeModeOf('weird'), 'off')
})

test('pikvm: the connection saves as credentials only, with the sealed pair and the address', () => {
  const form = { name: 'KVM', agent_id: '', user: 'kvm', passwd: 'p:w', address_text: 'https://a.example', write_mode: 'propose' }
  assert.equal(canSave(form, false, true, null, true), true)
  const body = connectorPayload(form, 'pikvm', pikvmSecret(form), { enabled: false, accept_any: false, ...extraBody(form) })
  assert.equal(body.kind, 'pikvm')
  assert.equal(body.token, 'kvm:p:w')
  assert.deepEqual(body.resources, ['https://a.example'])
})

test('pikvm: the Test line says what happened without the details of the failure', () => {
  assert.deepEqual(checkItem({ ok: true, model: 'v3', version: '3.300' }), { tone: 'ok', key: 'check.ok', vars: { model: 'v3', version: '3.300' } })
  assert.equal(checkItem({ failed: 'refused' }).key, 'check.refused')
  assert.equal(checkItem({ failed: 'certificate' }).key, 'check.certificate')
  assert.equal(checkItem(null).tone, 'warn')
  assert.equal(checkFailure({ status: 401 }), 'refused')
  assert.equal(checkFailure({ message: 'the device refused the user name or password' }), 'refused')
  assert.equal(checkFailure({ message: 'the certificate of the device is not the pinned one' }), 'certificate')
  assert.equal(checkFailure({ message: 'timeout' }), 'other')
})

test('pikvm: every word the page asks for exists, and says what is not supported', () => {
  const need = ['title', 'intro', 'new', 'edit', 'name', 'user', 'passwd', 'passwdHelp', 'credSaved', 'userMissing', 'passMissing', 'userColon', 'address', 'addressHelp', 'addressShape', 'addressHttp',
    'pinShape', 'pinHttp', 'pinHint', 'agent', 'test', 'save', 'cancel', 'editButton', 'delete', 'empty', 'inactive', 'limits', 'confirmDelete.title', 'confirmDelete.message', 'confirmDelete.confirm',
    'writeMode.label', 'writeMode.off', 'writeMode.propose', 'writeMode.auto', 'writeMode.line.propose', 'writeMode.line.auto', 'writeMode.note.propose', 'writeMode.note.auto', 'writeMode.row.propose', 'writeMode.row.auto',
    'check.ok', 'check.refused', 'check.certificate', 'check.failed', 'setup.title', 'setup.tls', 'setup.twofa',
    'settings.title', 'settings.physical', 'settings.typing', 'settings.hard', 'settings.never', 'settings.maxCharsShape',
    'approval.titlePower', 'approval.titleText', 'approval.titleKeys', 'approval.physical', 'approval.hard', 'approval.power.on']
  for (const key of need) {
    const v = key.split('.').reduce((o, k) => o?.[k], en)
    assert.equal(typeof v, 'string', `missing words: ${key}`)
  }
  assert.ok(/two-factor/.test(en.passwdHelp), 'the page says 2FA is not supported')
  assert.ok(!/—/.test(JSON.stringify(en)), 'no em dash in the words')
})

test('pikvm: Allow all has words in every language the add-on ships', async () => {
  const { readFileSync } = await import('node:fs')
  for (const loc of ['en','it','es','de','fr','zh-CN','id','ko','pt-BR']) {
    const w = JSON.parse(readFileSync(new URL(`../src/i18n/pikvm.${loc}.json`, import.meta.url), 'utf8')).writeMode
    for (const path of [['auto'], ['line', 'auto'], ['note', 'auto'], ['row', 'auto']]) {
      const v = path.reduce((o, k) => o?.[k], w)
      assert.equal(typeof v, 'string', `${loc}: writeMode.${path.join('.')}`)
      assert.ok(v.length > 0)
    }
    assert.notEqual(w.row.auto, w.row.propose, `${loc}: Allow all must not read as Asks you first`)
  }
})

test('pikvm: the Allow all note says typing and key presses run at once and the device switches still hold', () => {
  assert.match(en.writeMode.note.auto, /types text on the machine at once/)
  assert.doesNotMatch(en.writeMode.note.auto, /still waits/)
  assert.match(en.writeMode.note.auto, /still need the switch/)
  assert.match(en.writeMode.note.auto, /none of it can be taken back/)
})
