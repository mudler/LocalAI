import fs from 'node:fs'
import path from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { dataPath, defaultConfig, defaultConfigPath, loadConfig, migrate, saveConfig, serializeConfig } from '../src/main/config'
import { tmpDir } from './helpers'

const SECRET = 'S3CR3T-VALUE-9d41'

afterEach(() => {
  vi.restoreAllMocks()
})

function setup() {
  const home = tmpDir('cfg-')
  const file = path.join(home, '.localai', 'launcher.json')
  fs.mkdirSync(path.dirname(file), { recursive: true })
  return { home, file }
}

/** What the Go launcher wrote: json.MarshalIndent of Config, mode 0644, no trailing newline. */
function goFile(over: Record<string, unknown> = {}): string {
  const base = {
    models_path: '/data/ai/models',
    backends_path: '/data/ai/backends',
    address: '0.0.0.0:9090',
    auto_start_server: false,
    start_on_boot: true,
    log_level: 'debug',
    environment_vars: { LOCALAI_THREADS: '4', HF_TOKEN: SECRET },
    show_welcome: false,
  }
  return JSON.stringify({ ...base, ...over }, null, 2)
}

describe('defaults', () => {
  it('matches the Go Initialize defaults', () => {
    const c = defaultConfig('/home/u')
    expect(c).toEqual({
      models_path: '/home/u/.localai/models',
      backends_path: '/home/u/.localai/backends',
      address: '127.0.0.1:8080',
      auto_start_server: true,
      start_on_boot: false,
      log_level: 'info',
      environment_vars: {},
      show_welcome: true,
      extra: {},
    })
    expect(defaultConfigPath('/home/u')).toBe('/home/u/.localai/launcher.json')
  })

  it('derives the data folder from the models path, else from home', () => {
    expect(dataPath({ models_path: '/data/ai/models' }, '/home/u')).toBe('/data/ai')
    expect(dataPath({ models_path: '' }, '/home/u')).toBe('/home/u/.localai')
    expect(dataPath(undefined, '/home/u')).toBe('/home/u/.localai')
  })
})

describe('Go keys', () => {
  it('reads every key of a file the Go launcher wrote', async () => {
    const { home, file } = setup()
    fs.writeFileSync(file, goFile(), { mode: 0o644 })
    const r = await loadConfig({ home })
    expect(r.status).toBe('ok')
    expect(r.config).toEqual({
      models_path: '/data/ai/models',
      backends_path: '/data/ai/backends',
      address: '0.0.0.0:9090',
      auto_start_server: false,
      start_on_boot: true,
      log_level: 'debug',
      environment_vars: { LOCALAI_THREADS: '4', HF_TOKEN: SECRET },
      show_welcome: false,
      extra: {},
    })
  })

  it('round trips every key and writes them in the Go order', async () => {
    const { home, file } = setup()
    fs.writeFileSync(file, goFile())
    const { config } = await loadConfig({ home })
    await saveConfig(config, { home })
    const written = JSON.parse(fs.readFileSync(file, 'utf8'))
    expect(Object.keys(written)).toEqual([
      'models_path',
      'backends_path',
      'address',
      'auto_start_server',
      'start_on_boot',
      'log_level',
      'environment_vars',
      'show_welcome',
    ])
    expect(written).toEqual(JSON.parse(goFile()))
    expect((await loadConfig({ home })).config).toEqual(config)
  })
})

describe('auto-start default rule', () => {
  it('missing key means enabled', () => {
    expect(migrate({}, { home: '/h' }).config.auto_start_server).toBe(true)
  })
  it('null means enabled (Go pointer left nil)', () => {
    expect(migrate({ auto_start_server: null }, { home: '/h' }).config.auto_start_server).toBe(true)
  })
  it('an explicit false stays false and an explicit true stays true', () => {
    expect(migrate({ auto_start_server: false }, { home: '/h' }).config.auto_start_server).toBe(false)
    expect(migrate({ auto_start_server: true }, { home: '/h' }).config.auto_start_server).toBe(true)
  })
  it('the legacy auto_start key is never honored and is kept as an unknown key', async () => {
    const { home, file } = setup()
    fs.writeFileSync(file, JSON.stringify({ models_path: '/m', auto_start: false }))
    const { config } = await loadConfig({ home })
    expect(config.auto_start_server).toBe(true)
    expect(config.extra).toEqual({ auto_start: false })
    await saveConfig(config, { home })
    const written = JSON.parse(fs.readFileSync(file, 'utf8'))
    expect(written.auto_start).toBe(false)
    expect(written.auto_start_server).toBe(true)
  })
  it('show_welcome defaults to true the same way', () => {
    expect(migrate({}, { home: '/h' }).config.show_welcome).toBe(true)
    expect(migrate({ show_welcome: false }, { home: '/h' }).config.show_welcome).toBe(false)
  })
})

