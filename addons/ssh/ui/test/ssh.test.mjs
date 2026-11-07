import test from 'node:test'
import assert from 'node:assert/strict'
import { addressOf, checkFailure, checkItem, credentialProblem, defaultName, extraBody, extraForm, extraValid, fingerprintOfPin, followName, hostProblem, needsSecretAgain, normalizePin, parseHost, splitAddress, sshSecret, targetKey, userProblem, writeModeOf, WRITE_MODES } from '../src/ssh.js'
import { canSave, connectorPayload } from '../../../internal/connector-ui/src/connector.js'
import en from '../src/i18n/ssh.en.json' with { type: 'json' }

const PIN = 'ab'.repeat(32)

test('ssh: a password is saved as user:password and a key as its own text', () => {
  assert.equal(sshSecret({ method: 'password', user: 'deploy', passwd: 'a:b:c' }), 'deploy:a:b:c')
  assert.equal(sshSecret({ method: 'key', key: '\n-----BEGIN OPENSSH PRIVATE KEY-----\nx\n-----END OPENSSH PRIVATE KEY-----\n' }), '-----BEGIN OPENSSH PRIVATE KEY-----\nx\n-----END OPENSSH PRIVATE KEY-----')
  assert.equal(sshSecret({}), '')
  assert.equal(credentialProblem({}), '')
  assert.equal(credentialProblem({ passwd: 'x' }), 'userMissing')
  assert.equal(credentialProblem({ user: 'u', passwd: 'a\nb' }), 'credControl')
  assert.equal(credentialProblem({ method: 'key', key: 'not a key' }), 'keyShape')
  assert.equal(credentialProblem({ method: 'key', key: '-----BEGIN ENCRYPTED PRIVATE KEY-----\nx' }), 'keyPassphrase')
  assert.equal(sshSecret({ method: 'key', key: 'nope' }), '', 'a problem makes no secret')
})

test('ssh: a host is a name or an address with an optional port', () => {
  assert.deepEqual(parseHost('Server.Example.COM'), { host: 'server.example.com', port: '', problem: '' })
  assert.deepEqual(parseHost('10.0.0.5:2222'), { host: '10.0.0.5', port: '2222', problem: '' })
  assert.deepEqual(parseHost('[::1]:22'), { host: '[::1]', port: '22', problem: '' })
  assert.equal(parseHost('').problem, '')
  for (const bad of ['a b', 'h:ssh', 'h:70000', 'h:022', 'a:b:c', '[::1', 'h/x']) assert.equal(hostProblem(bad), 'hostShape', bad)
  assert.equal(userProblem('deploy'), '')
  assert.equal(userProblem('a b'), 'userShape')
})

test('ssh: the address holds the user, the host and the pinned key', () => {
  assert.equal(addressOf('deploy', 'Example.com:2222', PIN.toUpperCase()), `deploy@example.com:2222#sha256=${PIN}`)
  assert.equal(addressOf('deploy', 'example.com', ''), 'deploy@example.com')
  assert.equal(addressOf('', 'example.com', ''), '')
  assert.deepEqual(splitAddress(`deploy@example.com:22#sha256=${PIN}`), { user: 'deploy', host: 'example.com:22', pin: PIN })
  assert.equal(targetKey(`deploy@example.com:22#sha256=${PIN}`), 'deploy@example.com:22')
  assert.equal(normalizePin(PIN.match(/../g).join(':')), PIN)
  assert.equal(normalizePin('abcd'), '')
})

test('ssh: a pin is shown as ssh-keygen shows a fingerprint', () => {
  // The digest of 32 zero bytes in unpadded base64.
  assert.equal(fingerprintOfPin('00'.repeat(32)), 'SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA')
  assert.equal(fingerprintOfPin('zz'), '')
})

