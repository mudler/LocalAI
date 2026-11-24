// The core's use of the registry credentials: the generated file, the secret
// variables of the server, the scrub of server output, restarts after a change,
// and a failed write in init.
import fs from 'node:fs'
import path from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { LauncherCore, REDACTED, type CoreDeps } from '../src/main/core'
import type { SupervisorHandle } from '../src/main/serverInstall'
import { CREDENTIALS_FILE_NAME } from '../src/main/credentialsFile'
import { type SafeStorageLike } from '../src/main/secretStore'
import type { SandboxProbe } from '../src/main/sandboxProbe'
import { FAKE_SERVER, freePort, tmpDir } from './helpers'
import { FakeProvider, TARGET } from './serverInstallHarness'

const probe: SandboxProbe = { state: 'sandboxed', inputs: { platform: 'linux', sysctl: 0, noSandboxFlag: false, profile: 'unknown' } }

function fakeSafe(): SafeStorageLike {
  const tag = Buffer.from('ENC1:')
  return {
    isEncryptionAvailable: () => true,
    getSelectedStorageBackend: () => 'gnome_libsecret',
    encryptString: (s) => Buffer.concat([tag, Buffer.from(s, 'utf8').map((b) => b ^ 0x5a)]),
    decryptString: (b) => Buffer.from(b.subarray(tag.length).map((x) => x ^ 0x5a)).toString('utf8'),
  }
}

const cores: LauncherCore[] = []
afterEach(async () => {
  for (const c of cores.splice(0)) await c.shutdown()
})

function makeCore(home: string, over: Partial<CoreDeps> = {}) {
  const core = new LauncherCore({
    home,
    launcherVersion: '1.2.3',
    env: { PATH: process.env.PATH },
    target: TARGET,
    provider: new FakeProvider({ tag: 'v0.0.0', version: '0.0.0', prerelease: false, assets: [] }),
    fetch,
    probeSandbox: () => probe,
    sandboxRunner: async () => ({ exitCode: 0, stderr: '' }),
    emit: () => undefined,
    supervisorTuning: { pollIntervalMs: 25, readyTimeoutMs: 10_000, stopGraceMs: 2000 },
    logFlushMs: 5,
    platform: 'linux',
    networkInterfaces: () => ({}),
    webuiCredential: async () => undefined,
    safeStorage: fakeSafe(),
    ...over,
  })
  cores.push(core)
  return core
}

function fakeBinary(dir: string): string {
  fs.mkdirSync(dir, { recursive: true })
  const f = path.join(dir, 'local-ai')
  fs.writeFileSync(f, `#!/bin/sh\nexec "${process.execPath}" "${FAKE_SERVER}"\n`, { mode: 0o755 })
  return f
}

const until = async (cond: () => boolean, ms = 10_000) => {
  const end = Date.now() + ms
  while (!cond()) {
    if (Date.now() > end) throw new Error('timed out')
    await new Promise((r) => setTimeout(r, 20))
  }
}

const SECRET_ONE = 'registry-secret-one-0123456789'
const SECRET_TWO = 'registry-secret-two-9876543210'

async function setup() {
  const home = tmpDir('core-reg-')
  const port = await freePort()
  const envFile = path.join(home, 'server-env.json')
  const core = makeCore(home, { serverBinOverride: fakeBinary(path.join(home, 'bin')), endpointOverride: `http://127.0.0.1:${port}` })
  await core.init()
  for (const [k, v] of Object.entries({
    FAKE_PORT: String(port),
    FAKE_ENV_FILE: envFile,
    FAKE_ENV_KEYS: 'LOCALAI_CREDENTIALS_FILE,LOCALAI_LAUNCHER_CRED_1,LOCALAI_LAUNCHER_CRED_2',
    FAKE_ECHO_ENV: 'LOCALAI_LAUNCHER_CRED_1',
  })) {
    expect((await core.setEnvVar(k, v)).ok, k).toBe(true)
  }
  const file = path.join(home, '.localai', CREDENTIALS_FILE_NAME)
  const serverEnv = () => JSON.parse(fs.readFileSync(envFile, 'utf8')) as Record<string, string | null>
  return { home, core, file, envFile, serverEnv }
}

