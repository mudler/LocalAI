import { describe, expect, it } from 'vitest'
import { defaultConfig } from '../src/main/config'
import { buildServerLaunch, endpointFor, needsRestart } from '../src/main/serverLaunch'
import { applySettings, checkDataFolder, toSettings, type FolderProbe } from '../src/main/settings'

describe('settings mapping', () => {
  it('maps every config key and the status bar key', () => {
    const c = { ...defaultConfig('/home/u'), extra: { show_status_bar: false, theme: 'dark' } }
    expect(toSettings(c)).toEqual({
      modelsPath: '/home/u/.localai/models',
      backendsPath: '/home/u/.localai/backends',
      address: '127.0.0.1:8080',
      autoStartServer: true,
      startOnBoot: false,
      logLevel: 'info',
      showWelcome: true,
      showStatusBar: false,
      openWebuiInBrowser: false,
      remoteServerUrl: '',
      secretsPlaintextFile: false,
    })
  })

  it('never puts environment variable values in the settings the renderer gets', () => {
    const c = { ...defaultConfig('/h'), environment_vars: { HF_TOKEN: 'tok-secret-value' } }
    expect(JSON.stringify(toSettings(c))).not.toContain('tok-secret-value')
    expect(JSON.stringify(toSettings(c))).not.toContain('HF_TOKEN')
  })

  it('maps the plain-file opt-in to its own key', () => {
    const n = applySettings(defaultConfig('/h'), { secretsPlaintextFile: true })
    expect(n.extra).toEqual({ secrets_plaintext_file: true })
    expect(toSettings(n).secretsPlaintextFile).toBe(true)
    expect(applySettings(n, { secretsPlaintextFile: false }).extra).toEqual({})
  })

  it('shows an unknown log level from an old file as info', () => {
    expect(toSettings({ ...defaultConfig('/h'), log_level: 'loud' }).logLevel).toBe('info')
  })

  it('applies a patch and keeps unknown keys and untouched values', () => {
    const c = { ...defaultConfig('/h'), extra: { theme: 'dark' } }
    const n = applySettings(c, { address: '0.0.0.0:9000', showStatusBar: false })
    expect(n.address).toBe('0.0.0.0:9000')
    expect(n.extra).toEqual({ theme: 'dark', show_status_bar: false })
    expect(n.models_path).toBe(c.models_path)
    expect(c.address).toBe('127.0.0.1:8080') // input not changed
  })
})

describe('server launch', () => {
  const c = defaultConfig('/home/u')

  it('ports the Go run arguments, anchored to the data folder', () => {
    const l = buildServerLaunch(c, '/home/u/.localai', {})
    expect(l.args).toEqual([
      'run',
      '--models-path', '/home/u/.localai/models',
      '--backends-path', '/home/u/.localai/backends',
      '--address', '127.0.0.1:8080',
      '--log-level', 'info',
      '--data-path', '/home/u/.localai/data',
      '--localai-config-dir', '/home/u/.localai/configuration',
      '--generated-content-path', '/home/u/.localai/generated',
      '--upload-path', '/home/u/.localai/uploads',
    ])
    expect(l.endpoint).toBe('http://127.0.0.1:8080')
    expect(l.healthUrl).toBe('http://127.0.0.1:8080/readyz')
  })

  it('builds the child environment with the strip rule and the user variables', () => {
    const l = buildServerLaunch(c, '/d', { PATH: '/usr/bin', APPIMAGE: '/x.AppImage', ELECTRON_RUN_AS_NODE: '1', LD_LIBRARY_PATH: '/tmp/.mount/usr/lib' }, undefined, {
      env: { LOCALAI_THREADS: '4' },
    })
    expect(l.env).toEqual({ PATH: '/usr/bin', LOCALAI_THREADS: '4', LOCALAI_BASE_PATH: '/d' })
  })

  it('sets the server base folder to the data folder, over an inherited or user value', () => {
    const l = buildServerLaunch(c, '/d', { LOCALAI_BASE_PATH: '/elsewhere' }, undefined, { env: { LOCALAI_BASE_PATH: '/user' } })
    expect(l.env.LOCALAI_BASE_PATH).toBe('/d')
  })

  it('reaches a wildcard address on the loopback address', () => {
    expect(endpointFor('0.0.0.0:9090')).toBe('http://127.0.0.1:9090')
    expect(endpointFor(':9090')).toBe('http://127.0.0.1:9090')
    expect(endpointFor('[::]:9090')).toBe('http://[::1]:9090')
    expect(endpointFor('host.example.com:80')).toBe('http://host.example.com:80')
  })

  it('a test endpoint replaces the address', () => {
    const l = buildServerLaunch(c, '/d', {}, 'http://127.0.0.1:18082')
    expect(l.args[l.args.indexOf('--address') + 1]).toBe('127.0.0.1:18082')
    expect(l.healthUrl).toBe('http://127.0.0.1:18082/readyz')
  })

  it('knows which changes need a restart', () => {
    const a = defaultConfig('/h')
    expect(needsRestart(a, { ...a, show_welcome: false, auto_start_server: false })).toBe(false)
    expect(needsRestart(a, { ...a, address: '127.0.0.1:1' })).toBe(true)
    expect(needsRestart(a, { ...a, log_level: 'debug' })).toBe(true)
    // Environment variables live in the secret store; the core marks those restarts.
    expect(needsRestart(a, { ...a, environment_vars: { A: '1' } })).toBe(false)
  })
})

describe('data folder from the picker', () => {
  // A small fake disk: folders, one file, and one folder this user cannot write.
  const dirs = new Set(['/', '/home', '/home/u', '/home/u/models', '/srv', '/ro'])
  const files = new Set(['/home/u/notes.txt'])
  const probe: FolderProbe = {
    isDir: (p) => (dirs.has(p) ? true : files.has(p) ? false : undefined),
    writable: (p) => p !== '/ro' && p !== '/',
  }
  const check = (p: string) => checkDataFolder(p, '/home/u', probe)

  it('takes an existing folder, or one that can be created, normalized', () => {
    expect(check('/home/u/models')).toEqual({ ok: true, path: '/home/u/models' })
    expect(check('/home/u/models/')).toEqual({ ok: true, path: '/home/u/models' })
    expect(check('/home/u/./x/../models')).toEqual({ ok: true, path: '/home/u/models' })
    expect(check('/srv/localai/models')).toEqual({ ok: true, path: '/srv/localai/models' })
  })

  it('refuses the root, the home folder, relative paths, files and folders that cannot be created', () => {
    expect(check('/')).toEqual({ ok: false, reason: 'root' })
    expect(check('/home/u')).toEqual({ ok: false, reason: 'home' })
    expect(check('/home/u/')).toEqual({ ok: false, reason: 'home' })
    expect(check('/home/x/..//u')).toEqual({ ok: false, reason: 'home' })
    for (const p of ['models', '', './x', '/srv/\u0000x']) expect(check(p), p).toEqual({ ok: false, reason: 'not-absolute' })
    expect(check('/home/u/notes.txt')).toEqual({ ok: false, reason: 'not-a-folder' })
    expect(check('/home/u/notes.txt/x')).toEqual({ ok: false, reason: 'not-creatable' })
    expect(check('/ro/x/y')).toEqual({ ok: false, reason: 'not-creatable' })
    expect(check('/top-level')).toEqual({ ok: false, reason: 'not-creatable' })
  })
})
