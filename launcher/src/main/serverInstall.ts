// Install, update and rollback of the LocalAI server binary. No Electron import.
//
// Ported from the earlier Go launcher, since removed: release lookup, asset
// naming, checksum file, install location, version detection.
//
// This file has two layers. The reducer below is pure: it only maps a state
// and an event to the next state. The runner (further down) does the work and
// feeds the reducer; every side effect comes in through injected dependencies.

import { execFile } from 'node:child_process'
import crypto from 'node:crypto'
import { promises as fsp } from 'node:fs'
import path from 'node:path'
import semver from 'semver'
import { installAtomically } from './atomicInstall'
import { parseChecksums, sha256File } from './checksum'
import { executableFormat, minimalEnv, previousBinaryName, serverBinaryName, type ExecutableFormat } from './platform'
import { decideUpdate, parseVersion } from './serverVersion'
import { RateLimitError, type PlatformTarget, type ReleaseAsset, type ReleaseInfo, type UpdateProvider } from './updateProvider'
import type { ServerState } from '../shared/types'

/** Why an install failed. The strings are stable: a UI maps them to a message and a fix action. */
export type FailReason =
  | 'network'
  | 'no-checksum'
  | 'checksum-mismatch'
  | 'disk-full'
  | 'permission'
  | 'supervisor-busy'
  /** The new binary was installed but did not reach ready; the previous one was restored. */
  | 'start-failed'
  | 'cancelled'
  | 'unknown'

export type InstallState =
  | { kind: 'idle' }
  | { kind: 'checking' }
  | { kind: 'up-to-date'; version: string }
  | { kind: 'update-available'; version: string; tag: string; installed?: string }
  | { kind: 'downloading'; done: number; total: number }
  | { kind: 'verifying' }
  | { kind: 'installing' }
  | { kind: 'rolling-back' }
  | { kind: 'done'; version: string; adopted: boolean }
  | { kind: 'failed'; reason: FailReason; message: string; rolledBack: boolean }

export type InstallEvent =
  | { type: 'check-start' }
  | { type: 'up-to-date'; version: string }
  | { type: 'update-available'; version: string; tag: string; installed?: string }
  | { type: 'download-start'; total: number }
  | { type: 'progress'; done: number; total: number }
  | { type: 'verify-start' }
  | { type: 'install-start' }
  | { type: 'rollback-start' }
  | { type: 'installed'; version: string; adopted?: boolean }
  | { type: 'fail'; reason: FailReason; message: string; rolledBack?: boolean }
  | { type: 'reset' }

export const idleState: InstallState = { kind: 'idle' }

type Kind = InstallState['kind']
const RESTING: Kind[] = ['idle', 'up-to-date', 'update-available', 'done', 'failed']

/** Which current states accept which event. An event outside its row is ignored. */
const ACCEPTS: Record<InstallEvent['type'], Kind[]> = {
  'check-start': RESTING,
  'up-to-date': ['checking'],
  'update-available': ['checking'],
  // After verifying too: a failed adoption hash check goes on to a download.
  'download-start': ['update-available', 'verifying'],
  progress: ['downloading'],
  // Verifying also follows update-available directly: adopting an existing
  // binary hashes it without a download.
  'verify-start': ['downloading', 'update-available'],
  'install-start': ['verifying'],
  // Manual rollback starts from a resting state, automatic rollback from installing.
  'rollback-start': [...RESTING, 'installing'],
  installed: ['installing', 'verifying', 'rolling-back'],
  fail: ['idle', 'checking', 'downloading', 'verifying', 'installing', 'rolling-back', 'update-available', 'up-to-date', 'done', 'failed'],
  reset: RESTING,
}

