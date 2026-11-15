// Named secrets of the launcher: the server's API key, the worker token and
// the values of the user's environment variables. No Electron import: the OS
// secure storage (Electron's `safeStorage`) comes in through SafeStorageLike,
// so the tests use a fake.
//
// Rules:
// - Values live in `secrets.json`, next to launcher.json but never in it.
//   The file holds base64 of each encrypted blob, mode 0600, written
//   atomically (temp file, fsync, rename).
// - Values are never logged. Log lines name a count or a secret name only.
// - Without usable secure storage, nothing is written in plain text unless
//   the user opted in: a value is kept in memory for this session and `set`
//   answers `unavailable`, so the caller can say so. With the opt-in, values
//   go to the same file, base64 of the plain text, mode 0600.
// - On Linux, Chromium falls back to a fixed built-in password when there is
//   no keyring (`basic_text`). That is not secure storage, so it counts as
//   unavailable.
// - The flags are not trusted alone: a round trip of a fixed probe string at
//   start decides (see encryptionUsable). When encryption fails later
//   anyway, the value is kept in memory and the store stays session-only
//   until the launcher quits.
// - Blobs that cannot be decrypted now (a locked keyring, a copied home
//   folder) are kept in the file untouched and are never overwritten.
import crypto from 'node:crypto'
import fs from 'node:fs'
import path from 'node:path'
import { ENV_NAME, MAX_ENV_NAME, MAX_ENV_VALUE } from '../shared/envVars'
import type { SecretBackend, SecretStorageView } from '../shared/types'

/** The part of Electron's safeStorage the store uses. */
export interface SafeStorageLike {
  /** Not used: Electron 45 deprecates it, and the round trip decides. */
  isEncryptionAvailable?(): boolean
  encryptString(plain: string): Buffer
  decryptString(encrypted: Buffer): string
  /** Linux only in Electron: which backend Chromium chose. */
  getSelectedStorageBackend?(): string
}

export const SECRETS_FILE = 'secrets.json'
export const SECRETS_FILE_MODE = 0o600
const FILE_VERSION = 1

export const API_KEY_SECRET = 'api_key'
export const WORKER_TOKEN_SECRET = 'worker_token'
export const ENV_PREFIX = 'env:'
export const envSecret = (name: string) => `${ENV_PREFIX}${name}`

export function isSecretName(name: string): boolean {
  if (name === API_KEY_SECRET || name === WORKER_TOKEN_SECRET) return true
  if (!name.startsWith(ENV_PREFIX)) return false
  const env = name.slice(ENV_PREFIX.length)
  return env.length > 0 && env.length <= MAX_ENV_NAME && ENV_NAME.test(env)
}

export class SecretNameError extends Error {
  constructor() {
    super('not a known secret name')
    this.name = 'SecretNameError'
  }
}

/** A fixed string, never a secret, encrypted and decrypted once to test the storage. */
export const SAFE_STORAGE_PROBE = 'localai-launcher-storage-probe'

const probeCache = new WeakMap<SafeStorageLike, Map<NodeJS.Platform, boolean>>()

/**
 * True when values can be encrypted with storage that is really secure. On
 * Linux the `basic_text` and `unknown` backends (no keyring, a fixed
 * password) do not count. For every backend, a round trip of
 * SAFE_STORAGE_PROBE must work: Electron 45 on a Linux host with no keyring
 * names the backend `gnome_libsecret` and then throws in encryptString. On
 * macOS and Windows the round trip alone decides. The result is cached per
 * safeStorage object, so the probe runs once.
 */
export function encryptionUsable(s: SafeStorageLike | undefined, platform: NodeJS.Platform): boolean {
  if (!s) return false
  const cached = probeCache.get(s)?.get(platform)
  if (cached !== undefined) return cached
  const ok = probe(s, platform)
  if (!probeCache.has(s)) probeCache.set(s, new Map())
  probeCache.get(s)!.set(platform, ok)
  return ok
}

