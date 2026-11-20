import fs from 'node:fs'
import path from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { dataPath, defaultConfig, defaultConfigPath } from '../src/main/config'
import { LauncherCore } from '../src/main/core'
import { BASE_PATH_ENV, homeLayout, layoutNote, resolveLayout, type LayoutHost } from '../src/main/paths'
import { tmpDir } from './helpers'
import { FakeProvider, TARGET } from './serverInstallHarness'

const WIN_HOME = 'C:\\Users\\Ann Lee'
const WIN_LOCAL = 'C:\\Users\\Ann Lee\\AppData\\Local'

function host(over: Partial<LayoutHost> = {}): LayoutHost {
  return { platform: 'win32', env: { LOCALAPPDATA: WIN_LOCAL }, home: WIN_HOME, exists: () => false, ...over }
}

describe('resolveLayout', () => {
  it('keeps <home>/.localai on Linux and macOS', () => {
    for (const platform of ['linux', 'darwin'] as const) {
      expect(resolveLayout(host({ platform, home: '/home/u', env: {} }))).toEqual({
        launcherDir: '/home/u/.localai',
        dataDir: '/home/u/.localai',
        origin: 'home',
      })
    }
  })

  it('ignores LOCALAPPDATA and an old launcher.json outside Windows', () => {
    const l = resolveLayout(host({ platform: 'linux', home: '/home/u', env: { LOCALAPPDATA: '/x' }, exists: () => true }))
    expect(l).toEqual(homeLayout('/home/u'))
  })

  it('uses %LOCALAPPDATA%\\LocalAI on Windows, with the launcher files apart from the installer bin folder', () => {
    expect(resolveLayout(host())).toEqual({
      launcherDir: 'C:\\Users\\Ann Lee\\AppData\\Local\\LocalAI\\launcher',
      dataDir: 'C:\\Users\\Ann Lee\\AppData\\Local\\LocalAI',
      origin: 'standard',
    })
  })

  it('finds the local application data folder in the profile when LOCALAPPDATA is unset or relative', () => {
    for (const env of [{}, { LOCALAPPDATA: 'relative' }]) {
      expect(resolveLayout(host({ env })).dataDir).toBe('C:\\Users\\Ann Lee\\AppData\\Local\\LocalAI')
    }
  })

  it('keeps %USERPROFILE%\\.localai when an earlier build left a launcher.json there', () => {
    const seen: string[] = []
    const l = resolveLayout(host({ exists: (p) => (seen.push(p), p === 'C:\\Users\\Ann Lee\\.localai\\launcher.json') }))
    expect(l).toEqual({ launcherDir: 'C:\\Users\\Ann Lee\\.localai', dataDir: 'C:\\Users\\Ann Lee\\.localai', origin: 'legacy' })
    expect(seen).toEqual(['C:\\Users\\Ann Lee\\.localai\\launcher.json'])
    expect(layoutNote(l)).toMatch(/earlier build/)
  })

  it('moves only the data folder with an absolute LOCALAI_BASE_PATH, on every platform', () => {
    const win = resolveLayout(host({ env: { LOCALAPPDATA: WIN_LOCAL, [BASE_PATH_ENV]: 'D:\\AI' } }))
    expect(win).toEqual({ launcherDir: 'C:\\Users\\Ann Lee\\AppData\\Local\\LocalAI\\launcher', dataDir: 'D:\\AI', origin: 'env' })
    const linux = resolveLayout(host({ platform: 'linux', home: '/home/u', env: { [BASE_PATH_ENV]: '/srv/ai' } }))
    expect(linux).toEqual({ launcherDir: '/home/u/.localai', dataDir: '/srv/ai', origin: 'env' })
    expect(layoutNote(linux)).toMatch(/LOCALAI_BASE_PATH/)
  })

  it('ignores a relative LOCALAI_BASE_PATH: the launcher has no meaningful current folder', () => {
    expect(resolveLayout(host({ platform: 'linux', home: '/home/u', env: { [BASE_PATH_ENV]: 'ai' } })).origin).toBe('home')
  })

  it('has no log line for the usual layouts', () => {
    expect(layoutNote(resolveLayout(host()))).toBeUndefined()
    expect(layoutNote(homeLayout('/home/u'))).toBeUndefined()
  })
})

describe('config defaults follow the layout', () => {
  const layout = { launcherDir: '/l/launcher', dataDir: '/l', origin: 'standard' as const }

  it('puts launcher.json in the launcher folder and models and backends in the data folder', () => {
    expect(defaultConfigPath('/home/u', layout)).toBe(path.join('/l/launcher', 'launcher.json'))
    const c = defaultConfig('/home/u', layout)
    expect(c.models_path).toBe(path.join('/l', 'models'))
    expect(c.backends_path).toBe(path.join('/l', 'backends'))
    expect(dataPath(undefined, '/home/u', layout)).toBe('/l')
  })

  it('keeps <home>/.localai without a layout', () => {
    expect(defaultConfigPath('/home/u')).toBe('/home/u/.localai/launcher.json')
    expect(defaultConfig('/home/u').models_path).toBe('/home/u/.localai/models')
  })
})

describe('core with a layout', () => {
  const cores: LauncherCore[] = []
  afterEach(async () => {
    for (const c of cores.splice(0)) await c.shutdown()
  })

  it('creates the folders and the config in the layout, not in <home>/.localai', async () => {
    const home = tmpDir('layout-')
    const root = path.join(home, 'AppData', 'Local', 'LocalAI')
    const core = new LauncherCore({
      home,
      layout: { launcherDir: path.join(root, 'launcher'), dataDir: root, origin: 'standard' },
      launcherVersion: '1.2.3',
      env: { PATH: process.env.PATH },
      target: TARGET,
      provider: new FakeProvider({ tag: 'v0.0.0', version: '0.0.0', prerelease: false, assets: [] }),
      fetch,
      probeSandbox: () => ({ state: 'sandboxed', inputs: { platform: 'linux', sysctl: 0, noSandboxFlag: false, profile: 'unknown' } }),
      sandboxRunner: async () => ({ exitCode: 0, stderr: '' }),
      emit: () => undefined,
      testServer: async () => ({ status: 'ok', secure: true }),
      logFlushMs: 5,
    })
    cores.push(core)
    await core.init()
    for (const d of ['models', 'backends', 'logs']) expect(fs.statSync(path.join(root, d)).isDirectory()).toBe(true)
    expect(fs.existsSync(path.join(home, '.localai'))).toBe(false)
    expect(core.serverBinary().startsWith(path.join(root, 'launcher', 'bin'))).toBe(true)
  })
})