export function reduceInstall(s: InstallState, e: InstallEvent): InstallState {
  if (!ACCEPTS[e.type].includes(s.kind)) return s
  switch (e.type) {
    case 'check-start':
      return { kind: 'checking' }
    case 'up-to-date':
      return { kind: 'up-to-date', version: e.version }
    case 'update-available':
      return { kind: 'update-available', version: e.version, tag: e.tag, installed: e.installed }
    case 'download-start':
      return { kind: 'downloading', done: 0, total: e.total }
    case 'progress':
      return { kind: 'downloading', done: e.done, total: e.total }
    case 'verify-start':
      return { kind: 'verifying' }
    case 'install-start':
      return { kind: 'installing' }
    case 'rollback-start':
      return { kind: 'rolling-back' }
    case 'installed':
      return { kind: 'done', version: e.version, adopted: e.adopted ?? false }
    case 'fail':
      return { kind: 'failed', reason: e.reason, message: e.message, rolledBack: e.rolledBack ?? false }
    case 'reset':
      return idleState
  }
}

/** True while work is under way and a second request must not start. */
export function isActive(s: InstallState): boolean {
  return !RESTING.includes(s.kind)
}

// ---------------------------------------------------------------------------
// Runner
// ---------------------------------------------------------------------------

/** What the runner needs from the process supervisor. `ServerSupervisor` already fits. */
export interface SupervisorHandle {
  readonly state: ServerState
  /** Resolves when the server is ready or has failed. */
  start(): Promise<void>
  /** Resolves when the whole process group is gone. */
  stop(): Promise<void>
}

export interface FileWriter {
  write(chunk: Uint8Array): Promise<void>
  close(): Promise<void>
}

/** The file system the runner uses. Tests replace single entries to inject failures. */
export interface InstallFs {
  mkdir(dir: string): Promise<void>
  /** undefined when the path does not exist. */
  stat(p: string): Promise<{ size: number; mtimeMs: number } | undefined>
  /** undefined when the path does not exist. */
  readText(p: string): Promise<string | undefined>
  writeTextAtomic(p: string, text: string): Promise<void>
  openWriter(p: string): Promise<FileWriter>
  copyFile(src: string, dst: string): Promise<void>
  rename(from: string, to: string): Promise<void>
  /** Remove a file or directory; a missing path is not an error. */
  remove(p: string): Promise<void>
  sha256(p: string): Promise<string>
  /** Validate (as an executable of `format`), stage on the same file system, fsync and rename over `target`. */
  installAtomically(source: string, target: string, format?: ExecutableFormat): Promise<void>
}

const enoent = (e: unknown) => (e as NodeJS.ErrnoException)?.code === 'ENOENT'

export const nodeInstallFs: InstallFs = {
  mkdir: async (d) => void (await fsp.mkdir(d, { recursive: true })),
  stat: async (p) => {
    try {
      const s = await fsp.stat(p)
      return { size: s.size, mtimeMs: s.mtimeMs }
    } catch (e) {
      if (enoent(e)) return undefined
      throw e
    }
  },
  readText: async (p) => {
    try {
      return await fsp.readFile(p, 'utf8')
    } catch (e) {
      if (enoent(e)) return undefined
      throw e
    }
  },
  writeTextAtomic: async (p, text) => {
    const tmp = `${p}.${crypto.randomBytes(4).toString('hex')}.tmp`
    try {
      await fsp.writeFile(tmp, text, { mode: 0o644 })
      await fsp.rename(tmp, p)
    } catch (e) {
      await fsp.rm(tmp, { force: true })
      throw e
    }
  },
  openWriter: async (p) => {
    const h = await fsp.open(p, 'w', 0o600)
    return { write: (c) => h.writeFile(c), close: () => h.close() }
  },
  copyFile: (a, b) => fsp.copyFile(a, b),
  rename: (a, b) => fsp.rename(a, b),
  remove: (p) => fsp.rm(p, { force: true, recursive: true }),
  sha256: sha256File,
  installAtomically: async (s, t, format) => installAtomically(s, t, { format }),
}

