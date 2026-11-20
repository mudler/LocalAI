// Servers, the default target and the update channel, as the core runs them.
import fs from 'node:fs'
import path from 'node:path'
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import type { EventContract } from '../src/shared/ipc'
import { LauncherCore, type CoreDeps } from '../src/main/core'
import type { TestResult } from '../src/main/testConnection'
import { FAKE_SERVER, freePort, tmpDir } from './helpers'
import { FakeProvider, FeedServer, TARGET, bin, devProvider, publish, publishDev, readVersionFromFile } from './serverInstallHarness'

const cores: LauncherCore[] = []
afterEach(async () => {
  for (const c of cores.splice(0)) await c.shutdown()
})
const feed = new FeedServer()
beforeAll(() => feed.start())
afterAll(() => feed.stop())

const OK: TestResult = { status: 'ok', version: '4.2.1', secure: true }
const sandboxed = { state: 'sandboxed' as const, inputs: { platform: 'linux' as const, sysctl: 0, noSandboxFlag: false, profile: 'unknown' as const } }

interface Rig {
  core: LauncherCore
  home: string
  configFile: string
  binary: string
  port: number
  local: string
  effects: { kind: string; origin?: string }[]
  probes: string[]
  provider: FakeProvider
  results: Map<string, TestResult>
  server: <C extends keyof EventContract>(c: C) => EventContract[C][]
}

async function rig(config: Record<string, unknown> = {}, over: Partial<CoreDeps> = {}): Promise<Rig> {
  const home = over.home ?? tmpDir('servers-')
  const port = await freePort()
  const binDir = path.join(home, 'bin')
  fs.mkdirSync(binDir, { recursive: true })
  const binary = path.join(binDir, 'local-ai')
  fs.writeFileSync(binary, `#!/bin/sh\nexec "${process.execPath}" "${FAKE_SERVER}"\n`, { mode: 0o755 })
  fs.mkdirSync(path.join(home, '.localai'), { recursive: true })
  const configFile = path.join(home, '.localai', 'launcher.json')
  if (Object.keys(config).length) fs.writeFileSync(configFile, JSON.stringify(config))
  const local = `http://127.0.0.1:${port}`
  const effects: Rig['effects'] = []
  const probes: string[] = []
  const results = new Map<string, TestResult>()
  const events: { channel: string; payload: unknown }[] = []
  const provider = new FakeProvider({ tag: 'v0.0.0', version: '0.0.0', prerelease: false, assets: [] })
  const core = new LauncherCore({
    home,
    launcherVersion: '1.2.3',
    env: { PATH: process.env.PATH, FAKE_PORT: String(port), FAKE_ECHO_ENV: 'LOCALAI_PREFER_DEV_BACKENDS' },
    target: TARGET,
    provider,
    fetch,
    probeSandbox: () => sandboxed,
    sandboxRunner: async () => ({ exitCode: 0, stderr: '' }),
    emit: (channel, payload) => events.push({ channel, payload }),
    supervisorTuning: { pollIntervalMs: 25, readyTimeoutMs: 10_000, stopGraceMs: 2000 },
    logFlushMs: 5,
    serverBinOverride: binary,
    endpointOverride: local,
    webuiEffects: (fx) => effects.push(...fx),
    testServer: async (origin) => {
      probes.push(origin)
      return results.get(origin) ?? OK
    },
    remoteRetryMs: 60,
    runVersion: async (f) => readVersionFromFile(f),
    ...over,
  })
  cores.push(core)
  return { core, home, configFile, binary, port, local, effects, probes, provider, results, server: (c) => events.filter((e) => e.channel === c).map((e) => e.payload) as never }
}

const until = async (cond: () => boolean, ms = 10_000) => {
  const end = Date.now() + ms
  while (!cond()) {
    if (Date.now() > end) throw new Error('timed out')
    await new Promise((r) => setTimeout(r, 20))
  }
}
const cfg = (r: Rig) => JSON.parse(fs.readFileSync(r.configFile, 'utf8'))
const REMOTE = { id: 'srv_aaaaaaaa', name: 'Office', url: 'https://office.example.com' }
const REMOTE2 = { id: 'srv_bbbbbbbb', name: 'Lab', url: 'https://lab.example.com' }

