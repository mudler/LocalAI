// The one owner of launcher.json while the app runs. No Electron import.
//
// Every change goes through `update`, which runs the changes one after the
// other: a change sees the result of the change before it, and two saves never
// write the file at the same time. A failed save keeps the previous config in
// memory and on disk and does not block the changes queued after it.
//
// Keys that only this launcher writes live in `extra`, beside the keys of the
// Go launcher. An older launcher keeps them as unknown keys and ignores them.
import { loadConfig, saveConfig, type ConfigOptions, type LauncherConfig, type LoadResult } from './config'
import { parsePermissionStore, validateRemoteOrigin, type PermissionStore } from './webuiPolicy'

/** Show the slim status bar under the WebUI. Missing means true. */
export const SHOW_STATUS_BAR_KEY = 'show_status_bar'
/** The launcher version for which the user chose "Not now" on the sandbox notice. */
export const SANDBOX_NOTICE_DISMISSED_KEY = 'sandbox_notice_dismissed_version'

export function showStatusBar(c: LauncherConfig): boolean {
  const v = c.extra[SHOW_STATUS_BAR_KEY]
  return typeof v === 'boolean' ? v : true
}

export function withShowStatusBar(c: LauncherConfig, visible: boolean): LauncherConfig {
  return { ...c, extra: { ...c.extra, [SHOW_STATUS_BAR_KEY]: visible } }
}

export function sandboxNoticeDismissedVersion(c: LauncherConfig): string | null {
  const v = c.extra[SANDBOX_NOTICE_DISMISSED_KEY]
  return typeof v === 'string' && v !== '' ? v : null
}

export function withSandboxNoticeDismissed(c: LauncherConfig, version: string): LauncherConfig {
  return { ...c, extra: { ...c.extra, [SANDBOX_NOTICE_DISMISSED_KEY]: version } }
}

/** Open the WebUI in the system browser instead of the window. Missing means false. */
export const OPEN_WEBUI_IN_BROWSER_KEY = 'open_webui_in_browser'
/** The old "last server chosen in Connect": an origin, or missing. Read for the migration into the server list; never written. */
export const REMOTE_SERVER_URL_KEY = 'remote_server_url'
/** Remembered microphone and camera choices of the WebUI view, per origin. */
export const WEBUI_PERMISSIONS_KEY = 'webui_permissions'

export function openWebuiInBrowser(c: LauncherConfig): boolean {
  return c.extra[OPEN_WEBUI_IN_BROWSER_KEY] === true
}

export function withOpenWebuiInBrowser(c: LauncherConfig, v: boolean): LauncherConfig {
  return { ...c, extra: { ...c.extra, [OPEN_WEBUI_IN_BROWSER_KEY]: v } }
}

/** The saved remote origin, checked again on read: a hand-edited file is not trusted. */
export function remoteServerUrl(c: LauncherConfig): string {
  const v = c.extra[REMOTE_SERVER_URL_KEY]
  if (typeof v !== 'string' || v === '') return ''
  const r = validateRemoteOrigin(v)
  return r.ok ? r.origin : ''
}

export function webuiPermissions(c: LauncherConfig): PermissionStore {
  return parsePermissionStore(c.extra[WEBUI_PERMISSIONS_KEY])
}

export function withWebuiPermissions(c: LauncherConfig, store: PermissionStore): LauncherConfig {
  const extra = { ...c.extra }
  if (Object.keys(store).length === 0) delete extra[WEBUI_PERMISSIONS_KEY]
  else extra[WEBUI_PERMISSIONS_KEY] = store
  return { ...c, extra }
}

/** Show a desktop notification for each new item of a server's notification inbox. Missing means true. */
export const DESKTOP_NOTIFICATIONS_KEY = 'desktop_notifications'

export function desktopNotifications(c: LauncherConfig): boolean {
  return c.extra[DESKTOP_NOTIFICATIONS_KEY] !== false
}

export function withDesktopNotifications(c: LauncherConfig, on: boolean): LauncherConfig {
  return { ...c, extra: { ...c.extra, [DESKTOP_NOTIFICATIONS_KEY]: on } }
}

/** Check for LocalAI updates by itself (missing means true). */
export const AUTO_CHECK_UPDATES_KEY = 'auto_check_updates'
/** When the last server update check finished, in milliseconds since the epoch. */
export const LAST_UPDATE_CHECK_KEY = 'last_update_check'

export function autoCheckUpdates(c: LauncherConfig): boolean {
  return c.extra[AUTO_CHECK_UPDATES_KEY] !== false
}

export function withAutoCheckUpdates(c: LauncherConfig, on: boolean): LauncherConfig {
  return { ...c, extra: { ...c.extra, [AUTO_CHECK_UPDATES_KEY]: on } }
}

export function lastUpdateCheck(c: LauncherConfig): number | undefined {
  const v = c.extra[LAST_UPDATE_CHECK_KEY]
  return typeof v === 'number' && Number.isFinite(v) && v > 0 ? v : undefined
}

export function withLastUpdateCheck(c: LauncherConfig, ms: number): LauncherConfig {
  return { ...c, extra: { ...c.extra, [LAST_UPDATE_CHECK_KEY]: ms } }
}

/** Without secure storage, keep secrets in a plain 0600 file instead of only for the session. Missing means false. */
export const SECRETS_PLAINTEXT_FILE_KEY = 'secrets_plaintext_file'

export function secretsPlaintextFile(c: LauncherConfig): boolean {
  return c.extra[SECRETS_PLAINTEXT_FILE_KEY] === true
}

export function withSecretsPlaintextFile(c: LauncherConfig, on: boolean): LauncherConfig {
  const extra = { ...c.extra }
  if (on) extra[SECRETS_PLAINTEXT_FILE_KEY] = true
  else delete extra[SECRETS_PLAINTEXT_FILE_KEY]
  return { ...c, extra }
}

function clone(c: LauncherConfig): LauncherConfig {
  return structuredClone(c)
}

export interface ConfigStoreOptions extends ConfigOptions {
  /** Replaces saveConfig, for tests. */
  save?: (config: LauncherConfig, opts: ConfigOptions) => Promise<void>
}

export class ConfigStore {
  private current: LauncherConfig
  private queue: Promise<unknown> = Promise.resolve()
  private readonly save: (config: LauncherConfig, opts: ConfigOptions) => Promise<void>

  private constructor(
    private readonly opts: ConfigStoreOptions,
    readonly loaded: LoadResult,
  ) {
    this.current = clone(loaded.config)
    this.save = opts.save ?? saveConfig
  }

  static async open(opts: ConfigStoreOptions = {}): Promise<ConfigStore> {
    return new ConfigStore(opts, await loadConfig(opts))
  }

  /** A copy of the config as last saved (or loaded). */
  get(): LauncherConfig {
    return clone(this.current)
  }

  /**
   * Queues a change. `change` gets a copy of the config as it is after every
   * change queued before this one, and returns the new config. The promise
   * resolves with the saved config, or rejects with the save error; either way
   * the next queued change runs.
   */
  update(change: (c: LauncherConfig) => LauncherConfig): Promise<LauncherConfig> {
    const run = this.queue.then(async () => {
      const next = change(clone(this.current))
      await this.save(next, this.opts)
      this.current = clone(next)
      return clone(next)
    })
    this.queue = run.catch(() => undefined)
    return run
  }

  /** Resolves when every change queued so far has finished. */
  idle(): Promise<void> {
    return this.queue.then(() => undefined)
  }
}