export type ReleaseSource = UpdateProvider & {
  /** Newest release including pre-releases. Used only when the caller opts in; optional. */
  latestIncludingPrerelease?(): Promise<ReleaseInfo>
  /** The rolling development build, for the development channel; optional. */
  latestDevelopment?(): Promise<ReleaseInfo>
}

export interface InstallPaths {
  /** Holds `local-ai` and its rollback copy `local-ai.previous` (with `.exe` on Windows). */
  binDir: string
  /** Holds `installed-version.json`. */
  metaDir: string
  /** Scratch space for the download. A partial file is removed on every exit. */
  downloadDir: string
}

export interface InstallDeps {
  provider: ReleaseSource
  fetch: typeof fetch
  fs: InstallFs
  sleep(ms: number): Promise<void>
  now(): number
  supervisor: SupervisorHandle
  target: PlatformTarget
  paths: InstallPaths
  /** Output of `<binary> --version`. */
  runVersion(binary: string): Promise<string>
  /** Release lookup retries. Default 3 attempts, wait N * 1000 ms before retry N (as the Go launcher did). */
  retry?: { attempts?: number; baseMs?: number }
}

export interface RunOptions {
  /** The user opted into pre-releases. The caller reads the config. */
  allowPrerelease?: boolean
  /** Stop a running server first, and start it again on the new binary. Without it a running server is `supervisor-busy`. */
  stopFirst?: boolean
  /**
   * The development channel: follow the rolling development build. A build is
   * newer when its id differs from the installed one; versions are not compared.
   */
  dev?: boolean
}

/** The binary name on Linux and macOS. Use serverBinaryName(target), which adds `.exe` on Windows. */
export const BINARY_NAME = serverBinaryName()
export const PREVIOUS_NAME = previousBinaryName()
const META_NAME = 'installed-version.json'
const PROGRESS_INTERVAL_MS = 100

interface InstalledRecord {
  /** A semver for a release; the build id for a development build. */
  version: string
  /** `dev` for a development build. */
  channel?: 'dev'
  tag?: string
  size: number
  mtimeMs: number
  installedAt: string
}
interface Meta {
  current?: InstalledRecord
  previous?: { version?: string; channel?: 'dev' }
}

class StepFail extends Error {
  constructor(readonly reason: FailReason, message: string, readonly rolledBack = false) {
    super(message)
  }
}

const NETWORK_CODES = new Set(['ECONNRESET', 'ECONNREFUSED', 'ECONNABORTED', 'ENOTFOUND', 'ETIMEDOUT', 'EAI_AGAIN', 'EPIPE', 'ENETUNREACH', 'EHOSTUNREACH'])

/** Map an error, looking through `cause`, to a stable reason. `fallback` applies when nothing matches. */
export function classifyError(e: unknown, fallback: FailReason): FailReason {
  let cur: unknown = e
  for (let i = 0; cur && i < 6; i++) {
    const code = (cur as NodeJS.ErrnoException).code
    if (code === 'ENOSPC' || code === 'EDQUOT') return 'disk-full'
    if (code === 'EACCES' || code === 'EPERM' || code === 'EROFS') return 'permission'
    if (code && (NETWORK_CODES.has(code) || code.startsWith('UND_ERR'))) return 'network'
    if (cur instanceof TypeError && /fetch failed/i.test(cur.message)) return 'network'
    cur = (cur as { cause?: unknown }).cause
  }
  return fallback
}

const errMessage = (e: unknown) => (e instanceof Error ? e.message : String(e))

function abortable<T>(p: Promise<T>, sig: AbortSignal): Promise<T> {
  if (sig.aborted) {
    p.catch(() => {})
    return Promise.reject(new StepFail('cancelled', 'cancelled'))
  }
  return new Promise<T>((resolve, reject) => {
    const onAbort = () => reject(new StepFail('cancelled', 'cancelled'))
    sig.addEventListener('abort', onAbort, { once: true })
    p.then(resolve, reject).finally(() => sig.removeEventListener('abort', onAbort))
  })
}