test('ssh: the name follows the address until the person types one', () => {
  assert.equal(defaultName('deploy', 'example.com'), 'SSH: deploy@example.com')
  assert.equal(followName('', 'deploy', 'a.example', 'deploy', 'b.example'), 'SSH: deploy@b.example')
  assert.equal(followName('SSH: deploy@a.example', 'deploy', 'a.example', 'deploy', 'b.example'), 'SSH: deploy@b.example')
  assert.equal(followName('My box', 'deploy', 'a.example', 'deploy', 'b.example'), 'My box')
})

test('ssh: a new connection starts on "asks you first" and never on "auto"', () => {
  assert.equal(extraForm({}).write_mode, 'propose')
  assert.equal(extraForm({ id: 'x', write_mode: 'auto' }).write_mode, 'auto')
  assert.equal(extraForm({ id: 'x' }).write_mode, 'off')
  assert.deepEqual(WRITE_MODES, ['off', 'propose', 'auto'])
  assert.equal(writeModeOf('anything'), 'off')
})

test('ssh: saving needs a host, a user and a host key the person trusted', () => {
  const ok = { user: 'deploy', host_text: 'example.com', pin_text: PIN, pin_ok: true }
  assert.equal(extraValid(ok), true)
  assert.equal(extraValid({ ...ok, pin_ok: false }), false)
  assert.equal(extraValid({ ...ok, pin_text: '' }), false)
  assert.equal(extraValid({ ...ok, user: '' }), false)
  assert.equal(extraValid({ ...ok, host_text: 'a b' }), false)
  assert.deepEqual(extraBody({ ...ok, write_mode: 'propose' }), { resources: [`deploy@example.com#sha256=${PIN}`], write_mode: 'propose' })
  assert.deepEqual(extraBody({ user: 'u', host_text: 'bad host', write_mode: 'zzz' }), { resources: [], write_mode: 'off' })
})

test('ssh: a saved connection that moves to another user or host needs its secret again', () => {
  const saved = extraForm({ id: 'c1', resources: [`deploy@a.example:22#sha256=${PIN}`], write_mode: 'propose' })
  assert.equal(needsSecretAgain({ id: 'c1', ...saved }), false)
  assert.equal(needsSecretAgain({ id: 'c1', ...saved, user: 'other' }), true)
  assert.equal(needsSecretAgain({ id: 'c1', ...saved, user: 'other', method: 'password', passwd: 'x' }), false)
  assert.equal(extraValid({ id: 'c1', ...saved, host_text: 'b.example' }), false, 'the pin is reset by the page; without it the form is not valid')
})

test('ssh: the host check shows its own words for each failure', () => {
  assert.equal(checkFailure(new Error('the host key changed: the pinned key is x')), 'changed')
  assert.equal(checkFailure(new Error('no host key is pinned for this connection')), 'nopin')
  assert.equal(checkFailure(new Error('the host refused the login (wrong user)')), 'refused')
  assert.equal(checkFailure(new Error('the host could not be reached')), 'unreachable')
  assert.equal(checkFailure(new Error('boom')), 'other')
  for (const f of ['changed', 'nopin', 'refused', 'unreachable', 'busy']) assert.ok(en.check[checkItem({ failed: f }).key.split('.')[1]], f)
  assert.deepEqual(checkItem({ ok: true, user: 'deploy', uname: 'Linux box 6.1 x' }), { tone: 'ok', key: 'check.ok', vars: { user: 'deploy', system: 'Linux box' } })
})

test('ssh: the form payload carries the sealed secret, the address and the write mode', () => {
  const form = { name: 'SSH: deploy@example.com', ...extraForm({}), user: 'deploy', host_text: 'example.com', pin_text: PIN, pin_ok: true, passwd: 'pw' }
  const secret = sshSecret(form)
  const body = connectorPayload(form, 'ssh', secret, { enabled: false, accept_any: false, ...extraBody(form) })
  assert.equal(body.token, 'deploy:pw')
  assert.deepEqual(body.resources, [`deploy@example.com#sha256=${PIN}`])
  assert.equal(body.write_mode, 'propose')
  assert.equal(typeof canSave, 'function')
})
