import fs from 'node:fs'
import path from 'node:path'
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import type { EventContract } from '../src/shared/ipc'
import { AUTO_CHECK_INTERVAL_MS, displayPath, LauncherCore, LineSplitter, type CoreDeps } from '../src/main/core'
import type { LoginItemResult } from '../src/main/loginItem'
import type { SandboxProbe } from '../src/main/sandboxProbe'
import type { Runner } from '../src/main/sandboxSetup'
import { FAKE_SERVER, freePort, tmpDir } from './helpers'
import { FakeProvider, FeedServer, TARGET, bin, publish, readVersionFromFile } from './serverInstallHarness'

type Ev = { [C in keyof EventContract]: { channel: C; payload: EventContract[C] } }[keyof EventContract]

const probe = (state: SandboxProbe['state']): SandboxProbe => ({
  state,
  inputs: { platform: 'linux', sysctl: state === 'unsandboxed-restricted' ? 1 : 0, noSandboxFlag: state !== 'sandboxed', profile: 'unknown' },
})

const cores: LauncherCore[] = []
afterEach(async () => {
  for (const c of cores.splice(0)) await c.shutdown()
})

function makeCore(over: Partial<CoreDeps> & { home?: string } = {}) {
  const home = over.home ?? tmpDir('core-')
  const events: Ev[] = []
  const core = new LauncherCore({
    home,
    launcherVersion: '1.2.3',
    env: { PATH: process.env.PATH, ELECTRON_RUN_AS_NODE: '1' },
    target: TARGET,
    provider: new FakeProvider({ tag: 'v0.0.0', version: '0.0.0', prerelease: false, assets: [] }),
    fetch,
    probeSandbox: () => probe('sandboxed'),
    sandboxRunner: async () => ({ exitCode: 0, stderr: '' }),
    emit: (channel, payload) => events.push({ channel, payload } as Ev),
    testServer: async () => ({ status: 'ok', secure: true }),
    supervisorTuning: { pollIntervalMs: 25, readyTimeoutMs: 10_000, stopGraceMs: 2000 },
    logFlushMs: 5,
    ...over,
  })
  cores.push(core)
  const of = <C extends keyof EventContract>(c: C) => events.filter((e) => e.channel === c).map((e) => e.payload as EventContract[C])
  return { core, home, events, of, configFile: path.join(home, '.localai', 'launcher.json') }
}

/** A `local-ai` stand-in: a script that runs the fake server. */
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

describe('start order', () => {
  it('creates the data folders with mode 0700, reports not-installed and the sandbox state', async () => {
    const { core, home, of } = makeCore()
    await core.init()
    for (const d of ['models', 'backends', 'logs']) {
      const st = fs.statSync(path.join(home, '.localai', d))
      expect(st.isDirectory()).toBe(true)
      expect(st.mode & 0o777).toBe(0o700)
    }
    expect(core.configNotice).toBeUndefined()
    expect(core.snapshot().state).toBe('not-installed')
    expect(of('launcher:sandbox:event').at(-1)).toMatchObject({ state: 'sandboxed', notice: { kind: 'hidden' }, setupAvailable: false })
    expect(core.queryLogs({ source: 'launcher' }).map((l) => l.text)).toContain('launcher 1.2.3 starting')
  })

  it('keeps the mode of folders that already exist', async () => {
    const home = tmpDir('core-')
    fs.mkdirSync(path.join(home, '.localai', 'models'), { recursive: true, mode: 0o755 })
    fs.chmodSync(path.join(home, '.localai', 'models'), 0o755)
    const { core } = makeCore({ home })
    await core.init()
    expect(fs.statSync(path.join(home, '.localai', 'models')).mode & 0o777).toBe(0o755)
  })

  it('reports a recovered config file, and the config lines reach the launcher log', async () => {
    const home = tmpDir('core-')
    fs.mkdirSync(path.join(home, '.localai'), { recursive: true })
    fs.writeFileSync(path.join(home, '.localai', 'launcher.json'), '{ broken')
    const { core } = makeCore({ home })
    await core.init()
    expect(core.configNotice).toEqual({ status: 'recovered', reason: 'config file is not valid JSON' })
    expect(core.queryLogs({ source: 'launcher' }).some((l) => /config: config file is not valid JSON/.test(l.text))).toBe(true)
  })
})