export type InstallListener = (s: InstallState) => void

/** `<binary> --version`, with a timeout. The default for `InstallDeps.runVersion`. */
export function execServerVersion(binary: string, timeoutMs = 10_000): Promise<string> {
  return new Promise((resolve, reject) => {
    execFile(binary, ['--version'], { timeout: timeoutMs, windowsHide: true, env: minimalEnv() }, (err, stdout, stderr) => {
      if (err) return reject(err)
      resolve(`${stdout}${stderr}`.trim())
    })
  })
}

export class ServerInstaller {
  private _state: InstallState = idleState
  private listeners = new Set<InstallListener>()
  private inflight?: Promise<InstallState>
  private abort?: AbortController
  private committed = false
  private lastNotify = -Infinity

  constructor(private readonly d: InstallDeps) {}

  get state(): InstallState {
    return this._state
  }

  subscribe(fn: InstallListener): () => void {
    this.listeners.add(fn)
    return () => this.listeners.delete(fn)
  }

  /** Back to idle after a terminal state. Ignored while work is under way. */
  reset(): void {
    this.dispatch({ type: 'reset' })
  }

  /**
   * Cancel the run. Possible at any point before the swap begins. Returns
   * false when nothing is running or the swap has already begun.
   */
  cancel(): boolean {
    if (!this.inflight || this.committed || !this.abort) return false
    this.abort.abort()
    return true
  }

  /** Look up the latest release and decide. Downloads nothing. */
  check(o: RunOptions = {}): Promise<InstallState> {
    return this.start((sig) => this.doCheck(o, sig).then(() => undefined))
  }

  /** Check, then download, verify and swap when an update is available. */
  install(o: RunOptions = {}): Promise<InstallState> {
    return this.start((sig) => this.doInstall(o, sig))
  }

  /** Restore the rollback copy of the previous binary. */
  rollback(o: RunOptions = {}): Promise<InstallState> {
    return this.start((sig) => this.doRollback(o, sig))
  }

  // -- plumbing -------------------------------------------------------------

  private start(job: (sig: AbortSignal) => Promise<void>): Promise<InstallState> {
    if (this.inflight) return this.inflight
    const ac = new AbortController()
    this.abort = ac
    this.committed = false
    const run = (async () => {
      try {
        await job(ac.signal)
      } catch (e) {
        const reason: FailReason = ac.signal.aborted ? 'cancelled' : e instanceof StepFail ? e.reason : classifyError(e, 'unknown')
        const message = ac.signal.aborted ? 'cancelled' : errMessage(e)
        this.dispatch({ type: 'fail', reason, message, rolledBack: e instanceof StepFail && e.rolledBack })
      }
      return this._state
    })().finally(() => {
      this.inflight = undefined
      this.abort = undefined
    })
    this.inflight = run
    return run
  }

  private dispatch(e: InstallEvent): void {
    const next = reduceInstall(this._state, e)
    if (next === this._state) return
    this._state = next
    if (e.type === 'progress' && e.done < e.total && this.d.now() - this.lastNotify < PROGRESS_INTERVAL_MS) return
    this.lastNotify = this.d.now()
    for (const fn of [...this.listeners]) {
      try {
        fn(next)
      } catch {
        /* a bad listener must not break the install */
      }
    }
  }

  private get target() {
    return path.join(this.d.paths.binDir, serverBinaryName(this.d.target))
  }
  private get previous() {
    return path.join(this.d.paths.binDir, previousBinaryName(this.d.target))
  }
  private get format(): ExecutableFormat {
    return executableFormat(this.d.target.os)
  }
  private get metaFile() {
    return path.join(this.d.paths.metaDir, META_NAME)
  }

  private busy(): boolean {
    const s = this.d.supervisor.state
    return s === 'starting' || s === 'ready' || s === 'stopping'
  }

  // -- metadata and detection ----------------------------------------------

