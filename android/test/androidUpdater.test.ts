// SPDX-License-Identifier: MIT
import type { PluginListenerHandle } from '@capacitor/core'
import { afterEach, describe, expect, it } from 'vitest'
import { ConfigStore, autoCheckUpdates, lastUpdateCheck, withAutoCheckUpdates, withLastUpdateCheck } from '@launcher/core/configStore'
import type { UpdaterStatus } from '@launcher/shared/types'
import { AUTO_CHECK_INTERVAL_MS, DEV_APP_URL, type AppSelf } from '../src/core/apkUpdate'
import { SIGNER_MESSAGE, appSelf, createAndroidUpdater, idleUpdater, type AndroidUpdater } from '../src/core/androidUpdater'
import { clientConfigIO } from '../src/core/configIO'
import type { UpdaterPlugin } from '../src/plugins/definitions'
import { stableVersionCode } from '../scripts/version-code.mjs'
import { fakeFiles, flush } from './fakes/plugins'

const BASE = 'https://github.com/mudler/LocalAI/releases'
const STABLE = `${BASE}/latest/download`
const DEV = `${BASE}/download/dev-master`
const SHA = 'c'.repeat(64)
const NOW = Date.UTC(2026, 10, 25, 12, 0, 0)
const FILE = '/data/cache/updates/update.apk'

const stableSelf: AppSelf = {
  applicationId: 'io.localai.client',
  versionCode: stableVersionCode('4.2.1'),
  versionName: '4.2.1',
  sdkInt: 34,
  feedBase: BASE,
  channel: 'stable',
}

const devSelf: AppSelf = { ...stableSelf, applicationId: 'io.localai.client.dev', versionCode: 473328, versionName: '5.0.2-dev.1', channel: 'dev' }

function manifest(over: Record<string, unknown> = {}): string {
  return JSON.stringify({
    schema: 1,
    applicationId: 'io.localai.client',
    versionName: '4.3.0',
    versionCode: stableVersionCode('4.3.0'),
    minSdk: 26,
    apk: 'LocalAI-Android.apk',
    size: 41_234_567,
    sha256: SHA,
    commit: '0123456789abcdef0123456789abcdef01234567',
    notesUrl: 'https://github.com/mudler/LocalAI/releases/tag/v4.3.0',
    ...over,
  })
}

type Call = { name: string; args: unknown[] }
type StatusEvent = { status: 'pending-user-action' | 'success' | 'failure'; message?: string }

/** A scripted Updater plugin: answers per URL, records calls, sends events with `emit`. */
interface FakeUpdater extends UpdaterPlugin {
  calls: Call[]
  texts: Map<string, { status: number; text: string } | Error>
  allowed: boolean
  sameSigner: boolean
  downloadError?: Error
  installError?: Error
  emit(event: 'progress', x: { done: number; total: number }): void
  emit(event: 'status', x: StatusEvent): void
  count(name: string): number
}

function codeError(message: string, code: string): Error {
  return Object.assign(new Error(message), { code })
}

function fakeUpdater(): FakeUpdater {
  const listeners = new Map<string, Set<(x: never) => void>>()
  const calls: Call[] = []
  const f = {
    calls,
    texts: new Map<string, { status: number; text: string } | Error>(),
    allowed: true,
    sameSigner: true,
    downloadError: undefined as Error | undefined,
    installError: undefined as Error | undefined,
    count: (name: string) => calls.filter((c) => c.name === name).length,
    async fetchText(o: { url: string; maxBytes: number }) {
      calls.push({ name: 'fetchText', args: [o] })
      const a = f.texts.get(o.url) ?? { status: 404, text: '' }
      if (a instanceof Error) throw a
      return a
    },
    async download(o: { url: string; sha256: string; size: number }) {
      calls.push({ name: 'download', args: [o] })
      if (f.downloadError) throw f.downloadError
      return { file: FILE }
    },
    async verifySigner(o: { file: string }) {
      calls.push({ name: 'verifySigner', args: [o] })
      // The plugin deletes a file from another signer before it answers.
      if (!f.sameSigner) calls.push({ name: 'deleted', args: [o.file] })
      return { sameSigner: f.sameSigner }
    },
    async canInstall() {
      calls.push({ name: 'canInstall', args: [] })
      return { allowed: f.allowed }
    },
    async openInstallSettings() {
      calls.push({ name: 'openInstallSettings', args: [] })
    },
    async install(o: { file: string }) {
      calls.push({ name: 'install', args: [o] })
      if (f.installError) throw f.installError
    },
    async appInfo() {
      return { applicationId: stableSelf.applicationId, versionName: stableSelf.versionName, versionCode: stableSelf.versionCode, sdkInt: 34, feedBase: BASE }
    },
    addListener(event: string, cb: (x: never) => void): Promise<PluginListenerHandle> {
      let set = listeners.get(event)
      if (!set) listeners.set(event, (set = new Set()))
      set.add(cb)
      return Promise.resolve({ remove: async () => void set.delete(cb) })
    },
    emit(event: string, x: unknown) {
      for (const cb of listeners.get(event) ?? []) (cb as (d: unknown) => void)(x)
    },
  }
  return f as unknown as FakeUpdater
}