describe('a remote default: the local server is not touched', () => {
  it('migrates the old remote address into the list without making it the default', async () => {
    const r = await rig({ remote_server_url: 'https://office.example.com', auto_start_server: true })
    await r.core.init()
    const c = cfg(r)
    expect(c.remotes).toMatchObject([{ name: 'office.example.com', url: 'https://office.example.com' }])
    expect('default_target' in c).toBe(false)
    expect(c.remote_server_url).toBe('https://office.example.com')
    expect(r.core.webuiState().target.kind).toBe('local')
    expect(r.core.serversView().defaultId).toBe('local')
    await r.core.autoStart()
    await until(() => r.core.snapshot().state === 'ready')
  })

  it('opens a remote default at once, and never starts, checks or installs the local server', async () => {
    const r = await rig({ remotes: [REMOTE], default_target: REMOTE.id, auto_start_server: true })
    await r.core.init()
    r.core.openDefaultRemote()
    expect(r.core.webuiState().target).toMatchObject({ kind: 'remote', host: 'office.example.com' })
    expect(r.effects).toEqual([{ kind: 'show', origin: 'https://office.example.com', url: 'https://office.example.com/app' }])
    await r.core.autoStart()
    await r.core.startServer()
    expect(r.core.snapshot().state).toBe('stopped')
    expect(r.core.autoCheckDue(Date.now() + 1e12)).toBe(false)
    await r.core.checkServerUpdate({})
    r.core.installServer({})
    await r.core.installIdle()
    expect(r.provider.lookups).toBe(0)
  })

  it('does not open the remote default during init, before the shell has a window to show it in', async () => {
    const r = await rig({ remotes: [REMOTE], default_target: REMOTE.id })
    await r.core.init()
    // An effect emitted now has no window to land in and is lost, while the model would count the view as up.
    expect(r.effects).toEqual([])
    expect(r.probes).toEqual([])
    r.core.openDefaultRemote()
    expect(r.effects).toEqual([{ kind: 'show', origin: 'https://office.example.com', url: 'https://office.example.com/app' }])
    await until(() => r.probes.length > 0)
    expect(r.probes).toEqual(['https://office.example.com'])
  })

  it('openDefaultRemote does nothing when this computer is the default', async () => {
    const r = await rig({ remotes: [REMOTE] })
    await r.core.init()
    r.core.openDefaultRemote()
    expect(r.effects).toEqual([])
    expect(r.probes).toEqual([])
  })

  it('"keep local running" gives the local server its normal rules back', async () => {
    const r = await rig({ remotes: [REMOTE], default_target: REMOTE.id, keep_local_running: true })
    await r.core.init()
    await r.core.autoStart()
    await until(() => r.core.snapshot().state === 'ready')
    expect(r.core.serversView().keepLocal).toBe(true)
  })

  it('turning "keep local running" off stops a running local server; turning it on starts it', async () => {
    const r = await rig({ remotes: [REMOTE], default_target: REMOTE.id })
    await r.core.init()
    await r.core.setKeepLocalRunning(true)
    await until(() => r.core.snapshot().state === 'ready')
    expect(cfg(r).keep_local_running).toBe(true)
    await r.core.setKeepLocalRunning(false)
    expect(r.core.snapshot().state).toBe('stopped')
    expect('keep_local_running' in cfg(r)).toBe(false)
  })

  it('does not auto-start the local server, and the old remote address does not win over a recorded choice', async () => {
    const r = await rig({ remote_server_url: 'https://office.example.com', default_target: 'local' })
    await r.core.init()
    expect(r.core.webuiState().target.kind).toBe('local')
    await r.core.autoStart()
    await until(() => r.core.snapshot().state === 'ready')
  })
})

