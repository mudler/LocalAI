import test from 'node:test'
import assert from 'node:assert/strict'
import {
  QR_KINDS, SHARE_KINDS, clock, mintBlockedReason, mintModelFields, modelGrant, inviteFailure, inviteProblem, inviteWarnings, multiaddrHost, parseInviteLink,
  pathKey, pathTone, pendingInvites, phraseWords, relativeTime, secondsLeft, shareKinds, shortPeer, sortedDevices, splitWorkers, isWorkerKind,
} from '../../src/utils/p2p.js'
import { joinCommand, nodeRoute } from '../../src/utils/swarm.js'
import { qrPath } from '../../src/components/p2p/qrPath.js'

const PEER = '12D3KooWRk4eHq7vN2cYpTzS8mFJuX3aWbLdQe5Gh6iKoPzXk9'
const RELAY = `/dns4/relay.example.org/udp/4001/quic-v1/p2p/${PEER}`
const future = Math.floor(Date.now() / 1000) + 600
const link = (extra = '') => `localai://pair?v=1&k=remote&p=${PEER}&c=K7QF29MX4A&e=${future}&n=Branch%20LocalAI${extra}`

test('p2p: the share dialog offers device and remote invites, both with a QR code', () => {
  assert.deepEqual(SHARE_KINDS, ['device', 'remote'])
  assert.ok(SHARE_KINDS.every(k => QR_KINDS.includes(k)))
})

test('p2p: the share dialog offers a worker invite in distributed mode only, without a QR code', () => {
  assert.deepEqual(shareKinds(false), ['device', 'remote'])
  assert.deepEqual(shareKinds(true), ['device', 'remote', 'worker'])
  assert.ok(!QR_KINDS.includes('worker'))
})

test('p2p: the join command of a worker carries the invite and no token', () => {
  const option = { dockerFlags: '--gpus all', tag: 'latest-gpu', devTag: 'master-gpu' }
  const l = link().replace('k=remote', 'k=worker')
  const cli = joinCommand({ flavor: 'cli', option, dev: false, link: l })
  assert.match(cli, /^local-ai worker/)
  assert.ok(cli.includes(`--join "${l}"`))
  const docker = joinCommand({ flavor: 'docker', option, dev: true, link: l })
  assert.ok(docker.includes(`LOCALAI_JOIN="${l}"`))
  assert.match(docker, /LOCALAI_P2P_IDENTITY=\/data\/p2p\/identity\.key/)
  assert.match(docker, /master-gpu worker$/)
  assert.doesNotMatch(cli + docker, /TOKEN/)
})

test('p2p: the route of a node is NATS first, then the tunnel over a websocket or over P2P', () => {
  assert.deepEqual(nodeRoute({ attached: 'nats,tunnel' }), { kind: 'nats' })
  assert.deepEqual(nodeRoute({ attached: 'tunnel' }), { kind: 'websocket' })
  assert.deepEqual(nodeRoute({ attached: 'tunnel', peer_id: PEER, tunnel_transport: 'p2p', tunnel_path: 'relayed', tunnel_limited: true }),
    { kind: 'p2p', path: 'relayed', limited: true })
  assert.deepEqual(nodeRoute({ peer_id: PEER }), { kind: 'p2p', path: null, limited: false })
  assert.equal(nodeRoute({}), null)
})

test('p2p: an invite link is read with its relays, the open ones and the pass', () => {
  const r = parseInviteLink(link(`&r=${encodeURIComponent(RELAY)}&r=${encodeURIComponent('/ip4/198.51.100.20/tcp/4001/p2p/' + PEER)}&o=1&x=PASS`))
  assert.equal(r.ok, true)
  assert.equal(r.link.kind, 'remote')
  assert.equal(r.link.peer, PEER)
  assert.equal(r.link.label, 'Branch LocalAI')
  assert.equal(r.link.pass, true)
  assert.deepEqual(r.link.relays.map(x => [x.host, x.open]), [['relay.example.org', false], ['198.51.100.20', true]])
  assert.equal(r.link.expiresAt.getTime(), future * 1000)
})