  private async readMeta(): Promise<Meta> {
    try {
      const text = await this.d.fs.readText(this.metaFile)
      return text ? (JSON.parse(text) as Meta) : {}
    } catch {
      return {} // unreadable metadata is the same as none; the binary is asked instead
    }
  }

  private async writeMeta(m: Meta): Promise<void> {
    await this.d.fs.mkdir(this.d.paths.metaDir)
    await this.d.fs.writeTextAtomic(this.metaFile, JSON.stringify(m, null, 2))
  }

  /** Record `version` for the binary as it is on disk now. */
  private async stamp(version: string, tag: string | undefined, previous: Meta['previous'], channel?: 'dev'): Promise<void> {
    const st = await this.d.fs.stat(this.target)
    if (!st) return
    await this.writeMeta({
      current: { version, ...(channel ? { channel } : {}), tag, size: st.size, mtimeMs: st.mtimeMs, installedAt: new Date(this.d.now()).toISOString() },
      previous,
    })
  }

  /**
   * The installed version. The metadata is trusted only while the binary's
   * size and mtime still match it; otherwise the binary is asked.
   */
  /** The version of the rollback copy, when there is one and its version is recorded. */
  async rollbackVersion(): Promise<string | undefined> {
    if (!(await this.d.fs.stat(this.previous))) return undefined
    return (await this.readMeta()).previous?.version
  }

  /** The installed server's version, from the metadata or the binary; undefined when none is installed or it does not say. */
  async installedVersion(): Promise<string | undefined> {
    return (await this.detect()).version
  }

  private async detect(): Promise<{ exists: boolean; version?: string; channel?: 'dev' }> {
    const st = await this.d.fs.stat(this.target)
    if (!st) return { exists: false }
    const meta = await this.readMeta()
    if (meta.current && meta.current.size === st.size && meta.current.mtimeMs === st.mtimeMs) {
      return { exists: true, version: meta.current.version, ...(meta.current.channel ? { channel: meta.current.channel } : {}) }
    }
    try {
      return { exists: true, version: parseVersion(await this.d.runVersion(this.target)) }
    } catch {
      return { exists: true }
    }
  }

  // -- check ----------------------------------------------------------------

  private async lookup(o: RunOptions, sig: AbortSignal): Promise<ReleaseInfo> {
    const attempts = this.d.retry?.attempts ?? 3
    const base = this.d.retry?.baseMs ?? 1000
    let last: unknown
    for (let n = 1; n <= attempts; n++) {
      if (n > 1) {
        await abortable(this.d.sleep((n - 1) * base), sig)
      }
      try {
        const src = this.d.provider
        if (o.dev) {
          if (!src.latestDevelopment) throw new Error('this release source has no development build')
          return await abortable(src.latestDevelopment(), sig)
        }
        const p = o.allowPrerelease && src.latestIncludingPrerelease ? src.latestIncludingPrerelease() : src.latestRelease()
        return await abortable(p, sig)
      } catch (e) {
        if (sig.aborted) throw new StepFail('cancelled', 'cancelled')
        last = e
        if (e instanceof RateLimitError) break // waiting seconds will not lift an hourly limit
      }
    }
    throw new StepFail('network', `release lookup failed: ${errMessage(last)}`)
  }

