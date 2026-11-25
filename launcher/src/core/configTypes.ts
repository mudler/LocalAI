// The launcher.json model: the keys, the load result, the pure migration of a
// parsed file into a full config, and the text written back. Shared by the
// desktop main process and a client-only shell. Pure, no Electron or Node
// import: the folder defaults come from the caller.
import { normalizeAddress } from './address'

/** The keys the Go launcher wrote, with the same names and the same order. */
export interface LauncherConfig {
  models_path: string
  backends_path: string
  address: string
  /** Unset in an old file means enabled. See `migrateConfig`. */
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

/** The folder defaults of a config, chosen by the platform that stores it. */
export interface ConfigDefaults {
  modelsPath: string
  backendsPath: string
}

/** The id of a registry credential rule: short, no separators that could escape a name. */
export const CRED_ID = /^[A-Za-z0-9_-]{1,64}$/

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
export function migrateConfig(raw: unknown, defaults: ConfigDefaults): { config: LauncherConfig; invalidKeys: string[] } {
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
      models_path: str('models_path', defaults.modelsPath),
      backends_path: str('backends_path', defaults.backendsPath),
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
