// SPDX-License-Identifier: MIT
import { afterEach, describe, expect, it } from 'vitest'
import { remotes } from '@launcher/core/serversConfig'
import { CONFIG_FILE } from '../src/core/configIO'
import { ClientCore, profileFor, type ClientEvent } from '../src/core/clientCore'
import { mapForwardLine, nextLoopbackHost, P2P_LIBRARY_VERSION } from '../src/core/p2pTunnels'
import { fakePlatform, flush, pairJson, type FakePlatform } from './fakes/plugins'

const PEER = '12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGm'
const PEER2 = '12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGn'
const invite = (peer: string, n = 'studio') => `localai://pair?v=1&k=device&p=${peer}&c=ABCDEFGHJK&e=${Math.floor(Date.now() / 1000) + 600}&n=${n}`

let open: ClientCore[] = []
afterEach(async () => {
  for (const c of open) await c.shutdown()
  open = []
})

async function start(p: FakePlatform): Promise<{ core: ClientCore; events: ClientEvent[] }> {
  const core = await ClientCore.open(p)
  open.push(core)
  const events: ClientEvent[] = []
  core.on((e) => events.push(e))
  await flush()
  return { core, events }
}

const withP2P = (o: Parameters<typeof fakePlatform>[0] = {}) => fakePlatform({ p2pVersion: P2P_LIBRARY_VERSION, ...o })
const called = (p: FakePlatform, name: string) => p.p2p.calls.filter((c) => c.name === name)

/** Pairs with two servers in a row, as a person would. */
async function pairTwo(p: FakePlatform, core: ClientCore) {
  p.p2p.pairAnswers = [
    { ok: true, json: pairJson(PEER, 'Studio') },
    { ok: true, json: pairJson(PEER2, 'Office') },
  ]
  p.p2p.ports = [41001, 41001]
  const a = await core.servers.addServerByInvite({ link: invite(PEER) })
  const b = await core.servers.addServerByInvite({ link: invite(PEER2, 'office') })
  await flush()
  if (!a.ok || !b.ok) throw new Error('pairing failed')
  return { a, b }
}

describe('nextLoopbackHost', () => {
  it('starts at 127.0.0.2 and skips used hosts', () => {
    expect(nextLoopbackHost([])).toBe('127.0.0.2')
    expect(nextLoopbackHost(['127.0.0.2'])).toBe('127.0.0.3')
    expect(nextLoopbackHost(['127.0.0.1', '127.0.0.3'])).toBe('127.0.0.2')
    expect(nextLoopbackHost(['127.0.0.2', '127.0.0.3', '127.0.0.5'])).toBe('127.0.0.4')
  })

  it('never gives 127.0.0.1, a network or a broadcast address', () => {
    const used: string[] = []
    for (let i = 0; i < 300; i++) used.push(nextLoopbackHost(used))
    expect(used).not.toContain('127.0.0.1')
    expect(used.some((h) => h.endsWith('.0') || h.endsWith('.255'))).toBe(false)
    expect(new Set(used).size).toBe(300)
    expect(used.at(-1)).toBe('127.0.1.47')
  })
})

describe('mapForwardLine', () => {
  it('maps the facade and supervisor lines', () => {
    expect(mapForwardLine('{"state":"starting"}')).toEqual({ state: 'starting' })
    expect(mapForwardLine('{"state":"ready","addr":"127.0.0.2:41001"}')).toEqual({ state: 'ready' })
    expect(mapForwardLine('{"state":"up","path":"relayed"}')).toEqual({ state: 'up', path: 'relayed' })
    expect(mapForwardLine('{"state":"down","error":"x"}')).toEqual({ state: 'down', error: 'x' })
    expect(mapForwardLine('{"state":"closed"}')).toEqual({ state: 'stopped' })
    expect(mapForwardLine('{"state":"failed","error":"x"}')).toEqual({ state: 'failed', error: 'x' })
    expect(mapForwardLine('{"state":"failed"}')).toEqual({ state: 'failed' })
    expect(mapForwardLine('{"state":"other"}')).toBeUndefined()
    expect(mapForwardLine('nope')).toBeUndefined()
  })
})