describe('migrate', () => {
  it('is pure', () => {
    const input = { environment_vars: { A: 'b' }, zzz: { n: 1 } }
    const copy = structuredClone(input)
    const out = migrate(input, { home: '/h' })
    expect(input).toEqual(copy)
    out.config.environment_vars.A = 'changed'
    expect(input.environment_vars.A).toBe('b')
  })
  it('falls back per key on a wrong type and names the key', () => {
    const r = migrate({ address: 5, start_on_boot: 'yes', environment_vars: { A: 1, B: 'x' }, log_level: 'warn' }, { home: '/h' })
    expect(r.invalidKeys.sort()).toEqual(['address', 'environment_vars', 'start_on_boot'])
    expect(r.config.address).toBe('127.0.0.1:8080')
    expect(r.config.start_on_boot).toBe(false)
    expect(r.config.environment_vars).toEqual({ B: 'x' })
    expect(r.config.log_level).toBe('warn')
  })
  it('treats a non-object as empty', () => {
    expect(migrate([], { home: '/h' }).config).toEqual(defaultConfig('/h'))
    expect(migrate(null, { home: '/h' }).config).toEqual(defaultConfig('/h'))
  })
})

describe('unknown keys', () => {
  it('are preserved on write, and cannot override known keys', async () => {
    const { home, file } = setup()
    fs.writeFileSync(file, goFile({ future_flag: { nested: [1, 2, 3] }, theme: 'dark' }))
    const { config } = await loadConfig({ home })
    expect(config.extra).toEqual({ future_flag: { nested: [1, 2, 3] }, theme: 'dark' })
    config.address = '127.0.0.1:1234'
    config.extra.address = 'ignored'
    await saveConfig(config, { home })
    const written = JSON.parse(fs.readFileSync(file, 'utf8'))
    expect(written.future_flag).toEqual({ nested: [1, 2, 3] })
    expect(written.theme).toBe('dark')
    expect(written.address).toBe('127.0.0.1:1234')
  })
  it('serializeConfig ends with a newline', () => {
    expect(serializeConfig(defaultConfig('/h')).endsWith('}\n')).toBe(true)
  })
})

describe('writing', () => {
  it('creates the config folder and the file with mode 0600', async () => {
    const home = tmpDir('cfg-')
    await saveConfig(defaultConfig(home), { home })
    const file = defaultConfigPath(home)
    expect(fs.statSync(file).mode & 0o777).toBe(0o600)
  })

  it('fixes an existing 0644 file on first write', async () => {
    const { home, file } = setup()
    fs.writeFileSync(file, goFile())
    fs.chmodSync(file, 0o644)
    expect(fs.statSync(file).mode & 0o777).toBe(0o644)
    const { config } = await loadConfig({ home })
    await saveConfig(config, { home })
    expect(fs.statSync(file).mode & 0o777).toBe(0o600)
  })

  it('leaves no temp file behind', async () => {
    const { home, file } = setup()
    await saveConfig(defaultConfig(home), { home })
    expect(fs.readdirSync(path.dirname(file))).toEqual(['launcher.json'])
  })

  it('keeps the old file intact and cleans up when the rename fails', async () => {
    const { home, file } = setup()
    fs.writeFileSync(file, goFile())
    const before = fs.readFileSync(file, 'utf8')
    vi.spyOn(fs.promises, 'rename').mockRejectedValueOnce(Object.assign(new Error('boom'), { code: 'EXDEV' }))
    const cfg = defaultConfig(home)
    cfg.address = '1.2.3.4:1'
    await expect(saveConfig(cfg, { home })).rejects.toThrow('boom')
    expect(fs.readFileSync(file, 'utf8')).toBe(before)
    expect(fs.readdirSync(path.dirname(file))).toEqual(['launcher.json'])
  })

  it('writes the temp file in the same directory as the target', async () => {
    const { home, file } = setup()
    const spy = vi.spyOn(fs.promises, 'rename')
    await saveConfig(defaultConfig(home), { home })
    const [from, to] = spy.mock.calls[0] as [string, string]
    expect(path.dirname(from)).toBe(path.dirname(to))
    expect(to).toBe(file)
  })
})