describe('server', () => {
  it('auto-starts per config, feeds the server output into the log and stops on shutdown', async () => {
    const home = tmpDir('core-')
    const port = await freePort()
    const binary = fakeBinary(path.join(home, 'bin'))
    fs.mkdirSync(path.join(home, '.localai'), { recursive: true })
    fs.writeFileSync(path.join(home, '.localai', 'launcher.json'), JSON.stringify({ environment_vars: { FAKE_PORT: String(port) } }))
    const { core, of } = makeCore({ home, serverBinOverride: binary, endpointOverride: `http://127.0.0.1:${port}` })
    await core.init()
    expect(core.snapshot().state).toBe('stopped')
    await core.autoStart()
    await until(() => core.snapshot().state === 'ready')
    await until(() => core.queryLogs({ source: 'server' }).some((l) => l.text === 'fake-server: listening'))
    const args = core.queryLogs({ source: 'server' }).find((l) => l.text.startsWith('args: '))!.text
    expect(args).toContain(`--address 127.0.0.1:${port}`)
    expect(args).toContain(`--models-path ${path.join(home, '.localai', 'models')}`)
    expect(core.queryLogs({ source: 'launcher' }).map((l) => l.text)).toContain('server ready')
    expect(of('launcher:server:event').map((s) => s.state)).toEqual(expect.arrayContaining(['starting', 'ready']))
    await until(() => of('launcher:logs:event').flat().some((l) => l.source === 'server'))

    // A change that needs a restart while the server runs is applied on the next start.
    const r = await core.saveSettings({ logLevel: 'debug' })
    expect(r.restartRequired).toBe(true)
    await core.restartServer()
    await until(() => core.snapshot().state === 'ready')
    expect(core.queryLogs({ source: 'server' }).filter((l) => l.text.startsWith('args: ')).at(-1)!.text).toContain('--log-level debug')

    // A folder from the picker is saved and marks "restart needed" while the server runs.
    const models = path.join(home, 'picked', 'models')
    expect(await core.setDataFolder('models', models)).toMatchObject({ status: 'saved', restartRequired: true, settings: { modelsPath: models } })
    expect(fs.statSync(models).isDirectory()).toBe(true)
    await core.restartServer()
    await until(() => core.snapshot().state === 'ready')
    expect(core.queryLogs({ source: 'server' }).filter((l) => l.text.startsWith('args: ')).at(-1)!.text).toContain(`--models-path ${models}`)

    await core.shutdown()
    expect(core.snapshot().state).toBe('stopped')
  })

  it('does not auto-start when the config says no', async () => {
    const home = tmpDir('core-')
    const binary = fakeBinary(path.join(home, 'bin'))
    fs.mkdirSync(path.join(home, '.localai'), { recursive: true })
    fs.writeFileSync(path.join(home, '.localai', 'launcher.json'), JSON.stringify({ auto_start_server: false }))
    const { core } = makeCore({ home, serverBinOverride: binary })
    await core.init()
    await core.autoStart()
    expect(core.snapshot().state).toBe('stopped')
    expect(core.queryLogs({ source: 'launcher' }).map((l) => l.text)).toContain('auto-start is off')
  })

  it('saves a picked data folder only after the main-side check', async () => {
    const { core, home, configFile } = makeCore()
    await core.init()
    const before = core.getSettings()
    const file = path.join(home, 'a-file')
    fs.writeFileSync(file, 'x')
    for (const [p, reason] of [['/', 'root'], [home, 'home'], [`${home}/`, 'home'], ['relative', 'not-absolute'], [file, 'not-a-folder']] as const) {
      expect(await core.setDataFolder('models', p), p).toEqual({ status: 'refused', reason })
    }
    expect(core.getSettings()).toEqual(before)
    const backends = path.join(home, 'b', 'backends')
    expect(await core.setDataFolder('backends', `${backends}/`)).toMatchObject({ status: 'saved', restartRequired: false, settings: { backendsPath: backends } })
    expect(JSON.parse(fs.readFileSync(configFile, 'utf8')).backends_path).toBe(backends)
    expect(fs.statSync(backends).isDirectory()).toBe(true)
  })

  it('a change while stopped needs no restart', async () => {
    const { core } = makeCore()
    await core.init()
    expect(await core.saveSettings({ address: '127.0.0.1:9999' })).toMatchObject({ restartRequired: false, settings: { address: '127.0.0.1:9999' } })
    expect(core.snapshot().endpoint).toBe('http://127.0.0.1:9999')
  })
})

