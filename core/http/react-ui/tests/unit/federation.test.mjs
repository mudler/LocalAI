import test from 'node:test'
import assert from 'node:assert/strict'
import { candidateGroups, instancePath, memberTone, POLICIES } from '../../src/utils/federation.js'

test('federation: the path of an instance', () => {
  assert.deepEqual(instancePath({ local: true }), { key: 'local', tone: 'ok', limited: false })
  assert.equal(instancePath({ transport: '', status: 'ok' }).key, 'http')
  assert.equal(instancePath({ transport: '', status: 'unreachable' }).tone, 'error')
  assert.equal(instancePath({ transport: 'p2p', p2p: { state: 'off' } }).key, 'off')
  assert.equal(instancePath({ transport: 'p2p', p2p: { state: 'error' } }).tone, 'error')
  assert.equal(instancePath({ transport: 'p2p', p2p: { state: 'listening' } }).key, 'waiting')
  assert.equal(instancePath({ transport: 'p2p', p2p: { state: 'up', path: 'hole_punched' } }).key, 'holePunched')
  assert.deepEqual(instancePath({ transport: 'p2p', p2p: { state: 'up', path: 'relayed', limited: true } }), { key: 'relayed', tone: 'warn', limited: true })
})

test('federation: a drained member is idle whatever its health', () => {
  assert.equal(memberTone({ state: 'healthy' }), 'ok')
  assert.equal(memberTone({ state: 'down' }), 'error')
  assert.equal(memberTone({ state: 'recovering' }), 'warn')
  assert.equal(memberTone({ state: 'healthy', drained: true }), 'idle')
  assert.equal(memberTone({}), 'idle')
})

test('federation: candidates gather the same model of several instances first', () => {
  const out = candidateGroups([
    { id: 'local', local: true, models: [{ model: 'qwen', upstream_id: 'qwen' }, { model: 'solo', upstream_id: 'solo' }] },
    { id: 'r1', name: 'Branch', models: [{ model: 'branch-qwen', upstream_id: 'qwen' }] },
  ])
  assert.deepEqual(out.map(c => c.upstream), ['qwen', 'solo'])
  assert.deepEqual(out[0].options.map(o => [o.model, o.local, o.instanceName]), [['qwen', true, ''], ['branch-qwen', false, 'Branch']])
  assert.equal(out[0].options[1].suggested, 'qwen')
  assert.deepEqual(candidateGroups(undefined), [])
  assert.deepEqual(POLICIES, ['least_busy', 'round_robin'])
})