describe('switching the default', () => {
  it('stops the local server after the new page is on show, unless it is kept on', async () => {
    const r = await rig({ remotes: [REMOTE] })
    await r.core.init()
    await r.core.autoStart()
    await until(() => r.core.snapshot().state === 'ready')
    const res = await r.core.setDefaultServer(REMOTE.id)
    expect(res.ok).toBe(true)
    // The page is still loading: the old server runs, and the card shows.
    expect(r.core.serversView().switching).toMatchObject({ targetId: REMOTE.id, step: 'loading', name: 'Office' })
    expect(r.core.snapshot().state).toBe('ready')
    r.core.webuiDispatch({ type: 'view-loaded' })
    await until(() => r.core.snapshot().state === 'stopped')
    expect(r.core.serversView()).toMatchObject({ defaultId: REMOTE.id, switching: null })
    expect(cfg(r).default_target).toBe(REMOTE.id)
  })

  it('keeps the local server running when the user opted in', async () => {
    const r = await rig({ remotes: [REMOTE], keep_local_running: true })
    await r.core.init()
    await r.core.autoStart()
    await until(() => r.core.snapshot().state === 'ready')
    await r.core.setDefaultServer(REMOTE.id)
    r.core.webuiDispatch({ type: 'view-loaded' })
    await new Promise((x) => setTimeout(x, 100))
    expect(r.core.snapshot().state).toBe('ready')
  })

  it('switching to This computer makes it the default and starts it', async () => {
    const r = await rig({ remotes: [REMOTE], default_target: REMOTE.id })
    await r.core.init()
    expect(r.core.snapshot().state).toBe('stopped')
    const res = await r.core.setDefaultServer('local')
    expect(res.ok).toBe(true)
    await until(() => r.core.snapshot().state === 'ready')
    expect(cfg(r).default_target).toBe('local')
    expect(r.core.webuiState().target.kind).toBe('local')
  })

  it('does not switch to a server that does not answer, and does not touch the local server', async () => {
    const r = await rig({ remotes: [REMOTE] })
    r.results.set(REMOTE.url, { status: 'unreachable', reason: 'timeout' })
    await r.core.init()
    await r.core.autoStart()
    await until(() => r.core.snapshot().state === 'ready')
    expect(await r.core.setDefaultServer(REMOTE.id)).toMatchObject({ ok: false, reason: 'test-failed', test: { status: 'unreachable' } })
    expect(r.core.serversView()).toMatchObject({ defaultId: 'local', switching: null })
    expect(r.core.snapshot().state).toBe('ready')
    expect(r.core.serversView().rows[1].status).toBe('unreachable')
  })

  it('a server that needs a sign-in still opens', async () => {
    const r = await rig({ remotes: [REMOTE] })
    r.results.set(REMOTE.url, { status: 'needs-signin' })
    await r.core.init()
    expect((await r.core.setDefaultServer(REMOTE.id)).ok).toBe(true)
  })

  it('cancel while reaching the server keeps the previous default', async () => {
    let release!: (x: TestResult) => void
    const r = await rig({ remotes: [REMOTE] }, { testServer: (_o, x) => new Promise<TestResult>((res) => {
      release = res
      x.signal?.addEventListener('abort', () => res({ status: 'cancelled' }))
    }) })
    await r.core.init()
    const p = r.core.setDefaultServer(REMOTE.id)
    await until(() => r.core.serversView().switching?.step === 'reaching')
    await r.core.cancelSwitch()
    expect(await p).toEqual({ ok: false, reason: 'cancelled' })
    expect(r.core.serversView()).toMatchObject({ defaultId: 'local', switching: null })
    void release
  })

  it('cancel while the page loads goes back to the previous server', async () => {
    const r = await rig({ remotes: [REMOTE, REMOTE2], default_target: REMOTE2.id })
    await r.core.init()
    await r.core.setDefaultServer(REMOTE.id)
    await r.core.cancelSwitch()
    expect(r.core.serversView()).toMatchObject({ defaultId: REMOTE2.id, switching: null })
    expect(cfg(r).default_target).toBe(REMOTE2.id)
    expect(r.core.webuiState().target).toMatchObject({ origin: REMOTE2.url })
  })

  it('refuses a second switch while one is under way', async () => {
    const r = await rig({ remotes: [REMOTE, REMOTE2] })
    await r.core.init()
    await r.core.setDefaultServer(REMOTE.id)
    expect(await r.core.setDefaultServer(REMOTE2.id)).toMatchObject({ ok: false, reason: 'busy' })
  })
})

