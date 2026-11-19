/**
 * Where the launcher keeps its files and where the local server keeps its
 * data by default.
 *
 * Linux and macOS: everything in <home>/.localai, as the Go launcher did.
 *
 * Windows: the standard folders, as the server uses them
 * (pkg/system/defaultdirs.go). The server data (models, backends, data,
 * configuration, logs) is in %LOCALAPPDATA%\LocalAI, shared with
 * `local-ai run` from a terminal, so a model is downloaded once. The
 * launcher's own files (launcher.json, secrets.json, the managed server
 * binary) are in %LOCALAPPDATA%\LocalAI\launcher, apart from the `bin`
 * folder of the PowerShell installer, so neither replaces the other's
 * local-ai.exe. A %USERPROFILE%\.localai\launcher.json of an earlier
 * build keeps the whole earlier layout in use.
 *
 * LOCALAI_BASE_PATH (an absolute path) moves the default data folder on every
 * platform, as it moves the server's. Folders saved in launcher.json win.
 */
import path from 'node:path'

/** The server variable that moves the default data folder (pkg/system.BasePathEnv). */
export const BASE_PATH_ENV = 'LOCALAI_BASE_PATH'

export type LayoutOrigin = 'home' | 'standard' | 'legacy' | 'env'

export interface LauncherLayout {
  /** launcher.json, secrets.json, and the bin, metadata and downloads folders. */
  launcherDir: string
  /** Default parent of the models and backends folders. */
  dataDir: string
  origin: LayoutOrigin
}

export interface LayoutHost {
  platform: NodeJS.Platform
  env: NodeJS.ProcessEnv
  home: string
  exists(p: string): boolean
}

/** The layout of the Go launcher and of Linux and macOS: <home>/.localai. */
export function homeLayout(home: string, p: typeof path = path): LauncherLayout {
  const dir = p.join(home, '.localai')
  return { launcherDir: dir, dataDir: dir, origin: 'home' }
}

export function resolveLayout(h: LayoutHost): LauncherLayout {
  const win = h.platform === 'win32'
  const p = win ? path.win32 : path.posix
  let layout = homeLayout(h.home, p)
  if (win) {
    if (h.exists(p.join(layout.launcherDir, 'launcher.json'))) {
      layout = { ...layout, origin: 'legacy' }
    } else {
      const raw = h.env.LOCALAPPDATA
      const local = raw && p.isAbsolute(raw) ? raw : p.join(h.home, 'AppData', 'Local')
      const dataDir = p.join(local, 'LocalAI')
      layout = { launcherDir: p.join(dataDir, 'launcher'), dataDir, origin: 'standard' }
    }
  }
  const base = h.env[BASE_PATH_ENV]
  if (base && p.isAbsolute(base)) layout = { ...layout, dataDir: p.normalize(base), origin: 'env' }
  return layout
}

/** One line for the launcher log; undefined when the layout is the usual one. */
export function layoutNote(l: LauncherLayout): string | undefined {
  if (l.origin === 'legacy') return `using the folder of an earlier build, ${l.launcherDir}, because it has a launcher.json`
  if (l.origin === 'env') return `${BASE_PATH_ENV} is set: the default data folder is ${l.dataDir}`
  return undefined
}
