import test from 'node:test'
import assert from 'node:assert/strict'
import {
  ago, canAdd, defaultLimits, checkOutcome, formatBytes, formatSeconds, hasLimits, isRelayAddr, limitParts, limitsFromReported,
  hasCeiling, limitsIssues, limitsMaxText, limitsPatch, modeChoices, normalizeMode, p2pSettingsFrom, parsePeerList, relayCommands, relayPass,
  relayHost, relayName, relayPeerID, relayTone, shortID, validLimits, wrongPeer,
} from '../../src/components/p2psettings/p2pSettingsModel.js'

const ID = '12D3KooWRk4eHq7vN2cYpTzS8mFJuX3aWbLdQe5Gh6iKoPzXk9'
const OTHER = '12D3KooWZq9a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8Lp'
const ADDR = `/dns4/relay.example.org/udp/4001/quic-v1/p2p/${ID}`

test('p2p settings: modes, and share needs authentication', () => {
  assert.equal(normalizeMode('Share'), 'share')
  assert.equal(normalizeMode(''), 'off')
  assert.equal(normalizeMode('federated'), 'off')
  assert.deepEqual(modeChoices(false).map(c => [c.id, c.disabled]), [['off', false], ['connect', false], ['share', true]])
  assert.deepEqual(modeChoices(true).map(c => c.disabled), [false, false, false])
})

test('p2p settings: values from /api/settings with registry defaults', () => {
  const s = p2pSettingsFrom({})
  assert.deepEqual(s, {
    p2p_mode: 'off', p2p_public_dht: true, p2p_mdns: true, p2p_bootstrap_peers: [],
    p2p_relay_paired: true, p2p_relay_mode: 'auto', p2p_relay_rate_limit: 0,
  })
  const t = p2pSettingsFrom({ p2p_mode: 'connect', p2p_public_dht: false, p2p_relay_mode: 'bogus', p2p_relay_rate_limit: 65536, p2p_bootstrap_peers: ['/a'] })
  assert.equal(t.p2p_mode, 'connect')
  assert.equal(t.p2p_public_dht, false)
  assert.equal(t.p2p_relay_mode, 'auto')
  assert.equal(t.p2p_relay_rate_limit, 65536)
  assert.deepEqual(t.p2p_bootstrap_peers, ['/a'])
})

test('p2p settings: peer lists and relay addresses', () => {
  assert.deepEqual(parsePeerList(' /a \n\n/b,/a\n'), ['/a', '/b'])
  assert.equal(relayPeerID(ADDR), ID)
  assert.equal(relayPeerID('/ip4/1.2.3.4/tcp/4001'), '')
  assert.ok(isRelayAddr(ADDR))
  assert.ok(!isRelayAddr(`relay.example.org/p2p/${ID}`))
  assert.ok(!isRelayAddr('/ip4/1.2.3.4/tcp/4001'))
  assert.equal(shortID(ID), '12D3KooW…zXk9')
  assert.equal(shortID('short'), 'short')
  assert.equal(relayHost(ADDR), 'relay.example.org')
  assert.equal(relayHost(`/ip4/198.51.100.20/tcp/4001/p2p/${ID}`), '198.51.100.20')
  assert.equal(relayName({ label: 'Home', addr: ADDR }), 'Home')
  assert.equal(relayName({ label: '', addr: ADDR }), 'relay.example.org')
  assert.equal(relayName({ addr: `/p2p/${ID}` }), '12D3KooW…zXk9')
})

test('p2p settings: relay state tones and what invites get', () => {
  assert.equal(relayTone('in_sync'), 'ok')
  assert.equal(relayTone('out_of_sync'), 'warn')
  assert.equal(relayTone('unreachable'), 'error')
  assert.equal(relayTone('checking'), 'accent')
  assert.equal(relayTone('weird'), 'idle')
  assert.equal(relayPass({ state: 'in_sync' }), 'pass')
  assert.equal(relayPass({ state: 'out_of_sync' }), 'pass')
  assert.equal(relayPass({ state: 'open' }), 'open')
  for (const state of ['unclaimed', 'not_admin', 'denied', 'unreachable', 'checking']) assert.equal(relayPass({ state }), 'none')
})