describe('registry credential actions of the core', () => {
  it('save and remove report whether a restart is needed and never return a secret', async () => {
    const { core } = await setup()
    const saved = await core.saveRegistryCredential({ match: 'registry.example', kind: 'bearer', secret: SECRET_ONE })
    expect(saved).toEqual({ ok: true, id: expect.stringMatching(/^[0-9a-f]{16}$/), restartRequired: false })
    const view = core.registryCredentialsView()
    expect(view).toMatchObject({ userOverride: false, rules: [{ match: 'registry.example', kind: 'bearer', secretSet: true }] })
    expect(JSON.stringify(view)).not.toContain(SECRET_ONE)

    await core.startServer()
    await until(() => core.snapshot().state === 'ready')
    const id = (saved as { id: string }).id
    expect(await core.removeRegistryCredential(id)).toEqual({ restartRequired: true })
    expect(core.registryCredentialsView().rules).toEqual([])
    expect(await core.saveRegistryCredential({ match: 'bad host name', kind: 'bearer', secret: SECRET_TWO })).toMatchObject({ ok: false })
  })

  it('reports a user credentials file and tests an unknown rule as other', async () => {
    const { core } = await setup()
    expect(await core.testRegistryCredential('0123456789abcdef')).toEqual({ result: 'other' })
    expect(core.registryCredentialsView().userOverride).toBe(false)
    const own = makeCore(tmpDir('core-reg-own-'), { env: { PATH: process.env.PATH, LOCALAI_CREDENTIALS_FILE: '/from-env.yaml' } })
    await own.init()
    expect(own.registryCredentialsView().userOverride).toBe(true)
  })

  it('lists a LOCALAI_CREDENTIALS_FILE kept from an older version as passed, not ignored', async () => {
    const home = tmpDir('core-reg-old-')
    fs.mkdirSync(path.join(home, '.localai'), { recursive: true })
    fs.writeFileSync(path.join(home, '.localai', 'launcher.json'), JSON.stringify({ environment_vars: { LOCALAI_CREDENTIALS_FILE: '/own/credentials.yaml', PATH: '/x' } }), { mode: 0o600 })
    const core = makeCore(home)
    await core.init()
    const vars = core.envVarsView().vars
    expect(vars.find((v) => v.name === 'LOCALAI_CREDENTIALS_FILE')).toMatchObject({ ignored: false })
    expect(core.registryCredentialsView().userOverride).toBe(true)
  })
})

