// How the server is started, from the config. Pure, no Electron import.
//
// Ported from the Go launcher (Launcher.BuildRunArgs): every writable path of
// the server is anchored to the launcher's data folder, so nothing lands in
// the working directory or in a shared /tmp.
import path from 'node:path'
import type { LauncherConfig } from './config'
import { buildChildEnv } from './childEnv'
import { normalizeAddress } from './network'
import { BASE_PATH_ENV } from './paths'
import type { UpdateChannel } from './serversConfig'

export interface ServerLaunch {
  args: string[]
  env: Record<string, string>
  /** Base URL for the API and the WebUI, for example http://127.0.0.1:8080. */
  endpoint: string
  healthUrl: string
}

/**
 * The URL to reach a server that listens on `address`. A wildcard or empty
 * host is reached on the loopback address.
 */
export function endpointFor(address: string): string {
  const i = address.lastIndexOf(':')
  let host = i >= 0 ? address.slice(0, i) : address
  const port = i >= 0 ? address.slice(i + 1) : '8080'
  if (host === '' || host === '0.0.0.0') host = '127.0.0.1'
  if (host === '[::]' || host === '::') host = '[::1]'
  return `http://${host}:${port || '8080'}`
}

/** What the server gets from the secret store. Values go into the environment only. */
export interface LaunchSecrets {
  /** The user's environment variables (Settings, Advanced). */
  env?: Record<string, string>
  /** The API key; the server reads it from LOCALAI_API_KEY. */
  apiKey?: string
}

/** The environment variable the server reads its API keys from (core/cli: `env:"LOCALAI_API_KEY,API_KEY"`). */
export const API_KEY_ENV = 'LOCALAI_API_KEY'

/**
 * The server setting that makes backends install their development image
 * first (the released image stays as the fallback): core/cli/run.go,
 * `PreferDevelopmentBackends`, env `LOCALAI_PREFER_DEV_BACKENDS`. The
 * server's fallback tag (`master`) and suffix (`development`) already default
 * to the development names, so nothing else is needed. Backends that are
 * already installed are not touched by it.
 */
export const PREFER_DEV_BACKENDS_ENV = 'LOCALAI_PREFER_DEV_BACKENDS'

export function buildServerLaunch(
  config: Pick<LauncherConfig, 'models_path' | 'backends_path' | 'address' | 'log_level'>,
  dataFolder: string,
  processEnv: NodeJS.ProcessEnv,
  endpointOverride?: string,
  secrets: LaunchSecrets = {},
  channel: UpdateChannel = 'stable',
): ServerLaunch {
  let address = normalizeAddress(config.address)
  let endpoint = endpointFor(address)
  if (endpointOverride) {
    // Test hook: listen where the test expects the server.
    const u = new URL(endpointOverride)
    address = `${u.hostname.includes(':') ? `[${u.hostname}]` : u.hostname}:${u.port || '80'}`
    endpoint = endpointOverride.replace(/\/+$/, '')
  }
  return {
    args: [
      'run',
      '--models-path', config.models_path,
      '--backends-path', config.backends_path,
      '--address', address,
      '--log-level', config.log_level,
      '--data-path', path.join(dataFolder, 'data'),
      '--localai-config-dir', path.join(dataFolder, 'configuration'),
      '--generated-content-path', path.join(dataFolder, 'generated'),
      '--upload-path', path.join(dataFolder, 'uploads'),
    ],
    // The explicit child environment rule: launcher-only variables are
    // dropped, the user's variables are applied on top, then the launcher's
    // own. Secrets go here and never into `args`, which any user of the
    // computer can read with ps.
    env: buildChildEnv(processEnv, secrets.env ?? {}, {
      ...(secrets.apiKey ? { [API_KEY_ENV]: secrets.apiKey } : {}),
      // The server's base folder is the data folder, as the paths in `args`
      // say. Without it the server would pick a base from the current folder
      // on Windows, log that the folder holds LocalAI files, and stage
      // downloads there.
      [BASE_PATH_ENV]: dataFolder,
      // The update channel decides this one, so it is in the launcher's own layer.
      ...(channel === 'dev' ? { [PREFER_DEV_BACKENDS_ENV]: 'true' } : {}),
    }),
    endpoint,
    healthUrl: `${endpoint}/readyz`,
  }
}

/**
 * True when a change between the two configs only takes effect after a
 * server restart. Environment variables and the API key live in the secret
 * store; the core marks a restart for those itself.
 */
export function needsRestart(a: LauncherConfig, b: LauncherConfig): boolean {
  if (a.models_path !== b.models_path || a.backends_path !== b.backends_path) return true
  return a.address !== b.address || a.log_level !== b.log_level
}
