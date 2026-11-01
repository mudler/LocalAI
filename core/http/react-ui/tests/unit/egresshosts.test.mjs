import test from 'node:test'
import assert from 'node:assert/strict'
import { checkHost, hostEntries, hostsValue, MAX_ALLOWED_HOSTS } from '../../src/utils/egressHosts.js'
import { displayValue, runChecks, payloadFor } from '../../src/utils/settingsPending.js'
import { FIELD_BY_KEY } from '../../src/utils/settingsSchema.js'

const FIELD = FIELD_BY_KEY.chats_egress_allowed_hosts

test('allowed hosts: an entry is written lower case, in punycode, with the port it pins', () => {
  assert.deepEqual(checkHost('API.Example.com.'), { entry: 'api.example.com' })
  assert.deepEqual(checkHost('git.example.org:8443'), { entry: 'git.example.org:8443' })
  assert.deepEqual(checkHost('bücher.example'), { entry: 'xn--bcher-kva.example' })
  assert.deepEqual(checkHost('93.184.216.34'), { entry: '93.184.216.34' })
})

test('allowed hosts: what is not an exact host name is refused with a reason', () => {
  const reason = (v, o) => checkHost(v, o).error
  assert.equal(reason('*.example.com'), 'wildcard')
  assert.equal(reason('https://example.com'), 'shape')
  assert.equal(reason('example.com/path'), 'shape')
  assert.equal(reason('user@example.com'), 'shape')
  assert.equal(reason('bad host'), 'shape')
  assert.equal(reason('example.com:ssh'), 'port')
  assert.equal(reason('example.com:22'), 'port')
  assert.equal(reason('example.com:70000'), 'port')
  assert.equal(reason('0x7f.1'), 'numeric')
  assert.equal(reason('127.1'), 'numeric')
  assert.equal(reason(''), 'empty')
})

test('allowed hosts: a private literal needs the private switch', () => {
  for (const v of ['10.0.0.5', '127.0.0.1', '169.254.169.254', '192.168.1.9:8080', 'localhost', 'x.localhost', '[::1]']) {
    assert.equal(checkHost(v).error, 'private', v)
  }
  assert.deepEqual(checkHost('10.0.0.5', { allowPrivate: true }), { entry: '10.0.0.5' })
  assert.deepEqual(checkHost('[fd00::1]:8080', { allowPrivate: true }), { entry: '[fd00::1]:8080' })
})

test('allowed hosts: no duplicates and at most 64', () => {
  assert.equal(checkHost('a.example', { existing: ['a.example'] }).error, 'duplicate')
  assert.equal(checkHost('a.example:80', { existing: ['a.example'] }).entry, 'a.example:80')
  const full = Array.from({ length: MAX_ALLOWED_HOSTS }, (_, i) => `h${i}.example`)
  assert.equal(checkHost('more.example', { existing: full }).error, 'full')
})

test('allowed hosts: the setting is a comma list the page reads and writes', () => {
  assert.deepEqual(hostEntries(' a.example, ,b.example:8443 '), ['a.example', 'b.example:8443'])
  assert.equal(hostsValue(['a.example', 'b.example:8443']), 'a.example, b.example:8443')
  assert.equal(displayValue(FIELD, ''), 'empty')
  assert.equal(displayValue(FIELD, 'a.example'), '1 host')
  assert.equal(displayValue(FIELD, 'a.example, b.example'), '2 hosts')
  assert.deepEqual(payloadFor([{ field: FIELD }], { chats_egress_allowed_hosts: 'a.example, b.example' }), { chats_egress_allowed_hosts: 'a.example, b.example' })
})

test('allowed hosts: Apply stops on an entry the server would refuse', () => {
  const change = (to) => [{ field: FIELD, from: '', to }]
  const errors = (to, draft = {}) => runChecks(change(to), {}, draft).filter(c => c.level === 'error')
  assert.equal(errors('a.example, b.example:8443').length, 0)
  assert.equal(errors('*.example.com').length, 1)
  assert.equal(errors('10.0.0.5').length, 1)
  assert.equal(errors('10.0.0.5', { chats_egress_allow_private: true }).length, 0)
})
