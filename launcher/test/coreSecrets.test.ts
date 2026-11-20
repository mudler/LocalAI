// The core's secret handling: migration out of launcher.json, network
// exposure with the API key in the same step, environment variables, and the
// proof that a secret value reaches the server by its environment only.
import fs from 'node:fs'
import type os from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { EventContract } from '../src/shared/ipc'
import { LauncherCore, NETWORK_PLAN_TTL_MS, REDACTED, type CoreDeps } from '../src/main/core'
import { SAFE_STORAGE_PROBE, type SafeStorageLike } from '../src/main/secretStore'
import type { SandboxProbe } from '../src/main/sandboxProbe'
import { commandLine, FAKE_SERVER, freePort, tmpDir } from './helpers'
import { FakeProvider, TARGET } from './serverInstallHarness'

type Ev = { [C in keyof EventContract]: { channel: C; payload: EventContract[C] } }[keyof EventContract]

const probe: SandboxProbe = { state: 'sandboxed', inputs: { platform: 'linux', sysctl: 0, noSandboxFlag: false, profile: 'unknown' } }

function fakeSafe(available = true): SafeStorageLike {
  const tag = Buffer.from('ENC1:')
  return {
    isEncryptionAvailable: () => available,
    getSelectedStorageBackend: () => (available ? 'gnome_libsecret' : 'basic_text'),
    encryptString: (s) => Buffer.concat([tag, Buffer.from(s, 'utf8').map((b) => b ^ 0x5a)]),
    decryptString: (b) => Buffer.from(b.subarray(tag.length).map((x) => x ^ 0x5a)).toString('utf8'),
  }
}

const iface = (address: string, internal = false) => ({ address, family: 'IPv4', internal, netmask: '', mac: '', cidr: null }) as unknown as os.NetworkInterfaceInfo
const IFACES = { lo: [iface('127.0.0.1', true)], eth0: [iface('192.0.2.24')] }

const cores: LauncherCore[] = []
afterEach(async () => {
  for (const c of cores.splice(0)) await c.shutdown()
})

function makeCore(over: Partial<CoreDeps> & { home?: string } = {}) {
  const home = over.home ?? tmpDir('core-sec-')
  const events: Ev[] = []
  let clock = 1_000_000
  const credentials: [string, string | undefined][] = []
  const core = new LauncherCore({
    home,
    launcherVersion: '1.2.3',
    env: { PATH: process.env.PATH, ELECTRON_RUN_AS_NODE: '1', API_KEY: 'stray-desktop-value' },
    target: TARGET,
    provider: new FakeProvider({ tag: 'v0.0.0', version: '0.0.0', prerelease: false, assets: [] }),
    fetch,
    probeSandbox: () => probe,
    sandboxRunner: async () => ({ exitCode: 0, stderr: '' }),
    emit: (channel, payload) => events.push({ channel, payload } as Ev),
    supervisorTuning: { pollIntervalMs: 25, readyTimeoutMs: 10_000, stopGraceMs: 2000 },
    logFlushMs: 5,
    platform: 'linux',
    networkInterfaces: () => IFACES,
    webuiCredential: async (origin, key) => void credentials.push([origin, key]),
    now: () => clock,
    ...over,
  })
  cores.push(core)
  return {
    core,
    home,
    events,
    credentials,
    tick: (ms: number) => (clock += ms),
    configFile: path.join(home, '.localai', 'launcher.json'),
    secretsFile: path.join(home, '.localai', 'secrets.json'),
  }
}

function writeConfig(home: string, c: object) {
  fs.mkdirSync(path.join(home, '.localai'), { recursive: true })
  fs.writeFileSync(path.join(home, '.localai', 'launcher.json'), JSON.stringify(c), { mode: 0o600 })
}

function fakeBinary(dir: string): string {
  fs.mkdirSync(dir, { recursive: true })
  const f = path.join(dir, 'local-ai')
  fs.writeFileSync(f, `#!/bin/sh\necho "args: $*"\nexec "${process.execPath}" "${FAKE_SERVER}"\n`, { mode: 0o755 })
  return f
}

const until = async (cond: () => boolean, ms = 10_000) => {
  const end = Date.now() + ms
  while (!cond()) {
    if (Date.now() > end) throw new Error('timed out')
    await new Promise((r) => setTimeout(r, 20))
  }
}

const OLD_TOKEN = 'hf_old-token-from-the-go-launcher-0001'
const OLD_KEY = 'old-server-key-0123456789abcdef'

