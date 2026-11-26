// SPDX-License-Identifier: MIT
// The app's own updates: the release feed is read and judged (apkUpdate.ts),
// the APK is downloaded and checked by the Updater plugin, and Android
// installs it. The state goes to the shell as an UpdaterStatus, plus the
// update sheet's view (getApkUpdate).
//
// Nothing installs without a tap: the daily check may download by itself,
// but install() runs only from the sheet's "Update now".
import type { PluginListenerHandle } from '@capacitor/core'
import { autoCheckUpdates, lastUpdateCheck, withAutoCheckUpdates, withLastUpdateCheck, type ConfigStore } from '@launcher/core/configStore'
import type { ApkUpdateView } from '@launcher/renderer/bridge'
import type { UpdateInfoView, UpdaterStatus } from '@launcher/shared/types'
import type { UpdaterPlugin } from '../plugins/definitions'
import { checkDue, checkManifest, checksumAgrees, DEV_APP_URL, feedUrls, type ApkOffer, type AppSelf, type ManifestCheck } from './apkUpdate'

export interface AndroidUpdater {
  status(): UpdaterStatus
  on(cb: (s: UpdaterStatus) => void): () => void
  /** One channel per app: the development app has its own application ID. */
  info(): UpdateInfoView
  /** Reads the feed: `available` with an offer, `not-available` when up to date, `idle` when there is no release yet. */
  check(now: number): Promise<void>
  /** Downloads the offer and checks its signer: `downloaded`. */
  download(): Promise<void>
  /** From a tap only. Without the install consent it does nothing (the sheet shows the consent step). */
  install(): Promise<void>
  setAutoCheck(on: boolean): Promise<UpdateInfoView>
  /** On open and on resume: the daily check, then the download. Never the install. */
  maybeAutoCheck(now: number): Promise<void>
  /** Sends the current state again, so open views read theirs (back from the Android settings). */
  refresh(): void
  offer(): ApkOffer | undefined
  apkUpdate(): Promise<ApkUpdateView>
  openInstallSettings(): Promise<void>
  getDevApp(): Promise<void>
  dispose?(): void
}

/** A build or a page with no Updater plugin: nothing to check, nothing offered. */
export function idleUpdater(): AndroidUpdater {
  const info: UpdateInfoView = { autoCheck: false, channel: 'stable', devConfirmed: true, remoteDefault: false }
  return {
    status: () => ({ phase: 'idle' }),
    on: () => () => undefined,
    info: () => ({ ...info }),
    check: async () => undefined,
    download: async () => undefined,
    install: async () => undefined,
    setAutoCheck: async () => ({ ...info }),
    maybeAutoCheck: async () => undefined,
    refresh: () => undefined,
    offer: () => undefined,
    apkUpdate: async () => ({ canInstall: false }),
    openInstallSettings: async () => undefined,
    getDevApp: async () => undefined,
  }
}

/** The running app from the plugin's appInfo: the development app is the one whose ID ends in `.dev`. */
export function appSelf(a: Awaited<ReturnType<UpdaterPlugin['appInfo']>>): AppSelf {
  return { ...a, channel: a.applicationId.endsWith('.dev') ? 'dev' : 'stable' }
}

export const SIGNER_MESSAGE = 'This update is not signed by the same developer as this app. It was not installed.'

// The feed and checksum files are small; anything bigger is not ours.
const MAX_FEED_BYTES = 64 * 1024

const REFUSED: Record<Exclude<ManifestCheck, { ok: true }>['reason'], string> = {
  schema: 'The update information of the release cannot be read.',
  'app-id': 'The update information of the release is for another app.',
  'min-sdk': 'The new version needs a newer Android version than this device has.',
  'not-newer': 'This app is up to date.',
  'apk-name': 'The update information of the release names an unexpected file.',
  size: 'The update information of the release gives a wrong file size.',
  sha256: 'The update information of the release gives a wrong checksum.',
}