describe('P2P in the client core', () => {
  it('gives two P2P servers distinct loopback hosts, origins and WebView profiles',async () => {
    const p = withP2P()
    const { core } = await start(p)
    const { a, b } = await pairTwo(p, core)
    expect(called(p, 'freePort').map((c) => c.args[0])).toEqual([{ host: '127.0.0.2' }, { host: '127.0.0.3' }])
    const saved = remotes({ extra: JSON.parse(p.files.files.get(CONFIG_FILE) ?? '{}') } as never)
    const rows = core.servers.serversView().rows
    const origin = (id: string) => rows.find((r) => r.id === id)?.origin
    // Same port, different hosts: two origins.
    expect(origin(a.id)).toBe('http://127.0.0.2:41001')
    expect(origin(b.id)).toBe('http://127.0.0.3:41001')
    expect(saved.map((r) => r.p2p?.host)).toEqual(['127.0.0.2', '127.0.0.3'])
    expect(await profileFor(origin(a.id)!)).not.toBe(await profileFor(origin(b.id)!))
    const last = p.p2p.forwardLists().at(-1)!
    expect(last.map((f) => [f.id, f.host, f.port, f.peer])).toEqual([
      [a.id, '127.0.0.2', 41001, PEER],
      [b.id, '127.0.0.3', 41001, PEER2],
    ])
    // Each opens in its own profile.
    p.fakeHttp.answers.set('http://127.0.0.2:41001', 'ok')
    p.fakeHttp.answers.set('http://127.0.0.3:41001', 'ok')
    // Each origin loads only once its own forwarder listens.
    p.p2p.emit('forwardState', { id: a.id, line: '{"state":"ready"}' })
    p.p2p.emit('forwardState', { id: b.id, line: '{"state":"ready"}' })
    await core.servers.setDefaultServer(a.id)
    p.webui.emit('loaded')
    await flush()
    await core.servers.setDefaultServer(b.id)
    await flush()
    const shows = p.webui.calls.filter((c) => c.name === 'show').map((c) => c.args[0] as { origin: string; profile: string })
    expect(shows.map((s) => s.origin)).toEqual(['http://127.0.0.2:41001', 'http://127.0.0.3:41001'])
    expect(shows[0].profile).not.toBe(shows[1].profile)
  })

  it('sends only loopback hosts to the plugin', async () => {
    const p = withP2P()
    const { core } = await start(p)
    await pairTwo(p, core)
    for (const c of called(p, 'freePort')) expect((c.args[0] as { host: string }).host).toMatch(/^127\./)
    for (const list of p.p2p.forwardLists()) for (const f of list) expect(f.host).toMatch(/^127\.\d+\.\d+\.\d+$/)
  })

  it('removing one server sends a list with the other one', async () => {
    const p = withP2P()
    const { core } = await start(p)
    const { a, b } = await pairTwo(p, core)
    await core.servers.removeServer(a.id)
    await flush()
    expect(p.p2p.forwardLists().at(-1)!.map((f) => f.id)).toEqual([b.id])
    expect(core.servers.serversView().rows.map((r) => r.id)).toEqual([b.id])
  })

  it('removing a server deletes its stored sign-in, and only its own', async () => {
    const p = withP2P()
    const { core } = await start(p)
    const { a } = await pairTwo(p, core)
    expect(called(p, 'forgetCredential')).toEqual([])
    await core.servers.removeServer(a.id)
    await flush()
    expect(called(p, 'forgetCredential').map((c) => c.args[0])).toEqual([{ peer: PEER }])
  })

  it('a new invite of a saved server keeps its sign-in', async () => {
    const p = withP2P()
    const { core } = await start(p)
    await pairTwo(p, core)
    const r = await core.servers.addServerByInvite({ link: `${invite(PEER)}&r=/ip4/192.0.2.9/tcp/4001/p2p/12D3KooWRelay` })
    await flush()
    expect(r.ok).toBe(true)
    expect(called(p, 'forgetCredential')).toEqual([])
  })

  it('keeps only the sign-ins of saved servers when it starts', async () => {
    const p = withP2P()
    const first = await start(p)
    await pairTwo(p, first.core)
    await first.core.shutdown()
    open = open.filter((c) => c !== first.core)
    expect(called(p, 'forgetCredential')).toEqual([])
    p.p2p.calls.length = 0
    await start(p)
    const retained = called(p, 'retainCredentials').map((c) => (c.args[0] as { peers: string[] }).peers)
    expect(retained).toHaveLength(1)
    expect([...retained[0]].sort()).toEqual([PEER, PEER2].sort())
  })

  it('passes the sign-in fields of a pairing through and still pairs', async () => {
    const p = withP2P()
    const { core } = await start(p)
    const json = JSON.stringify({ ...JSON.parse(pairJson(PEER)), sign_in: true, owner_name: 'Ada' })
    p.p2p.pairAnswers = [{ ok: true, json }]
    const r = await core.servers.addServerByInvite({ link: invite(PEER) })
    expect(r.ok).toBe(true)
  })

  it('maps the state lines onto the row and publishes a fresh view for each', async () => {
    const p = withP2P()
    const { core, events } = await start(p)
    p.p2p.pairAnswers = [{ ok: true, json: pairJson(PEER) }]
    const r = await core.servers.addServerByInvite({ link: invite(PEER) })
    if (!r.ok) throw new Error('pairing failed')
    const row = () => core.servers.serversView().rows[0].p2p
    expect(row()).toEqual({ state: 'starting' })
    const lastServers = () => [...events].reverse().find((e) => e.kind === 'servers')
    for (const [line, want] of [
      ['{"state":"failed","error":"x"}', { state: 'failed', error: 'x' }],
      ['{"state":"starting"}', { state: 'starting' }],
      ['{"state":"ready","addr":"127.0.0.2:41000"}', { state: 'ready' }],
      ['{"state":"up","path":"direct"}', { state: 'up', path: 'direct' }],
      ['{"state":"closed"}', { state: 'stopped' }],
    ] as const) {
      const before = events.length
      p.p2p.emit('forwardState', { id: r.id, line })
      expect(row()).toEqual(want)
      expect(events.length).toBeGreaterThan(before)
      const e = lastServers()
      expect(e?.kind === 'servers' && e.view.rows[0].p2p).toEqual(want)
    }
  })

  it('opens a new default once its forwarder is ready', async () => {
    const p = withP2P({ answers: { 'http://127.0.0.2:41000': 'ok' } })
    const { core } = await start(p)
    p.p2p.pairAnswers = [{ ok: true, json: pairJson(PEER) }]
    const pending = core.servers.addServerByInvite({ link: invite(PEER), makeDefault: true })
    await flush()
    const id = p.p2p.forwardLists().at(-1)![0].id
    p.p2p.emit('forwardState', { id, line: '{"state":"ready"}' })
    expect(await pending).toMatchObject({ ok: true, opened: true })
  })

  it('a forwarder that fails to start does not open the new default', async () => {
    const p = withP2P()
    const { core } = await start(p)
    p.p2p.pairAnswers = [{ ok: true, json: pairJson(PEER) }]
    const pending = core.servers.addServerByInvite({ link: invite(PEER), makeDefault: true })
    await flush()
    const id = p.p2p.forwardLists().at(-1)![0].id
    p.p2p.emit('forwardState', { id, line: '{"state":"failed","error":"no route"}' })
    expect(await pending).toMatchObject({ ok: true, opened: false })
    expect(core.servers.serversView().rows[0].p2p).toEqual({ state: 'failed', error: 'no route' })
  })

  it('a plugin that cannot set the forwards leaves the server saved with a tunnel error', async () => {
    const p = withP2P()
    const { core } = await start(p)
    p.p2p.pairAnswers = [{ ok: true, json: pairJson(PEER) }]
    p.p2p.failSetForwards = 'service gone'
    const r = await core.servers.addServerByInvite({ link: invite(PEER) })
    await flush()
    expect(r.ok).toBe(true)
    expect(core.servers.serversView().rows[0].p2p).toEqual({ state: 'failed', error: 'service gone' })
  })

  it('a build without the library answers no-binary and never pairs', async () => {
    const p = fakePlatform()
    const { core } = await start(p)
    expect(await core.servers.addServerByInvite({ link: invite(PEER) })).toEqual({ ok: false, reason: 'no-binary' })
    expect(called(p, 'pair')).toEqual([])
    expect(called(p, 'setForwards')).toEqual([])
  })

  it('an unknown library version counts as no library', async () => {
    const p = fakePlatform({ p2pVersion: 'p2pmobile/0' })
    const { core } = await start(p)
    expect(await core.servers.addServerByInvite({ link: invite(PEER) })).toEqual({ ok: false, reason: 'no-binary' })
  })

  it('maps a failed pairing and an unexpected answer to pair-failed', async () => {
    const p = withP2P()
    const { core } = await start(p)
    p.p2p.pairAnswers = [{ ok: false, detail: 'invite expired on the server' }, { ok: true, json: 'not json' }]
    expect(await core.servers.addServerByInvite({ link: invite(PEER) })).toEqual({ ok: false, reason: 'pair-failed', detail: 'invite expired on the server' })
    expect(await core.servers.addServerByInvite({ link: invite(PEER) })).toEqual({ ok: false, reason: 'pair-failed', detail: 'unexpected answer' })
    expect(called(p, 'requestNotifications')).toEqual([])
  })

  it('maps the needs_relay code to needs-relay, without the library text', async () => {
    const p = withP2P()
    const { core } = await start(p)
    p.p2p.pairAnswers = [{ ok: false, code: 'needs_relay', detail: 'cannot connect directly' }, { ok: false, code: 'gone', detail: 'used' }]
    expect(await core.servers.addServerByInvite({ link: invite(PEER) })).toEqual({ ok: false, reason: 'needs-relay', detail: '' })
    expect(await core.servers.addServerByInvite({ link: invite(PEER) })).toEqual({ ok: false, reason: 'pair-failed', detail: 'used' })
    expect(core.servers.serversView().rows).toEqual([])
  })

  it('saves the hints of the invite and passes them to the forwarder', async () => {
    const p = withP2P()
    const { core } = await start(p)
    const hint = '/ip4/192.0.2.7/udp/4001/quic-v1/p2p/12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGo'
    p.p2p.pairAnswers = [{ ok: true, json: pairJson(PEER) }]
    const r = await core.servers.addServerByInvite({ link: `${invite(PEER)}&h=${encodeURIComponent(hint)}` })
    expect(r.ok).toBe(true)
    await flush()
    const saved = remotes({ extra: JSON.parse(p.files.files.get(CONFIG_FILE) ?? '{}') } as never)
    expect(saved.map((x) => x.p2p?.hints)).toEqual([[hint]])
    const lists = p.p2p.forwardLists()
    expect(lists[lists.length - 1]?.[0]?.hints).toEqual([hint])
  })

  it.each(['p2pmobile/3', 'p2pmobile/2'])('works with a library of an earlier API revision (%s)', async (version) => {
    const p = fakePlatform({ p2pVersion: version })
    const { core } = await start(p)
    p.p2p.pairAnswers = [{ ok: true, json: pairJson(PEER) }]
    expect((await core.servers.addServerByInvite({ link: invite(PEER) })).ok).toBe(true)
  })

  it('saves the routes a forwarder reports into the saved server, and keeps its state', async () => {
    const p = withP2P()
    const { core } = await start(p)
    p.p2p.pairAnswers = [{ ok: true, json: pairJson(PEER) }]
    const r = await core.servers.addServerByInvite({ link: `${invite(PEER)}&a=${encodeURIComponent('/ip4/203.0.113.1/tcp/4001')}` })
    if (!r.ok) throw new Error('pairing failed')
    await flush()
    p.p2p.emit('forwardState', { id: r.id, line: '{"state":"up","path":"direct"}' })
    const hint = '/ip4/192.0.2.7/tcp/4001/p2p/12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGo'
    const relay = '/ip4/198.51.100.1/tcp/4001/p2p/12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGp'
    p.p2p.emit('forwardState', {
      id: r.id,
      line: JSON.stringify({ state: 'routes', addrs: ['/ip4/203.0.113.9/tcp/4001', 'junk'], relays: [relay], hints: [hint] }),
    })
    await flush()
    const saved = remotes({ extra: JSON.parse(p.files.files.get(CONFIG_FILE) ?? '{}') } as never)
    expect(saved[0]?.p2p).toMatchObject({ peer: PEER, addrs: ['/ip4/203.0.113.9/tcp/4001'], relays: [relay], hints: [hint], host: '127.0.0.2' })
    expect(core.servers.serversView().rows[0]?.p2p).toEqual({ state: 'up', path: 'direct' })
  })

  it('never replaces the saved routes with an empty answer', async () => {
    const p = withP2P()
    const { core } = await start(p)
    p.p2p.pairAnswers = [{ ok: true, json: pairJson(PEER) }]
    const r = await core.servers.addServerByInvite({ link: `${invite(PEER)}&a=${encodeURIComponent('/ip4/203.0.113.1/tcp/4001')}` })
    if (!r.ok) throw new Error('pairing failed')
    await flush()
    p.p2p.emit('forwardState', { id: r.id, line: '{"state":"routes","addrs":[],"relays":[],"hints":[]}' })
    await flush()
    const saved = remotes({ extra: JSON.parse(p.files.files.get(CONFIG_FILE) ?? '{}') } as never)
    expect(saved[0]?.p2p?.addrs).toEqual(['/ip4/203.0.113.1/tcp/4001'])
  })

  it('dials the routes of an invite of a saved server and saves only what the server then gives', async () => {
    const p = withP2P()
    const { core } = await start(p)
    p.p2p.pairAnswers = [{ ok: true, json: pairJson(PEER) }]
    const savedAddr = '/ip4/203.0.113.1/tcp/4001'
    const first = await core.servers.addServerByInvite({ link: `${invite(PEER)}&a=${encodeURIComponent(savedAddr)}` })
    if (!first.ok) throw new Error('pairing failed')
    await flush()
    const hint = '/ip4/192.0.2.7/tcp/4001/p2p/12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGo'
    const r = await core.servers.addServerByInvite({ link: `${invite(PEER)}&a=${encodeURIComponent('/ip4/203.0.113.9/tcp/4001')}&h=${encodeURIComponent(hint)}` })
    expect(r).toMatchObject({ ok: true, id: first.id, phrase: '', routesUpdated: true })
    expect(called(p, 'pair')).toHaveLength(1)
    await flush()
    const saved = () => remotes({ extra: JSON.parse(p.files.files.get(CONFIG_FILE) ?? '{}') } as never)
    expect(saved()).toHaveLength(1)
    // Anyone can write a link with the peer ID of a saved server: its routes are only dialled.
    expect(saved()[0]?.p2p).toMatchObject({ addrs: [savedAddr], host: '127.0.0.2' })
    expect(saved()[0]?.p2p?.hints).toBeUndefined()
    const lists = p.p2p.forwardLists()
    expect(lists[lists.length - 1]?.[0]).toMatchObject({ addrs: ['/ip4/203.0.113.9/tcp/4001', savedAddr], hints: [hint] })

    // Once connected, the server gives its routes over the authenticated connection: those are saved.
    p.p2p.emit('forwardState', { id: first.id, line: JSON.stringify({ state: 'routes', addrs: ['/ip4/203.0.113.9/tcp/4001'], relays: [], hints: [hint] }) })
    await flush()
    expect(saved()[0]?.p2p).toMatchObject({ addrs: ['/ip4/203.0.113.9/tcp/4001'], hints: [hint], host: '127.0.0.2' })
  })

  it('keeps the needs_relay code of a down line', () => {
    expect(mapForwardLine('{"state":"down","error":"no direct path","code":"needs_relay"}')).toEqual({ state: 'down', error: 'no direct path', code: 'needs_relay' })
  })

  it('asks for the notification permission when a P2P server is added', async () => {
    const p = withP2P()
    const { core } = await start(p)
    expect(called(p, 'requestNotifications')).toEqual([])
    await pairTwo(p, core)
    // The plugin shows the system dialog at most once; the web side just asks.
    expect(called(p, 'requestNotifications').length).toBeGreaterThan(0)
  })

  it('starts the saved forwarders at open, with their states, before the default opens', async () => {
    const url = 'http://127.0.0.2:41001'
    const files = {
      [CONFIG_FILE]: JSON.stringify({
        remotes: [{ id: 'srv_00000001', name: 'Studio', url, p2p: { peer: PEER, port: 41001, addrs: [], relays: [], host: '127.0.0.2' } }],
        default_target: 'srv_00000001',
      }),
    }
    const p = withP2P({ files })
    p.p2p.lines = [{ id: 'srv_00000001', line: '{"state":"up","path":"direct"}' }]
    const { core } = await start(p)
    const names = p.p2p.calls.map((c) => c.name)
    expect(names.indexOf('statuses')).toBeLessThan(names.indexOf('setForwards'))
    expect(p.p2p.forwardLists()[0]).toEqual([{ id: 'srv_00000001', name: 'Studio', peer: PEER, host: '127.0.0.2', port: 41001, addrs: [], relays: [], hints: [] }])
    expect(core.servers.serversView().rows[0].p2p).toEqual({ state: 'up', path: 'direct' })
  })

  it('reads the states again on resume before the default is checked', async () => {
    const url = 'http://127.0.0.2:41001'
    const files = {
      [CONFIG_FILE]: JSON.stringify({
        remotes: [{ id: 'srv_00000001', name: 'Studio', url, p2p: { peer: PEER, port: 41001, addrs: [], relays: [], host: '127.0.0.2' } }],
        default_target: 'srv_00000001',
      }),
    }
    const p = withP2P({ files })
    const { core } = await start(p)
    const checks = p.fakeHttp.checks(url)
    core.setForeground(false)
    await flush()
    p.p2p.lines = [{ id: 'srv_00000001', line: '{"state":"ready"}' }]
    const statusesBefore = called(p, 'statuses').length
    core.setForeground(true)
    // Nothing is checked until the plugin answered.
    expect(p.fakeHttp.checks(url)).toBe(checks)
    await flush()
    expect(called(p, 'statuses').length).toBe(statusesBefore + 1)
    expect(core.servers.serversView().rows[0].p2p).toEqual({ state: 'ready' })
    expect(p.fakeHttp.checks(url)).toBe(checks + 1)
  })

  it('learns that the notification turned "keep in background" off (event and read on resume)', async () => {
    const p = withP2P()
    p.p2p.keep = true
    const { core, events } = await start(p)
    expect(core.p2pKeepInBackground()).toBe(true)
    // Disconnect while the app is on screen: the plugin's event.
    p.p2p.emit('keepInBackground', { on: false })
    expect(core.p2pKeepInBackground()).toBe(false)
    expect(events).toContainEqual({ kind: 'p2pKeep', on: false })
    // The person turns it on again from the app.
    expect(await core.setP2PKeepInBackground(true)).toBe(true)
    expect(called(p, 'setKeepInBackground').at(-1)?.args[0]).toEqual({ on: true })
    // Disconnect while the app was in the background, event missed: read on resume.
    core.setForeground(false)
    p.p2p.keep = false
    core.setForeground(true)
    await flush()
    expect(core.p2pKeepInBackground()).toBe(false)
    expect(events.filter((e) => e.kind === 'p2pKeep')).toEqual([
      { kind: 'p2pKeep', on: false },
      { kind: 'p2pKeep', on: true },
      { kind: 'p2pKeep', on: false },
    ])
  })
})