let updaters: AndroidUpdater[] = []
afterEach(() => {
  for (const u of updaters) u.dispose?.()
  updaters = []
})

async function rig(o: { self?: AppSelf; config?: (s: ConfigStore) => Promise<unknown> } = {}) {
  const plugin = fakeUpdater()
  const store = await ConfigStore.open(clientConfigIO(fakeFiles(), () => new Date(NOW)))
  await o.config?.(store)
  const opened: string[] = []
  const u = createAndroidUpdater({ plugin, store, self: o.self ?? stableSelf, openExternal: async (url) => void opened.push(url) })
  updaters.push(u)
  const seen: UpdaterStatus[] = []
  u.on((s) => seen.push(s))
  await flush()
  return { u, plugin, store, opened, seen }
}

/** A stable feed with a newer release whose checksum file agrees. */
function goodFeed(plugin: FakeUpdater, sha = SHA): void {
  plugin.texts.set(`${STABLE}/android-update.json`, { status: 200, text: manifest() })
  plugin.texts.set(`${STABLE}/LocalAI-Android.apk.sha256`, { status: 200, text: `${sha}  LocalAI-Android.apk\n` })
}

describe('check', () => {
  it('a newer manifest with an agreeing .sha256 is available', async () => {
    const { u, plugin, store } = await rig()
    goodFeed(plugin)
    await u.check(NOW)
    expect(u.status()).toEqual({ phase: 'available', version: '4.3.0' })
    expect(u.offer()).toMatchObject({ versionName: '4.3.0', apkUrl: `${STABLE}/LocalAI-Android.apk`, size: 41_234_567, sha256: SHA })
    expect(plugin.count('download')).toBe(0)
    expect(lastUpdateCheck(store.get())).toBe(NOW)
    expect(u.info().checkedAt).toBe(NOW)
  })

  it('a .sha256 that disagrees with the manifest is an error and nothing is downloaded', async () => {
    const { u, plugin } = await rig()
    goodFeed(plugin, 'd'.repeat(64))
    await u.check(NOW)
    expect(u.status().phase).toBe('error')
    expect(u.status().message).toMatch(/checksum/)
    expect(u.offer()).toBeUndefined()
    await u.download()
    expect(plugin.count('download')).toBe(0)
  })

  it('a missing .sha256 is an error', async () => {
    const { u, plugin } = await rig()
    goodFeed(plugin)
    plugin.texts.delete(`${STABLE}/LocalAI-Android.apk.sha256`)
    await u.check(NOW)
    expect(u.status().phase).toBe('error')
    expect(u.offer()).toBeUndefined()
  })

  it('a 404 on the stable manifest means no update, not an error', async () => {
    const { u, plugin, store } = await rig()
    await u.check(NOW)
    expect(u.status()).toEqual({ phase: 'idle' })
    expect(u.offer()).toBeUndefined()
    expect(plugin.count('fetchText')).toBe(1)
    expect(lastUpdateCheck(store.get())).toBe(NOW)
  })

  it('a 404 on the dev manifest means no update, not an error (debug-key dev builds have no manifest)', async () => {
    const { u, plugin } = await rig({ self: devSelf })
    await u.check(NOW)
    expect(u.status()).toEqual({ phase: 'idle' })
    expect(u.offer()).toBeUndefined()
    expect(plugin.count('fetchText')).toBe(1)
  })

  it('another HTTP error on the manifest is still an error', async () => {
    const { u, plugin } = await rig({ self: devSelf })
    plugin.texts.set(`${DEV_APP_URL.slice(0, DEV_APP_URL.lastIndexOf('/'))}/android-update-dev.json`, { status: 500, text: '' })
    await u.check(NOW)
    expect(u.status().phase).toBe('error')
  })

  it('an equal or lower versionCode means up to date, not an error', async () => {
    for (const code of [stableSelf.versionCode, stableSelf.versionCode - 100]) {
      const { u, plugin } = await rig()
      plugin.texts.set(`${STABLE}/android-update.json`, { status: 200, text: manifest({ versionCode: code }) })
      await u.check(NOW)
      expect(u.status()).toEqual({ phase: 'not-available' })
      expect(plugin.count('fetchText')).toBe(1)
    }
  })

  it('a manifest for another app or an unsafe name is an error and fetches nothing more', async () => {
    for (const over of [{ applicationId: 'io.localai.client.dev' }, { apk: 'evil.apk' }, { minSdk: 99 }, { schema: 2 }]) {
      const { u, plugin } = await rig()
      plugin.texts.set(`${STABLE}/android-update.json`, { status: 200, text: manifest(over) })
      await u.check(NOW)
      expect(u.status().phase, JSON.stringify(over)).toBe('error')
      expect(u.offer()).toBeUndefined()
      expect(plugin.count('fetchText')).toBe(1)
    }
  })

  it('a feed that is not JSON, a server error or a failed request is an error', async () => {
    for (const a of [{ status: 200, text: '<html>' }, { status: 500, text: '' }, new Error('offline')]) {
      const { u, plugin } = await rig()
      plugin.texts.set(`${STABLE}/android-update.json`, a)
      await u.check(NOW)
      expect(u.status().phase).toBe('error')
      expect(u.status().message).toBeTruthy()
    }
  })

  it('the dev app reads the dev-master feed', async () => {
    const { u, plugin } = await rig({ self: devSelf })
    plugin.texts.set(`${DEV}/android-update-dev.json`, {
      status: 200,
      text: manifest({ applicationId: 'io.localai.client.dev', versionName: '5.0.2-dev.7', versionCode: 480000, apk: 'LocalAI-Android-dev.apk' }),
    })
    plugin.texts.set(`${DEV}/LocalAI-Android-dev.apk.sha256`, { status: 200, text: `${SHA}  LocalAI-Android-dev.apk\n` })
    await u.check(NOW)
    expect(u.status()).toEqual({ phase: 'available', version: '5.0.2-dev.7' })
    expect(u.info().channel).toBe('dev')
  })

  it('emits checking, then the result', async () => {
    const { u, plugin, seen } = await rig()
    goodFeed(plugin)
    await u.check(NOW)
    expect(seen.map((s) => s.phase)).toEqual(['checking', 'available'])
  })
})

