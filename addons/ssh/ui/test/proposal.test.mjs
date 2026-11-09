import test from 'node:test'
import assert from 'node:assert/strict'
import { connectionsFor, proposalView } from '../src/proposal.js'

test('ssh proposal: the host, the user and the exact command are shown from the preview', () => {
  const v = proposalView('run', { host: 'deploy@a.example:22#sha256=ab', command: 'ls -l\nwhoami' },
    { host: 'deploy@a.example:22', user: 'deploy', command: 'ls -l\nwhoami', timeoutSeconds: 30, hostKey: 'SHA256:xyz', login: 'key' })
  assert.equal(v.host, 'deploy@a.example:22#sha256=ab')
  assert.equal(v.user, 'deploy')
  assert.equal(v.command, 'ls -l\nwhoami')
  assert.equal(v.lines, 2)
  assert.equal(v.timeoutSeconds, 30)
  assert.equal(v.hostKey, 'SHA256:xyz')
  assert.equal(v.login, 'key')
})

test('ssh proposal: with no preview the stored arguments still show the command', () => {
  const v = proposalView('run', { host: 'h', command: 'id', timeoutSeconds: 5 }, null)
  assert.equal(v.command, 'id')
  assert.equal(v.timeoutSeconds, 5)
  assert.equal(v.login, '')
  assert.equal(proposalView('run', null, undefined).lines, 0)
})

test('ssh proposal: connections are matched by address without the pin, in the order of the list', () => {
  const list = [
    { kind: 'ssh', name: 'Work', resources: ['deploy@a.example:22#sha256=ab'] },
    { kind: 'ssh', name: 'Other', resources: ['deploy@b.example:22'] },
    { kind: 'pikvm', name: 'Not ssh', resources: ['deploy@a.example:22'] },
  ]
  assert.deepEqual(connectionsFor(list, 'Deploy@A.example:22#sha256=ab'), ['Work'])
  assert.deepEqual(connectionsFor(list, 'deploy@a.example:22#sha256=zz'), ['Work'])
  assert.deepEqual(connectionsFor(list, ''), [])
  assert.deepEqual(connectionsFor(null, 'x'), [])
})