describe('migration out of launcher.json', () => {
  it('moves env values and an API key into secure storage, then removes them from the config', async () => {
    const home = tmpDir('core-sec-')
    writeConfig(home, { environment_vars: { HF_TOKEN: OLD_TOKEN, LOCALAI_THREADS: '4', LOCALAI_API_KEY: OLD_KEY, 'BAD NAME': 'x' } })
    const { core, configFile, secretsFile } = makeCore({ home, safeStorage: fakeSafe() })
    await core.init()
    const cfg = fs.readFileSync(configFile, 'utf8')
    expect(JSON.parse(cfg).environment_vars).toEqual({})
    expect(cfg).not.toContain(OLD_TOKEN)
    expect(cfg).not.toContain(OLD_KEY)
    const sec = fs.readFileSync(secretsFile, 'utf8')
    expect(sec).not.toContain(OLD_TOKEN)
    expect(sec).not.toContain(Buffer.from(OLD_TOKEN).toString('base64'))
    expect(fs.statSync(secretsFile).mode & 0o777).toBe(0o600)
    expect(core.envVarsView().vars.map((v) => v.name)).toEqual(['HF_TOKEN', 'LOCALAI_THREADS'])
    expect(core.networkView()).toMatchObject({ apiKeySet: true, exposed: false })
    const r = core.revealApiKey()
    expect(r).toEqual({ ok: true, key: OLD_KEY })
    const log = core.queryLogs({ limit: 5000 }).map((l) => l.text).join('\n')
    expect(log).toContain('secrets: moved 3 environment variables out of launcher.json, dropped 1')
    expect(log).not.toContain(OLD_TOKEN)
  })

  it('without secure storage, uses the values for this session and leaves the config alone', async () => {
    const home = tmpDir('core-sec-')
    writeConfig(home, { environment_vars: { HF_TOKEN: OLD_TOKEN } })
    const { core, configFile, secretsFile } = makeCore({ home, safeStorage: fakeSafe(false) })
    await core.init()
    expect(JSON.parse(fs.readFileSync(configFile, 'utf8')).environment_vars).toEqual({ HF_TOKEN: OLD_TOKEN })
    expect(fs.existsSync(secretsFile)).toBe(false)
    expect(core.envVarsView()).toMatchObject({ vars: [{ name: 'HF_TOKEN', ignored: false, sessionOnly: true }], storage: { backend: 'session' } })
    // The opt-in moves them into the plain file and out of launcher.json.
    await core.saveSettings({ secretsPlaintextFile: true })
    expect(JSON.parse(fs.readFileSync(configFile, 'utf8')).environment_vars).toEqual({})
    expect(fs.statSync(secretsFile).mode & 0o777).toBe(0o600)
    expect(core.secretStorageView()).toMatchObject({ backend: 'plaintext-file', plaintextOptIn: true })
  })
})