// Another app can bind the loopback address of a P2P
// server while this app's forwarder does not listen. The WebUI view (which
// sends the sign-in cookie) must load that origin only while this app's own
// forwarder listens on it.
describe('the WebUI view of a server over P2P (loopback squatting)', () => {
  const url = 'http://127.0.0.2:41001'
  const files = {
    [CONFIG_FILE]: JSON.stringify({
      remotes: [{ id: 'srv_00000001', name: 'Studio', url, p2p: { peer: PEER, port: 41001, addrs: [], relays: [], host: '127.0.0.2' } }],
      default_target: 'srv_00000001',
    }),
  }
  const ID = 'srv_00000001'
  const webuiCalls = (p: FakePlatform, name: string) => p.webui.calls.filter((c) => c.name === name)
  const line = (p: FakePlatform, l: string) => p.p2p.emit('forwardState', { id: ID, line: l })
  // Something answers on the address: this app's forwarder or another app.
  const answering = () => withP2P({ files, answers: { [url]: 'ok' } })

  it('does not load the origin at start before the forwarder listens, then loads it once it is ready', async () => {
    const p = answering()
    const { core } = await start(p)
    expect(webuiCalls(p, 'show')).toEqual([])
    // Nothing is sent to the address while another app may hold it.
    expect(p.fakeHttp.checks(url)).toBe(0)
    expect(core.webuiState().layout).not.toBe('webui')
    expect(core.webuiState().remote).toMatchObject({ kind: 'unreachable' })
    line(p, '{"state":"ready"}')
    await flush()
    expect(webuiCalls(p, 'show').map((c) => (c.args[0] as { origin: string }).origin)).toEqual([url])
    expect(core.webuiState().remote).toBeNull()
  })

  it('a forwarder that cannot bind (port in use) never opens the view, whatever answers the probe', async () => {
    const p = answering()
    const { core } = await start(p)
    line(p, '{"state":"failed","error":"listen tcp 127.0.0.2:41001: bind: address already in use"}')
    await flush()
    expect(core.servers.serversView().rows[0].p2p).toMatchObject({ state: 'failed' })
    await core.servers.retryRemote()
    core.webuiDispatch({ type: 'reload' })
    await flush()
    expect(webuiCalls(p, 'show')).toEqual([])
    expect(webuiCalls(p, 'reload')).toEqual([])
    expect(p.fakeHttp.checks(url)).toBe(0)
    expect(core.webuiState().remote).toMatchObject({ kind: 'unreachable' })
    // A test of the server does not reach the address either.
    const t = await core.servers.testServer({ id: ID })
    expect(t).toMatchObject({ ok: true, result: { status: 'unreachable' } })
    expect(p.fakeHttp.checks(url)).toBe(0)
  })

  it('a switch to a P2P server whose forwarder does not listen fails and loads nothing', async () => {
    const p = withP2P({ answers: { [url]: 'ok' } })
    const { core } = await start(p)
    p.p2p.pairAnswers = [{ ok: true, json: pairJson(PEER) }]
    p.p2p.ports = [41001]
    const r = await core.servers.addServerByInvite({ link: invite(PEER) })
    if (!r.ok) throw new Error('pairing failed')
    line(p, '{"state":"failed","error":"listen tcp 127.0.0.2:41001: bind: address already in use"}')
    const sw = await core.servers.setDefaultServer(r.id)
    await flush()
    expect(sw).toMatchObject({ ok: false, reason: 'test-failed' })
    expect(webuiCalls(p, 'show')).toEqual([])
  })

  it('closes the view when the forwarder stops, and loads the origin again only once it listens', async () => {
    const p = answering()
    p.p2p.lines = [{ id: ID, line: '{"state":"up","path":"direct"}' }]
    const { core } = await start(p)
    expect(webuiCalls(p, 'show')).toHaveLength(1)
    p.webui.emit('loaded')
    await flush()
    const closes = webuiCalls(p, 'close').length
    line(p, '{"state":"closed"}')
    await flush()
    expect(webuiCalls(p, 'close').length).toBeGreaterThan(closes)
    expect(core.webuiState().layout).not.toBe('webui')
    expect(core.webuiState().remote).toMatchObject({ kind: 'unreachable' })
    // Reload and a starting forwarder load nothing.
    core.webuiDispatch({ type: 'reload' })
    line(p, '{"state":"starting"}')
    line(p, '{"state":"failed","error":"listen tcp 127.0.0.2:41001: bind: address already in use"}')
    await core.servers.retryRemote()
    await flush()
    expect(webuiCalls(p, 'show')).toHaveLength(1)
    expect(webuiCalls(p, 'reload')).toEqual([])
    line(p, '{"state":"ready"}')
    await flush()
    expect(webuiCalls(p, 'show')).toHaveLength(2)
    expect(core.webuiState().remote).toBeNull()
  })

  it('on resume, loads nothing until the forwarder listens again', async () => {
    const p = answering()
    p.p2p.lines = [{ id: ID, line: '{"state":"up"}' }]
    const { core } = await start(p)
    expect(webuiCalls(p, 'show')).toHaveLength(1)
    core.setForeground(false)
    // The service stopped the forwarders in the background; the line came while the page slept.
    p.p2p.lines = [{ id: ID, line: '{"state":"closed"}' }]
    core.setForeground(true)
    await flush()
    expect(webuiCalls(p, 'show')).toHaveLength(1)
    expect(core.webuiState().layout).not.toBe('webui')
    line(p, '{"state":"ready"}')
    await flush()
    expect(webuiCalls(p, 'show')).toHaveLength(2)
  })
})

