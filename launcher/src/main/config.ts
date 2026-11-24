// Reads and writes launcher.json. Same file, same keys as the Go launcher, so
// existing installs keep working with no data move. No Electron import.
//
// Rules:
// - Unknown keys survive a load and save round trip.
// - Writes are atomic (temp file, fsync, rename) and the file mode is 0600.
// - A corrupt file is moved aside and defaults are used. The caller gets a
//   reason, nothing is thrown.
// - Config values are never logged. Only key names and outcomes are.
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import crypto from 'node:crypto'
import { normalizeAddress } from './network'
import { homeLayout, type LauncherLayout } from './paths'

/** The keys the Go launcher wrote, with the same names and the same order. */
export interface LauncherConfig {
  models_path: string
  backends_path: string
  address: string
  /** Unset in an old file means enabled. See `migrate`. */
  auto_start_server: boolean
  start_on_boot: boolean
  log_level: string
  environment_vars: Record<string, string>
  show_welcome: boolean
  /** Keys this version does not know. Written back unchanged. */
  extra: Record<string, unknown>
}

export const KNOWN_KEYS = [
  'models_path',
  'backends_path',
  'address',
  'auto_start_server',
  'start_on_boot',
  'log_level',
  'environment_vars',
  'show_welcome',
] as const

export type ConfigLog = (message: string) => void

export interface ConfigOptions {
  /** Full path of launcher.json. Default: <home>/.localai/launcher.json. */
  path?: string
  /** Home directory used for defaults. Default: os.homedir(). */
  home?: string
  /** Folders for defaults (paths.ts). Default: <home>/.localai for all. */
  layout?: LauncherLayout
  /** Receives key names and outcomes only. */
  log?: ConfigLog
  now?: () => Date
}

export type LoadStatus = 'ok' | 'missing' | 'recovered'

export interface LoadResult {
  config: LauncherConfig
  status: LoadStatus
  /** Set when status is `recovered`. Never contains file content. */
  reason?: string
  /** Where the corrupt file went. */
  backupPath?: string
  /** Names of keys that had the wrong type and fell back to the default. */
  invalidKeys: string[]
}

export function defaultConfigPath(home: string = os.homedir(), layout: LauncherLayout = homeLayout(home)): string {
  return path.join(layout.launcherDir, 'launcher.json')
}

/** Port of Launcher.GetDataPath: the parent of the models folder, else the default data folder. */
export function dataPath(config: Pick<LauncherConfig, 'models_path'> | undefined, home: string = os.homedir(), layout: LauncherLayout = homeLayout(home)): string {
  if (config && config.models_path) return path.dirname(config.models_path)
  return layout.dataDir
}

export function defaultConfig(home: string = os.homedir(), layout?: LauncherLayout): LauncherConfig {
  return migrate({}, { home, layout }).config
}

const isObject = (v: unknown): v is Record<string, unknown> =>
  typeof v === 'object' && v !== null && !Array.isArray(v)

/**
 * Pure. Turns whatever was parsed from launcher.json into a full config.
 *
 * Defaults are the ones Launcher.Initialize applies in the Go launcher.
 * `auto_start_server` and `show_welcome` were pointers there: a missing or
 * null key means true. The key is `auto_start_server`, not the old
 * `auto_start`, because every old file carries a meaningless `auto_start:
 * false`; that key is kept as an unknown key and never read.
 * A value of the wrong type falls back to the default and is named in
 * `invalidKeys`.
 */
export function migrate(raw: unknown, opts: { home?: string; layout?: LauncherLayout } = {}): { config: LauncherConfig; invalidKeys: string[] } {
  const home = opts.home ?? os.homedir()
  const layout = opts.layout ?? homeLayout(home)
  const src = isObject(raw) ? raw : {}
  const invalid: string[] = []

  const str = (key: string, fallback: string): string => {
    const v = src[key]
    if (v === undefined || v === null || v === '') return fallback
    if (typeof v === 'string') return v
    invalid.push(key)
    return fallback
  }
  const bool = (key: string, fallback: boolean): boolean => {
    const v = src[key]
    if (v === undefined || v === null) return fallback
    if (typeof v === 'boolean') return v
    invalid.push(key)
    return fallback
  }

  const env: Record<string, string> = {}
  const rawEnv = src.environment_vars
  if (rawEnv !== undefined && rawEnv !== null) {
    if (isObject(rawEnv)) {
      let bad = false
      for (const [k, v] of Object.entries(rawEnv)) {
        if (typeof v === 'string') env[k] = v
        else bad = true
      }
      if (bad) invalid.push('environment_vars')
    } else {
      invalid.push('environment_vars')
    }
  }

  const extra: Record<string, unknown> = {}
  const known = new Set<string>(KNOWN_KEYS)
  for (const [k, v] of Object.entries(src)) if (!known.has(k)) extra[k] = v

  return {
    config: {
      models_path: str('models_path', path.join(layout.dataDir, 'models')),
      backends_path: str('backends_path', path.join(layout.dataDir, 'backends')),
      address: normalizeAddress(str('address', '127.0.0.1:8080')),
      auto_start_server: bool('auto_start_server', true),
      start_on_boot: bool('start_on_boot', false),
      log_level: str('log_level', 'info'),
      environment_vars: env,
      show_welcome: bool('show_welcome', true),
      extra,
    },
    invalidKeys: invalid,
  }
}

