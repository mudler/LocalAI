// @vitest-environment jsdom
// The shared core under a browser environment: no Node, no Electron. A
// client-only shell runs these modules with injected I/O.
//
// This file is a composition smoke test. Under vitest's jsdom environment the
// Node built-ins are still reachable, so it cannot prove that the core is
// Node-free: that guarantee comes from coreBoundary.test.ts (the import
// guard) and tsconfig.core.json (a type-check with no Node types).
import fs from 'node:fs'
import path from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { migrateConfig, type LauncherConfig, type LoadResult } from '../src/core/configTypes'
import { ConfigStore, showStatusBar, withShowStatusBar, type ConfigIO } from '../src/core/configStore'
import { newRemoteId, remotes } from '../src/core/serversConfig'
import { parseForwardState, parsePairJson } from '../src/core/p2pInvite'
import { parseChecksums } from '../src/core/checksums'
import { DEV_RELEASE_TAG, devBuildLabel, parseDevBuild } from '../src/core/devFeed'
import { runServerTest, type HttpGet } from '../src/core/testClassify'
import { WebuiModel } from '../src/core/webuiState'
import { RemoteServers, type P2PTunnels } from '../src/core/remoteServers'

const PEER = '12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGm'

const D = { modelsPath: '/data/models', backendsPath: '/data/backends' }

const fresh = (): LoadResult => ({ config: migrateConfig({}, D).config, status: 'missing', invalidKeys: [] })

describe('ConfigStore over a ConfigIO', () => {
  it('ConfigStore runs on an in-memory ConfigIO', async () => {
    const saved: LauncherConfig[] = []
    const io: ConfigIO = { load: async () => fresh(), save: async (c) => void saved.push(c) }
    const s = await ConfigStore.open(io)
    await s.update((c) => withShowStatusBar(c, false))
    expect(showStatusBar(s.get())).toBe(false)
    expect(saved).toHaveLength(1)
  })

  it('a failed save rejects that change and the next change still runs', async () => {
    const saved: LauncherConfig[] = []
    const boom = new Error('disk full')
    let fail = true
    const io: ConfigIO = {
      load: async () => fresh(),
      save: async (c) => {
        if (fail) {
          fail = false
          throw boom
        }
        saved.push(c)
      },
    }
    const s = await ConfigStore.open(io)
    const first = s.update((c) => withShowStatusBar(c, false))
    const second = s.update((c) => ({ ...c, log_level: 'debug' }))
    await expect(first).rejects.toBe(boom)
    // The failed change is not kept in memory.
    expect(showStatusBar(s.get())).toBe(true)
    const next = await second
    expect(next.log_level).toBe('debug')
    expect(showStatusBar(next)).toBe(true)
    expect(saved).toHaveLength(1)
    expect(saved[0].log_level).toBe('debug')
    expect(s.get().log_level).toBe('debug')
    await s.idle()
  })

  it('keeps the recovered load result', async () => {
    const recovered: LoadResult = { config: migrateConfig({}, D).config, status: 'recovered', reason: 'x', invalidKeys: [] }
    const io: ConfigIO = { load: async () => recovered, save: async () => undefined }
    const s = await ConfigStore.open(io)
    expect(s.loaded.status).toBe('recovered')
    expect(s.loaded).toEqual(recovered)
    expect(s.loaded.reason).toBe('x')
  })

  it('migrateConfig uses the given folder defaults', () => {
    expect(migrateConfig({}, D).config.models_path).toBe('/data/models')
    expect(migrateConfig({}, D).config.backends_path).toBe('/data/backends')
  })
})

describe('invite parsing and the server list', () => {
  it('newRemoteId works with the browser random source', () => {
    const id = newRemoteId((n) => crypto.getRandomValues(new Uint8Array(n)))
    expect(id).toMatch(/^srv_[0-9a-f]{8}$/)
  })

  it('newRemoteId gives the same id for the same bytes as before', () => {
    expect(newRemoteId(() => new Uint8Array([0, 1, 16, 255]))).toBe('srv_000110ff')
  })

  it('parsePairJson reads the CLI and the mobile library answer', () => {
    expect(parsePairJson(`{"kind":"device","phrase":"a b c d","server":{"peer_id":"${PEER}","name":"Home"}}`)).toEqual({ kind: 'device', phrase: 'a b c d', serverName: 'Home', peer: PEER })
    expect(parsePairJson('{"phrase":"x","server":{"peer_id":"bad"}}')).toBeUndefined()
  })

  it('parseForwardState maps the forwarder lines', () => {
    expect(parseForwardState('{"state":"listening"}')).toEqual({ state: 'ready' })
    expect(parseForwardState('{"state":"up","path":"none"}')).toEqual({ state: 'up' })
    expect(parseForwardState('{"state":"down","error":"x"}')).toEqual({ state: 'down', error: 'x' })
    expect(parseForwardState('{"state":"down","error":"x","code":"needs_relay"}')).toEqual({ state: 'down', error: 'x', code: 'needs_relay' })
    expect(parseForwardState('{"state":"down"}')).toEqual({ state: 'down' })
    expect(parseForwardState('not json')).toBeUndefined()
  })
})