describe('network exposure', () => {
  it('needs a confirmed plan, turns the key on in the same step, and keeps it when going back', async () => {
    const { core, configFile, credentials, tick } = makeCore({ safeStorage: fakeSafe() })
    await core.init()
    expect(core.networkView()).toMatchObject({ exposed: false, apiKeySet: false, lanAddresses: [], address: '127.0.0.1:8080' })
    expect(await core.setNetworkExposed(true)).toEqual({ ok: false, reason: 'plan-required' })
    expect(await core.setNetworkExposed(true, 'f'.repeat(32))).toEqual({ ok: false, reason: 'plan-required' })

    const plan = core.networkPlan()
    expect(plan).toMatchObject({ bindAddress: '0.0.0.0:8080', lanAddresses: ['http://192.0.2.24:8080'], createsKey: true, restartRequired: false })
    expect(plan.planId).toMatch(/^[0-9a-f]{32}$/)
    // Nothing changes before the plan is applied.
    expect(core.networkView().exposed).toBe(false)

    const r = await core.setNetworkExposed(true, plan.planId)
    expect(r.ok && r.createdKey).toBe(true)
    expect(r.ok && r.network).toMatchObject({ exposed: true, apiKeySet: true, address: '0.0.0.0:8080', lanAddresses: ['http://192.0.2.24:8080'] })
    expect(JSON.stringify(r)).not.toMatch(/lai-[A-Za-z0-9_-]{43}/)
    expect(JSON.parse(fs.readFileSync(configFile, 'utf8')).address).toBe('0.0.0.0:8080')
    const key = (core.revealApiKey() as { key: string }).key
    expect(key).toMatch(/^lai-[A-Za-z0-9_-]{43}$/)
    expect(fs.readFileSync(configFile, 'utf8')).not.toContain(key)
    // The WebUI view keeps the loopback origin and gets the key the server gets.
    expect(core.endpoint).toBe('http://127.0.0.1:8080')
    expect(credentials.at(-1)).toEqual(['http://127.0.0.1:8080', key])

    // A plan is used once and expires.
    expect(await core.setNetworkExposed(true, plan.planId)).toEqual({ ok: false, reason: 'plan-required' })
    const late = core.networkPlan()
    tick(NETWORK_PLAN_TTL_MS + 1)
    expect(await core.setNetworkExposed(true, late.planId)).toEqual({ ok: false, reason: 'plan-required' })

    const back = await core.setNetworkExposed(false)
    expect(back.ok && back.network).toMatchObject({ exposed: false, apiKeySet: true, address: '127.0.0.1:8080' })
    tick(2000)
    expect(core.revealApiKey()).toEqual({ ok: true, key })
  })

  it('an exposed address never starts without a key, also with no keyring and after a restart', async () => {
    const home = tmpDir('core-sec-')
    writeConfig(home, { address: '0.0.0.0:8080' })
    const first = makeCore({ home, safeStorage: fakeSafe(false) })
    await first.core.init()
    expect(first.core.networkView()).toMatchObject({ exposed: true, apiKeySet: true, storage: { backend: 'session' } })
    const k1 = (first.core.revealApiKey() as { key: string }).key
    await first.core.shutdown()
    // Next start: the session key is gone, the saved address is not. A new key is made.
    const second = makeCore({ home, safeStorage: fakeSafe(false) })
    await second.core.init()
    expect(second.core.networkView()).toMatchObject({ exposed: true, apiKeySet: true })
    expect((second.core.revealApiKey() as { key: string }).key).not.toBe(k1)
    expect(second.core.queryLogs({ limit: 5000 }).map((l) => l.text)).toContain('api key created: the server listens on the network and had no key it could read')
    // No keyring: a lost key is expected, so no notice in Connect.
    expect(second.core.networkView().keyCreatedAtStart).toBeUndefined()
  })

  it('an older config exposed without a key: a key is made, Connect says so once, the log has no key', async () => {
    const home = tmpDir('core-sec-')
    writeConfig(home, { address: '0.0.0.0:8080' })
    const { core } = makeCore({ home, safeStorage: fakeSafe() })
    await core.init()
    expect(core.networkView()).toMatchObject({ exposed: true, apiKeySet: true, keyCreatedAtStart: true, storage: { backend: 'keychain' } })
    const key = (core.revealApiKey() as { key: string }).key
    const lines = core.queryLogs({ limit: 5000 }).map((l) => l.text)
    expect(lines).toContain('api key created: LocalAI was open to the network without one')
    expect(lines.join('\n')).not.toContain(key)
    expect(core.dismissKeyNotice().keyCreatedAtStart).toBeUndefined()
    expect(core.networkView().keyCreatedAtStart).toBeUndefined()
    await core.shutdown()
    // The key is kept now, so the next start makes none and says nothing.
    const again = makeCore({ home, safeStorage: fakeSafe() })
    await again.core.init()
    expect(again.core.networkView()).toMatchObject({ apiKeySet: true })
    expect(again.core.networkView().keyCreatedAtStart).toBeUndefined()
  })

  it('a typed address that reaches other devices also creates the key', async () => {
    const { core } = makeCore({ safeStorage: fakeSafe() })
    await core.init()
    await core.saveSettings({ address: '0.0.0.0:9090' })
    expect(core.networkView()).toMatchObject({ exposed: true, apiKeySet: true, port: 9090 })
  })

  it('reveal is rate limited and answers no-key without a key', async () => {
    const { core, tick } = makeCore({ safeStorage: fakeSafe() })
    await core.init()
    expect(core.revealApiKey()).toEqual({ ok: false, reason: 'no-key' })
    await core.regenerateApiKey()
    expect(core.revealApiKey().ok).toBe(true)
    expect(core.revealApiKey()).toEqual({ ok: false, reason: 'rate-limited' })
    tick(1000)
    expect(core.revealApiKey().ok).toBe(true)
    expect(core.queryLogs({ limit: 5000 }).map((l) => l.text)).toEqual(expect.arrayContaining(['api key shown', 'api key: show refused, too many requests']))
  })

  it('copy text is the key or an example built in the main process', async () => {
    const { core } = makeCore({ safeStorage: fakeSafe() })
    await core.init()
    expect(core.apiKeyCopyText('key')).toBeUndefined()
    await core.regenerateApiKey()
    const key = core.apiKeyCopyText('key')!
    expect(core.apiKeyCopyText('openai')).toBe(`export OPENAI_BASE_URL=http://127.0.0.1:8080/v1\nexport OPENAI_API_KEY=${key}`)
    expect(core.apiKeyCopyText('curl')).toContain(`Authorization: Bearer ${key}`)
  })
})