describe('saving servers', () => {
  it('saves a server only after a passing test and keeps nothing secret', async () => {
    const r = await rig()
    await r.core.init()
    r.results.set('https://bad.example.com', { status: 'not-localai', reason: 'answer' })
    expect(await r.core.addServer({ name: 'Bad', address: 'https://bad.example.com' })).toMatchObject({ ok: false, reason: 'test-failed', test: { status: 'not-localai' } })
    expect(fs.existsSync(r.configFile) ? cfg(r).remotes : undefined).toBeUndefined()
    const ok = await r.core.addServer({ name: 'Office', address: 'https://office.example.com/app/chat?token=abc' })
    expect(ok).toMatchObject({ ok: true, test: { status: 'ok' } })
    expect(cfg(r).remotes).toMatchObject([{ name: 'Office', url: 'https://office.example.com' }])
    expect(JSON.stringify(cfg(r))).not.toContain('abc')
    expect(r.core.serversView().rows.map((x) => x.name)).toEqual(['This computer', 'Office'])
    expect(r.core.serversView().defaultId).toBe('local')
  })

  it('"Save without testing" saves a server that is off', async () => {
    const r = await rig()
    await r.core.init()
    r.results.set('https://off.example.com', { status: 'unreachable', reason: 'refused' })
    expect(await r.core.addServer({ name: 'Off', address: 'https://off.example.com', skipTest: true })).toMatchObject({ ok: true })
    expect(r.probes).toEqual([])
  })

  it('refuses duplicates, bad names and bad addresses without testing', async () => {
    const r = await rig({ remotes: [REMOTE] })
    await r.core.init()
    expect(await r.core.addServer({ name: 'Again', address: 'https://office.example.com/other' })).toMatchObject({ ok: false, reason: 'duplicate', existingId: REMOTE.id })
    expect(await r.core.addServer({ name: '', address: 'https://x.example.com' })).toMatchObject({ ok: false, reason: 'name' })
    expect(await r.core.addServer({ name: 'x', address: 'ftp://x.example.com' })).toMatchObject({ ok: false, reason: 'scheme' })
    expect(await r.core.addServer({ name: 'x', address: 'user:pw@x.example.com' })).toMatchObject({ ok: false })
    expect(r.probes).toEqual([])
  })

  it('"Connect" saves, makes it the default and opens it', async () => {
    const r = await rig()
    await r.core.init()
    const res = await r.core.addServer({ name: 'Office', address: 'https://office.example.com', makeDefault: true })
    expect(res).toMatchObject({ ok: true })
    expect(r.core.serversView().defaultId).toBe((res as { id: string }).id)
    expect(r.core.webuiState().target).toMatchObject({ kind: 'remote' })
    // Tested once by the save; the monitor then watches the server it opened.
    expect(r.probes.filter((p) => p === 'https://office.example.com')).toHaveLength(2)
  })

  it('an address change is tested first; a failing one is not saved', async () => {
    const r = await rig({ remotes: [REMOTE] })
    await r.core.init()
    r.results.set('https://new.example.com', { status: 'tls', reason: 'expired' })
    expect(await r.core.updateServer({ id: REMOTE.id, address: 'https://new.example.com' })).toMatchObject({ ok: false, reason: 'test-failed' })
    expect(r.core.serversView().rows[1].origin).toBe(REMOTE.url)
    expect(await r.core.updateServer({ id: REMOTE.id, name: 'Renamed' })).toMatchObject({ ok: true })
    expect(r.core.serversView().rows[1].name).toBe('Renamed')
    expect(await r.core.updateServer({ id: 'srv_zzzzzzzz', name: 'x' })).toMatchObject({ ok: false, reason: 'not-found' })
  })

  it('removing the default falls back to This computer and starts it', async () => {
    const r = await rig({ remotes: [REMOTE], default_target: REMOTE.id })
    await r.core.init()
    expect(await r.core.removeServer(REMOTE.id)).toMatchObject({ ok: true })
    expect(r.core.serversView()).toMatchObject({ defaultId: 'local' })
    expect(cfg(r).remotes).toEqual([])
    await until(() => r.core.snapshot().state === 'ready')
  })

  it('tests a saved server by id and a typed address, and records the answer in the list', async () => {
    const r = await rig({ remotes: [REMOTE] })
    await r.core.init()
    r.results.set(REMOTE.url, { status: 'needs-signin' })
    expect(await r.core.testServer({ id: REMOTE.id })).toMatchObject({ ok: true, insecure: false, result: { status: 'needs-signin' } })
    expect(r.core.serversView().rows[1]).toMatchObject({ status: 'needs-signin' })
    expect(await r.core.testServer({ address: 'http://192.0.2.40:8080/x' })).toMatchObject({ ok: true, origin: 'http://192.0.2.40:8080', insecure: true })
    expect(await r.core.testServer({ address: 'office' })).toMatchObject({ ok: false, reason: 'invalid-url' })
    expect(await r.core.testServer({ id: 'srv_zzzzzzzz' })).toEqual({ ok: false, reason: 'not-found' })
  })

  it('shows the first-run question only when nothing is set up', async () => {
    const a = await rig()
    fs.rmSync(a.binary)
    await a.core.init()
    expect(a.core.serversView().firstRun).toBe(true)
    const b = await rig({ show_welcome: false })
    await b.core.init()
    expect(b.core.serversView().firstRun).toBe(false)
    const c = await rig({ remotes: [REMOTE] })
    await c.core.init()
    expect(c.core.serversView().firstRun).toBe(false)
  })
})