function probe(s: SafeStorageLike, platform: NodeJS.Platform): boolean {
  try {
    if (platform === 'linux' && s.getSelectedStorageBackend) {
      const b = s.getSelectedStorageBackend()
      if (b === 'basic_text' || b === 'unknown') return false
    }
    const blob = s.encryptString(SAFE_STORAGE_PROBE)
    if (!blob || blob.length === 0) return false
    return s.decryptString(blob) === SAFE_STORAGE_PROBE
  } catch {
    return false
  }
}

export type SetResult =
  | { status: 'stored'; backend: 'keychain' | 'plaintext-file' }
  /** No secure storage and no opt-in: kept in memory for this session only. */
  | { status: 'unavailable'; kept: 'session' }

type FileFormat = 'safeStorage' | 'plaintext'

interface FileShape {
  version: number
  format: FileFormat
  secrets: Record<string, string>
}

export interface SecretStoreOptions {
  /** Full path of secrets.json. */
  file: string
  safeStorage?: SafeStorageLike
  platform?: NodeJS.Platform
  /** The user chose a plain file when there is no secure storage. */
  plaintextOptIn?: boolean
  /** Receives counts, names and outcomes only. */
  log?: (message: string) => void
  now?: () => Date
}

const isObject = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)

function stamp(d: Date): string {
  return d.toISOString().replace(/[-:]/g, '').replace(/\.\d+Z$/, 'Z')
}

/** Atomic write with the final mode set from the start: temp file 0600, fsync, rename, then fsync the folder. */
export async function writeFileAtomic(file: string, data: string, mode = SECRETS_FILE_MODE): Promise<void> {
  const dir = path.dirname(file)
  await fs.promises.mkdir(dir, { recursive: true, mode: 0o700 })
  const tmp = path.join(dir, `.${path.basename(file)}.${process.pid}.${crypto.randomBytes(6).toString('hex')}.tmp`)
  let fh: fs.promises.FileHandle | undefined
  try {
    fh = await fs.promises.open(tmp, 'wx', mode)
    await fh.writeFile(data, 'utf8')
    await fh.sync()
    await fh.close()
    fh = undefined
    await fs.promises.rename(tmp, file)
  } catch (e) {
    if (fh) await fh.close().catch(() => undefined)
    await fs.promises.rm(tmp, { force: true }).catch(() => undefined)
    throw e
  }
  try {
    const dh = await fs.promises.open(dir, 'r')
    await dh.sync().catch(() => undefined)
    await dh.close()
  } catch {
    /* best effort */
  }
}

export class SecretStore {
  /** keychain: encrypted blobs. plaintext-file: the plain values. */
  private persisted = new Map<string, Buffer | string>()
  /** Values kept for this session only. */
  private session = new Map<string, string>()
  /** Blobs of the file that cannot be decrypted now; written back unchanged. */
  private locked = new Map<string, string>()
  private optIn: boolean
  private usable: boolean
  /** Encryption failed after the start probe: session-only until quit. */
  private encryptFailed = false
  /** The file on disk is a plain file (the user chose it once). */
  private fileIsPlain = false
  private queue: Promise<unknown> = Promise.resolve()
  private readonly log: (m: string) => void

  private constructor(private readonly o: SecretStoreOptions) {
    this.optIn = !!o.plaintextOptIn
    this.usable = encryptionUsable(o.safeStorage, o.platform ?? process.platform)
    this.log = o.log ?? (() => undefined)
  }

  static async open(o: SecretStoreOptions): Promise<SecretStore> {
    const s = new SecretStore(o)
    await s.load()
    return s
  }

  // ---- state ----------------------------------------------------------------

  /** Where a new value goes now. */
  backend(): SecretBackend {
    if (this.usable) return 'keychain'
    if (this.encryptFailed || this.locked.size > 0) return 'session'
    return this.plainOn() ? 'plaintext-file' : 'session'
  }

  private plainOn(): boolean {
    return this.optIn || this.fileIsPlain
  }