describe('environment variables', () => {
  it('refuses names the launcher controls and reports reasons', async () => {
    const { core } = makeCore({ safeStorage: fakeSafe() })
    await core.init()
    for (const [n, reason] of [['PATH', 'name-controlled'], ['LD_LIBRARY_PATH', 'name-controlled'], ['ELECTRON_RUN_AS_NODE', 'name-controlled'], ['APPIMAGE', 'name-controlled'], ['LOCALAI_API_KEY', 'name-controlled'], ['1A', 'name-format']] as const) {
      expect(await core.setEnvVar(n, 'x'), n).toEqual({ ok: false, reason })
    }
    expect(core.envVarsView().vars).toEqual([])
    const r = await core.setEnvVar('LOCALAI_THREADS', '8')
    expect(r.ok && r.view.vars).toEqual([{ name: 'LOCALAI_THREADS', ignored: false, sessionOnly: false }])
    expect((await core.deleteEnvVar('LOCALAI_THREADS')).vars).toEqual([])
  })

  it('a change while the server runs needs a restart', async () => {
    const home = tmpDir('core-sec-')
    const port = await freePort()
    const { core } = makeCore({ home, safeStorage: fakeSafe(), serverBinOverride: fakeBinary(path.join(home, 'bin')), endpointOverride: `http://127.0.0.1:${port}` })
    await core.init()
    await core.setEnvVar('FAKE_PORT', String(port))
    expect(core.envVarsView().restartRequired).toBe(false)
    await core.startServer()
    await until(() => core.snapshot().state === 'ready')
    const r = await core.setEnvVar('LOCALAI_THREADS', '8')
    expect(r.ok && r.view.restartRequired).toBe(true)
    expect((await core.regenerateApiKey()).restartRequired).toBe(true)
    await core.restartServer()
    await until(() => core.snapshot().state === 'ready')
    expect(core.networkView().restartRequired).toBe(false)
  })
})

describe('secure storage that says yes and then fails', () => {
  // Electron 45 on a host with no keyring: the backend is reported as libsecret and the
  // availability flags say yes, but encryptString throws. 'always': it throws for the start
  // probe too. 'later': the probe passes and the real values fail.
  function lyingSafe(when: 'always' | 'later'): SafeStorageLike {
    const s = fakeSafe()
    return {
      ...s,
      isEncryptionAvailable: () => true,
      encryptString: (v) => {
        if (when === 'later' && v === SAFE_STORAGE_PROBE) return s.encryptString(v)
        throw new Error('Encryption is not available.')
      },
    }
  }

  for (const when of ['always', 'later'] as const) {
    it(`the server still gets the user's variables and the API key by its environment (${when})`, async () => {
      const home = tmpDir('core-sec-')
      const port = await freePort()
      const envFile = path.join(home, 'server-env.json')
      const old = { FAKE_PORT: String(port), FAKE_ENV_FILE: envFile, FAKE_ENV_KEYS: 'LOCALAI_API_KEY,HF_TOKEN', HF_TOKEN: OLD_TOKEN, LOCALAI_API_KEY: OLD_KEY }
      writeConfig(home, { environment_vars: old })
      const { core, configFile, secretsFile } = makeCore({
        home,
        safeStorage: lyingSafe(when),
        serverBinOverride: fakeBinary(path.join(home, 'bin')),
        endpointOverride: `http://127.0.0.1:${port}`,
      })
      await core.init()
      expect(core.secretStorageView()).toEqual({ backend: 'session', encryptionAvailable: false, plaintextOptIn: false, unreadable: 0 })
      expect(core.envVarsView().vars.every((v) => v.sessionOnly)).toBe(true)
      // Nothing could be stored for good: the old plain values stay in launcher.json, no secrets file.
      expect(JSON.parse(fs.readFileSync(configFile, 'utf8')).environment_vars).toEqual(old)
      expect(fs.existsSync(secretsFile)).toBe(false)

      await core.startServer()
      await until(() => core.snapshot().state === 'ready')
      expect(JSON.parse(fs.readFileSync(envFile, 'utf8'))).toEqual({ LOCALAI_API_KEY: OLD_KEY, HF_TOKEN: OLD_TOKEN })
      const log = core.queryLogs({ limit: 5000 }).map((l) => l.text).join('\n')
      expect(log).toContain('environment variables are used for this session and stay in launcher.json')
      expect(log).not.toContain(OLD_TOKEN)
      expect(log).not.toContain(OLD_KEY)
    })
  }
})