describe('a remote default that does not answer', () => {
  it('shows the recovery state, retries by itself, and never starts the local server', async () => {
    const r = await rig({ remotes: [REMOTE], default_target: REMOTE.id })
    r.results.set(REMOTE.url, { status: 'unreachable', reason: 'timeout' })
    await r.core.init()
    r.core.openDefaultRemote()
    await until(() => r.core.webuiState().remote !== null)
    expect(r.core.webuiState()).toMatchObject({ layout: 'shell-only', home: 'shell-only', remote: { kind: 'unreachable', reason: 'timed out' } })
    expect(r.effects.map((e) => e.kind)).toEqual(['show', 'close'])
    expect(r.core.serversView().rows[1].status).toBe('unreachable')
    await new Promise((x) => setTimeout(x, 200))
    expect(r.probes.length).toBeGreaterThan(2)
    expect(r.core.snapshot().state).toBe('stopped')
    r.results.delete(REMOTE.url)
    await until(() => r.core.webuiState().remote === null)
    expect(r.core.webuiState().layout).toBe('webui')
    expect(r.core.snapshot().state).toBe('stopped')
  })

  it('Retry tries at once', async () => {
    const r = await rig({ remotes: [REMOTE], default_target: REMOTE.id }, { remoteRetryMs: 60_000 })
    r.results.set(REMOTE.url, { status: 'unreachable', reason: 'refused' })
    await r.core.init()
    r.core.openDefaultRemote()
    await until(() => r.core.webuiState().remote !== null)
    r.results.delete(REMOTE.url)
    await r.core.retryRemote()
    expect(r.core.webuiState().remote).toBeNull()
  })
})

describe('the update channel', () => {
  it('asks for a confirmation before Development, once, and again after a return to Stable', async () => {
    const r = await rig()
    await r.core.init()
    expect(await r.core.setUpdateChannel('dev')).toEqual({ ok: false, reason: 'confirm-required' })
    expect(await r.core.setUpdateChannel('dev', true)).toMatchObject({ ok: true, info: { channel: 'dev', devConfirmed: true } })
    expect(cfg(r).update_channel).toBe('dev')
    expect(await r.core.setUpdateChannel('rc')).toMatchObject({ ok: true })
    // From a release candidate, Development does not ask again.
    expect(await r.core.setUpdateChannel('dev')).toMatchObject({ ok: true })
    await r.core.setUpdateChannel('stable')
    expect(await r.core.setUpdateChannel('dev')).toEqual({ ok: false, reason: 'confirm-required' })
  })

  it('the launcher updater follows release candidates only', async () => {
    const told: boolean[] = []
    const r = await rig({}, { onIncludePrereleases: (on) => told.push(on) })
    await r.core.init()
    await r.core.setUpdateChannel('rc')
    await r.core.setUpdateChannel('dev', true)
    await r.core.setUpdateChannel('stable')
    expect(told).toEqual([true, false, false])
  })

  it('reads the old include_prereleases key as release candidates and keeps it', async () => {
    const r = await rig({ include_prereleases: true })
    await r.core.init()
    expect((await r.core.updateInfo()).channel).toBe('rc')
    expect(r.core.channelIsRc()).toBe(true)
    await r.core.setUpdateChannel('stable')
    expect(cfg(r).include_prereleases).toBe(true)
    expect((await r.core.updateInfo()).channel).toBe('stable')
  })

  it('on Development the server starts with the development backend setting, and on Stable it does not', async () => {
    const r = await rig({ update_channel: 'dev', auto_start_server: true })
    await r.core.init()
    await r.core.startServer()
    await until(() => r.core.snapshot().state === 'ready')
    await until(() => r.core.queryLogs({ source: 'server' }).some((l) => l.text.includes('LOCALAI_PREFER_DEV_BACKENDS=true')))
    await r.core.stopServer()
    await r.core.setUpdateChannel('stable')
    await r.core.startServer()
    await until(() => r.core.queryLogs({ source: 'server' }).some((l) => l.text.includes('LOCALAI_PREFER_DEV_BACKENDS=undefined')))
  })

  it('installs from the rolling build on Development, and Back to Stable installs the release and keeps the build as the rollback copy', async () => {
    const stable = publish(feed, 'v4.2.1', bin('4.2.1'))
    const devBin = bin('dev-build')
    const { apiBase } = publishDev(feed, devBin, { sha: 'abc1234def' })
    const dev = devProvider(apiBase)
    const real = new FakeProvider(stable)
    // One source: development releases resolve through the dev rules, stable ones through the stable rules.
    const both = {
      latestRelease: () => real.latestRelease(),
      latestDevelopment: () => dev.latestDevelopment(),
      resolveAsset: (rel: { build?: unknown }, t: never) => (rel.build ? dev.resolveAsset(rel as never, t) : real.resolveAsset(rel as never, t)),
      resolveChecksumFile: (rel: { build?: unknown }, t: never) => (rel.build ? dev.resolveChecksumFile(rel as never, t) : real.resolveChecksumFile(rel as never)),
    }
    const r = await rig({ update_channel: 'dev', auto_start_server: false }, { provider: both as never })
    await r.core.init()
    r.core.installServer({})
    expect(await r.core.installIdle()).toMatchObject({ kind: 'done', version: 'abc1234' })
    expect(fs.readFileSync(r.binary).equals(devBin)).toBe(true)
    expect(await r.core.updateInfo()).toMatchObject({ channel: 'dev', devBuild: 'abc1234' })

    expect(await r.core.backToStable()).toMatchObject({ channel: 'stable' })
    expect(await r.core.installIdle()).toMatchObject({ kind: 'done', version: '4.2.1' })
    expect(fs.readFileSync(r.binary + '.previous').equals(devBin)).toBe(true)
    expect(await r.core.updateInfo()).toMatchObject({ channel: 'stable', previousVersion: 'abc1234' })
    expect(cfg(r).update_channel).toBe('stable')
  })
})