describe('status bar and settings', () => {
  it('saves the status bar choice, keeps unknown keys and tells the renderer', async () => {
    const home = tmpDir('core-')
    fs.mkdirSync(path.join(home, '.localai'), { recursive: true })
    fs.writeFileSync(path.join(home, '.localai', 'launcher.json'), JSON.stringify({ theme: 'dark' }))
    const { core, of, configFile } = makeCore({ home })
    await core.init()
    expect(core.statusBarVisible()).toBe(true)
    expect(await core.setStatusBarVisible(false)).toBe(false)
    expect(JSON.parse(fs.readFileSync(configFile, 'utf8'))).toMatchObject({ show_status_bar: false, theme: 'dark' })
    expect(of('launcher:status-bar:event')).toEqual([false])
    await core.saveSettings({ showStatusBar: true })
    expect(of('launcher:status-bar:event')).toEqual([false, true])
    expect(core.getSettings().showStatusBar).toBe(true)
  })

  it('applies "Launch at login" through the login item and saves only what took effect', async () => {
    const calls: boolean[] = []
    let next: LoginItemResult = { status: 'ok' }
    const { core, configFile } = makeCore({ loginItem: (on) => (calls.push(on), next) })
    await core.init()
    expect(await core.saveSettings({ startOnBoot: true })).toMatchObject({ settings: { startOnBoot: true }, loginItem: { status: 'ok' } })
    expect(JSON.parse(fs.readFileSync(configFile, 'utf8')).start_on_boot).toBe(true)
    // The same value again does not touch the login item.
    expect((await core.saveSettings({ startOnBoot: true })).loginItem).toBeUndefined()
    expect(calls).toEqual([true])
    // A removal that fails keeps the switch on.
    next = { status: 'failed', reason: 'read-only' }
    expect(await core.saveSettings({ startOnBoot: false })).toMatchObject({ settings: { startOnBoot: true }, loginItem: { status: 'failed', reason: 'read-only' } })
    next = { status: 'ok' }
    expect(await core.saveSettings({ startOnBoot: false })).toMatchObject({ settings: { startOnBoot: false } })
    // Unavailable (a run from source): the switch stays off, and other keys of the patch still save.
    next = { status: 'unavailable', reason: 'not-appimage' }
    expect(await core.saveSettings({ startOnBoot: true, logLevel: 'warn' })).toMatchObject({
      settings: { startOnBoot: false, logLevel: 'warn' },
      loginItem: { status: 'unavailable', reason: 'not-appimage' },
    })
    expect(JSON.parse(fs.readFileSync(configFile, 'utf8')).start_on_boot).toBe(false)
    expect(calls).toEqual([true, false, false, true])
    expect(core.queryLogs({ source: 'launcher' }).map((l) => l.text)).toContain('launch at login: unavailable (not-appimage)')
  })

  it('refreshes an enabled login item at start, so a moved AppImage still starts', async () => {
    const home = tmpDir('core-')
    fs.mkdirSync(path.join(home, '.localai'), { recursive: true })
    fs.writeFileSync(path.join(home, '.localai', 'launcher.json'), JSON.stringify({ start_on_boot: true }))
    const calls: boolean[] = []
    const { core } = makeCore({ home, loginItem: (on) => (calls.push(on), { status: 'ok' }) })
    await core.init()
    core.syncLoginItem()
    expect(calls).toEqual([true])
    // Off in the config: nothing is touched at start.
    const off = makeCore({ loginItem: (on) => (calls.push(on), { status: 'ok' }) })
    await off.core.init()
    off.core.syncLoginItem()
    expect(calls).toEqual([true])
  })

  it('concurrent saves from several sources all land', async () => {
    const { core, configFile } = makeCore()
    await core.init()
    await Promise.all([
      core.setStatusBarVisible(false),
      core.saveSettings({ logLevel: 'warn' }),
      core.saveSettings({ openWebuiInBrowser: true }),
      core.dismissSandboxNotice(),
    ])
    const w = JSON.parse(fs.readFileSync(configFile, 'utf8'))
    expect(w).toMatchObject({ show_status_bar: false, log_level: 'warn', open_webui_in_browser: true })
  })
})

