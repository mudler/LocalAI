// @vitest-environment jsdom
// RemoteServers in a client-only set-up: no local server, no Node. The config
// lives in memory, the connection test and the P2P tunnels are scripted, and
// the WebUI model is the real one with no local server.
import { describe, expect, it } from 'vitest'
import { migrateConfig, type LauncherConfig, type LoadResult } from '../src/core/configTypes'
import { ConfigStore, type ConfigIO } from '../src/core/configStore'
import { LOCAL_TARGET, remotes, withDefaultTarget } from '../src/core/serversConfig'
import { WebuiModel, type WebuiEvent } from '../src/core/webuiState'
import type { TestResult } from '../src/core/testClassify'
import type { P2PTarget } from '../src/core/p2pInvite'
import { RemoteServers, type P2PTunnels, type PairOutcome } from '../src/core/remoteServers'
import type { ServersView } from '../src/shared/types'

const PEER = '12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGm'
const invite = () => `localai://pair?v=1&k=device&p=${PEER}&c=ABCDEFGHJK&e=${Math.floor(Date.now() / 1000) + 600}&n=studio`

const D = { modelsPath: '/data/models', backendsPath: '/data/backends' }

interface FakeP2P extends P2PTunnels {
  isAvailable: boolean
  pairs: { link: string; name: string; allowOpenRelay: boolean }[]
  starts: { id: string; name: string; target: P2PTarget }[]
  stops: string[]
}

function fakeP2P(): FakeP2P {
  const p: FakeP2P = {
    isAvailable: true,
    pairs: [],
    starts: [],
    stops: [],
    available: () => p.isAvailable,
    pair: async (input): Promise<PairOutcome> => {
      p.pairs.push(input)
      return { ok: true, result: { kind: 'device', phrase: 'apple banana cherry', serverName: 'Studio', peer: PEER } }
    },
    allocatePort: async () => 41234,
    start: (id, name, target) => void p.starts.push({ id, name, target }),
    stop: (id) => void p.stops.push(id),
    status: () => undefined,
    whenReady: async () => true,
  }
  return p
}

async function rig(o: { config?: (c: LauncherConfig) => LauncherConfig; check?: (origin: string, signal?: AbortSignal) => Promise<TestResult> } = {}) {
  let config = migrateConfig({}, D).config
  if (o.config) config = o.config(config)
  const loaded: LoadResult = { config, status: 'ok', invalidKeys: [] }
  const io: ConfigIO = { load: async () => loaded, save: async () => undefined }
  const store = await ConfigStore.open(io)
  const webui = new WebuiModel({
    server: 'not-installed',
    localOrigin: '',
    installActive: false,
    statusBar: true,
    updateReady: false,
    sandboxOff: false,
    sandboxNoticeShown: false,
    openInBrowser: false,
    workerMode: false,
    serverUpdate: false,
    startedHidden: false,
  })
  const events: WebuiEvent[] = []
  const emitted: ServersView[] = []
  const p2p = fakeP2P()
  let localDefaults = 0
  let n = 0
  const servers = new RemoteServers({
    store,
    now: () => 1_000_000,
    randomBytes: (k) => new Uint8Array(k).map(() => n++),
    checkOrigin: o.check ?? (async () => ({ status: 'ok', version: 'v4.0.0', secure: true })),
    p2p,
    deviceName: () => 'Phone',
    webuiDispatch: (e) => {
      events.push(e)
      webui.dispatch(e)
      servers.afterWebuiEvent(e)
      return webui.state()
    },
    webuiViewOrigin: () => webui.viewOrigin,
    emitServers: (v) => void emitted.push(v),
    log: () => undefined,
    localRow: () => undefined,
    firstRun: (c) => remotes(c).length === 0 && c.show_welcome,
    onLocalDefault: async () => void localDefaults++,
    onSwitchFinished: async () => undefined,
    remoteRetryMs: 60_000,
  })
  return { servers, store, webui, events, emitted, p2p, localDefaults: () => localDefaults }
}

