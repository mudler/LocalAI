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

  it('stopAll stops a forwarder whose start threw', async () => {
    const r = await rig()
    r.p2p.start = (id, name, target) => {
      r.p2p.starts.push({ id, name, target })
      throw new Error('spawn failed')
    }
    await expect(r.servers.addServerByInvite({ link: invite() })).rejects.toThrow('spawn failed')
    const saved = remotes(r.store.get())[0]
    expect(r.p2p.starts.map((s) => s.id)).toEqual([saved.id])
    r.servers.stopAll()
    expect(r.p2p.stops).toEqual([saved.id])
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
})