describe('logs', () => {
  it('records updater status and exports through the chosen path only', async () => {
    const { core, home } = makeCore()
    await core.init()
    core.recordUpdater({ phase: 'available', version: '9.9.9' })
    core.recordUpdater({ phase: 'error', message: 'feed down' })
    expect(core.queryLogs({ source: 'updater' }).map((l) => `${l.level} ${l.text}`)).toEqual(['info available 9.9.9', 'error error: feed down'])
    expect(await core.exportLogs(async () => undefined)).toEqual({ status: 'cancelled' })
    const dest = path.join(home, 'out', 'logs.txt')
    const r = await core.exportLogs(async () => dest)
    expect(r).toMatchObject({ status: 'saved', fileName: 'logs.txt' })
    expect(fs.readFileSync(dest, 'utf8')).toContain('available 9.9.9')
  })

  it('splits server output into whole lines', () => {
    const s = new LineSplitter()
    const out: string[] = []
    s.push('one\ntw', (l) => out.push(l))
    s.push('o\r\nthree', (l) => out.push(l))
    s.flush((l) => out.push(l))
    expect(out).toEqual(['one', 'two', 'three'])
  })
})

describe('sandbox', () => {
  it('shows the notice, remembers "Not now" per launcher version', async () => {
    const home = tmpDir('core-')
    const a = makeCore({ home, probeSandbox: () => probe('unsandboxed-restricted') })
    await a.core.init()
    expect(a.core.sandboxView().notice).toEqual({ kind: 'shown' })
    expect((await a.core.dismissSandboxNotice()).notice).toEqual({ kind: 'dismissed' })
    expect(JSON.parse(fs.readFileSync(a.configFile, 'utf8')).sandbox_notice_dismissed_version).toBe('1.2.3')

    const b = makeCore({ home, probeSandbox: () => probe('unsandboxed-restricted') })
    await b.core.init()
    expect(b.core.sandboxView().notice).toEqual({ kind: 'dismissed' })
    const c = makeCore({ home, launcherVersion: '1.3.0', probeSandbox: () => probe('unsandboxed-restricted') })
    await c.core.init()
    expect(c.core.sandboxView().notice).toEqual({ kind: 'shown' })
  })

  it('refuses setup while the attach rule is undecided, but shows the removal plan', async () => {
    const calls: string[][] = []
    const runner: Runner = async (argv) => (calls.push([...argv]), { exitCode: 0, stderr: '' })
    const { core } = makeCore({ probeSandbox: () => probe('unsandboxed-restricted'), sandboxRunner: runner })
    await core.init()
    expect(core.sandboxPlan('setup')).toMatchObject({ available: false, steps: [] })
    expect((await core.runSandboxSetup()).notice).toEqual({ kind: 'shown' })
    expect(calls).toEqual([])
    const remove = core.sandboxPlan('remove')
    expect(remove.available).toBe(true)
    expect(remove.steps.every((s) => s.command.startsWith('pkexec '))).toBe(true)
  })

  it('runs setup through pkexec with a decided pattern and asks for a restart', async () => {
    const calls: string[][] = []
    const runner: Runner = async (argv) => (calls.push([...argv]), { exitCode: 0, stderr: '' })
    const { core, of } = makeCore({
      probeSandbox: () => probe('unsandboxed-restricted'),
      sandboxRunner: runner,
      sandboxAttachPattern: '/opt/example/launcher',
    })
    await core.init()
    const plan = core.sandboxPlan('setup')
    expect(plan.available).toBe(true)
    expect(plan.profileText).toContain('/opt/example/launcher')
    expect((await core.runSandboxSetup()).notice).toEqual({ kind: 'installed-needs-restart' })
    expect(calls.map((c) => c[0])).toEqual(['pkexec', 'pkexec'])
    expect(of('launcher:sandbox:event').map((s) => s.notice.kind)).toEqual(['shown', 'installing', 'installed-needs-restart'])
  })

  it('reports a closed password dialog as cancelled', async () => {
    const { core } = makeCore({
      probeSandbox: () => probe('unsandboxed-restricted'),
      sandboxRunner: async () => ({ exitCode: 126, stderr: '' }),
      sandboxAttachPattern: '/opt/example/launcher',
    })
    await core.init()
    expect((await core.runSandboxSetup()).notice).toEqual({ kind: 'failed', reason: 'cancelled' })
  })

  it('ignores an invalid attach pattern', async () => {
    const { core } = makeCore({ probeSandbox: () => probe('unsandboxed-restricted'), sandboxAttachPattern: 'relative/path' })
    await core.init()
    expect(core.sandboxView().setupAvailable).toBe(false)
  })
})