  state(): SecretStorageView {
    return {
      backend: this.backend(),
      encryptionAvailable: this.usable,
      plaintextOptIn: this.plainOn(),
      unreadable: this.locked.size,
    }
  }

  // ---- read -------------------------------------------------------------------

  has(name: string): boolean {
    return this.persisted.has(name) || this.session.has(name)
  }

  get(name: string): string | undefined {
    // A value set for this session wins over an older stored one of the same name.
    const v = this.session.get(name)
    if (v !== undefined) return v
    const p = this.persisted.get(name)
    if (p !== undefined) {
      if (typeof p === 'string') return p
      try {
        return this.o.safeStorage!.decryptString(p)
      } catch {
        this.log(`secrets: could not decrypt ${name}`)
        return undefined
      }
    }
    return undefined
  }

  /** Names of the readable secrets, sorted; with `prefix`, only those that start with it. */
  list(prefix = ''): string[] {
    const names = new Set([...this.persisted.keys(), ...this.session.keys()])
    return [...names].filter((n) => n.startsWith(prefix)).sort()
  }

  /** Every readable value, for redaction of log lines. */
  values(): string[] {
    const out: string[] = []
    for (const n of this.list()) {
      const v = this.get(n)
      if (v) out.push(v)
    }
    return out
  }

  /** True when the value of `name` lives only in memory. */
  isSessionOnly(name: string): boolean {
    return this.session.has(name)
  }

  // ---- write ------------------------------------------------------------------

  private serial<T>(fn: () => Promise<T>): Promise<T> {
    const run = this.queue.then(fn)
    this.queue = run.catch(() => undefined)
    return run
  }

  set(name: string, value: string): Promise<SetResult> {
    if (!isSecretName(name)) return Promise.reject(new SecretNameError())
    if (typeof value !== 'string' || value.length > MAX_ENV_VALUE || value.includes('\u0000')) {
      return Promise.reject(new Error('secret value refused'))
    }
    return this.serial(async () => {
      const backend = this.backend()
      if (backend === 'session') {
        this.session.set(name, value)
        // A plain-file value of the same name stays only when the file is still in use.
        return { status: 'unavailable', kept: 'session' } as const
      }
      const before = this.persisted.get(name)
      let stored: Buffer | string = value
      if (backend === 'keychain') {
        const blob = this.encrypt(value)
        if (!blob) {
          // Never lose the value: keep it for this session, and stop using secure storage.
          this.usable = false
          this.encryptFailed = true
          this.session.set(name, value)
          this.log(`secrets: secure storage could not encrypt ${name}; values are kept for this session only`)
          return { status: 'unavailable', kept: 'session' } as const
        }
        stored = blob
      }
      this.persisted.set(name, stored)
      try {
        await this.write()
      } catch (e) {
        if (before === undefined) this.persisted.delete(name)
        else this.persisted.set(name, before)
        this.log(`secrets: save failed (${(e as NodeJS.ErrnoException).code ?? 'error'})`)
        throw e
      }
      this.session.delete(name)
      return { status: 'stored', backend } as const
    })
  }

  /** The encrypted blob, or undefined when encryption throws or gives nothing. */
  private encrypt(value: string): Buffer | undefined {
    try {
      const b = this.o.safeStorage!.encryptString(value)
      return b && b.length > 0 ? b : undefined
    } catch {
      return undefined
    }
  }

  delete(name: string): Promise<void> {
    return this.serial(async () => {
      this.session.delete(name)
      const had = this.persisted.delete(name)
      const hadLocked = this.locked.delete(name)
      if (had || hadLocked) await this.write()
    })
  }

  /**
   * Turns the plain-file opt-in on or off. On: values kept for this session
   * go to the file. Off: the file's plain values move to memory and the file
   * is removed. Has no effect on the keychain backend.
   */
  setPlaintextOptIn(on: boolean): Promise<SecretStorageView> {
    return this.serial(async () => {
      this.optIn = on
      if (this.usable || this.encryptFailed || this.locked.size > 0) return this.state()
      if (on) {
        for (const [k, v] of this.session) this.persisted.set(k, v)
        if (this.session.size > 0) await this.write()
        this.session.clear()
        this.fileIsPlain = true
        this.log('secrets: kept in a plain file, by choice')
      } else {
        for (const [k, v] of this.persisted) if (typeof v === 'string') this.session.set(k, v)
        this.persisted.clear()
        this.fileIsPlain = false
        await fs.promises.rm(this.o.file, { force: true })
        this.log('secrets: plain file removed; values kept for this session')
      }
      return this.state()
    })
  }