describe('download', () => {
  it('downloads the offer and checks the signer: downloaded', async () => {
    const { u, plugin } = await rig()
    goodFeed(plugin)
    await u.check(NOW)
    await u.download()
    expect(plugin.calls.find((c) => c.name === 'download')?.args[0]).toEqual({ url: `${STABLE}/LocalAI-Android.apk`, sha256: SHA, size: 41_234_567 })
    expect(plugin.calls.find((c) => c.name === 'verifySigner')?.args[0]).toEqual({ file: FILE })
    expect(u.status()).toEqual({ phase: 'downloaded', version: '4.3.0' })
  })

  it('reports progress while downloading', async () => {
    const { u, plugin, seen } = await rig()
    goodFeed(plugin)
    await u.check(NOW)
    let release!: () => void
    const gate = new Promise<void>((r) => (release = r))
    const orig = plugin.download.bind(plugin)
    plugin.download = async (o) => {
      await gate
      return orig(o)
    }
    const p = u.download()
    await flush()
    plugin.emit('progress', { done: 20_617_284, total: 41_234_567 })
    expect(u.status()).toEqual({ phase: 'downloading', version: '4.3.0', percent: 50 })
    release()
    await p
    expect(seen.some((s) => s.phase === 'downloading' && s.percent === 50)).toBe(true)
  })

  it('another signer: error with the signer message, the file is deleted, nothing installs', async () => {
    const { u, plugin } = await rig()
    goodFeed(plugin)
    plugin.sameSigner = false
    await u.check(NOW)
    await u.download()
    expect(u.status()).toEqual({ phase: 'error', message: SIGNER_MESSAGE })
    expect(SIGNER_MESSAGE).toBe('This update is not signed by the same developer as this app. It was not installed.')
    expect(plugin.calls.find((c) => c.name === 'deleted')?.args).toEqual([FILE])
    expect(u.offer()).toBeUndefined()
    await u.install()
    expect(plugin.count('install')).toBe(0)
  })

  it('a refused download (size or hash) is an error and keeps no offer', async () => {
    const { u, plugin } = await rig()
    goodFeed(plugin)
    plugin.downloadError = codeError('sha256', 'sha256')
    await u.check(NOW)
    await u.download()
    expect(u.status().phase).toBe('error')
    expect(u.status().message).toMatch(/does not match/)
    expect(u.offer()).toBeUndefined()
    expect(plugin.count('verifySigner')).toBe(0)
  })
})