test('p2p: a link that is not an invite, or of another version, is refused', () => {
  assert.deepEqual(parseInviteLink('  '), { ok: false, reason: 'empty' })
  assert.equal(parseInviteLink('https://example.com/pair?v=1').reason, 'scheme')
  assert.equal(parseInviteLink('not a link').reason, 'scheme')
  assert.equal(parseInviteLink(link().replace('v=1', 'v=2')).reason, 'version')
  assert.equal(parseInviteLink(`localai://pair?v=1&k=remote&p=${PEER}`).reason, 'fields')
})

test('p2p: the invite step accepts only a remote invite that has not expired', () => {
  assert.equal(inviteProblem(parseInviteLink('')), '')
  assert.equal(inviteProblem(parseInviteLink('nope')), 'malformed')
  assert.equal(inviteProblem(parseInviteLink(link())), '')
  assert.equal(inviteProblem(parseInviteLink(link().replace('k=remote', 'k=device'))), 'wrongKind')
  assert.equal(inviteProblem(parseInviteLink(link()), (future + 1) * 1000), 'expired')
})

test('p2p: a failed connect maps the status to what the person reads', () => {
  assert.equal(inviteFailure({ status: 410 }), 'invalid')
  assert.equal(inviteFailure({ status: 429 }), 'rateLimited')
  assert.equal(inviteFailure({ status: 400 }), 'malformed')
  assert.equal(inviteFailure({ status: 404 }), 'unsupported')
  assert.equal(inviteFailure({ status: 409 }), 'conflict')
  assert.equal(inviteFailure({ status: 504 }), 'unreachable')
  assert.equal(inviteFailure(new Error('network')), 'unreachable')
})

test('p2p: the countdown never goes below zero and shows hours past one', () => {
  assert.equal(secondsLeft(new Date(10_000), 0), 10)
  assert.equal(secondsLeft(new Date(0), 10_000), 0)
  assert.equal(secondsLeft('nonsense'), 0)
  assert.equal(clock(582), '09:42')
  assert.equal(clock(0), '00:00')
  assert.equal(clock(3 * 3600 + 5 * 60 + 10), '3:05:10')
})

test('p2p: relay_unreachable replaces no_relay, unknown warnings are dropped', () => {
  assert.deepEqual(inviteWarnings({ warnings: ['no_relay', 'relay_unreachable', 'something'] }), ['relay_unreachable'])
  assert.deepEqual(inviteWarnings({ warnings: ['no_public_addr', 'open_relay'] }), ['open_relay', 'no_public_addr'])
  assert.deepEqual(inviteWarnings({}), [])
})

test('p2p: punch_only shows after the relay warnings and before no_public_addr, and replaces no_relay', () => {
  assert.deepEqual(inviteWarnings({ warnings: ['no_public_addr', 'punch_only'] }), ['punch_only', 'no_public_addr'])
  assert.deepEqual(inviteWarnings({ warnings: ['punch_only', 'no_relay'] }), ['punch_only'])
  assert.deepEqual(inviteWarnings({ warnings: ['open_relay', 'punch_only', 'relay_unreachable'] }), ['relay_unreachable', 'punch_only', 'open_relay'])
  assert.deepEqual(inviteWarnings({ warnings: ['no_public_addr', 'no_relay'] }), ['no_relay', 'no_public_addr'])
})

test('p2p: a path reads as a tone and words; a limited relay needs a look', () => {
  assert.equal(pathTone({ kind: 'direct' }), 'ok')
  assert.equal(pathTone({ kind: 'hole_punched' }), 'ok')
  assert.equal(pathTone({ kind: 'relayed', limited: true }), 'warn')
  assert.equal(pathTone({ kind: 'relayed' }), 'ok')
  assert.equal(pathTone({ kind: 'none' }), 'idle')
  assert.deepEqual(pathKey({ kind: 'relayed', relay_label: 'home' }), { key: 'relayed', relay: 'home' })
  assert.deepEqual(pathKey({ kind: 'relayed' }), { key: 'relayedUnknown' })
  assert.deepEqual(pathKey({ kind: 'hole_punched' }), { key: 'holePunched' })
  assert.deepEqual(pathKey(undefined), { key: 'none' })
})