describe('RemoteServers with no local server', () => {
  it('lists no rows and defaults to local when nothing is saved', async () => {
    const r = await rig()
    const v = r.servers.serversView()
    expect(v.rows).toEqual([])
    expect(v.defaultId).toBe(LOCAL_TARGET)
    expect(v.firstRun).toBe(true)
    expect(v.switching).toBeNull()
  })

  it('adds a server, makes it the default and opens it', async () => {
    const r = await rig()
    const res = await r.servers.addServer({ address: 'https://ai.example.com', name: 'Office', makeDefault: true })
    expect(res.ok).toBe(true)
    if (!res.ok) return
    const v = r.servers.serversView()
    expect(v.rows).toHaveLength(1)
    expect(v.rows[0]).toMatchObject({ id: res.id, kind: 'remote', name: 'Office', host: 'ai.example.com', isDefault: true, isCurrent: true, status: 'connected' })
    expect(v.defaultId).toBe(res.id)
    expect(v.firstRun).toBe(false)
    expect(r.events.some((e) => e.type === 'connect')).toBe(true)
    expect(r.webui.state().target).toMatchObject({ kind: 'remote', origin: 'https://ai.example.com' })
    expect(r.emitted.length).toBeGreaterThan(0)
    r.servers.stopAll()
  })

  it('removing the default server goes back to local once', async () => {
    const r = await rig()
    const res = await r.servers.addServer({ address: 'https://ai.example.com', name: 'Office', makeDefault: true })
    if (!res.ok) throw new Error('add failed')
    const removed = await r.servers.removeServer(res.id)
    expect(removed.ok).toBe(true)
    expect(r.localDefaults()).toBe(1)
    expect(r.servers.serversView().rows).toEqual([])
    expect(r.servers.serversView().defaultId).toBe(LOCAL_TARGET)
    expect(r.events.some((e) => e.type === 'back-to-local')).toBe(true)
  })

  it('refuses an invite when P2P is not available', async () => {
    const r = await rig()
    r.p2p.isAvailable = false
    expect(await r.servers.addServerByInvite({ link: invite() })).toEqual({ ok: false, reason: 'no-binary' })
    expect(r.p2p.pairs).toEqual([])
  })

  it('pairs with an invite under the device name and starts the forwarder', async () => {
    const r = await rig()
    const res = await r.servers.addServerByInvite({ link: invite() })
    expect(res).toMatchObject({ ok: true, phrase: 'apple banana cherry', opened: false })
    expect(r.p2p.pairs).toEqual([{ link: invite(), name: 'Phone', allowOpenRelay: false }])
    expect(r.p2p.starts).toHaveLength(1)
    const saved = remotes(r.store.get())[0]
    expect(r.p2p.starts[0]).toEqual({ id: saved.id, name: 'Studio', target: saved.p2p })
    expect(saved.p2p).toMatchObject({ peer: PEER, port: 41234 })
    expect(saved.url).toBe('http://127.0.0.1:41234')
  })

  it('saves the hints of the invite with the server and starts the forwarder with them', async () => {
    const r = await rig()
    const hint = '/ip4/192.0.2.7/udp/4001/quic-v1/p2p/12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGo'
    const res = await r.servers.addServerByInvite({ link: `${invite()}&h=${encodeURIComponent(hint)}` })
    expect(res.ok).toBe(true)
    const saved = remotes(r.store.get())[0]
    expect(saved.p2p?.hints).toEqual([hint])
    expect(r.p2p.starts[0]?.target.hints).toEqual([hint])
  })

  it('startTunnels does nothing while P2P is not available', async () => {
    const r = await rig()
    await r.servers.addServerByInvite({ link: invite() })
    r.p2p.starts.length = 0
    r.p2p.isAvailable = false
    r.servers.startTunnels()
    expect(r.p2p.starts).toEqual([])
    r.p2p.isAvailable = true
    r.servers.startTunnels()
    expect(r.p2p.starts).toHaveLength(1)
  })

  it('removing a server over P2P stops its forwarder', async () => {
    const r = await rig()
    const res = await r.servers.addServerByInvite({ link: invite() })
    if (!res.ok) throw new Error('add failed')
    await r.servers.removeServer(res.id)
    expect(r.p2p.stops).toEqual([res.id])
  })

  it('stopAll stops every started forwarder once', async () => {
    const r = await rig()
    const res = await r.servers.addServerByInvite({ link: invite() })
    if (!res.ok) throw new Error('add failed')
    r.servers.stopAll()
    const started = [...new Set(r.p2p.starts.map((s) => s.id))]
    expect(started).toEqual([res.id])
    expect([...r.p2p.stops].sort()).toEqual([...started].sort())
    r.servers.stopAll()
    expect(r.p2p.stops).toEqual([res.id])
  })

  it('an invite of a saved server only dials its routes until the server confirms them, and does not pair', async () => {
    const saved = '/ip4/198.51.100.2/tcp/4001'
    const r = await rig({
      config: (c) => ({
        ...c,
        extra: { ...c.extra, remotes: [{ id: 'srv_00000001', name: 'Studio', url: 'http://127.0.0.1:41001', p2p: { peer: PEER, port: 41001, addrs: [saved], relays: [] } }] },
      }),
    })
    r.servers.startTunnels()
    const relay = `/ip4/198.51.100.1/tcp/4001/p2p/${PEER}`
    const addr = '/ip4/198.51.100.3/tcp/4001'
    const link = `${invite()}&r=${encodeURIComponent(relay)}&a=${encodeURIComponent(addr)}&a=${encodeURIComponent(saved)}`
    const res = await r.servers.addServerByInvite({ link })
    expect(res).toMatchObject({ ok: true, id: 'srv_00000001', phrase: '', opened: false, routesUpdated: true })
    expect(r.p2p.pairs).toHaveLength(0)
    // The link is not authenticated: nothing of it is saved.
    expect(remotes(r.store.get())[0].p2p).toMatchObject({ addrs: [saved], relays: [] })
    // The forwarder dials the routes of the link first, then the saved ones, each once.
    expect(r.p2p.stops).toEqual(['srv_00000001'])
    expect(r.p2p.starts.map((x) => x.target)).toEqual([
      { peer: PEER, port: 41001, addrs: [saved], relays: [] },
      { peer: PEER, port: 41001, addrs: [addr, saved], relays: [relay] },
    ])

    // The same invite again: the forwarder already dials it.
    const again = await r.servers.addServerByInvite({ link })
    expect(again).toMatchObject({ ok: true, routesUpdated: true })
    expect(r.p2p.starts).toHaveLength(2)

    // The routes the server gives once connected are saved.
    expect(await r.servers.updateP2PRoutes('srv_00000001', { addrs: [addr], relays: [relay], hints: [] })).toBe(true)
    expect(remotes(r.store.get())[0].p2p).toMatchObject({ addrs: [addr], relays: [relay] })
  })

  it('the routes a forwarder reports replace the saved ones, without a restart', async () => {
    const r = await rig()
    const first = await r.servers.addServerByInvite({ link: invite() })
    if (!first.ok) throw new Error('pairing failed')
    const routes = { addrs: ['/ip4/203.0.113.9/tcp/4001'], relays: [], hints: [] }
    expect(await r.servers.updateP2PRoutes(first.id, routes)).toBe(true)
    expect(remotes(r.store.get())[0].p2p?.addrs).toEqual(routes.addrs)
    expect(await r.servers.updateP2PRoutes(first.id, routes)).toBe(false)
    expect(await r.servers.updateP2PRoutes('srv_missing0', routes)).toBe(false)
    expect(r.p2p.stops).toEqual([])
    expect(r.p2p.starts).toHaveLength(1)
  })

  it('stopAll stops a forwarder whose start threw', async () => {
    const r = await rig()
    r.p2p.start = (id, name, target) => {
      r.p2p.starts.push({ id, name, target })
      throw new Error('spawn failed')
    }
    // The server is saved before its forwarder starts: the add succeeds and
    // the row carries the tunnel error.
    const res = await r.servers.addServerByInvite({ link: invite(), makeDefault: true })
    expect(res).toMatchObject({ ok: true, opened: false })
    const saved = remotes(r.store.get())[0]
    expect(res.ok && res.id).toBe(saved.id)
    expect(r.servers.serversView().rows[0].p2p).toEqual({ state: 'failed', error: 'spawn failed' })
    expect(r.p2p.starts.map((s) => s.id)).toEqual([saved.id])
    r.servers.stopAll()
    expect(r.p2p.stops).toEqual([saved.id])
  })

  it('a forwarder whose start throws does not keep the others from starting', async () => {
    const PEER2 = '12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGn'
    const t = (peer: string, port: number) => ({ peer, port, addrs: [], relays: [] })
    const r = await rig({
      config: (c) => ({
        ...c,
        extra: {
          ...c.extra,
          remotes: [
            { id: 'srv_00000001', name: 'A', url: 'http://127.0.0.1:41001', p2p: t(PEER, 41001) },
            { id: 'srv_00000002', name: 'B', url: 'http://127.0.0.1:41002', p2p: t(PEER2, 41002) },
          ],
        },
      }),
    })
    r.p2p.start = (id, name, target) => {
      r.p2p.starts.push({ id, name, target })
      if (id === 'srv_00000001') throw new Error('no forwarder')
    }
    r.servers.startTunnels()
    expect(r.p2p.starts.map((s) => s.id)).toEqual(['srv_00000001', 'srv_00000002'])
    const rows = r.servers.serversView().rows
    expect(rows[0].p2p).toEqual({ state: 'failed', error: 'no forwarder' })
    expect(rows[1].p2p).toEqual({ state: 'stopped' })
    // A later start that works clears the error.
    r.p2p.start = (id, name, target) => void r.p2p.starts.push({ id, name, target })
    r.servers.startTunnels()
    expect(r.servers.serversView().rows[0].p2p).toEqual({ state: 'stopped' })
  })

  it('takes the port and the loopback host from allocate when the tunnels have it', async () => {
    const r = await rig()
    let ports = 0
    r.p2p.allocatePort = async () => {
      ports++
      return 1
    }
    r.p2p.allocate = async () => ({ port: 41005, host: '127.0.0.4' })
    const res = await r.servers.addServerByInvite({ link: invite() })
    expect(res.ok).toBe(true)
    expect(ports).toBe(0)
    const saved = remotes(r.store.get())[0]
    expect(saved.url).toBe('http://127.0.0.4:41005')
    expect(saved.p2p).toMatchObject({ host: '127.0.0.4', port: 41005 })
    expect(r.p2p.starts[0].target).toEqual(saved.p2p)
  })

  it('refuses a non-loopback host from allocate and saves nothing', async () => {
    const r = await rig()
    r.p2p.allocate = async () => ({ port: 41005, host: '192.0.2.4' })
    const res = await r.servers.addServerByInvite({ link: invite() })
    expect(res).toEqual({ ok: false, reason: 'invalid-url' })
    expect(remotes(r.store.get())).toEqual([])
    expect(r.p2p.starts).toEqual([])
  })

  it('a failed check keeps the default', async () => {
    let fail = false
    const r = await rig({ check: async () => (fail ? { status: 'unreachable', reason: 'refused' } : { status: 'ok', secure: true }) })
    const res = await r.servers.addServer({ address: 'https://ai.example.com', name: 'Office' })
    if (!res.ok) throw new Error('add failed')
    fail = true
    expect(await r.servers.setDefaultServer(res.id)).toMatchObject({ ok: false, reason: 'test-failed' })
    expect(r.servers.serversView().defaultId).toBe(LOCAL_TARGET)
    expect(r.servers.serversView().switching).toBeNull()
  })

  it('cancelling while the server is reached ends the switch', async () => {
    const r = await rig({
      check: (_o, signal) =>
        new Promise((resolve) => {
          if (!signal) return resolve({ status: 'ok', secure: true })
          signal.addEventListener('abort', () => resolve({ status: 'cancelled' }))
        }),
    })
    const res = await r.servers.addServer({ address: 'https://ai.example.com', name: 'Office', skipTest: true })
    if (!res.ok) throw new Error('add failed')
    const sw = r.servers.setDefaultServer(res.id)
    expect(r.servers.serversView().switching).toMatchObject({ targetId: res.id, step: 'reaching' })
    await r.servers.cancelSwitch()
    expect(await sw).toEqual({ ok: false, reason: 'cancelled' })
    expect(r.servers.serversView().defaultId).toBe(LOCAL_TARGET)
    expect(r.servers.serversView().switching).toBeNull()
  })

  it('a default that names a missing server is treated as local', async () => {
    const r = await rig({ config: (c) => withDefaultTarget(c, 'srv_deadbeef') })
    const v = r.servers.serversView()
    expect(v.defaultId).toBe(LOCAL_TARGET)
    expect(v.rows.some((x) => x.isCurrent)).toBe(false)
    r.servers.openDefaultRemote()
    expect(r.events).toEqual([])
  })

  it('pauses the watch on an unreachable default and checks once on resume', async () => {
    let checks = 0
    const r = await rig({
      config: (c) => withDefaultTarget({ ...c, extra: { ...c.extra, remotes: [{ id: 'srv_00000001', name: 'Office', url: 'https://ai.example.com' }] } }, 'srv_00000001'),
      check: async () => {
        checks++
        return { status: 'unreachable', reason: 'refused' }
      },
    })
    r.servers.openDefaultRemote()
    await Promise.resolve()
    expect(checks).toBe(1)
    r.servers.pauseMonitor()
    r.servers.resumeMonitor()
    await Promise.resolve()
    expect(checks).toBe(2)
    r.servers.stopAll()
  })

  it('resume does nothing when the default is local', async () => {
    let checks = 0
    const r = await rig({ check: async () => (checks++, { status: 'ok', secure: true }) })
    r.servers.resumeMonitor()
    await Promise.resolve()
    expect(checks).toBe(0)
    expect(r.events).toEqual([])
  })
})