describe('checksums and the development feed names', () => {
  it('parseChecksums reads a sha256sum line', () => {
    const hash = 'ab'.repeat(32)
    const sums = parseChecksums(`${hash}  LocalAI-Android.apk\n`)
    expect(sums.get('LocalAI-Android.apk')).toBe(hash)
    expect(sums.size).toBe(1)
  })

  it('parseDevBuild reads the sha, the date and the subject', () => {
    const b = parseDevBuild({ name: 'Development build', body: 'sha 0a1b2c3d4e5f\ndate 2026-10-02\nAdd the client shell', published_at: '2026-10-02T08:30:00Z' })
    expect(b).toEqual({ id: '0a1b2c3', date: '2026-10-02', subject: 'Development build' })
    expect(devBuildLabel(b!)).toBe('2026-10-02 (0a1b2c3)')
    expect(DEV_RELEASE_TAG).toBe('dev-master')
  })
})

// Every module of the core, loaded at once. The count check below stops a
// wrong glob from passing on zero modules.
const coreModules = import.meta.glob('../src/core/*.ts', { eager: true }) as Record<string, Record<string, unknown>>
const coreFiles = fs.readdirSync(path.resolve(__dirname, '../src/core')).filter((f) => f.endsWith('.ts'))

describe('the whole core in a browser environment', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('loads every core module and each one exports something', () => {
    expect(coreFiles.length).toBeGreaterThan(0)
    expect(Object.keys(coreModules)).toHaveLength(coreFiles.length)
    for (const [file, mod] of Object.entries(coreModules)) {
      expect(Object.keys(mod).length, file).toBeGreaterThan(0)
    }
  })

  it('adds a server through a client-only RemoteServers with no Buffer', async () => {
    // A stray Buffer use in the flow fails here instead of passing on Node.
    vi.stubGlobal('Buffer', undefined)
    const io: ConfigIO = { load: async () => fresh(), save: async () => undefined }
    const store = await ConfigStore.open(io)
    const fakeGet: HttpGet = async (url) =>
      url.endsWith('/.well-known/localai.json')
        ? { status: 200, contentType: 'application/json', body: '{"version":"v4.0.0","endpoints":{"chat_completions":"/v1/chat/completions"}}' }
        : { status: 404, contentType: 'text/plain', body: '' }
    const p2p: P2PTunnels = {
      available: () => false,
      pair: async () => ({ ok: false, reason: 'no-binary', detail: '' }),
      allocatePort: async () => 41234,
      start: () => undefined,
      stop: () => undefined,
      status: () => undefined,
      whenReady: async () => false,
    }
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
    const servers: RemoteServers = new RemoteServers({
      store,
      now: () => 1_000_000,
      randomBytes: (n) => crypto.getRandomValues(new Uint8Array(n)),
      checkOrigin: (o) => runServerTest(o, fakeGet, () => ({ status: 'unreachable', reason: 'network' })),
      p2p,
      deviceName: () => 'Phone',
      webuiDispatch: (e) => {
        webui.dispatch(e)
        servers.afterWebuiEvent(e)
        return webui.state()
      },
      webuiViewOrigin: () => webui.viewOrigin,
      emitServers: () => undefined,
      log: () => undefined,
      localRow: () => undefined,
      firstRun: (c) => remotes(c).length === 0 && c.show_welcome,
      onLocalDefault: async () => undefined,
      onSwitchFinished: async () => undefined,
    })
    const res = await servers.addServer({ address: 'https://ai.example.com', name: 'Office', makeDefault: true })
    expect(res.ok).toBe(true)
    const v = servers.serversView()
    expect(v.rows.length).toBe(1)
    expect(v.rows[0]).toMatchObject({ kind: 'remote', host: 'ai.example.com', status: 'connected' })
    expect(v.rows[0].id).toMatch(/^srv_[0-9a-f]{8}$/)
    servers.stopAll()
  })
})
