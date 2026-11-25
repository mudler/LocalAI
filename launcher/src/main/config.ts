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
import { KNOWN_KEYS, migrateConfig, serializeConfig, type LauncherConfig, type LoadResult } from '../core/configTypes'
import { withoutP2PHosts } from '../core/serversConfig'
import { homeLayout, type LauncherLayout } from './paths'

export { KNOWN_KEYS, migrateConfig, serializeConfig } from '../core/configTypes'
export type { ConfigDefaults, LauncherConfig, LoadResult, LoadStatus } from '../core/configTypes'

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
 * Pure. Turns whatever was parsed from launcher.json into a full config, with
 * the folder defaults of this home. See `migrateConfig` for the rules.
 */
export function migrate(raw: unknown, opts: { home?: string; layout?: LauncherLayout } = {}): { config: LauncherConfig; invalidKeys: string[] } {
  const home = opts.home ?? os.homedir()
  const layout = opts.layout ?? homeLayout(home)
  return migrateConfig(raw, {
    modelsPath: path.join(layout.dataDir, 'models'),
    backendsPath: path.join(layout.dataDir, 'backends'),
  })
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

  const migrated = migrate(parsed, { home: opts.home, layout: opts.layout })
  const invalidKeys = migrated.invalidKeys
  const { config, dropped } = withoutP2PHosts(migrated.config)
  const present = Object.keys(parsed as Record<string, unknown>)
  log(`config: loaded keys [${present.join(', ')}]`)
  if (invalidKeys.length) log(`config: wrong type, default used for [${invalidKeys.join(', ')}]`)
  if (dropped) log(`config: ignored ${dropped} P2P server(s) with a host other than 127.0.0.1`)
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
