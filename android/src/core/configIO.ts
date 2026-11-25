// SPDX-License-Identifier: MIT
// The client config: a JSON file in the app's private data directory, read and
// written through the Capacitor Filesystem plugin. It is a file and not
// localStorage, because WebView data is kept out of Android backups and the
// server list must survive a backup and restore.
//
// Only the keys this client uses are written (the `extra` keys of
// LauncherConfig: remotes, default target, WebUI settings). The desktop's
// local-server keys (address, folders, environment) never reach the file.
import { Directory, Encoding, Filesystem } from '@capacitor/filesystem'
import type { ConfigIO } from '@launcher/core/configStore'
import { KNOWN_KEYS, migrateConfig, type ConfigDefaults, type LauncherConfig, type LoadResult } from '@launcher/core/configTypes'

export const CONFIG_FILE = 'localai-client.json'
/** The client has no local folders. */
export const ANDROID_DEFAULTS: ConfigDefaults = { modelsPath: '', backendsPath: '' }

/** Text files by name in one private directory. `read` answers undefined for a missing file. */
export interface FileApi {
  read(name: string): Promise<string | undefined>
  write(name: string, text: string): Promise<void>
  /** Replaces `to` when it exists. */
  rename(from: string, to: string): Promise<void>
}

/** "File does not exist": the code of the Android and iOS plugin, or the message of the web one. */
function isMissing(e: unknown): boolean {
  const o = e as { code?: unknown; message?: unknown } | null
  if (o?.code === 'OS-PLUG-FILE-0008') return true
  return typeof o?.message === 'string' && /does not exist/i.test(o.message)
}

export function capacitorFileApi(): FileApi {
  const directory = Directory.Data
  return {
    async read(name) {
      try {
        const r = await Filesystem.readFile({ path: name, directory, encoding: Encoding.UTF8 })
        return typeof r.data === 'string' ? r.data : await r.data.text()
      } catch (e) {
        if (isMissing(e)) return undefined
        throw e
      }
    },
    async write(name, text) {
      await Filesystem.writeFile({ path: name, directory, encoding: Encoding.UTF8, data: text })
    },
    async rename(from, to) {
      await Filesystem.rename({ from, to, directory, toDirectory: directory })
    },
  }
}

const pad = (n: number) => String(n).padStart(2, '0')

/** The same stamp as the desktop's `<file>.bad-<stamp>`. */
function stamp(d: Date): string {
  return `${d.getUTCFullYear()}${pad(d.getUTCMonth() + 1)}${pad(d.getUTCDate())}T${pad(d.getUTCHours())}${pad(d.getUTCMinutes())}${pad(d.getUTCSeconds())}Z`
}

const isObject = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)

function defaults(): LauncherConfig {
  return migrateConfig({}, ANDROID_DEFAULTS).config
}

/** The text written to the file: the client keys only, 2-space indent, a final newline. */
export function serializeClientConfig(c: LauncherConfig): string {
  const known = new Set<string>(KNOWN_KEYS)
  const out: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(c.extra ?? {})) if (!known.has(k)) out[k] = v
  return JSON.stringify(out, null, 2) + '\n'
}

/**
 * Load and save the config in `files`. The reason texts of a recovered load
 * are the desktop's (main/config.ts), so the shell shows the same notice.
 */
export function clientConfigIO(files: FileApi, now: () => Date): ConfigIO {
  const tmp = `${CONFIG_FILE}.tmp`
  return {
    async load(): Promise<LoadResult> {
      let text: string | undefined
      try {
        text = await files.read(CONFIG_FILE)
        // The rename of a save replaces the file in two steps (delete, then
        // rename). When the app stopped between them, the temp file is the
        // newest complete config.
        if (text === undefined) {
          const t = await files.read(tmp)
          if (t !== undefined && isObject(safeParse(t))) text = t
        }
      } catch {
        // Not moved: it may be readable later.
        return { config: defaults(), status: 'recovered', reason: 'config file could not be read (error)', invalidKeys: [] }
      }
      if (text === undefined) return { config: defaults(), status: 'missing', invalidKeys: [] }

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
        const backupPath = `${CONFIG_FILE}.bad-${stamp(now())}`
        let moved = false
        try {
          await files.rename(CONFIG_FILE, backupPath)
          moved = true
        } catch {
          // Left in place; the next save replaces it.
        }
        return { config: defaults(), status: 'recovered', reason, backupPath: moved ? backupPath : undefined, invalidKeys: [] }
      }
      const { config, invalidKeys } = migrateConfig(parsed, ANDROID_DEFAULTS)
      return { config, status: 'ok', invalidKeys }
    },

    async save(config: LauncherConfig): Promise<void> {
      await files.write(tmp, serializeClientConfig(config))
      await files.rename(tmp, CONFIG_FILE)
    },
  }
}

function safeParse(text: string): unknown {
  try {
    return JSON.parse(text)
  } catch {
    return undefined
  }
}