describe('desktop notifications', () => {
  it('are on by default, and the choice is saved and passed on', async () => {
    let changed = 0
    const r = await rig({}, { notificationTargetsChanged: () => void changed++ })
    await r.core.init()
    expect(r.core.windowSettings().desktopNotifications).toBe(true)
    expect(r.core.notificationTargets().enabled).toBe(true)
    expect(await r.core.setDesktopNotifications(false)).toMatchObject({ desktopNotifications: false })
    expect(cfg(r).desktop_notifications).toBe(false)
    expect(r.core.notificationTargets().enabled).toBe(false)
    expect(changed).toBeGreaterThan(0)
    expect(await r.core.setDesktopNotifications(true)).toMatchObject({ desktopNotifications: true })
    expect(r.core.notificationTargets().enabled).toBe(true)
  })

  it('watch the local server only while it runs, and every saved remote server', async () => {
    const r = await rig({ remotes: [REMOTE, REMOTE2] })
    await r.core.init()
    let t = r.core.notificationTargets()
    expect(t.all).toBe(3)
    expect(t.targets.map((x) => x.id)).toEqual([REMOTE.id, REMOTE2.id])
    await r.core.startServer()
    await until(() => r.core.snapshot().state === 'ready')
    t = r.core.notificationTargets()
    expect(t.targets.map((x) => [x.id, x.origin])).toEqual([['local', r.local], [REMOTE.id, REMOTE.url], [REMOTE2.id, REMOTE2.url]])
    await r.core.removeServer(REMOTE.id)
    expect(r.core.notificationTargets().targets.map((x) => x.id)).toEqual(['local', REMOTE2.id])
    expect(r.core.notificationTargets().all).toBe(2)
  })

  it('open the page of a notification only when the link stays on the server', async () => {
    const r = await rig({ remotes: [REMOTE] })
    await r.core.init()
    await r.core.startServer()
    await until(() => r.core.snapshot().state === 'ready')
    r.effects.length = 0
    expect(r.core.openNotificationLink('local', '/app/chats/1')).toBe(true)
    expect(r.core.openNotificationLink('local', 'https://evil.example.test/')).toBe(false)
    expect(r.core.openNotificationLink('local', '//evil.example.test/')).toBe(false)
    expect(r.core.openNotificationLink('nobody', '/app')).toBe(false)
    // A remote server that is not on show opens in the browser, never in the local view.
    expect(r.core.openNotificationLink(REMOTE.id, '/app/chats/2')).toBe(true)
    expect(r.effects).toEqual([
      { kind: 'navigate', url: `${r.local}/app/chats/1` },
      { kind: 'open-browser', url: `${REMOTE.url}/app/chats/2` },
    ])
  })
})
