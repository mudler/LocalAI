// Maps launcher.json to the typed settings the renderer sees. Pure, no Electron import.
//
// Secrets are not here: environment variable values and the API key live in
// the secret store (secretStore.ts) and never reach the renderer through
// settings. `environment_vars` of launcher.json is only read once, to move
// old plain-text values into the store (see core.ts).
import path from 'node:path'
import type { FolderRefusal, LauncherSettings, ServerLogLevel } from '../shared/types'
import type { LauncherConfig } from './config'
import {
  openWebuiInBrowser,
  remoteServerUrl,
  secretsPlaintextFile,
  showStatusBar,
  withOpenWebuiInBrowser,
  withSecretsPlaintextFile,
  withShowStatusBar,
} from './configStore'
import { SERVER_LOG_LEVELS } from './ipcValidate'

export function toSettings(c: LauncherConfig): LauncherSettings {
  const level = (SERVER_LOG_LEVELS as readonly string[]).includes(c.log_level) ? (c.log_level as ServerLogLevel) : 'info'
  return {
    modelsPath: c.models_path,
    backendsPath: c.backends_path,
    address: c.address,
    autoStartServer: c.auto_start_server,
    startOnBoot: c.start_on_boot,
    logLevel: level,
    showWelcome: c.show_welcome,
    showStatusBar: showStatusBar(c),
    openWebuiInBrowser: openWebuiInBrowser(c),
    remoteServerUrl: remoteServerUrl(c),
    secretsPlaintextFile: secretsPlaintextFile(c),
  }
}

/** Applies an already validated patch. Unknown config keys are kept. */
export function applySettings(c: LauncherConfig, p: Partial<LauncherSettings>): LauncherConfig {
  let out: LauncherConfig = {
    ...c,
    models_path: p.modelsPath ?? c.models_path,
    backends_path: p.backendsPath ?? c.backends_path,
    address: p.address ?? c.address,
    auto_start_server: p.autoStartServer ?? c.auto_start_server,
    start_on_boot: p.startOnBoot ?? c.start_on_boot,
    log_level: p.logLevel ?? c.log_level,
    environment_vars: { ...c.environment_vars },
    show_welcome: p.showWelcome ?? c.show_welcome,
    extra: { ...c.extra },
  }
  if (p.showStatusBar !== undefined) out = withShowStatusBar(out, p.showStatusBar)
  if (p.openWebuiInBrowser !== undefined) out = withOpenWebuiInBrowser(out, p.openWebuiInBrowser)
  if (p.secretsPlaintextFile !== undefined) out = withSecretsPlaintextFile(out, p.secretsPlaintextFile)
  return out
}

/** What checkDataFolder needs to know about the disk. */
export interface FolderProbe {
  /** true: a folder; false: something else; undefined: nothing there. */
  isDir(p: string): boolean | undefined
  /** This user can create entries in the folder p. */
  writable(p: string): boolean
}

/**
 * Checks a data folder that the main-process folder picker returned. The
 * renderer never sends a path: the folder comes only from the native picker,
 * and this check runs on it before it is saved. Absolute, normalized, not
 * the file system root, not the home folder itself, and a folder that
 * exists or can be created.
 */
export function checkDataFolder(raw: string, home: string, probe: FolderProbe): { ok: true; path: string } | { ok: false; reason: FolderRefusal } {
  if (typeof raw !== 'string' || raw === '' || raw.includes('\u0000') || !path.isAbsolute(raw)) return { ok: false, reason: 'not-absolute' }
  let p = path.normalize(raw)
  const root = path.parse(p).root
  if (p === root) return { ok: false, reason: 'root' }
  p = p.replace(/[\\/]+$/, '')
  if (p === path.normalize(home).replace(/[\\/]+$/, '')) return { ok: false, reason: 'home' }
  const kind = probe.isDir(p)
  if (kind === true) return { ok: true, path: p }
  if (kind === false) return { ok: false, reason: 'not-a-folder' }
  // Missing: the nearest folder that exists above it must be writable.
  let up = path.dirname(p)
  for (;;) {
    const k = probe.isDir(up)
    if (k === true) return probe.writable(up) ? { ok: true, path: p } : { ok: false, reason: 'not-creatable' }
    if (k === false || up === path.dirname(up)) return { ok: false, reason: 'not-creatable' }
    up = path.dirname(up)
  }
}