describe('install', () => {
  const feed = new FeedServer()
  beforeAll(() => feed.start())
  afterAll(() => feed.stop())

  it('installs through the core, sends progress and turns not-installed into stopped', async () => {
    const rel = publish(feed, 'v3.1.0', bin('3.1.0'))
    const home = tmpDir('core-')
    const { core, of } = makeCore({ home, provider: new FakeProvider(rel), runVersion: async (f) => readVersionFromFile(f) })
    await core.init()
    expect(core.snapshot().state).toBe('not-installed')
    const first = core.installServer({})
    expect(first.kind).toBe('checking')
    const end = await core.installIdle()
    expect(end, JSON.stringify(end)).toMatchObject({ kind: 'done', version: '3.1.0' })
    expect(fs.existsSync(path.join(home, '.localai', 'bin', 'local-ai'))).toBe(true)
    expect(core.snapshot().state).toBe('stopped')
    expect(of('launcher:install:event').map((s) => s.kind)).toEqual(expect.arrayContaining(['checking', 'update-available', 'downloading', 'verifying', 'installing', 'done']))
    expect(of('launcher:server:event').at(-1)?.state).toBe('stopped')
  })

  it('records the check time, offers release notes only for the offered version, and reports install details', async () => {
    const rel = publish(feed, 'v3.2.0', bin('3.2.0'))
    const home = tmpDir('core-')
    const { core, configFile } = makeCore({ home, provider: new FakeProvider(rel), runVersion: async (f) => readVersionFromFile(f) })
    await core.init()
    expect(await core.updateInfo()).toEqual({ checkedAt: undefined, autoCheck: true, previousVersion: undefined, channel: 'stable', devConfirmed: false, remoteDefault: false })
    // Nothing installed: no automatic check.
    expect(core.autoCheckDue(Date.now())).toBe(false)
    expect(await core.checkServerUpdate({})).toMatchObject({ kind: 'update-available', version: '3.2.0' })
    const info = await core.updateInfo()
    expect(info.checkedAt).toBeGreaterThan(Date.now() - 60_000)
    expect(JSON.parse(fs.readFileSync(configFile, 'utf8')).last_update_check).toBe(info.checkedAt)
    expect(core.releaseNotesUrl('3.2.0')).toBe('https://github.com/mudler/LocalAI/releases/tag/v3.2.0')
    expect(core.releaseNotesUrl('v3.2.0')).toBe('https://github.com/mudler/LocalAI/releases/tag/v3.2.0')
    expect(core.releaseNotesUrl('3.1.9')).toBeUndefined()
    expect(core.releaseNotesUrl('1.5.0', '1.5.0')).toBe('https://github.com/mudler/LocalAI/releases/tag/v1.5.0')
    expect(core.releaseNotesUrl('../../evil', '../../evil')).toBeUndefined()
    expect(await core.installDetails()).toEqual({ build: 'linux · amd64', size: undefined, installDir: '~/.localai/bin' })
    core.installServer({})
    expect(await core.installIdle()).toMatchObject({ kind: 'done' })
    expect((await core.installDetails()).size).toBe(bin('3.2.0').length)
    // Installed and checked just now: not due; a day later it is.
    const now = Date.now()
    expect(core.autoCheckDue(now)).toBe(false)
    expect(core.autoCheckDue(now + AUTO_CHECK_INTERVAL_MS + 1000)).toBe(true)
    expect(await core.setAutoCheck(false)).toMatchObject({ autoCheck: false })
    expect(JSON.parse(fs.readFileSync(configFile, 'utf8')).auto_check_updates).toBe(false)
    expect(core.autoCheckDue(now + AUTO_CHECK_INTERVAL_MS + 1000)).toBe(false)
    expect(await core.autoCheck(now + AUTO_CHECK_INTERVAL_MS + 1000)).toBe(false)
  })

  it('the update channel decides which release the server check and install look up', async () => {
    const stable = publish(feed, 'v3.2.0', bin('3.2.0'))
    const rc = publish(feed, 'v3.3.0-rc.1', bin('3.3.0-rc.1'), { prerelease: true })
    const provider = Object.assign(new FakeProvider(stable), {
      prereleaseLookups: 0,
      async latestIncludingPrerelease() {
        provider.prereleaseLookups++
        return rc
      },
    })
    const home = tmpDir('core-')
    const told: boolean[] = []
    const { core, configFile } = makeCore({ home, provider, runVersion: async (f) => readVersionFromFile(f), onIncludePrereleases: (on) => told.push(on) })
    await core.init()
    const config = () => JSON.parse(fs.readFileSync(configFile, 'utf8'))
    // Off (the default): stable only, and the key is not written.
    expect(core.channelIsRc()).toBe(false)
    expect(await core.checkServerUpdate({})).toMatchObject({ kind: 'update-available', version: '3.2.0' })
    expect(provider.prereleaseLookups).toBe(0)
    expect('update_channel' in config()).toBe(false)
    // Release candidates: saved through the config queue, the launcher updater is told, the lookup includes pre-releases.
    expect(await core.setUpdateChannel('rc')).toMatchObject({ ok: true, info: { channel: 'rc' } })
    expect(config().update_channel).toBe('rc')
    expect('include_prereleases' in config()).toBe(false)
    expect(told).toEqual([true])
    expect(await core.checkServerUpdate({})).toMatchObject({ kind: 'update-available', version: '3.3.0-rc.1' })
    core.installServer({})
    expect(await core.installIdle()).toMatchObject({ kind: 'done', version: '3.3.0-rc.1' })
    // Stable again: a stable lookup follows.
    expect(await core.setUpdateChannel('stable')).toMatchObject({ ok: true, info: { channel: 'stable' } })
    expect(config().update_channel).toBe('stable')
    expect(told).toEqual([true, false])
    const before = provider.prereleaseLookups
    await core.checkServerUpdate({})
    expect(provider.prereleaseLookups).toBe(before)
    // A one-off choice of the caller still works with the setting off.
    await core.checkServerUpdate({ allowPrerelease: true })
    expect(provider.prereleaseLookups).toBe(before + 1)
  })

  it('reports the installed server version in the install details, from the metadata or the binary', async () => {
    const home = tmpDir('core-')
    const { core } = makeCore({ home, provider: new FakeProvider(publish(feed, 'v3.5.0', bin('3.5.0'))), runVersion: async (f) => readVersionFromFile(f) })
    await core.init()
    expect((await core.installDetails()).version).toBeUndefined()
    core.installServer({})
    expect(await core.installIdle()).toMatchObject({ kind: 'done', version: '3.5.0' })
    expect((await core.installDetails()).version).toBe('3.5.0')
    // A binary with no metadata (placed by hand) is asked for its version.
    const other = tmpDir('core-')
    fs.mkdirSync(path.join(other, '.localai', 'bin'), { recursive: true })
    fs.writeFileSync(path.join(other, '.localai', 'bin', 'local-ai'), bin('3.4.2'), { mode: 0o755 })
    const second = makeCore({ home: other, runVersion: async (f) => readVersionFromFile(f) })
    await second.core.init()
    expect((await second.core.installDetails()).version).toBe('3.4.2')
  })

  it('reports the version of the rollback copy after an update', async () => {
    const home = tmpDir('core-')
    const v1 = publish(feed, 'v3.3.0', bin('3.3.0'))
    const provider = new FakeProvider(v1)
    const { core } = makeCore({ home, provider, runVersion: async (f) => readVersionFromFile(f) })
    await core.init()
    core.installServer({})
    expect(await core.installIdle()).toMatchObject({ kind: 'done', version: '3.3.0' })
    expect((await core.updateInfo()).previousVersion).toBeUndefined()
    provider.release = publish(feed, 'v3.4.0', bin('3.4.0'))
    core.installServer({})
    expect(await core.installIdle()).toMatchObject({ kind: 'done', version: '3.4.0' })
    expect((await core.updateInfo()).previousVersion).toBe('3.3.0')
  })

  it('refuses on a platform with no server build', async () => {
    const { core } = makeCore({ target: undefined })
    await core.init()
    expect(core.installServer({})).toMatchObject({ kind: 'failed', reason: 'unknown' })
    expect(await core.checkServerUpdate({})).toMatchObject({ kind: 'failed' })
  })
})

