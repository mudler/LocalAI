// "Launch at login". No Electron import: index.ts passes Electron's login
// item call for macOS, and the file calls are injectable for tests.
//
// - Linux: an XDG autostart entry, `<config>/autostart/localai-launcher.desktop`,
//   that runs the AppImage file (from APPIMAGE) with --hidden. Written
//   through a temporary file, fsync and rename, so a desktop never reads half
//   a file. Turning it off removes the file. Without APPIMAGE (a run from
//   source) there is no stable file to start, so turning it on is
//   `unavailable`.
// - macOS: Electron's login item (openAtLogin). macOS passes no arguments to
//   a login item; index.ts reads `wasOpenedAtLogin` for the hidden start.
// - Windows: Electron's login item with --hidden.
import fs from 'node:fs'
import path from 'node:path'
import crypto from 'node:crypto'
import type { LoginItemView } from '../shared/types'

/**
 * `unavailable`: nothing to register (no AppImage file, as in a run from
 * source, or a platform with no login items). `failed`: the change was not
 * made; `reason` is a stable code: read-only, no-space, write-failed,
 * remove-failed, requires-approval, error.
 */
export type LoginItemResult = LoginItemView

export const AUTOSTART_FILE = 'localai-launcher.desktop'

/** The file calls the Linux entry uses; tests pass a fake. */
export interface LoginItemFs {
  mkdirp(dir: string): void
  /** Writes `data` to a new file (fails when it exists), fsyncs and closes it. */
  writeNew(file: string, data: string, mode: number): void
  rename(from: string, to: string): void
  /** Removes a file; a missing file is not an error. */
  remove(file: string): void
}

export const realLoginItemFs: LoginItemFs = {
  mkdirp: (dir) => fs.mkdirSync(dir, { recursive: true, mode: 0o700 }),
  writeNew: (file, data, mode) => {
    const fd = fs.openSync(file, 'wx', mode)
    try {
      fs.writeSync(fd, data)
      fs.fsyncSync(fd)
    } finally {
      fs.closeSync(fd)
    }
  },
  rename: (a, b) => fs.renameSync(a, b),
  remove: (file) => fs.rmSync(file, { force: true }),
}

export interface LoginItemDeps {
  platform: NodeJS.Platform
  env: NodeJS.ProcessEnv
  home: string
  fs?: LoginItemFs
  /** Electron's app.setLoginItemSettings, for macOS and Windows. Returns the macOS status when it has one. */
  setElectronLoginItem?(open: boolean, args: string[]): { status?: string } | void
}

/** `$XDG_CONFIG_HOME/autostart`, or `~/.config/autostart` when it is unset or not absolute (as the spec says). */
export function autostartDir(env: NodeJS.ProcessEnv, home: string): string {
  const xdg = env.XDG_CONFIG_HOME
  return path.join(xdg && path.isAbsolute(xdg) ? xdg : path.join(home, '.config'), 'autostart')
}

/**
 * One argument of a desktop entry's Exec key: in double quotes, with the
 * reserved characters escaped, and `%` doubled so it is never a field code.
 */
export function execArg(arg: string): string {
  return `"${arg.replace(/[\\"`$]/g, (c) => `\\${c}`).replace(/%/g, '%%')}"`
}

/** The autostart entry for this AppImage file. */
export function desktopEntry(appImage: string): string {
  // A desktop entry value is one line; escape the characters that would break it.
  const exec = `${execArg(appImage)} --hidden`.replace(/\\/g, '\\\\').replace(/\n/g, '\\n').replace(/\r/g, '\\r').replace(/\t/g, '\\t')
  return [
    '[Desktop Entry]',
    'Type=Application',
    'Name=LocalAI Launcher',
    'Comment=Starts the LocalAI Launcher in the tray',
    `Exec=${exec}`,
    'Terminal=false',
    'X-GNOME-Autostart-enabled=true',
    '',
  ].join('\n')
}

function errnoReason(e: unknown, fallback: string): string {
  const code = (e as NodeJS.ErrnoException)?.code
  if (code === 'EROFS' || code === 'EACCES' || code === 'EPERM') return 'read-only'
  if (code === 'ENOSPC' || code === 'EDQUOT') return 'no-space'
  return fallback
}

function linux(enabled: boolean, d: LoginItemDeps): LoginItemResult {
  const ops = d.fs ?? realLoginItemFs
  const dir = autostartDir(d.env, d.home)
  const file = path.join(dir, AUTOSTART_FILE)
  if (!enabled) {
    try {
      ops.remove(file)
      return { status: 'ok' }
    } catch (e) {
      return { status: 'failed', reason: errnoReason(e, 'remove-failed') }
    }
  }
  const appImage = d.env.APPIMAGE
  if (!appImage || !path.isAbsolute(appImage) || appImage.includes('\0')) return { status: 'unavailable', reason: 'not-appimage' }
  const tmp = path.join(dir, `.${AUTOSTART_FILE}.${crypto.randomBytes(6).toString('hex')}.tmp`)
  try {
    ops.mkdirp(dir)
    ops.writeNew(tmp, desktopEntry(appImage), 0o644)
    ops.rename(tmp, file)
    return { status: 'ok' }
  } catch (e) {
    try {
      ops.remove(tmp)
    } catch {
      /* nothing written */
    }
    return { status: 'failed', reason: errnoReason(e, 'write-failed') }
  }
}

/** Turns "Launch at login" on or off for this platform. Never throws. */
export function applyLoginItem(enabled: boolean, d: LoginItemDeps): LoginItemResult {
  if (d.platform === 'linux') return linux(enabled, d)
  if ((d.platform === 'darwin' || d.platform === 'win32') && d.setElectronLoginItem) {
    try {
      const r = d.setElectronLoginItem(enabled, ['--hidden'])
      // macOS 13 and later can ask the user to approve a login item in System Settings first.
      if (enabled && r && r.status === 'requires-approval') return { status: 'failed', reason: 'requires-approval' }
      return { status: 'ok' }
    } catch {
      return { status: 'failed', reason: 'error' }
    }
  }
  return { status: 'unavailable', reason: 'platform' }
}