describe('corrupt file', () => {
  it('is moved aside, defaults are returned with a reason, nothing throws', async () => {
    const { home, file } = setup()
    fs.writeFileSync(file, `{"models_path": "${SECRET}", oops`)
    const r = await loadConfig({ home, now: () => new Date(Date.UTC(2026, 10, 14, 9, 8, 7)) })
    expect(r.status).toBe('recovered')
    expect(r.reason).toBe('config file is not valid JSON')
    expect(r.reason).not.toContain(SECRET)
    expect(r.config).toEqual(defaultConfig(home))
    expect(r.backupPath).toBe(`${file}.bad-20261114T090807Z`)
    expect(fs.existsSync(file)).toBe(false)
    expect(fs.readFileSync(r.backupPath!, 'utf8')).toContain('oops')
  })

  it('treats a JSON array as corrupt', async () => {
    const { home, file } = setup()
    fs.writeFileSync(file, '[1,2]')
    const r = await loadConfig({ home })
    expect(r.status).toBe('recovered')
    expect(r.reason).toBe('config file is not a JSON object')
    expect(fs.existsSync(`${file}.bad-${r.backupPath!.split('.bad-')[1]}`)).toBe(true)
  })

  it('treats an empty file as corrupt', async () => {
    const { home, file } = setup()
    fs.writeFileSync(file, '')
    expect((await loadConfig({ home })).status).toBe('recovered')
  })

  it('reports a missing file as missing and does not create it', async () => {
    const { home, file } = setup()
    const r = await loadConfig({ home })
    expect(r.status).toBe('missing')
    expect(r.config).toEqual(defaultConfig(home))
    expect(fs.existsSync(file)).toBe(false)
  })

  it('the next save after recovery writes a fresh file', async () => {
    const { home, file } = setup()
    fs.writeFileSync(file, 'nope')
    const r = await loadConfig({ home })
    await saveConfig(r.config, { home })
    expect(JSON.parse(fs.readFileSync(file, 'utf8')).address).toBe('127.0.0.1:8080')
  })
})

describe('log output', () => {
  it('never contains a config value, only key names and outcomes', async () => {
    const { home, file } = setup()
    const lines: string[] = []
    const log = (m: string) => lines.push(m)
    fs.writeFileSync(file, goFile({ extra_secret_key: SECRET, address: `${SECRET}:1` }))
    const { config } = await loadConfig({ home, log })
    await saveConfig(config, { home, log })

    fs.writeFileSync(file, `{"x": "${SECRET}" bad`)
    await loadConfig({ home, log })

    fs.writeFileSync(file, JSON.stringify({ address: 5, environment_vars: { A: SECRET, B: 1 } }))
    await loadConfig({ home, log })

    vi.spyOn(fs.promises, 'rename').mockRejectedValueOnce(Object.assign(new Error(`failed ${SECRET}`), { code: 'EIO' }))
    await saveConfig(config, { home, log }).catch(() => undefined)

    expect(lines.length).toBeGreaterThan(3)
    const all = lines.join('\n')
    expect(all).not.toContain(SECRET)
    expect(all).not.toContain('0.0.0.0')
    expect(all).not.toContain('/data/ai')
    expect(all).toContain('environment_vars')
    expect(all).toContain('extra_secret_key')
  })
})

describe('migrate: an address saved as a URL', () => {
  it('keeps host:port only, so the server accepts it', () => {
    expect(migrate({ address: 'http://localhost:8080' }, { home: '/h' }).config.address).toBe('localhost:8080')
  })
})
