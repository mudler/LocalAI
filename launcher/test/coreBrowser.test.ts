// @vitest-environment jsdom
// The shared core under a browser environment: no Node, no Electron. A
// client-only shell runs these modules with injected I/O.
import { describe, expect, it } from 'vitest'
import { migrateConfig, type LauncherConfig, type LoadResult } from '../src/core/configTypes'
import { ConfigStore, showStatusBar, withShowStatusBar, type ConfigIO } from '../src/core/configStore'
import { newRemoteId } from '../src/core/serversConfig'
import { parseForwardState, parsePairJson } from '../src/core/p2pInvite'

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
    expect(parseForwardState('not json')).toBeUndefined()
  })
})
