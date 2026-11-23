// Turns electron-updater events into one status shape. No Electron import.
import path from 'node:path'
import type { UpdateChannelName, UpdaterStatus } from '../shared/types'
import { devLauncherFeedUrl } from './devFeed'

export type RawUpdaterEvent =
  | { type: 'checking-for-update' }
  | { type: 'update-available'; info?: { version?: string } }
  | { type: 'update-not-available'; info?: { version?: string } }
  | { type: 'download-progress'; progress?: { percent?: number } }
  | { type: 'update-downloaded'; info?: { version?: string } }
  | { type: 'error'; error?: unknown }

export function normalizeUpdaterEvent(ev: RawUpdaterEvent, prev: UpdaterStatus): UpdaterStatus {
  switch (ev.type) {
    case 'checking-for-update':
      return { phase: 'checking' }
    case 'update-available':
      return { phase: 'available', version: ev.info?.version }
    case 'update-not-available':
      return { phase: 'not-available', version: ev.info?.version }
    case 'download-progress': {
      const p = ev.progress?.percent
      const percent = typeof p === 'number' && Number.isFinite(p) ? Math.max(0, Math.min(100, Math.round(p))) : 0
      return { phase: 'downloading', version: prev.version, percent }
    }
    case 'update-downloaded':
      return { phase: 'downloaded', version: ev.info?.version ?? prev.version }
    case 'error': {
      const e = ev.error
      const message = e instanceof Error ? e.message : typeof e === 'string' ? e : 'update check failed'
      // First line only: electron-updater appends long HTTP headers to messages.
      return { phase: 'error', message: message.split('\n')[0].slice(0, 200) }
    }
  }
}

export type LauncherUpdateAction = 'install' | 'download' | 'none'

/**
 * What the user's "Restart to update" does in each phase: install a
 * downloaded update, download an available one, otherwise nothing.
 */
export function launcherUpdateAction(s: UpdaterStatus): LauncherUpdateAction {
  if (s.phase === 'downloaded') return 'install'
  if (s.phase === 'available') return 'download'
  return 'none'
}

export type SelfUpdateGate =
  /** `dev`: the update channel is Development, so the feed is the rolling `dev-master` release. */
  | { enabled: true; feedUrl?: string; dev?: true }
  | { enabled: false; reason: 'unpackaged' | 'dev-build' | 'portable' }

/** A development build from master: `<version>-dev.<run number>` (see the release flow). */
const DEV_BUILD = /-dev\.\d+(?:[.+]|$)/

/**
 * Whether the launcher updates itself, and from which feed. Off in a run from
 * source. A test feed (`LOCALAI_LAUNCHER_FEED_URL`) always wins. Otherwise:
 *
 * - Channel `dev`: on, from the development feed (`dev` in the result). The
 *   portable Windows zip stays off, because it has no installer to update it.
 * - Any other channel: a development build stays off, because it must never
 *   take a stable release from the feed. The portable zip stays off. Every
 *   other build uses the release feed, as before.
 *
 * A missing channel means `stable`.
 */
export function selfUpdateGate(o: { packaged: boolean; version: string; feedUrl?: string; portable?: boolean; channel?: UpdateChannelName }): SelfUpdateGate {
  if (!o.packaged) return { enabled: false, reason: 'unpackaged' }
  const feedUrl = o.feedUrl || undefined
  if (feedUrl) return { enabled: true, feedUrl }
  if (o.channel === 'dev') return o.portable ? { enabled: false, reason: 'portable' } : { enabled: true, dev: true }
  if (DEV_BUILD.test(o.version)) return { enabled: false, reason: 'dev-build' }
  if (o.portable) return { enabled: false, reason: 'portable' }
  return { enabled: true }
}

/** What the updater reads: electron-updater's own default feed (the GitHub releases of the app), or a generic feed folder. */
export type LauncherFeed = { kind: 'default' } | { kind: 'generic'; url: string }

/** The feed of an enabled gate: the test feed, the development feed, or the release feed. */
export function launcherFeed(gate: Extract<SelfUpdateGate, { enabled: true }>, repo: { owner: string; repo: string }): LauncherFeed {
  if (gate.feedUrl) return { kind: 'generic', url: gate.feedUrl }
  if (gate.dev) return { kind: 'generic', url: devLauncherFeedUrl(repo) }
  return { kind: 'default' }
}

/**
 * The uninstaller that the NSIS installer puts next to the launcher. Its name
 * follows the install folder name ("Uninstall <folder>.exe"), which is the
 * package name for a per-user install, so match the pattern, not one name.
 */
export const NSIS_UNINSTALLER = /^Uninstall .+\.exe$/i

/**
 * True for a Windows launcher that the installer did not install: the
 * portable zip, unpacked anywhere. Only an installed launcher can update
 * itself, because the update is the next installer. Other platforms: false.
 */
export function isPortableWindows(platform: NodeJS.Platform, execPath: string, listDir: (dir: string) => string[]): boolean {
  if (platform !== 'win32') return false
  return !listDir(path.win32.dirname(execPath)).some((f) => NSIS_UNINSTALLER.test(f))
}

/**
 * electron-updater settings. electron-updater turns `allowPrerelease` on by
 * itself when the running version has a pre-release part, so it is always set
 * here: a release-candidate launcher takes stable updates only, unless the
 * user turned on "Also offer release candidates" (config
 * `include_prereleases`). Downgrades are never offered. Drafts are never
 * visible to an unauthenticated feed. Nothing downloads or installs without
 * the user.
 */
export function updaterSettings(includePrereleases: boolean): {
  autoDownload: false
  autoInstallOnAppQuit: false
  allowPrerelease: boolean
  allowDowngrade: false
} {
  return { autoDownload: false, autoInstallOnAppQuit: false, allowPrerelease: includePrereleases, allowDowngrade: false }
}

/**
 * The electron-updater settings of a channel. Development adds one thing: no
 * differential download. The development files keep one name from build to
 * build, so a block map cannot tell the old file from the new one by name; a
 * full download is checked against the SHA-512 of the feed, as always. Downgrades
 * stay off: a development launcher is "newer" when its version is higher
 * (`<next patch>-dev.<run number>` rises with each build, see scripts/version.mjs).
 */
export function launcherUpdaterSettings(channel: UpdateChannelName): ReturnType<typeof updaterSettings> & { disableDifferentialDownload: boolean } {
  return { ...updaterSettings(channel === 'rc'), disableDifferentialDownload: channel === 'dev' }
}