  private async doCheck(o: RunOptions, sig: AbortSignal): Promise<{ release: ReleaseInfo; version: string; installed: { exists: boolean; version?: string; channel?: 'dev' } } | undefined> {
    this.dispatch({ type: 'check-start' })
    const installed = await this.detect()
    if (sig.aborted) throw new StepFail('cancelled', 'cancelled')
    const release = await this.lookup(o, sig)
    if (o.dev) {
      // A development build is newer when its id differs. No semver compare.
      const id = release.build?.id
      if (!id) throw new StepFail('unknown', 'the development release names no build')
      const have = installed.channel === 'dev' ? installed.version : undefined
      if (have === id) {
        this.dispatch({ type: 'up-to-date', version: id })
        return undefined
      }
      this.dispatch({ type: 'update-available', version: id, tag: release.tag, installed: have })
      return { release, version: id, installed }
    }
    const decision = decideUpdate({ installed: installed.version, tag: release.tag, prerelease: release.prerelease, allowPrerelease: !!o.allowPrerelease })
    if (decision.kind === 'ignored') {
      if (installed.version) {
        this.dispatch({ type: 'up-to-date', version: installed.version })
        return undefined
      }
      throw new StepFail('unknown', decision.reason === 'prerelease' ? 'only a pre-release is available' : `release tag ${release.tag} is not a version`)
    }
    if (decision.kind === 'up-to-date') {
      this.dispatch({ type: 'up-to-date', version: decision.version })
      return undefined
    }
    this.dispatch({ type: 'update-available', version: decision.version, tag: release.tag, installed: installed.version })
    return { release, version: decision.version, installed }
  }

  // -- install --------------------------------------------------------------

  private async fetchChecksums(url: string, sig: AbortSignal): Promise<string> {
    let res: Response
    try {
      res = await this.d.fetch(url, { headers: { 'User-Agent': 'localai-launcher' }, signal: sig })
    } catch (e) {
      throw new StepFail(sig.aborted ? 'cancelled' : classifyError(e, 'network'), `checksum download failed: ${errMessage(e)}`)
    }
    if (res.status === 404 || res.status === 410) throw new StepFail('no-checksum', 'the release has no checksum file; refusing to install')
    if (!res.ok) throw new StepFail('network', `checksum download failed: HTTP ${res.status}`)
    try {
      return await res.text()
    } catch (e) {
      throw new StepFail(sig.aborted ? 'cancelled' : 'network', `checksum download failed: ${errMessage(e)}`)
    }
  }

  private async download(asset: ReleaseAsset, dest: string, sig: AbortSignal): Promise<void> {
    let res: Response
    try {
      res = await this.d.fetch(asset.url, { headers: { 'User-Agent': 'localai-launcher' }, signal: sig })
    } catch (e) {
      throw new StepFail(sig.aborted ? 'cancelled' : classifyError(e, 'network'), `download failed: ${errMessage(e)}`)
    }
    if (!res.ok || !res.body) throw new StepFail('network', `download failed: HTTP ${res.status}`)
    const total = Number(res.headers.get('content-length')) || asset.size || 0
    await this.d.fs.mkdir(this.d.paths.downloadDir)
    const writer = await this.d.fs.openWriter(dest)
    this.dispatch({ type: 'download-start', total })
    let done = 0
    try {
      const reader = res.body.getReader()
      for (;;) {
        if (sig.aborted) {
          await reader.cancel().catch(() => {})
          throw new StepFail('cancelled', 'cancelled')
        }
        let chunk: { done: boolean; value?: Uint8Array }
        try {
          chunk = await reader.read()
        } catch (e) {
          throw new StepFail(sig.aborted ? 'cancelled' : classifyError(e, 'network'), `download failed: ${errMessage(e)}`)
        }
        if (chunk.done || !chunk.value) break
        try {
          await writer.write(chunk.value)
        } catch (e) {
          throw new StepFail(classifyError(e, 'unknown'), `could not write the download: ${errMessage(e)}`)
        }
        done += chunk.value.length
        this.dispatch({ type: 'progress', done, total: Math.max(total, done) })
      }
      await writer.close().catch((e) => {
        throw new StepFail(classifyError(e, 'unknown'), `could not write the download: ${errMessage(e)}`)
      })
      if (total && done !== total) throw new StepFail('network', 'download was cut short')
    } catch (e) {
      await writer.close().catch(() => {})
      throw e
    }
  }

  private async restartQuietly(): Promise<void> {
    try {
      await this.d.supervisor.start()
    } catch {
      /* the caller already reports the real failure */
    }
  }