describe('install', () => {
  async function downloaded(allowed: boolean) {
    const r = await rig()
    goodFeed(r.plugin)
    r.plugin.allowed = allowed
    await r.u.check(NOW)
    await r.u.download()
    return r
  }

  it('without the install consent it does not call plugin.install and stays downloaded', async () => {
    const { u, plugin } = await downloaded(false)
    await u.install()
    expect(plugin.count('install')).toBe(0)
    expect(u.status().phase).toBe('downloaded')
    expect(await u.apkUpdate()).toEqual({
      offer: { version: '4.3.0', size: 41_234_567, notesUrl: 'https://github.com/mudler/LocalAI/releases/tag/v4.3.0', downloaded: true },
      canInstall: false,
    })
  })

  it('with the consent it calls plugin.install once with the downloaded file', async () => {
    const { u, plugin } = await downloaded(true)
    await u.install()
    expect(plugin.calls.filter((c) => c.name === 'install').map((c) => c.args[0])).toEqual([{ file: FILE }])
  })

  it('install() returns when Android has the file; the state waits for the status event', async () => {
    const { u, plugin } = await downloaded(true)
    await u.install()
    expect((await u.apkUpdate()).install).toBe('installing')
    plugin.emit('status', { status: 'pending-user-action' })
    expect((await u.apkUpdate()).install).toBe('confirm')
    plugin.emit('status', { status: 'failure', message: 'aborted' })
    const v = await u.apkUpdate()
    expect(v.install).toBeUndefined()
    expect(v.offer?.downloaded).toBe(true)
    expect(u.status()).toEqual({ phase: 'downloaded', version: '4.3.0', message: 'The update was not installed: aborted.' })
    // The file is kept: a second tap installs again without a new download.
    await u.install()
    expect(plugin.count('install')).toBe(2)
    expect(plugin.count('download')).toBe(1)
  })

  it('success keeps the installing state (Android replaces the app)', async () => {
    const { u, plugin } = await downloaded(true)
    await u.install()
    plugin.emit('status', { status: 'success' })
    expect((await u.apkUpdate()).install).toBe('installing')
  })

  it('a status event with no install running is ignored', async () => {
    const { u, plugin } = await downloaded(true)
    plugin.emit('status', { status: 'failure', message: 'aborted' })
    expect(u.status()).toEqual({ phase: 'downloaded', version: '4.3.0' })
  })

  it('a signer refusal at install is the signer error', async () => {
    const { u, plugin } = await downloaded(true)
    plugin.installError = codeError('signer', 'signer')
    await u.install()
    expect(u.status()).toEqual({ phase: 'error', message: SIGNER_MESSAGE })
    expect(u.offer()).toBeUndefined()
    expect((await u.apkUpdate()).install).toBeUndefined()
  })

  it('a consent refusal at install goes back to the consent step', async () => {
    const { u, plugin } = await downloaded(true)
    plugin.installError = codeError('not-allowed', 'not-allowed')
    await u.install()
    expect(u.status().phase).toBe('downloaded')
    expect((await u.apkUpdate()).install).toBeUndefined()
  })

  it('a tap on an offer that is not downloaded yet downloads first, then installs', async () => {
    const { u, plugin } = await rig()
    goodFeed(plugin)
    await u.check(NOW)
    await u.install()
    expect(plugin.calls.map((c) => c.name).filter((n) => n !== 'fetchText')).toEqual(['download', 'verifySigner', 'canInstall', 'install'])
  })

  it('opens the Android settings and the dev app page', async () => {
    const { u, plugin, opened } = await rig()
    await u.openInstallSettings()
    await u.getDevApp()
    expect(plugin.count('openInstallSettings')).toBe(1)
    expect(opened).toEqual([DEV_APP_URL])
  })

  it('refresh sends the current state again, so the sheet re-reads the consent on resume', async () => {
    const { u, plugin, seen } = await downloaded(false)
    await u.install()
    expect((await u.apkUpdate()).canInstall).toBe(false)
    plugin.allowed = true
    const before = seen.length
    u.refresh()
    expect(seen.length).toBe(before + 1)
    expect(seen[seen.length - 1]).toEqual(u.status())
    expect((await u.apkUpdate()).canInstall).toBe(true)
    expect(plugin.count('install')).toBe(0)
  })
})