describe('displayPath', () => {
  it('shows a folder under the home as ~, and anything else as it is', () => {
    expect(displayPath('/home/u/.localai/bin', '/home/u')).toBe('~/.localai/bin')
    expect(displayPath('/home/u', '/home/u/')).toBe('~')
    expect(displayPath('/home/user2/x', '/home/u')).toBe('/home/user2/x')
    expect(displayPath('/opt/localai', '/home/u')).toBe('/opt/localai')
  })
})

describe('WebUI view lifecycle', () => {
  it('loads /app once when the server is ready, keeps it when it stops, and switches to a remote server', async () => {
    const home = tmpDir('core-')
    const port = await freePort()
    const binary = fakeBinary(path.join(home, 'bin'))
    fs.mkdirSync(path.join(home, '.localai'), { recursive: true })
    fs.writeFileSync(path.join(home, '.localai', 'launcher.json'), JSON.stringify({ environment_vars: { FAKE_PORT: String(port) } }))
    const effects: unknown[] = []
    const local = `http://127.0.0.1:${port}`
    const { core, of, configFile } = makeCore({
      home,
      serverBinOverride: binary,
      endpointOverride: local,
      webuiEffects: (fx) => effects.push(...fx),
      webuiCurrentUrl: () => `${local}/app/chat/7`,
    })
    await core.init()
    expect(core.webuiState()).toMatchObject({ layout: 'shell-only', target: { kind: 'local', origin: local } })
    await core.autoStart()
    await until(() => core.snapshot().state === 'ready')
    expect(effects).toEqual([{ kind: 'show', origin: local, url: `${local}/app` }])
    core.webuiDispatch({ type: 'view-loaded' })
    expect(of('launcher:webui:event').at(-1)).toMatchObject({ layout: 'webui', loaded: true, banner: null })

    // Shell screens hide it without effects.
    core.setShellScreen('logs')
    expect(core.webuiState().layout).toBe('shell-only')
    core.setShellScreen(null)

    // Deep link to a model navigates the same view.
    core.openChat('my model')
    expect(effects.at(-1)).toEqual({ kind: 'navigate', url: `${local}/app?model=my%20model` })

    core.openWebuiInBrowser()
    expect(effects.at(-1)).toEqual({ kind: 'open-browser', url: `${local}/app/chat/7` })

    // Stop: the view stays, dimmed.
    const before = effects.length
    await core.stopServer()
    expect(effects.length).toBe(before)
    expect(core.webuiState()).toMatchObject({ layout: 'webui', banner: 'stopped', shellAbove: false })

    // Remote: a bad scheme is refused, https is accepted and saved as the default.
    expect(await core.addServer({ name: 'Remote', address: 'ftp://remote.example.test', skipTest: true })).toMatchObject({ ok: false, reason: 'scheme' })
    const added = await core.addServer({ name: 'Remote', address: 'https://remote.example.test/app', makeDefault: true })
    expect(added).toMatchObject({ ok: true })
    core.webuiDispatch({ type: 'view-loaded' })
    expect(core.webuiState()).toMatchObject({ target: { kind: 'remote', host: 'remote.example.test' }, layout: 'webui', banner: null })
    expect(effects.slice(-2)).toEqual([{ kind: 'close' }, { kind: 'show', origin: 'https://remote.example.test', url: 'https://remote.example.test/app' }])
    expect(JSON.parse(fs.readFileSync(configFile, 'utf8')).remotes).toMatchObject([{ name: 'Remote', url: 'https://remote.example.test' }])

    // Back to this computer: it is the default again and it starts.
    expect(await core.backToLocal()).toMatchObject({ target: { kind: 'local' } })
    expect(effects.some((f) => (f as { kind: string }).kind === 'close')).toBe(true)
  })

  it('remembers and resets permission choices in launcher.json', async () => {
    const { core, configFile } = makeCore()
    await core.init()
    await core.rememberPermission('http://127.0.0.1:8080', 'allow')
    expect(core.permissionStore()).toEqual({ 'http://127.0.0.1:8080': { media: 'allow' } })
    expect(JSON.parse(fs.readFileSync(configFile, 'utf8')).webui_permissions).toEqual({ 'http://127.0.0.1:8080': { media: 'allow' } })
    await core.resetWebuiPermissions()
    expect(core.permissionStore()).toEqual({})
    expect(JSON.parse(fs.readFileSync(configFile, 'utf8')).webui_permissions).toBeUndefined()
  })

  it('"open in browser" is a setting; turning it on closes the view', async () => {
    const effects: unknown[] = []
    const { core } = makeCore({ webuiEffects: (fx) => effects.push(...fx) })
    await core.init()
    const r = await core.saveSettings({ openWebuiInBrowser: true })
    expect(r.settings.openWebuiInBrowser).toBe(true)
    expect(core.webuiState().placement).toBe('browser')
  })
})
