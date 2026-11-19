// Replaces an executable (an AppImage or the server binary) without ever
// leaving a half-written file at the target path. No Electron import.
//
// Steps: validate the source, stage a copy next to the target (same
// directory, so same filesystem and rename(2) stays atomic), chmod, fsync the
// file, rename over the target, fsync the directory. On any failure the
// staged file is removed and the old target is untouched. The target keeps its
// file name, so a desktop entry or a symlink that points to it keeps working.
import fs from 'node:fs'
import path from 'node:path'
import crypto from 'node:crypto'
import type { ExecutableFormat } from './platform'

export class InstallError extends Error {
  constructor(message: string, readonly step: string, options?: { cause?: unknown }) {
    super(message, options)
    this.name = 'InstallError'
  }
}

/** The file system calls the installer uses; tests replace single entries to inject failures. */
export interface FsOps {
  copyFile(src: string, dst: string): void
  chmod(fd: number, mode: number): void
  fsync(fd: number): void
  rename(from: string, to: string): void
  unlink(p: string): void
}

/** Error codes Windows gives while another process (an antivirus scan, the search indexer) holds the file for a moment. */
const TRANSIENT_WIN_CODES = new Set(['EPERM', 'EBUSY', 'EACCES'])

function sleepSync(ms: number): void {
  Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms)
}

/**
 * rename(2), with retries on Windows. There a scanner can hold a file that was
 * just written for a short time, and the rename fails with EPERM or EBUSY.
 * The wait grows to about 2 s in total. On other platforms one try.
 */
export function renameWithRetry(
  rename: (from: string, to: string) => void,
  from: string,
  to: string,
  o: { platform?: NodeJS.Platform; attempts?: number; sleep?: (ms: number) => void } = {},
): void {
  const platform = o.platform ?? process.platform
  const attempts = platform === 'win32' ? (o.attempts ?? 8) : 1
  const sleep = o.sleep ?? sleepSync
  for (let n = 1; ; n++) {
    try {
      rename(from, to)
      return
    } catch (e) {
      const code = (e as NodeJS.ErrnoException)?.code
      if (n >= attempts || !code || !TRANSIENT_WIN_CODES.has(code)) throw e
      sleep(50 * n)
    }
  }
}

export const realFs: FsOps = {
  copyFile: (s, d) => fs.copyFileSync(s, d, fs.constants.COPYFILE_EXCL),
  // On Windows only the read-only flag follows the mode; 0o755 keeps the file writable.
  chmod: (fd, mode) => fs.fchmodSync(fd, mode),
  fsync: (fd) => fs.fsyncSync(fd),
  rename: (a, b) => renameWithRetry((x, y) => fs.renameSync(x, y), a, b),
  unlink: (p) => fs.unlinkSync(p),
}

export interface ValidateOptions {
  minBytes?: number
  /** Require the AppImage type 2 marker ("AI\x02" at offset 8). */
  appImage?: boolean
  /** The exact size the file must have (from the release feed). */
  size?: number
  /** The executable format to expect. Default `elf`. */
  format?: ExecutableFormat
}

export interface InstallOptions extends ValidateOptions {
  ops?: Partial<FsOps>
  /** Mode of the installed file. Default 0o755. */
  mode?: number
  /**
   * Runs on the staged copy after it is written and synced, before the
   * rename: a checksum of the bytes that will be installed. Throw to stop;
   * the step is then 'verify' and the target stays untouched.
   */
  verifyStaged?: (staged: string) => void
}

const ELF = Buffer.from([0x7f, 0x45, 0x4c, 0x46])
/** Mach-O 32 and 64 bit in both byte orders, and the universal (fat) header. */
const MACHO = ['feedface', 'feedfacf', 'cefaedfe', 'cffaedfe', 'cafebabe'].map((h) => Buffer.from(h, 'hex'))
const FORMAT_NAME: Record<ExecutableFormat, string> = { elf: 'an ELF', macho: 'a Mach-O', pe: 'a Windows (PE)' }

/** True when `head` (the first bytes of the file) starts an executable of `format`. */
export function hasExecutableMagic(head: Buffer, format: ExecutableFormat): boolean {
  if (format === 'elf') return head.length >= 4 && head.subarray(0, 4).equals(ELF)
  if (format === 'macho') return head.length >= 4 && MACHO.some((m) => head.subarray(0, 4).equals(m))
  // PE: the DOS header "MZ", and at the offset in e_lfanew (0x3c) the "PE\0\0" signature.
  if (head.length < 0x40 || head[0] !== 0x4d || head[1] !== 0x5a) return false
  const off = head.readUInt32LE(0x3c)
  return off + 4 <= head.length && head.readUInt32BE(off) === 0x50450000
}

