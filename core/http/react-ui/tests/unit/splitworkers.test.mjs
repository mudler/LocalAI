import test from 'node:test'
import assert from 'node:assert/strict'
import {
  arrivedSince, pathOf, slowAction, splitCommand, stateOf, summarize,
} from '../../src/components/swarm/splitWorkersModel.js'

const worker = (extra = {}) => ({ id: 'w', state: 'online', path: { kind: 'direct' }, allow_slow_path: false, ...extra })

test('split workers: a state has a tone, an unknown state reads as offline', () => {
  assert.deepEqual(stateOf(worker()), { key: 'online', tone: 'ok' })
  assert.deepEqual(stateOf(worker({ state: 'slow' })), { key: 'slow', tone: 'warn' })
  assert.equal(stateOf(worker({ state: 'limited' })).tone, 'error')
  assert.deepEqual(stateOf(worker({ state: 'new-thing' })), { key: 'offline', tone: 'idle' })
})

test('split workers: Use anyway only for a slow worker, Stop using only for one used over a relay', () => {
  assert.equal(slowAction(worker({ state: 'slow', path: { kind: 'relayed' } })), 'useAnyway')
  assert.equal(slowAction(worker({ allow_slow_path: true, path: { kind: 'relayed' } })), 'stopUsing')
  // A worker that found a direct path needs no choice, even with Use anyway on.
  assert.equal(slowAction(worker({ allow_slow_path: true })), null)
  // A relay with a data limit can never carry model layers: no choice helps.
  assert.equal(slowAction(worker({ state: 'limited', path: { kind: 'relayed', limited: true } })), null)
  assert.equal(slowAction(worker({ state: 'offline' })), null)
})

test('split workers: the path names the relay and the round trip', () => {
  assert.deepEqual(pathOf(worker({ path: { kind: 'direct', rtt_ms: 3 } })), { key: 'direct', rtt: 3 })
  assert.deepEqual(pathOf(worker({ path: { kind: 'relayed', relay_label: 'home', rtt_ms: 52 } })), { key: 'relayed', relay: 'home', rtt: 52 })
  assert.equal(pathOf(worker({ path: { kind: 'relayed' } })).key, 'relayedUnknown')
  assert.deepEqual(pathOf(worker({ path: { kind: 'none' } })), { key: 'none', rtt: null })
})

test('split workers: the summary counts the online ones and lists the slow ones', () => {
  const s = summarize([worker(), worker({ id: 'b', state: 'slow' }), worker({ id: 'c', state: 'offline' })])
  assert.equal(s.total, 3)
  assert.equal(s.online, 1)
  assert.deepEqual(s.slow.map(w => w.id), ['b'])
  assert.equal(summarize(null).total, 0)
})

test('split workers: the add flow sees only workers that paired after it opened', () => {
  const since = Date.parse('2026-11-18T10:00:00Z')
  const list = [
    worker({ id: 'old', created_at: '2026-11-18T09:00:00Z' }),
    worker({ id: 'new', created_at: '2026-11-18T10:01:00Z' }),
    worker({ id: 'newer', created_at: '2026-11-18T10:02:00Z' }),
  ]
  assert.deepEqual(arrivedSince(list, since).map(w => w.id), ['newer', 'new'])
})

test('split workers: the command joins with the link and keeps its state', () => {
  const link = 'localai://pair?v=1&k=split&p=12D3KooW&c=ABCDE23456&e=1'
  assert.equal(splitCommand({ engine: 'llama', link, flavor: 'cli' }), `local-ai worker llama-cpp-rpc \\\n  --join "${link}"`)
  const docker = splitCommand({ engine: 'llama', link, flavor: 'docker', option: { dockerFlags: '--gpus all', tag: 'latest-gpu', devTag: 'master-gpu' }, dev: false })
  assert.match(docker, /--gpus all/)
  assert.match(docker, /-v localai-llama-cpp-rpc:\/p2p/)
  assert.match(docker, /LOCALAI_P2P_STATE_DIR=\/p2p/)
  assert.match(docker, /localai\/localai:latest-gpu worker llama-cpp-rpc/)
  // MLX runs on Apple Silicon outside a container.
  assert.equal(splitCommand({ engine: 'mlx', link, flavor: 'docker' }), `local-ai worker mlx-distributed \\\n  --join "${link}"`)
  assert.match(splitCommand({ engine: 'llama', flavor: 'cli' }), /<invite link>/)
})