test('p2p settings: limits read as words', () => {
  assert.equal(formatBytes(1 << 30), '1 GiB')
  assert.equal(formatBytes(64 << 10), '64 KiB')
  assert.equal(formatBytes(1536), '1.5 KiB')
  assert.equal(formatSeconds(1800), '30 min')
  assert.equal(formatSeconds(3600), '1 h')
  assert.equal(formatSeconds(5400), '1 h 30 min')
  assert.equal(formatSeconds(45), '45 s')
  assert.ok(!hasLimits({ circuit_bytes: 0, circuit_seconds: 0, rate_bps: 0 }))
  assert.ok(hasLimits({ rate_bps: 1 }))
  assert.deepEqual(limitParts({ circuit_bytes: 1 << 30, circuit_seconds: 1800, rate_bps: 65536 }), ['1 GiB', '30 min', '64 KiB/s'])
})

test('p2p settings: a gated relay that works', () => {
  const o = checkOutcome({
    connected: true, reserved: true, admin_ok: true, circuit_ok: true, in_sync: true,
    detail: ['connect: ok in 12 ms', 'reserve: ok, until 2026-11-17T10:00:00Z', 'admin: in_sync (in sync)', 'circuit: ok', 'digest: equal'],
    relay: { state: 'in_sync' },
  })
  assert.equal(o.kind, 'ok')
  assert.deepEqual(o.steps, { reserve: 'ok', circuit: 'ok', allowlist: 'ok' })
  assert.ok(canAdd(o.kind))
})

test('p2p settings: an unreachable relay stops at the first step', () => {
  const o = checkOutcome({
    connected: false, detail: ['connect: failed: no good addresses'], relay: { state: 'unreachable', health: 'cannot connect: no good addresses' },
  })
  assert.equal(o.kind, 'unreachable')
  assert.deepEqual(o.steps, { reserve: 'failed', circuit: 'waiting', allowlist: 'waiting' })
  assert.equal(o.details.reserve, 'failed: no good addresses')
  assert.ok(!canAdd(o.kind))
})

test('p2p settings: the relay answers with another peer ID', () => {
  const err = `failed to dial: peer id mismatch: expected ${ID}, but remote key matches ${OTHER}`
  const check = { connected: false, detail: [`connect: failed: ${err}`], relay: { state: 'unreachable', health: `cannot connect: ${err}` } }
  assert.deepEqual(wrongPeer(check), { expected: ID, actual: OTHER })
  const o = checkOutcome(check)
  assert.equal(o.kind, 'wrong_peer')
  assert.deepEqual(o.peer, { expected: ID, actual: OTHER })
  assert.equal(wrongPeer({ detail: ['connect: failed: timeout'] }), null)
})

test('p2p settings: open, unclaimed, not admin and a failed circuit', () => {
  const open = checkOutcome({ connected: true, reserved: true, circuit_ok: true, detail: ['circuit: ok'], relay: { state: 'open' } })
  assert.equal(open.kind, 'open')
  assert.equal(open.steps.allowlist, 'skipped')
  assert.ok(canAdd(open.kind))

  const unclaimed = checkOutcome({ connected: true, reserved: true, detail: ['circuit: skipped, this server cannot add a test peer to the relay'], relay: { state: 'unclaimed' } })
  assert.equal(unclaimed.kind, 'unclaimed')
  assert.equal(unclaimed.steps.circuit, 'skipped')
  assert.equal(unclaimed.steps.allowlist, 'waiting')
  assert.ok(!canAdd(unclaimed.kind))

  const notAdmin = checkOutcome({ connected: true, reserved: false, detail: ['reserve: refused: denied'], relay: { state: 'denied' } })
  assert.equal(notAdmin.kind, 'not_admin')
  assert.equal(notAdmin.steps.reserve, 'failed')

  const circuit = checkOutcome({ connected: true, reserved: true, admin_ok: true, in_sync: true, circuit_ok: false, detail: ['circuit: failed: the echo differs'], relay: { state: 'in_sync' } })
  assert.equal(circuit.kind, 'circuit')
  assert.equal(circuit.details.circuit, 'failed: the echo differs')
})

test('p2p settings: relay start commands name this server as admin', () => {
  const c = relayCommands(ID)
  assert.match(c.docker, /--network host/)
  assert.match(c.docker, /-v localai-relay:\/data/)
  assert.ok(c.docker.endsWith(`localai/relay --admin ${ID}`))
  assert.equal(c.binary, `local-ai p2p relay --admin ${ID}`)
  assert.match(relayCommands('').binary, /--admin '<peer ID of this server>'$/)
})