/** Cheap sanity checks on a downloaded executable. */
export function validateExecutable(file: string, opts: ValidateOptions = {}): void {
  let st: fs.Stats
  try {
    st = fs.statSync(file)
  } catch (e) {
    throw new InstallError(`downloaded file is missing: ${(e as Error).message}`, 'validate', { cause: e })
  }
  if (!st.isFile()) throw new InstallError('downloaded path is not a regular file', 'validate')
  if (st.size < (opts.minBytes ?? 1024)) throw new InstallError('downloaded file is too small to be an executable', 'validate')
  if (opts.size !== undefined && st.size !== opts.size) throw new InstallError(`downloaded file has ${st.size} bytes, the release says ${opts.size}`, 'validate')
  const format = opts.format ?? 'elf'
  const fd = fs.openSync(file, 'r')
  try {
    // 1 KiB holds the PE signature of every normal Windows executable.
    const head = Buffer.alloc(1024)
    const n = fs.readSync(fd, head, 0, head.length, 0)
    if (!hasExecutableMagic(head.subarray(0, n), format)) throw new InstallError(`downloaded file is not ${FORMAT_NAME[format]} executable`, 'validate')
    if (opts.appImage && !(head[8] === 0x41 && head[9] === 0x49 && head[10] === 0x02)) {
      throw new InstallError('downloaded file is not an AppImage (type 2)', 'validate')
    }
  } finally {
    fs.closeSync(fd)
  }
}

export function installAtomically(source: string, target: string, opts: InstallOptions = {}): void {
  const ops: FsOps = { ...realFs, ...opts.ops }
  validateExecutable(source, opts)

  const dir = path.dirname(target)
  const staged = path.join(dir, `.${path.basename(target)}.${crypto.randomBytes(6).toString('hex')}.staging`)
  let fd: number | undefined
  let step = 'stage'
  try {
    ops.copyFile(source, staged)
    fd = fs.openSync(staged, 'r+')
    step = 'chmod'
    ops.chmod(fd, opts.mode ?? 0o755)
    step = 'fsync'
    ops.fsync(fd)
    fs.closeSync(fd)
    fd = undefined
    if (opts.verifyStaged) {
      step = 'verify'
      opts.verifyStaged(staged)
    }
    step = 'rename'
    ops.rename(staged, target)
  } catch (e) {
    if (fd !== undefined) {
      try {
        fs.closeSync(fd)
      } catch {
        /* ignore */
      }
    }
    try {
      ops.unlink(staged)
    } catch {
      /* nothing staged, or already renamed away */
    }
    throw new InstallError(`install failed at ${step}: ${(e as Error).message}`, step, { cause: e })
  }
  // Persist the rename itself. A failure here does not undo the install.
  try {
    const dfd = fs.openSync(dir, 'r')
    try {
      fs.fsyncSync(dfd)
    } finally {
      fs.closeSync(dfd)
    }
  } catch {
    /* some file systems refuse to fsync a directory */
  }
}

/** Download `url` to `dest` through a .part file. `fetchImpl` is injectable for tests. */
export async function downloadToFile(
  url: string,
  dest: string,
  onProgress?: (done: number, total: number) => void,
  fetchImpl: typeof fetch = fetch,
): Promise<void> {
  const res = await fetchImpl(url, { headers: { 'User-Agent': 'localai-launcher' } })
  if (!res.ok || !res.body) throw new InstallError(`download failed: HTTP ${res.status}`, 'download')
  const total = Number(res.headers.get('content-length') ?? 0)
  const part = `${dest}.part`
  const out = fs.createWriteStream(part)
  let done = 0
  try {
    const reader = res.body.getReader()
    for (;;) {
      const { value, done: end } = await reader.read()
      if (end) break
      done += value.length
      if (!out.write(value)) await new Promise<void>((r) => out.once('drain', r))
      onProgress?.(done, total)
    }
    await new Promise<void>((r, j) => out.end((e?: Error | null) => (e ? j(e) : r())))
    if (total && done !== total) throw new InstallError('download was cut short', 'download')
    fs.renameSync(part, dest)
  } catch (e) {
    out.destroy()
    try {
      fs.unlinkSync(part)
    } catch {
      /* ignore */
    }
    throw e
  }
}