  /** Resolves when every queued write has finished. */
  idle(): Promise<void> {
    return this.queue.then(() => undefined)
  }

  // ---- file -------------------------------------------------------------------

  private async write(): Promise<void> {
    const format: FileFormat = this.usable || this.encryptFailed || this.locked.size > 0 ? 'safeStorage' : 'plaintext'
    const secrets: Record<string, string> = {}
    for (const [k, b64] of this.locked) secrets[k] = b64
    for (const [k, v] of this.persisted) {
      secrets[k] = typeof v === 'string' ? Buffer.from(v, 'utf8').toString('base64') : v.toString('base64')
    }
    if (Object.keys(secrets).length === 0) {
      await fs.promises.rm(this.o.file, { force: true })
      return
    }
    const body: FileShape = { version: FILE_VERSION, format, secrets }
    await writeFileAtomic(this.o.file, JSON.stringify(body, null, 2) + '\n')
    this.fileIsPlain = format === 'plaintext'
  }

  private async load(): Promise<void> {
    let text: string
    try {
      text = await fs.promises.readFile(this.o.file, 'utf8')
    } catch (e) {
      if ((e as NodeJS.ErrnoException).code !== 'ENOENT') this.log(`secrets: cannot read the file (${(e as NodeJS.ErrnoException).code ?? 'error'})`)
      return
    }
    let parsed: unknown
    try {
      parsed = JSON.parse(text)
    } catch {
      parsed = undefined
    }
    if (!isObject(parsed) || (parsed.format !== 'safeStorage' && parsed.format !== 'plaintext') || !isObject(parsed.secrets)) {
      // Never parse further or quote it: move it aside and start empty.
      const aside = `${this.o.file}.bad-${stamp((this.o.now ?? (() => new Date()))())}`
      await fs.promises.rename(this.o.file, aside).catch(() => undefined)
      this.log('secrets: the file is not valid, moved aside')
      return
    }
    // Tighten an older or hand-made file.
    await fs.promises.chmod(this.o.file, SECRETS_FILE_MODE).catch(() => undefined)
    const entries = Object.entries(parsed.secrets).filter((e): e is [string, string] => typeof e[1] === 'string' && isSecretName(e[0]))
    if (parsed.format === 'plaintext') {
      this.fileIsPlain = true
      for (const [k, b64] of entries) this.persisted.set(k, Buffer.from(b64, 'base64').toString('utf8'))
      if (this.usable) {
        // Secure storage works now: move every value into it. If one fails, the plain file stays as it is.
        const blobs = new Map<string, Buffer>()
        for (const [k, v] of this.persisted) {
          const b = this.encrypt(v as string)
          if (!b) break
          blobs.set(k, b)
        }
        if (blobs.size < this.persisted.size) {
          this.usable = false
          this.log('secrets: secure storage could not encrypt; the plain file is kept')
          return
        }
        for (const [k, b] of blobs) this.persisted.set(k, b)
        await this.write()
        this.log(`secrets: moved ${entries.length} values from the plain file to secure storage`)
      }
      return
    }
    for (const [k, b64] of entries) {
      const blob = Buffer.from(b64, 'base64')
      if (!this.usable) {
        this.locked.set(k, b64)
        continue
      }
      try {
        this.o.safeStorage!.decryptString(blob)
        this.persisted.set(k, blob)
      } catch {
        this.locked.set(k, b64)
      }
    }
    if (this.locked.size > 0) this.log(`secrets: ${this.locked.size} stored values cannot be read now; kept unchanged`)
  }
}