function errorCode(e: unknown): string {
  const code = (e as { code?: unknown } | null)?.code
  if (typeof code === 'string' && code) return code
  return e instanceof Error ? e.message : String(e)
}

function downloadMessage(code: string): string {
  if (code === 'size' || code === 'sha256') return 'The download does not match its checksum. It was not installed.'
  return 'The download failed. Check the network connection and try again.'
}

export function createAndroidUpdater(p: { plugin: UpdaterPlugin; store: ConfigStore; self: AppSelf; openExternal(url: string): Promise<void> }): AndroidUpdater {
  const { plugin, store, self } = p
  const subs = new Set<(s: UpdaterStatus) => void>()
  let state: UpdaterStatus = { phase: 'idle' }
  let current: ApkOffer | undefined
  let file: string | undefined
  let installing: 'installing' | 'confirm' | undefined
  let busy: Promise<void> | undefined
  const handles: Promise<PluginListenerHandle>[] = []

  const set = (s: UpdaterStatus) => {
    state = s
    for (const cb of [...subs]) cb({ ...state })
  }
  const fail = (message: string) => {
    current = undefined
    file = undefined
    installing = undefined
    set({ phase: 'error', message })
  }
  // One check or download at a time; a second call waits for the first.
  const exclusive = (run: () => Promise<void>): Promise<void> => {
    if (busy) return busy
    busy = run().finally(() => {
      busy = undefined
    })
    return busy
  }
  // A failed call counts as "not allowed": the sheet then shows the consent step.
  const allowedToInstall = (): Promise<boolean> =>
    plugin.canInstall().then(
      (r) => r.allowed === true,
      () => false,
    )
  const recordCheck = async (now: number) => {
    try {
      await store.update((c) => withLastUpdateCheck(c, now))
    } catch {
      // The next start checks again; nothing else depends on it.
    }
  }

  const listen = (run: () => Promise<PluginListenerHandle>) => {
    try {
      handles.push(run().catch(() => ({ remove: async () => undefined })))
    } catch {
      // No native plugin (a web preview): no events.
    }
  }
  listen(() =>
    plugin.addListener('progress', (x) => {
      if (state.phase !== 'downloading' || !(x.total > 0)) return
      const percent = Math.min(100, Math.floor((x.done * 100) / x.total))
      if (percent !== state.percent) set({ ...state, percent })
    }),
  )
  // install() returns once Android has the file; the result comes here.
  listen(() =>
    plugin.addListener('status', (x) => {
      if (!installing) return
      if (x.status === 'success') return // Android replaces the app and ends this process.
      if (x.status === 'pending-user-action') {
        installing = 'confirm'
        set({ ...state })
        return
      }
      installing = undefined
      const why = x.message?.trim()
      set({ phase: 'downloaded', version: current?.versionName, message: `The update was not installed${why ? `: ${why}` : ''}.` })
    }),
  )

  const runCheck = async (now: number) => {
    set({ phase: 'checking' })
    const urls = feedUrls(self)
    try {
      const r = await plugin.fetchText({ url: urls.manifest, maxBytes: MAX_FEED_BYTES })
      // No manifest means no update to offer. GitHub's `latest` has none until the
      // first stable APK is released, and `dev-master` has none while CI publishes
      // the dev APK signed with a throwaway debug key, which no installed app
      // could update to (see .github/workflows/android-client.yaml).
      if (r.status === 404) {
        current = undefined
        file = undefined
        set({ phase: 'idle' })
        return
      }
      if (r.status !== 200) return fail(`The update feed answered with HTTP ${r.status}.`)
      let raw: unknown
      try {
        raw = JSON.parse(r.text)
      } catch {
        return fail(REFUSED.schema)
      }
      const c = checkManifest(raw, self)
      if (!c.ok) {
        if (c.reason === 'not-newer') {
          current = undefined
          file = undefined
          set({ phase: 'not-available' })
          return
        }
        return fail(REFUSED[c.reason])
      }
      const sums = await plugin.fetchText({ url: c.offer.checksumUrl, maxBytes: MAX_FEED_BYTES })
      if (sums.status !== 200) return fail('The release has no checksum file, so the app cannot check the update.')
      const apkName = c.offer.apkUrl.slice(c.offer.apkUrl.lastIndexOf('/') + 1)
      if (!checksumAgrees(c.offer.sha256, sums.text, apkName)) return fail('The checksum file of the release does not match its update information. Nothing was downloaded.')
      // The same release, already downloaded and checked: keep the file.
      const same = current && file && current.versionCode === c.offer.versionCode && current.sha256 === c.offer.sha256
      current = c.offer
      if (same) set({ phase: 'downloaded', version: c.offer.versionName })
      else {
        file = undefined
        set({ phase: 'available', version: c.offer.versionName })
      }
    } catch {
      fail('The app could not check for updates. Check the network connection.')
    } finally {
      await recordCheck(now)
    }
  }

  const runDownload = async () => {
    const offer = current
    if (!offer || file) return
    set({ phase: 'downloading', version: offer.versionName, percent: 0 })
    let got: string
    try {
      got = (await plugin.download({ url: offer.apkUrl, sha256: offer.sha256, size: offer.size })).file
    } catch (e) {
      return fail(downloadMessage(errorCode(e)))
    }
    try {
      // The plugin deletes a file from another signer before it answers.
      if (!(await plugin.verifySigner({ file: got })).sameSigner) return fail(SIGNER_MESSAGE)
    } catch {
      return fail('The app could not check the downloaded update. It was not installed.')
    }
    file = got
    set({ phase: 'downloaded', version: offer.versionName })
  }

  const u: AndroidUpdater = {
    status: () => ({ ...state }),
    on(cb) {
      subs.add(cb)
      return () => void subs.delete(cb)
    },
    info: () => {
      const c = store.get()
      const checkedAt = lastUpdateCheck(c)
      return { autoCheck: autoCheckUpdates(c), ...(checkedAt ? { checkedAt } : {}), channel: self.channel, devConfirmed: true, remoteDefault: false }
    },
    check: (now) => exclusive(() => runCheck(now)),
    download: () => exclusive(runDownload),
    async install() {
      if (busy) await busy
      if (!file) await u.download()
      const f = file
      if (!f) return
      if (!(await allowedToInstall())) {
        // Tell the sheet to read its view again: it shows the consent step.
        set({ ...state })
        return
      }
      installing = 'installing'
      set({ phase: 'downloaded', version: current?.versionName })
      try {
        await plugin.install({ file: f })
      } catch (e) {
        const code = errorCode(e)
        if (code === 'signer') return fail(SIGNER_MESSAGE)
        if (code === 'file') return fail('The downloaded update is gone. Check for updates again.')
        installing = undefined
        if (code === 'not-allowed') set({ phase: 'downloaded', version: current?.versionName })
        else set({ phase: 'downloaded', version: current?.versionName, message: 'The update was not installed. Try again.' })
      }
    },
    async setAutoCheck(on) {
      await store.update((c) => withAutoCheckUpdates(c, on))
      return u.info()
    },
    async maybeAutoCheck(now) {
      if (busy || installing) return
      const c = store.get()
      if (!checkDue(autoCheckUpdates(c), lastUpdateCheck(c), now)) return
      await u.check(now)
      if (state.phase === 'available') await u.download()
    },
    refresh: () => set({ ...state }),
    offer: () => (current ? { ...current } : undefined),
    async apkUpdate() {
      const canInstall = await allowedToInstall()
      return {
        ...(current ? { offer: { version: current.versionName, size: current.size, notesUrl: current.notesUrl, downloaded: !!file } } : {}),
        canInstall,
        ...(installing ? { install: installing } : {}),
      }
    },
    openInstallSettings: () => plugin.openInstallSettings(),
    getDevApp: () => p.openExternal(DEV_APP_URL),
    dispose() {
      for (const h of handles) void h.then((x) => x.remove()).catch(() => undefined)
      subs.clear()
    },
  }
  return u
}
