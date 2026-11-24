import test from 'node:test'
import assert from 'node:assert/strict'
import {
  availability, chosenWeights, clampUses, commandOf, countdown, defaultWays, maskCommand, maskToken, mintBody, needsInvite, needsToken, secondsUntil, tokenRoutes, usesLeft,
} from '../../src/utils/addWorker.js'

const nats = { bus: { nats_configured: true, nats_connected: true }, carrier_weights: { nats: 100, websocket: 50, p2p: 25 } }
const noNats = { bus: { nats_configured: false }, carrier_weights: {} }

test('add worker: NATS is offered only when it is set up, P2P only when sharing is on', () => {
  assert.deepEqual(availability('nats', nats, null), { available: true, state: 'ready', needs: null })
  assert.deepEqual(availability('nats', noNats, null), { available: false, state: 'off', needs: 'cluster' })
  assert.equal(availability('nats', { bus: { nats_configured: true, nats_connected: false } }, null).state, 'unreachable')
  assert.equal(availability('nats', { bus: { nats_configured: true, nats_connected: false } }, null).available, true)
  assert.deepEqual(availability('websocket', noNats, null), { available: true, state: 'ready', needs: null })
  assert.deepEqual(availability('p2p', nats, 'share'), { available: true, state: 'ready', needs: null })
  assert.deepEqual(availability('p2p', nats, 'off'), { available: false, state: 'off', needs: 'sharing' })
  assert.equal(availability('p2p', nats, null).state, 'checking')
})

test('add worker: the step opens with every way that needs no set-up, never P2P', () => {
  assert.deepEqual(defaultWays(nats, 'share'), ['nats', 'websocket'])
  assert.deepEqual(defaultWays(noNats, 'share'), ['websocket'])
})

test('add worker: a token carries NATS and WebSocket, an invite carries P2P', () => {
  assert.deepEqual(tokenRoutes(['p2p', 'websocket', 'nats']), ['nats', 'websocket'])
  assert.deepEqual(tokenRoutes(['p2p']), [])
  assert.equal(needsToken(['p2p']), false)
  assert.equal(needsToken(['nats', 'p2p']), true)
  assert.equal(needsInvite(['nats', 'p2p']), true)
  assert.equal(needsInvite(['nats']), false)
})

test('add worker: the request that mints a token names the routes, the uses and the lifetime', () => {
  assert.deepEqual(mintBody({ ways: ['websocket', 'nats', 'p2p'], uses: 1, ttl: 600 }), { ttl_seconds: 600, uses: 1, routes: ['nats', 'websocket'] })
  assert.deepEqual(mintBody({ ways: ['websocket'], uses: 5, ttl: 86400 }), { ttl_seconds: 86400, uses: 5, routes: ['websocket'] })
})

test('add worker: the weights of the chosen ways, in the order of the cards', () => {
  assert.deepEqual(chosenWeights(['websocket', 'nats'], nats), [{ way: 'nats', weight: 100 }, { way: 'websocket', weight: 50 }])
  assert.deepEqual(chosenWeights(['p2p'], noNats), [{ way: 'p2p', weight: 25 }])
})

test('add worker: uses are clamped from 2 to 100', () => {
  assert.equal(clampUses('7'), 7)
  assert.equal(clampUses('1'), 2)
  assert.equal(clampUses('500'), 100)
  assert.equal(clampUses('abc'), 5)
})

test('add worker: the countdown reads m:ss, and h:mm:ss from an hour', () => {
  assert.equal(countdown(581), '9:41')
  assert.equal(countdown(5), '0:05')
  assert.equal(countdown(3725), '1:02:05')
  assert.equal(countdown(-3), '0:00')
  const now = Date.parse('2026-11-21T10:00:00Z')
  assert.equal(secondsUntil('2026-11-21T10:10:00Z', now), 600)
  assert.equal(secondsUntil('2026-11-21T09:00:00Z', now), 0)
  assert.equal(secondsUntil('nonsense', now), 0)
})

test('add worker: a token is masked on the page and whole in the copy', () => {
  assert.equal(maskToken('lai_join_k3F9aaaaaaaaaaaa'), 'lai_join_k3F••••••••••')
  assert.equal(maskToken('short'), '•••••')
  const command = "curl -fsSL 'https://x/join.sh?t=lai_join_SECRET' | sudo sh"
  assert.equal(maskCommand(command, 'lai_join_SECRET'), "curl -fsSL 'https://x/join.sh?t=••••••••••••' | sudo sh")
  assert.equal(maskCommand(command, ''), command)
})

test('add worker: each tab shows the command of its field, and Windows on the install tab', () => {
  const commands = { install_script: 'sh', windows: 'ps', docker: 'd', docker_compose: 'c', kubernetes: 'k', manual: 'm' }
  assert.deepEqual(['install', 'docker', 'compose', 'kubernetes', 'manual'].map(id => commandOf(commands, id)), ['sh', 'd', 'c', 'k', 'm'])
  assert.equal(commandOf(commands, 'install', true), 'ps')
  assert.equal(commandOf(commands, 'docker', true), 'd')
  assert.equal(commandOf(null, 'docker'), '')
})

test('add worker: the uses left come from the token row, or from the workers that arrived', () => {
  assert.equal(usesLeft({ uses: 2, max_uses: 5 }, 0, 5), 3)
  assert.equal(usesLeft(null, 1, 5), 4)
  assert.equal(usesLeft(null, 3, 1), 0)
})
