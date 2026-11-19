// Turns electron-updater events into one status shape. No Electron import.
import type { UpdaterStatus } from '../shared/types'

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
  | { enabled: true; feedUrl?: string }
  | { enabled: false; reason: 'unpackaged' | 'dev-build' }

/** A development build from master: `<version>-dev.<run number>` (see the release flow). */
const DEV_BUILD = /-dev\.\d+(?:[.+]|$)/

/**
 * Whether the launcher updates itself. Off in a run from source. Off in a
 * development build, which must never take a stable release from the
 * feed, unless a test feed is set (`LOCALAI_LAUNCHER_FEED_URL`).
 */
export function selfUpdateGate(o: { packaged: boolean; version: string; feedUrl?: string }): SelfUpdateGate {
  if (!o.packaged) return { enabled: false, reason: 'unpackaged' }
  const feedUrl = o.feedUrl || undefined
  if (DEV_BUILD.test(o.version)) return feedUrl ? { enabled: true, feedUrl } : { enabled: false, reason: 'dev-build' }
  return feedUrl ? { enabled: true, feedUrl } : { enabled: true }
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