/** The JSON text written to disk: Go key order, 2-space indent, unknown keys after. */
export function serializeConfig(config: LauncherConfig): string {
  const out: Record<string, unknown> = {
    models_path: config.models_path,
    backends_path: config.backends_path,
    address: config.address,
    auto_start_server: config.auto_start_server,
    start_on_boot: config.start_on_boot,
    log_level: config.log_level,
    environment_vars: config.environment_vars,
    show_welcome: config.show_welcome,
  }
  for (const [k, v] of Object.entries(config.extra ?? {})) {
    if (!(KNOWN_KEYS as readonly string[]).includes(k)) out[k] = v
  }
  return JSON.stringify(out, null, 2) + '\n'
}

function resolvePath(opts: ConfigOptions): string {
  return opts.path ?? defaultConfigPath(opts.home, opts.layout)
}

function pad(n: number): string {
  return String(n).padStart(2, '0')
}

function stamp(d: Date): string {
  return `${d.getUTCFullYear()}${pad(d.getUTCMonth() + 1)}${pad(d.getUTCDate())}T${pad(d.getUTCHours())}${pad(d.getUTCMinutes())}${pad(d.getUTCSeconds())}Z`
}

export async function loadConfig(opts: ConfigOptions = {}): Promise<LoadResult> {
  const file = resolvePath(opts)
  const log = opts.log ?? (() => undefined)
  const now = opts.now ?? (() => new Date())

  let text: string
  try {
    text = await fs.promises.readFile(file, 'utf8')
  } catch (e) {
    if ((e as NodeJS.ErrnoException).code === 'ENOENT') {
      log('config: no file, using defaults')
      return { config: defaultConfig(opts.home, opts.layout), status: 'missing', invalidKeys: [] }
    }
    // Unreadable (permissions, a directory). Do not move it: report and use defaults.
    const code = (e as NodeJS.ErrnoException).code ?? 'error'
    log(`config: cannot read file (${code}), using defaults`)
    return {
      config: defaultConfig(opts.home, opts.layout),
      status: 'recovered',
      reason: `config file could not be read (${code})`,
      invalidKeys: [],
    }
  }

  let parsed: unknown
  let reason: string | undefined
  try {
    parsed = JSON.parse(text)
    if (!isObject(parsed)) reason = 'config file is not a JSON object'
  } catch {
    // The engine's message can quote file content, so it is not passed on.
    reason = 'config file is not valid JSON'
  }

  if (reason !== undefined) {
    const backupPath = `${file}.bad-${stamp(now())}`
    let moved = false
    try {
      await fs.promises.rename(file, backupPath)
      moved = true
    } catch (e) {
      log(`config: could not move the bad file aside (${(e as NodeJS.ErrnoException).code ?? 'error'})`)
    }
    log(`config: ${reason}, ${moved ? 'moved aside' : 'left in place'}, using defaults`)
    return {
      config: defaultConfig(opts.home, opts.layout),
      status: 'recovered',
      reason,
      backupPath: moved ? backupPath : undefined,
      invalidKeys: [],
    }
  }

  const { config, invalidKeys } = migrate(parsed, { home: opts.home, layout: opts.layout })
  const present = Object.keys(parsed as Record<string, unknown>)
  log(`config: loaded keys [${present.join(', ')}]`)
  if (invalidKeys.length) log(`config: wrong type, default used for [${invalidKeys.join(', ')}]`)
  return { config, status: 'ok', invalidKeys }
}

/**
 * Atomic write: a temp file in the same directory, fsync, rename. The temp
 * file is created 0600, so the renamed file is 0600 even when it replaces an
 * old 0644 file. On any failure the temp file is removed and the old file is
 * untouched.
 */
export async function saveConfig(config: LauncherConfig, opts: ConfigOptions = {}): Promise<void> {
  const file = resolvePath(opts)
  const log = opts.log ?? (() => undefined)
  const dir = path.dirname(file)
  await fs.promises.mkdir(dir, { recursive: true, mode: 0o755 })
  const tmp = path.join(dir, `.${path.basename(file)}.${process.pid}.${crypto.randomBytes(6).toString('hex')}.tmp`)
  const data = serializeConfig(config)
  let fh: fs.promises.FileHandle | undefined
  try {
    fh = await fs.promises.open(tmp, 'wx', 0o600)
    await fh.writeFile(data, 'utf8')
    await fh.sync()
    await fh.close()
    fh = undefined
    await fs.promises.rename(tmp, file)
  } catch (e) {
    if (fh) await fh.close().catch(() => undefined)
    await fs.promises.rm(tmp, { force: true }).catch(() => undefined)
    log(`config: save failed (${(e as NodeJS.ErrnoException).code ?? 'error'}), previous file kept`)
    throw e
  }
  // Make the rename durable. Not every platform can open a directory.
  try {
    const dh = await fs.promises.open(dir, 'r')
    await dh.sync().catch(() => undefined)
    await dh.close()
  } catch {
    /* best effort */
  }
  log(`config: saved keys [${[...KNOWN_KEYS, ...Object.keys(config.extra ?? {}).filter((k) => !(KNOWN_KEYS as readonly string[]).includes(k))].join(', ')}]`)
}