describe('automatic check', () => {
  it('checks and downloads by itself when due, never installs', async () => {
    const { u, plugin } = await rig()
    goodFeed(plugin)
    await u.maybeAutoCheck(NOW)
    expect(u.status().phase).toBe('downloaded')
    expect(plugin.count('install')).toBe(0)
  })

  it('respects checkDue: not within a day of the last check, not when off', async () => {
    const recent = await rig({ config: (s) => s.update((c) => withLastUpdateCheck(c, NOW - AUTO_CHECK_INTERVAL_MS + 60_000)) })
    await recent.u.maybeAutoCheck(NOW)
    expect(recent.plugin.count('fetchText')).toBe(0)

    const old = await rig({ config: (s) => s.update((c) => withLastUpdateCheck(c, NOW - AUTO_CHECK_INTERVAL_MS - 60_000)) })
    await old.u.maybeAutoCheck(NOW)
    expect(old.plugin.count('fetchText')).toBe(1)

    const off = await rig({ config: (s) => s.update((c) => withAutoCheckUpdates(c, false)) })
    await off.u.maybeAutoCheck(NOW)
    expect(off.plugin.count('fetchText')).toBe(0)
  })

  it('setAutoCheck saves the switch', async () => {
    const { u, store } = await rig()
    expect(u.info()).toEqual({ autoCheck: true, channel: 'stable', devConfirmed: true, remoteDefault: false })
    expect((await u.setAutoCheck(false)).autoCheck).toBe(false)
    expect(autoCheckUpdates(store.get())).toBe(false)
  })
})

describe('appSelf', () => {
  it('reads the channel from the application ID', () => {
    const a = { applicationId: 'io.localai.client', versionName: '4.2.1', versionCode: 400200199, sdkInt: 34, feedBase: BASE }
    expect(appSelf(a)).toEqual({ ...a, channel: 'stable' })
    expect(appSelf({ ...a, applicationId: 'io.localai.client.dev' }).channel).toBe('dev')
  })
})

describe('idleUpdater', () => {
  it('offers nothing and does nothing', async () => {
    const u = idleUpdater()
    expect(u.status()).toEqual({ phase: 'idle' })
    await u.check(NOW)
    await u.maybeAutoCheck(NOW)
    expect(u.offer()).toBeUndefined()
    expect(await u.apkUpdate()).toEqual({ canInstall: false })
  })
})