test('p2p settings: limits form and the body that saves it', () => {
  assert.deepEqual(limitsFromReported({ circuit_bytes: 0, circuit_seconds: 0, rate_bps: 0 }), { dataGiB: 0, minutes: 0, kibps: 0 })
  assert.deepEqual(limitsFromReported({ circuit_bytes: 2 * 2 ** 30, circuit_seconds: 3600, rate_bps: 10 << 20 }), { dataGiB: 2, minutes: 60, kibps: 10240 })
  assert.deepEqual(limitsFromReported(undefined), { dataGiB: 0, minutes: 0, kibps: 0 })
  assert.deepEqual(defaultLimits(true), { dataGiB: 1, minutes: 30, kibps: 64 })
  assert.deepEqual(defaultLimits(false), { dataGiB: 1, minutes: 30, kibps: 0 })
  // Reset never goes above the ceiling of the relay.
  assert.deepEqual(defaultLimits(false, { circuit_bytes: 512 << 20, circuit_seconds: 600, rate_bps: 32768 }), { dataGiB: 0.5, minutes: 10, kibps: 32 })
  assert.deepEqual(defaultLimits(true, { circuit_bytes: 8 << 30, circuit_seconds: 86400, rate_bps: 0 }), { dataGiB: 1, minutes: 30, kibps: 64 })

  assert.ok(validLimits({ dataGiB: '1', minutes: '0', kibps: '64' }))
  assert.ok(!validLimits({ dataGiB: '-1', minutes: '30', kibps: '64' }))
  assert.ok(!validLimits({ dataGiB: '1', minutes: '', kibps: '64' }))
  assert.ok(!validLimits({ dataGiB: '1', minutes: 'x', kibps: '64' }))

  assert.deepEqual(limitsPatch({ form: { dataGiB: 1, minutes: 90, kibps: 64 } }), { circuit_bytes: 1073741824, circuit_seconds: 5400, rate_bps: 65536 })
  assert.deepEqual(limitsPatch({ form: { dataGiB: 1, minutes: 90, kibps: 64 }, noLimit: true }), { circuit_bytes: 0, circuit_seconds: 0, rate_bps: 0 })
})

test('p2p settings: how long ago a relay was checked', () => {
  const now = Date.parse('2026-11-17T12:00:00Z')
  assert.deepEqual(ago('', now), { key: '', count: 0 })
  assert.deepEqual(ago('2026-11-17T11:59:40Z', now), { key: 'now', count: 0 })
  assert.deepEqual(ago('2026-11-17T11:58:00Z', now), { key: 'minutes', count: 2 })
  assert.deepEqual(ago('2026-11-17T09:00:00Z', now), { key: 'hours', count: 3 })
  assert.deepEqual(ago('2026-11-15T12:00:00Z', now), { key: 'days', count: 2 })
})

test('p2p settings: the ceiling of a relay and what it refuses', () => {
  const ceiling = { circuit_bytes: 8 * 2 ** 30, circuit_seconds: 86400, rate_bps: 0 }
  assert.ok(hasCeiling(ceiling))
  assert.ok(!hasCeiling({ circuit_bytes: 0, circuit_seconds: 0, rate_bps: 0 }))
  assert.ok(!hasCeiling(undefined))

  assert.equal(limitsMaxText('dataGiB', ceiling), '8 GiB')
  assert.equal(limitsMaxText('minutes', ceiling), '24 h')
  assert.equal(limitsMaxText('kibps', ceiling), '')
  assert.equal(limitsMaxText('kibps', { rate_bps: 1 << 20 }), '1 MiB/s')

  const ok = { dataGiB: 8, minutes: 1440, kibps: 0 }
  assert.deepEqual(limitsIssues({ form: ok, noLimit: false, ceiling }), {})
  assert.deepEqual(limitsIssues({ form: { ...ok, dataGiB: 8.01 }, noLimit: false, ceiling }), { dataGiB: 'above' })
  assert.deepEqual(limitsIssues({ form: { ...ok, minutes: 1441 }, noLimit: false, ceiling }), { minutes: 'above' })
  // Zero removes a limit, which a ceiling does not allow.
  assert.deepEqual(limitsIssues({ form: { ...ok, dataGiB: 0 }, noLimit: false, ceiling }), { dataGiB: 'required' })
  assert.deepEqual(limitsIssues({ form: ok, noLimit: true, ceiling }), { dataGiB: 'required', minutes: 'required' })
  // No ceiling: nothing is refused here.
  assert.deepEqual(limitsIssues({ form: { dataGiB: 100, minutes: 0, kibps: 0 }, noLimit: false, ceiling: undefined }), {})
  // A value equal to the ceiling in bytes is allowed although the form rounds it.
  assert.deepEqual(limitsIssues({ form: { dataGiB: 0.5, minutes: 10, kibps: 32 }, noLimit: false, ceiling: { circuit_bytes: 512 << 20, circuit_seconds: 600, rate_bps: 32768 } }), {})
})