  private async doInstall(o: RunOptions, sig: AbortSignal): Promise<void> {
    const plan = await this.doCheck(o, sig)
    if (!plan) return
    const { release, version, installed } = plan
    const cancelled = () => {
      if (sig.aborted) throw new StepFail('cancelled', 'cancelled')
    }

    if (this.busy() && !o.stopFirst) throw new StepFail('supervisor-busy', 'the server is running; stop it before installing')

    const asset = this.d.provider.resolveAsset(release, this.d.target)
    if (!asset) throw new StepFail('unknown', `release ${release.tag} has no server binary for ${this.d.target.os}/${this.d.target.arch}`)
    const sumAsset = this.d.provider.resolveChecksumFile(release, this.d.target)
    if (!sumAsset) throw new StepFail('no-checksum', 'the release has no checksum file; refusing to install')
    let sumText = await this.fetchChecksums(sumAsset.url, sig)
    // A per-file checksum of the development build may hold the hash alone.
    if (o.dev && /^[0-9a-fA-F]{64}\s*$/.test(sumText)) sumText = `${sumText.trim()}  ${asset.name}\n`
    const sums = parseChecksums(sumText)
    cancelled()
    const want = sums.get(asset.name)
    if (!want) throw new StepFail('no-checksum', `the checksum file has no valid entry for ${asset.name}; refusing to install`)

    // Adopt a binary that is already the released one.
    if (installed.exists && !installed.version) {
      this.dispatch({ type: 'verify-start' })
      let have: string | undefined
      try {
        have = await this.d.fs.sha256(this.target)
      } catch {
        have = undefined
      }
      cancelled()
      if (have === want) {
        await this.stamp(version, release.tag, undefined).catch(() => {})
        this.dispatch({ type: 'installed', version, adopted: true })
        return
      }
    }

    const dest = path.join(this.d.paths.downloadDir, `${asset.name}.part`)
    try {
      await this.d.fs.remove(dest)
      await this.download(asset, dest, sig)
      cancelled()

      this.dispatch({ type: 'verify-start' })
      let got: string
      try {
        got = await this.d.fs.sha256(dest)
      } catch (e) {
        throw new StepFail(classifyError(e, 'unknown'), `could not read the download: ${errMessage(e)}`)
      }
      cancelled()
      if (got !== want) throw new StepFail('checksum-mismatch', `checksum mismatch for ${asset.name}`)

      // From here on the old server is touched, so cancel is no longer offered.
      if (this.busy() && !o.stopFirst) throw new StepFail('supervisor-busy', 'the server started during the download; stop it and try again')
      cancelled()
      this.committed = true
      this.dispatch({ type: 'install-start' })
      let wasRunning = false
      if (this.busy()) {
        wasRunning = true
        await this.d.supervisor.stop()
      }
      await this.swap(dest, installed, wasRunning)
      await this.afterSwap(version, release.tag, installed, wasRunning, o.dev)
    } finally {
      await this.d.fs.remove(dest).catch(() => {})
    }
  }

  /** Keep a rollback copy, then swap. On failure the old binary is untouched. */
  private async swap(dest: string, installed: { exists: boolean; version?: string }, wasRunning: boolean): Promise<void> {
    const tmp = `${this.previous}.tmp`
    try {
      // A first install has no binary folder yet.
      await this.d.fs.mkdir(this.d.paths.binDir)
      if (installed.exists) {
        await this.d.fs.copyFile(this.target, tmp)
        await this.d.fs.rename(tmp, this.previous)
      }
      await this.d.fs.installAtomically(dest, this.target, this.format)
    } catch (e) {
      await this.d.fs.remove(tmp).catch(() => {})
      if (wasRunning) await this.restartQuietly()
      throw new StepFail(classifyError(e, 'unknown'), `install failed: ${errMessage(e)}`)
    }
  }