describe('registry credentials in the core', () => {
  it('a stopped server gets the file and the secret variables, and its output is scrubbed', async () => {
    const { core, file, serverEnv } = await setup()
    const rules = core.registryCredentialRules()
    const saved = await rules.save({ match: 'registry.example', kind: 'bearer', secret: SECRET_ONE })
    expect(saved.ok).toBe(true)
    expect(await core.registryCredentialsChanged()).toBe(false)
    await core.startServer()
    await until(() => core.snapshot().state === 'ready')
    expect(serverEnv()).toEqual({ LOCALAI_CREDENTIALS_FILE: file, LOCALAI_LAUNCHER_CRED_1: SECRET_ONE, LOCALAI_LAUNCHER_CRED_2: null })
    expect(fs.statSync(file).mode & 0o777).toBe(0o600)
    expect(fs.readFileSync(file, 'utf8')).not.toContain(SECRET_ONE)
    // The value is scrubbed wherever it shows up in server output, whatever the line looks like.
    expect(core.redact(`pull failed with ${SECRET_ONE} in the header`)).toBe(`pull failed with ${REDACTED} in the header`)
    const log = core.queryLogs({ limit: 5000 }).map((l) => l.text).join('\n')
    expect(log).not.toContain(SECRET_ONE)
  })

  it('a rule saved while the server runs marks the launch stale, and a restart uses it', async () => {
    const { core, file, serverEnv } = await setup()
    await core.startServer()
    await until(() => core.snapshot().state === 'ready')
    expect(serverEnv().LOCALAI_CREDENTIALS_FILE).toBeNull()
    expect(fs.existsSync(file)).toBe(false)

    await core.registryCredentialRules().save({ match: 'registry.example', kind: 'bearer', secret: SECRET_ONE })
    expect(await core.registryCredentialsChanged()).toBe(true)

    await core.restartServer()
    await until(() => core.snapshot().state === 'ready' && serverEnv().LOCALAI_LAUNCHER_CRED_1 === SECRET_ONE)
    expect(serverEnv().LOCALAI_CREDENTIALS_FILE).toBe(file)
  })

  it('removing the last rule removes the file and the variable at the next start', async () => {
    const { core, file, serverEnv } = await setup()
    const rules = core.registryCredentialRules()
    const { id } = (await rules.save({ match: 'registry.example', kind: 'bearer', secret: SECRET_ONE })) as { id: string }
    await core.registryCredentialsChanged()
    await core.startServer()
    await until(() => core.snapshot().state === 'ready')
    expect(fs.existsSync(file)).toBe(true)

    await rules.remove(id)
    expect(await core.registryCredentialsChanged()).toBe(true)
    expect(fs.existsSync(file)).toBe(false)
    await core.restartServer()
    await until(() => core.snapshot().state === 'ready' && serverEnv().LOCALAI_CREDENTIALS_FILE === null)
    expect(serverEnv().LOCALAI_LAUNCHER_CRED_1).toBeNull()
  })

  it('the installer restart after the last rule was removed does not name the removed file', async () => {
    const { core, file, serverEnv } = await setup()
    const rules = core.registryCredentialRules()
    const { id } = (await rules.save({ match: 'registry.example', kind: 'bearer', secret: SECRET_ONE })) as { id: string }
    await core.startServer()
    await until(() => core.snapshot().state === 'ready' && serverEnv().LOCALAI_CREDENTIALS_FILE === file)

    await core.removeRegistryCredential(id)
    expect(fs.existsSync(file)).toBe(false)
    // What an update or a rollback does: stop, then start through the installer's handle.
    const handle = (core as unknown as { installer: { d: { supervisor: SupervisorHandle } } }).installer.d.supervisor
    await handle.stop()
    await handle.start()
    expect(handle.state).toBe('ready')
    const env = serverEnv()
    expect(env.LOCALAI_CREDENTIALS_FILE === null || fs.existsSync(env.LOCALAI_CREDENTIALS_FILE)).toBe(true)
    expect(env.LOCALAI_CREDENTIALS_FILE).toBeNull()
    expect(env.LOCALAI_LAUNCHER_CRED_1).toBeNull()
  })

  it('the installer restart after a rule was added while running uses the new file', async () => {
    const { core, file, serverEnv } = await setup()
    await core.startServer()
    await until(() => core.snapshot().state === 'ready')
    expect(serverEnv().LOCALAI_CREDENTIALS_FILE).toBeNull()
    await core.saveRegistryCredential({ match: 'registry.example', kind: 'bearer', secret: SECRET_ONE })
    const handle = (core as unknown as { installer: { d: { supervisor: SupervisorHandle } } }).installer.d.supervisor
    await handle.stop()
    await handle.start()
    expect(serverEnv()).toMatchObject({ LOCALAI_CREDENTIALS_FILE: file, LOCALAI_LAUNCHER_CRED_1: SECRET_ONE })
    expect(core.registryCredentialsView().restartRequired).toBe(false)
  })

  it('a restart that did not go through startServer first still sees a changed rule', async () => {
    const { core, serverEnv } = await setup()
    await core.registryCredentialRules().save({ match: 'registry.example', kind: 'bearer', secret: SECRET_ONE })
    // No call to registryCredentialsChanged: the start itself refreshes.
    await core.restartServer()
    await until(() => core.snapshot().state === 'ready')
    expect(serverEnv().LOCALAI_LAUNCHER_CRED_1).toBe(SECRET_ONE)
  })

  it('a failed write in init reaches the launcher log with the error code and no secret', async () => {
    const home = tmpDir('core-reg-')
    const first = makeCore(home)
    await first.init()
    await first.registryCredentialRules().save({ match: 'registry.example', kind: 'bearer', secret: SECRET_TWO })
    await first.shutdown()
    // A folder where the file goes makes the atomic rename fail.
    const file = path.join(home, '.localai', CREDENTIALS_FILE_NAME)
    fs.rmSync(file, { force: true })
    fs.mkdirSync(file)
    const second = makeCore(home)
    await second.init()
    const log = second.queryLogs({ limit: 5000 }).map((l) => l.text).join('\n')
    expect(log).toMatch(/registry credentials: could not write registry-credentials\.yaml \([A-Z]+\)/)
    expect(log).not.toContain(SECRET_TWO)
  })
})
