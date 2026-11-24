// The launcher's own update on Linux: swap the running AppImage file for the
// one electron-updater downloaded, atomically. No Electron import.
//
// electron-updater's own AppImage install (AppImageUpdater.doInstall in
// electron-updater 6.8) deletes the old file first and then moves the new
// one in with `mv`, which can leave no launcher at all when the move fails
// (another filesystem, disk full, a read-only folder). Here the old file is
// never touched until the new one is complete:
//
//   1. check the download: size and ELF/AppImage magic, against the release
//      feed entry electron-updater already verified the download against
//   2. stage a copy next to the target (same folder, so same filesystem),
//      with the target's mode, and fsync it
//   3. SHA-512 of the staged copy against the feed (the bytes that will be
//      installed, so a copy cut short is caught too); the file is read from
//      disk, never downloaded again
//   4. rename(2) over the target, fsync the folder
//
// The file name stays the same, so a desktop entry, the autostart entry or a
// symlink that points to it keeps working. Any failure removes the staged
// copy and leaves the old file as it was, with a stable reason.
import fs from 'node:fs'
import { installAtomically, InstallError, type FsOps } from './atomicInstall'
import { sha512Base64Sync } from './checksum'

/** The downloaded update, as electron-updater reports it in `update-downloaded`. */
export interface DownloadedAppImage {
  /** `downloadedFile` of the event: the verified file in electron-updater's cache. */
  file: string
  /** SHA-512 (base64) of the AppImage, from the feed file (latest-linux.yml). */
  sha512?: string
  /** Size in bytes, from the feed file, when it says. */
  size?: number
  version?: string
}

export type SwapFailReason =
  /** Not running from an AppImage file (APPIMAGE unset, not absolute, or the file is gone). */
  | 'not-appimage'
  /** No downloaded file, or the feed gave no checksum for it. */
  | 'no-download'
  | 'size-mismatch'
  /** Not an ELF file, or not a type 2 AppImage. */
  | 'not-an-appimage'
  | 'checksum-mismatch'
  /** The folder of the AppImage cannot be written. */
  | 'read-only'
  | 'no-space'
  | 'failed'

export type SwapResult = { ok: true; target: string } | { ok: false; reason: SwapFailReason; message: string }

/** The way "Restart to update" installs: the atomic swap for an AppImage, electron-updater's own install elsewhere (macOS). */
export type InstallPath = 'appimage-swap' | 'electron-updater'

export function installPath(platform: NodeJS.Platform, env: NodeJS.ProcessEnv): InstallPath {
  return platform === 'linux' && !!env.APPIMAGE ? 'appimage-swap' : 'electron-updater'
}

interface FeedFile {
  url?: string
  sha512?: string
  size?: number
}

/** The AppImage entry of an electron-updater UpdateInfo (`files`, or the older top-level `path` and `sha512`). */
export function appImageEntry(info: { files?: FeedFile[]; path?: string; sha512?: string }): { sha512: string; size?: number } | undefined {
  const isAppImage = (u?: string) => !!u && /\.appimage$/i.test(u)
  const f = info.files?.find((x) => isAppImage(x.url) && typeof x.sha512 === 'string' && x.sha512 !== '')
  if (f) return { sha512: f.sha512!, size: typeof f.size === 'number' && f.size > 0 ? f.size : undefined }
  if (isAppImage(info.path) && info.sha512) return { sha512: info.sha512 }
  return undefined
}

const fail = (reason: SwapFailReason, message: string): SwapResult => ({ ok: false, reason, message })

function reasonOf(e: InstallError): SwapFailReason {
  if (e.step === 'verify') return 'checksum-mismatch'
  if (e.step === 'validate') {
    if (/bytes, the release says/.test(e.message)) return 'size-mismatch'
    if (/missing|not a regular file/.test(e.message)) return 'no-download'
    return 'not-an-appimage'
  }
  const code = (e.cause as NodeJS.ErrnoException | undefined)?.code
  if (code === 'EROFS' || code === 'EACCES' || code === 'EPERM') return 'read-only'
  if (code === 'ENOSPC' || code === 'EDQUOT') return 'no-space'
  return 'failed'
}

export interface SwapOptions {
  /** Test hook: replace single file system calls to inject failures. */
  ops?: Partial<FsOps>
  /** Test hook: the checksum of the staged copy. */
  hash?: (file: string) => string
}

/** Replaces `target` (the running AppImage file) with the download. Never throws. */
export function swapAppImage(download: DownloadedAppImage | undefined, target: string | undefined, o: SwapOptions = {}): SwapResult {
  if (!target || !targetOk(target)) return fail('not-appimage', 'the launcher does not run from an AppImage file')
  if (!download?.file) return fail('no-download', 'no downloaded update')
  const want = download.sha512
  if (!want) return fail('no-download', 'the release feed gave no checksum for the AppImage')
  let mode: number
  try {
    const st = fs.statSync(target)
    if (!st.isFile()) return fail('not-appimage', 'the AppImage path is not a regular file')
    // Keep the file's own mode (and so who may run it); never install a file nobody can run.
    mode = st.mode & 0o7777 & ~0o7000
    if ((mode & 0o100) === 0) mode |= 0o100
  } catch {
    return fail('not-appimage', 'the AppImage file is gone')
  }
  const hash = o.hash ?? sha512Base64Sync
  try {
    installAtomically(download.file, target, {
      appImage: true,
      size: download.size,
      mode,
      ops: o.ops,
      verifyStaged: (staged) => {
        if (hash(staged) !== want) throw new Error('the copy does not match the release checksum')
      },
    })
    return { ok: true, target }
  } catch (e) {
    if (e instanceof InstallError) return fail(reasonOf(e), e.message)
    return fail('failed', e instanceof Error ? e.message : 'install failed')
  }
}

function targetOk(p: string): boolean {
  return p.startsWith('/') && !p.includes('\0')
}
