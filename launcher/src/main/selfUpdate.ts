// Updates of the launcher itself, through electron-updater. Packaged release
// builds only (see selfUpdateGate: not from source, not a development build).
// This file imports Electron and electron-updater, so it has no unit test; the
// event mapping it uses is tested in policy.test.ts and selfUpdateGate.test.ts,
// and the Linux install in appImageUpdate.test.ts.
import { app } from 'electron'
import fs from 'node:fs'
import { autoUpdater, type UpdateDownloadedEvent } from 'electron-updater'
import type { UpdaterStatus } from '../shared/types'
import { appImageEntry, installPath, swapAppImage, type DownloadedAppImage } from './appImageUpdate'
import { isPortableWindows, launcherUpdateAction, normalizeUpdaterEvent, selfUpdateGate, updaterSettings, type RawUpdaterEvent } from './updaterStatus'

let status: UpdaterStatus = { phase: 'unsupported', message: 'self-update runs in packaged builds only' }
let push: (s: UpdaterStatus) => void = () => undefined
let enabled = false
/** The last download electron-updater finished and verified, for the AppImage swap. */
let downloaded: DownloadedAppImage | undefined

function set(ev: RawUpdaterEvent) {
  status = normalizeUpdaterEvent(ev, status)
  push(status)
}

export function getUpdaterStatus(): UpdaterStatus {
  return status
}

export function initSelfUpdate(onStatus: (s: UpdaterStatus) => void, o: { includePrereleases: boolean }): void {
  push = onStatus
  const gate = selfUpdateGate({
    packaged: app.isPackaged,
    version: app.getVersion(),
    feedUrl: process.env.LOCALAI_LAUNCHER_FEED_URL,
    portable: isPortableWindows(process.platform, process.execPath, (d) => { try { return fs.readdirSync(d) } catch { return [] } }),
  })
  if (!gate.enabled) {
    if (gate.reason === 'dev-build') status = { phase: 'unsupported', message: 'development builds do not update themselves' }
    if (gate.reason === 'portable') status = { phase: 'unsupported', message: 'the zip build does not update itself; use the installer, or download the new zip' }
    return
  }
  enabled = true
  status = { phase: 'idle' }
  Object.assign(autoUpdater, updaterSettings(o.includePrereleases))
  // A test or a mirror can point the launcher at a generic feed.
  if (gate.feedUrl) autoUpdater.setFeedURL({ provider: 'generic', url: gate.feedUrl })
  autoUpdater.on('checking-for-update', () => set({ type: 'checking-for-update' }))
  autoUpdater.on('update-available', (info) => set({ type: 'update-available', info }))
  autoUpdater.on('update-not-available', (info) => set({ type: 'update-not-available', info }))
  autoUpdater.on('download-progress', (progress) => set({ type: 'download-progress', progress }))
  autoUpdater.on('update-downloaded', (info: UpdateDownloadedEvent) => {
    // electron-updater checked this file against the feed's SHA-512 before it emitted the event.
    downloaded = { file: info.downloadedFile, version: info.version, ...appImageEntry(info) }
    set({ type: 'update-downloaded', info })
  })
  autoUpdater.on('error', (error) => set({ type: 'error', error }))
}

/** "Also offer release candidates" changed: the next check uses the new value. */
export function setLauncherPrereleases(on: boolean): void {
  if (!enabled) return
  autoUpdater.allowPrerelease = updaterSettings(on).allowPrerelease
}

export async function checkForLauncherUpdate(): Promise<void> {
  if (!enabled) return
  try {
    await autoUpdater.checkForUpdates()
  } catch (error) {
    set({ type: 'error', error })
  }
}

export interface InstallHooks {
  /** Stops the server; electron-updater's install quits the app after it. */
  beforeQuit(): Promise<void>
  /** Stops the server and starts the launcher again from its AppImage file. */
  relaunch(): void
  log(message: string, level?: 'info' | 'warn' | 'error'): void
}

/** How "Restart to update" installs a downloaded update. */
interface UpdateInstaller {
  install(hooks: InstallHooks): Promise<void>
}

/**
 * macOS, and Windows (the next NSIS installer, which closes the launcher,
 * installs and starts it again): electron-updater's own install. Any build
 * that is not an AppImage.
 */
const electronUpdaterInstall: UpdateInstaller = {
  async install(hooks) {
    await hooks.beforeQuit()
    autoUpdater.quitAndInstall(false, true)
  },
}

/**
 * Linux AppImage: the atomic swap of appImageUpdate.ts, then a relaunch of
 * the same file name. electron-updater's own AppImage install is never
 * called, because it deletes the old file before it moves the new one in.
 * On a failure the launcher keeps running on the old file and reports why.
 */
const appImageSwapInstall: UpdateInstaller = {
  async install(hooks) {
    const r = swapAppImage(downloaded, process.env.APPIMAGE)
    if (!r.ok) {
      hooks.log(`launcher update not installed (${r.reason}): ${r.message}`, 'error')
      set({ type: 'error', error: new Error(`the update could not be installed (${r.reason}); this version stays`) })
      return
    }
    hooks.log(`launcher update${downloaded?.version ? ` ${downloaded.version}` : ''} installed in place; restarting`)
    hooks.relaunch()
  },
}

/**
 * The user's "Restart to update": installs a downloaded update (the
 * AppImage swap on Linux, electron-updater on macOS and Windows), or downloads an
 * available one. Packaged release builds only.
 */
export async function installLauncherUpdate(hooks: InstallHooks): Promise<void> {
  if (!enabled) return
  const action = launcherUpdateAction(status)
  if (action === 'download') {
    try {
      await autoUpdater.downloadUpdate()
    } catch (error) {
      set({ type: 'error', error })
    }
  } else if (action === 'install') {
    const installer = installPath(process.platform, process.env) === 'appimage-swap' ? appImageSwapInstall : electronUpdaterInstall
    await installer.install(hooks)
  }
}