describe('a secret value never leaks', () => {
  it('reaches the server by its environment only: not args, logs, config, renderer views or the process command line', async () => {
    const home = tmpDir('core-sec-')
    const port = await freePort()
    const envFile = path.join(home, 'server-env.json')
    const pidsFile = path.join(home, 'pids.json')
    // A name the log hub's own key=value patterns do not catch, so this checks the scrub by value.
    const TOKEN = 'plain-looking-value-9f8e7d6c5b4a'
    const { core, configFile, secretsFile, events } = makeCore({
      home,
      safeStorage: fakeSafe(),
      serverBinOverride: fakeBinary(path.join(home, 'bin')),
      endpointOverride: `http://127.0.0.1:${port}`,
    })
    await core.init()
    for (const [k, v] of Object.entries({
      FAKE_PORT: String(port),
      FAKE_ENV_FILE: envFile,
      FAKE_ENV_KEYS: 'LOCALAI_API_KEY,API_KEY,MIRROR_URL,ELECTRON_RUN_AS_NODE',
      FAKE_ECHO_ENV: 'MIRROR_URL',
      FAKE_PIDS_FILE: pidsFile,
      MIRROR_URL: TOKEN,
    })) {
      expect((await core.setEnvVar(k, v)).ok, k).toBe(true)
    }
    const plan = core.networkPlan()
    expect((await core.setNetworkExposed(true, plan.planId)).ok).toBe(true)
    const key = (core.revealApiKey() as { key: string }).key

    await core.startServer()
    await until(() => core.snapshot().state === 'ready')
    await until(() => core.queryLogs({ source: 'server' }).some((l) => l.text.startsWith('fake-server: MIRROR_URL=')))

    // The server got both by environment; the stray desktop API_KEY and the Electron variable did not pass.
    expect(JSON.parse(fs.readFileSync(envFile, 'utf8'))).toEqual({ LOCALAI_API_KEY: key, API_KEY: null, MIRROR_URL: TOKEN, ELECTRON_RUN_AS_NODE: null })

    // Not on the command line: the launch args and the server's real command line.
    const argsLine = core.queryLogs({ source: 'server' }).find((l) => l.text.startsWith('args: '))!.text
    const pids = JSON.parse(fs.readFileSync(pidsFile, 'utf8')) as { server: number }
    const cmdline = commandLine(pids.server)
    for (const text of [argsLine, cmdline]) {
      expect(text).not.toContain(key)
      expect(text).not.toContain(TOKEN)
    }

    // Not in the logs: the server printed the token, the log has it scrubbed.
    const all = core.queryLogs({ limit: 5000 }).map((l) => l.text).join('\n')
    expect(all).toContain(`fake-server: MIRROR_URL=${REDACTED}`)
    expect(all).not.toContain(TOKEN)
    expect(all).not.toContain(key)
    await until(() => events.some((e) => e.channel === 'launcher:logs:event'))
    const logDir = path.join(home, '.localai', 'logs')
    for (const f of fs.readdirSync(logDir)) {
      const t = fs.readFileSync(path.join(logDir, f), 'utf8')
      expect(t, f).not.toContain(TOKEN)
      expect(t, f).not.toContain(key)
    }

    // Not in the files: the config has neither, the secrets file has no plain text.
    for (const f of [configFile, secretsFile]) {
      const t = fs.readFileSync(f, 'utf8')
      expect(t, f).not.toContain(TOKEN)
      expect(t, f).not.toContain(key)
      expect(t, f).not.toContain(Buffer.from(TOKEN).toString('base64'))
    }

    // Not in anything the renderer gets without asking: views, settings, snapshots and every event so far.
    const views = JSON.stringify([core.getSettings(), core.envVarsView(), core.networkView(), core.networkPlan(), core.snapshot(), core.secretStorageView(), events])
    expect(views).not.toContain(TOKEN)
    expect(views).not.toContain(key)
  })

  it('logs carry names and counts only', async () => {
    const write = vi.fn()
    const { core } = makeCore({ safeStorage: fakeSafe() })
    await core.init()
    await core.setEnvVar('HF_TOKEN', 'hf_a-value-that-must-not-be-logged')
    await core.regenerateApiKey()
    write(core.queryLogs({ limit: 5000 }).map((l) => l.text))
    const text = write.mock.calls[0][0].join('\n') as string
    expect(text).toContain('environment variable HF_TOKEN set')
    expect(text).toContain('api key replaced')
    expect(text).not.toContain('hf_a-value-that-must-not-be-logged')
  })
})