  /** Version check, metadata, health start. Any failure restores the previous binary. */
  private async afterSwap(version: string, tag: string, installed: { exists: boolean; version?: string; channel?: 'dev' }, wasRunning: boolean, dev = false): Promise<void> {
    const before = await this.readMeta()
    const fail = async (why: string, reason: FailReason = 'unknown'): Promise<never> => {
      await this.restore(installed, before, wasRunning)
      throw new StepFail(reason, `${why}; the previous version was restored`, true)
    }

    let reported: string | undefined
    try {
      reported = parseVersion(await this.d.runVersion(this.target))
    } catch (e) {
      return fail(`the new binary does not run: ${errMessage(e)}`)
    }
    // A development build has no version to compare: it must run, and that is all.
    if (!dev && (!reported || !semver.eq(reported, version))) return fail(`the new binary reports ${reported ?? 'no version'}, expected ${version}`)

    await this.stamp(version, tag, installed.exists ? { version: installed.version, ...(installed.channel ? { channel: installed.channel } : {}) } : undefined, dev ? 'dev' : undefined).catch(() => {
      /* the binary is asked next time */
    })

    if (wasRunning) {
      await this.d.supervisor.start()
      if (this.d.supervisor.state !== 'ready') {
        await this.d.supervisor.stop().catch(() => {})
        return fail('the new version did not start', 'start-failed')
      }
    }
    this.dispatch({ type: 'installed', version })
  }

  /** Put the rollback copy back (or remove a first install), then restart the old server if it was running. */
  private async restore(installed: { exists: boolean; version?: string }, before: Meta, wasRunning: boolean): Promise<void> {
    this.dispatch({ type: 'rollback-start' })
    try {
      if (installed.exists) await this.d.fs.installAtomically(this.previous, this.target, this.format)
      else await this.d.fs.remove(this.target)
    } catch (e) {
      throw new StepFail(classifyError(e, 'unknown'), `the new version failed and the previous one could not be restored: ${errMessage(e)}`)
    }
    try {
      if (installed.exists && before.current) await this.stamp(before.current.version, before.current.tag, before.previous, before.current.channel)
      else await this.d.fs.remove(this.metaFile)
    } catch {
      /* stale metadata is detected by size and mtime */
    }
    if (wasRunning) await this.restartQuietly()
  }

  // -- manual rollback ------------------------------------------------------

  private async doRollback(o: RunOptions, sig: AbortSignal): Promise<void> {
    this.dispatch({ type: 'rollback-start' })
    if (!(await this.d.fs.stat(this.previous))) throw new StepFail('unknown', 'there is no previous version to restore')
    if (this.busy() && !o.stopFirst) throw new StepFail('supervisor-busy', 'the server is running; stop it before rolling back')
    if (sig.aborted) throw new StepFail('cancelled', 'cancelled')
    this.committed = true
    const wasRunning = this.busy()
    if (wasRunning) await this.d.supervisor.stop()
    try {
      await this.d.fs.installAtomically(this.previous, this.target, this.format)
    } catch (e) {
      if (wasRunning) await this.restartQuietly()
      throw new StepFail(classifyError(e, 'unknown'), `rollback failed: ${errMessage(e)}`)
    }
    let version: string | undefined
    const prev = (await this.readMeta()).previous
    try {
      version = parseVersion(await this.d.runVersion(this.target))
    } catch (e) {
      throw new StepFail('unknown', `the restored binary does not run: ${errMessage(e)}`, true)
    }
    // A development build reports no usable version: take the build id that was recorded for the copy.
    if (prev?.channel === 'dev' && prev.version) version = prev.version
    if (!version) throw new StepFail('unknown', 'the restored binary reports no version', true)
    await this.stamp(version, undefined, undefined, prev?.channel).catch(() => {})
    await this.d.fs.remove(this.previous).catch(() => {})
    if (wasRunning) {
      await this.d.supervisor.start()
      if (this.d.supervisor.state !== 'ready') throw new StepFail('unknown', 'the previous version was restored but did not start', true)
    }
    this.dispatch({ type: 'installed', version })
  }
}