test('p2p: pending invites soonest first; devices active first, last seen first', () => {
  const now = 1_000_000
  const inv = [
    { id: 'a', status: 'pending', expires_at: new Date(now + 50_000) },
    { id: 'b', status: 'used', expires_at: new Date(now + 10_000) },
    { id: 'c', status: 'pending', expires_at: new Date(now + 5_000) },
    { id: 'd', status: 'pending', expires_at: new Date(now - 1) },
  ]
  assert.deepEqual(pendingInvites(inv, now).map(i => i.id), ['c', 'a'])
  const devs = [
    { id: 'old', status: 'revoked', last_seen_at: '2026-01-03T00:00:00Z' },
    { id: 'x', status: 'active', last_seen_at: '2026-01-01T00:00:00Z' },
    { id: 'y', status: 'active', last_seen_at: '2026-01-02T00:00:00Z' },
    { id: 'z', status: 'active' },
  ]
  assert.deepEqual(sortedDevices(devs).map(d => d.id), ['y', 'x', 'z', 'old'])
})

test('p2p: small text helpers', () => {
  assert.equal(shortPeer(PEER), '12D3KooWRk…Xk9')
  assert.equal(shortPeer('short'), 'short')
  assert.equal(multiaddrHost(RELAY), 'relay.example.org')
  assert.equal(multiaddrHost('/p2p/abc'), '')
  assert.deepEqual(phraseWords(' amber  canal dune echo '), ['amber', 'canal', 'dune', 'echo'])
  assert.match(relativeTime(new Date(1_000_000 - 5 * 60_000), 'en', 1_000_000), /^5 min.* ago$/)
  assert.equal(relativeTime(undefined), '')
})

test('p2p: the QR code of an invite link is a square with the finder patterns', () => {
  const { size, d } = qrPath(link(`&r=${encodeURIComponent(RELAY)}&x=PASSCODE01`))
  assert.ok(size >= 21 && (size - 17) % 4 === 0, `size ${size} is a QR version`)
  // The top-left finder pattern starts dark at the corner.
  assert.ok(d.startsWith('M0 0h1v1h-1z'))
  assert.equal(qrPath('a').size, 21)
})

test('p2p: a remote invite or device grants all models or a list; other kinds grant none', () => {
  assert.deepEqual(modelGrant({ kind: 'remote', all_models: true }), { all: true, names: [] })
  // An invite made before all_models existed has no list and grants every model.
  assert.deepEqual(modelGrant({ kind: 'remote' }), { all: true, names: [] })
  assert.deepEqual(modelGrant({ kind: 'remote', models: [] }), { all: true, names: [] })
  assert.deepEqual(modelGrant({ kind: 'remote', models: ['a', 'b'] }), { all: false, names: ['a', 'b'] })
  assert.equal(modelGrant({ kind: 'device' }), null)
  assert.equal(modelGrant({ kind: 'worker', all_models: true }), null)
  assert.equal(modelGrant(null), null)
})

test('p2p: the mint request sends all_models, or the chosen names, for a remote only', () => {
  assert.deepEqual(mintModelFields('remote', 'all', new Set(['a'])), { all_models: true })
  assert.deepEqual(mintModelFields('remote', 'specific', new Set(['a', 'b'])), { models: ['a', 'b'] })
  assert.deepEqual(mintModelFields('device', 'specific', new Set(['a'])), {})
  assert.deepEqual(mintModelFields('worker', 'all', new Set()), {})
})

test('p2p: Make invite is blocked for a remote limited to models with none chosen', () => {
  assert.equal(mintBlockedReason('remote', 'specific', new Set()), 'noModels')
  assert.equal(mintBlockedReason('remote', 'specific', new Set(['a'])), '')
  assert.equal(mintBlockedReason('remote', 'all', new Set()), '')
  assert.equal(mintBlockedReason('device', 'specific', new Set()), '')
})

test('p2p: Sharing lists what other people hold and leaves the workers to Workers', () => {
  assert.equal(isWorkerKind('worker'), true)
  assert.equal(isWorkerKind('split'), true)
  assert.equal(isWorkerKind('device'), false)
  assert.equal(isWorkerKind('remote'), false)
  const rows = [{ id: 1, kind: 'device' }, { id: 2, kind: 'worker' }, { id: 3, kind: 'remote' }, { id: 4, kind: 'split' }]
  const { shared, workers } = splitWorkers(rows)
  assert.deepEqual(shared.map(r => r.id), [1, 3])
  assert.deepEqual(workers.map(r => r.id), [2, 4])
  assert.deepEqual(splitWorkers(undefined), { shared: [], workers: [] })
})