// A sign-out in the WebUI stops the automatic sign-in of a server (Kotlin
// keeps the flag); only an open from the server list turns it on again.
describe('automatic sign-in after a sign-out', () => {
  const urlA = 'http://127.0.0.2:41001'
  const urlB = 'http://127.0.0.3:41001'
  const files = {
    [CONFIG_FILE]: JSON.stringify({
      remotes: [
        { id: 'srv_00000001', name: 'Studio', url: urlA, p2p: { peer: PEER, port: 41001, addrs: [], relays: [], host: '127.0.0.2' } },
        { id: 'srv_00000002', name: 'Office', url: urlB, p2p: { peer: PEER2, port: 41001, addrs: [], relays: [], host: '127.0.0.3' } },
      ],
      default_target: 'srv_00000001',
    }),
  }
  const webuiCalls = (p: FakePlatform, name: string) => p.webui.calls.filter((c) => c.name === name)
  const listening = () => {
    const p = withP2P({ files, answers: { [urlA]: 'ok', [urlB]: 'ok' } })
    p.p2p.lines = [
      { id: 'srv_00000001', line: '{"state":"up","path":"direct"}' },
      { id: 'srv_00000002', line: '{"state":"up","path":"direct"}' },
    ]
    return p
  }

  it('a cold start opens the default server without turning the sign-in on again', async () => {
    const p = listening()
    p.p2p.signedOut.add(PEER)
    await start(p)
    expect(webuiCalls(p, 'show')).toHaveLength(1)
    expect(called(p, 'allowAutoSignIn')).toEqual([])
    expect(p.p2p.signedOut.has(PEER)).toBe(true)
  })

  it('an open from the list turns it on before the server opens', async () => {
    const p = listening()
    p.p2p.signedOut.add(PEER2)
    const { core } = await start(p)
    p.webui.emit('loaded')
    await flush()
    const allow = p.p2p.allowAutoSignIn.bind(p.p2p)
    let showsBefore = -1
    p.p2p.allowAutoSignIn = async (o) => {
      showsBefore = webuiCalls(p, 'show').length
      return allow(o)
    }
    const r = await core.openServer('srv_00000002')
    await flush()
    expect(r.ok).toBe(true)
    expect(called(p, 'allowAutoSignIn').map((c) => c.args[0])).toEqual([{ peer: PEER2 }])
    expect(p.p2p.signedOut.has(PEER2)).toBe(false)
    // Only the start's show of the default came before it.
    expect(showsBefore).toBe(1)
    expect(webuiCalls(p, 'show').map((c) => (c.args[0] as { origin: string }).origin)).toEqual([urlA, urlB])
    // A new page loads: no reload on top of it.
    expect(webuiCalls(p, 'reload')).toEqual([])
  })

  it('reloads the page on show when it was signed out, so the sign-in runs', async () => {
    const p = listening()
    p.p2p.signedOut.add(PEER)
    const { core } = await start(p)
    p.webui.emit('loaded')
    await flush()
    await core.openServer('srv_00000001')
    await flush()
    expect(called(p, 'allowAutoSignIn').map((c) => c.args[0])).toEqual([{ peer: PEER }])
    expect(webuiCalls(p, 'reload')).toHaveLength(1)
  })

  it('does not reload the page on show when it was not signed out', async () => {
    const p = listening()
    const { core } = await start(p)
    p.webui.emit('loaded')
    await flush()
    await core.openServer('srv_00000001')
    await flush()
    expect(called(p, 'allowAutoSignIn')).toHaveLength(1)
    expect(webuiCalls(p, 'reload')).toEqual([])
  })

  it('still opens the server when the plugin fails', async () => {
    const p = listening()
    const { core } = await start(p)
    p.webui.emit('loaded')
    await flush()
    p.p2p.allowAutoSignIn = async () => Promise.reject(new Error('boom'))
    const r = await core.openServer('srv_00000002')
    await flush()
    expect(r.ok).toBe(true)
    expect(webuiCalls(p, 'show').map((c) => (c.args[0] as { origin: string }).origin)).toEqual([urlA, urlB])
  })
})
