// Log lines for the launcher: a bounded in-memory ring for the logs screen, a
// rotating file per source, and one-file export. No Electron import.
//
// Every line is redacted once on the way in, so neither the ring nor the files
// hold a secret. Export redacts again, which also covers files written by an
// older version.
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import crypto from 'node:crypto'

export type LogSource = 'server' | 'launcher' | 'updater'
export type LogLevel = 'debug' | 'info' | 'warn' | 'error'

export const LOG_SOURCES: readonly LogSource[] = ['server', 'launcher', 'updater']

export interface LogLine {
  /** Grows by one for every line the ring accepts. */
  seq: number
  /** Milliseconds since the epoch. */
  ts: number
  source: LogSource
  level: LogLevel
  text: string
}

/** What the server supervisor feeds. */
export interface LogSink {
  write(source: LogSource, line: string, level?: LogLevel): void
}

const LEVEL_RANK: Record<LogLevel, number> = { debug: 0, info: 1, warn: 2, error: 3 }

// ---- redaction -------------------------------------------------------------

const MASK = '[REDACTED]'
const SECRET_KEY = String.raw`[\w.-]*(?:api[_-]?key|token|secret|passw(?:or)?d)`
const VALUE = String.raw`"[^"]*"|'[^']*'|[^\s,;&"']+`

const REDACTIONS: Array<[RegExp, string]> = [
  // Authorization: Bearer abc, Authorization=Basic abc, "authorization": "x"
  [new RegExp(String.raw`(authorization["']?\s*[:=]\s*)(?:"[^"]*"|'[^']*'|(?:Bearer|Basic)\s+\S+|\S+)`, 'gi'), `$1${MASK}`],
  // Bearer abc
  [/\bBearer\s+[A-Za-z0-9._~+/=-]+/gi, `Bearer ${MASK}`],
  // api_key=abc, "password": "abc", LOCALAI_API_KEY=abc, access_token: abc
  [new RegExp(String.raw`\b(${SECRET_KEY}["']?\s*[:=]\s*)(?:${VALUE})`, 'gi'), `$1${MASK}`],
  // --api-key abc, --token abc
  [new RegExp(String.raw`(--?${SECRET_KEY}\s+)(?!-)\S+`, 'gi'), `$1${MASK}`],
]

export function redact(text: string): string {
  let out = text
  for (const [re, repl] of REDACTIONS) out = out.replace(re, repl)
  return out
}

// ---- ring ------------------------------------------------------------------

export interface RingOptions {
  capacity?: number
  maxLineLength?: number
}

export interface LogQuery {
  source?: LogSource
  /** Lines at this level or above. */
  minLevel?: LogLevel
  /** Only lines with a larger seq, for catching up after a gap. */
  afterSeq?: number
  /** The most recent N matching lines. */
  limit?: number
}

export type LogListener = (line: LogLine) => void

export class LogRing {
  private lines: LogLine[] = []
  private seq = 0
  private listeners = new Set<LogListener>()
  readonly capacity: number
  readonly maxLineLength: number

  constructor(opts: RingOptions = {}) {
    this.capacity = Math.max(1, opts.capacity ?? 2000)
    this.maxLineLength = Math.max(16, opts.maxLineLength ?? 8192)
  }

  push(source: LogSource, level: LogLevel, text: string, ts = Date.now()): LogLine {
    const clipped = text.length > this.maxLineLength ? `${text.slice(0, this.maxLineLength)}... [truncated]` : text
    const line: LogLine = { seq: ++this.seq, ts, source, level, text: clipped }
    this.lines.push(line)
    if (this.lines.length > this.capacity) this.lines.splice(0, this.lines.length - this.capacity)
    for (const l of [...this.listeners]) {
      try {
        l(line)
      } catch {
        /* a bad subscriber must not stop logging */
      }
    }
    return line
  }

  get size(): number {
    return this.lines.length
  }

  /** Returns the unsubscribe function. */
  subscribe(listener: LogListener): () => void {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  query(q: LogQuery = {}): LogLine[] {
    const min = q.minLevel ? LEVEL_RANK[q.minLevel] : 0
    let out = this.lines.filter(
      (l) => (!q.source || l.source === q.source) && LEVEL_RANK[l.level] >= min && (q.afterSeq === undefined || l.seq > q.afterSeq),
    )
    if (q.limit !== undefined && out.length > q.limit) out = out.slice(out.length - Math.max(0, q.limit))
    return out
  }
}

// ---- rotating file ---------------------------------------------------------

export interface RotatingFileOptions {
  dir: string
  /** File name, e.g. `server.log`. Rotated files are `server.log.1`, `.2`, ... */
  name: string
  maxBytes?: number
  /** Number of rotated files kept besides the current one. */
  keep?: number
}

export class RotatingFileWriter {
  private fd: number | undefined
  private size = 0
  private readonly maxBytes: number
  private readonly keep: number
  /** Last write or rotation error code, if any. Writing never throws. */
  lastError: string | undefined

  constructor(private readonly o: RotatingFileOptions) {
    this.maxBytes = o.maxBytes ?? 5 * 1024 * 1024
    this.keep = Math.max(0, o.keep ?? 3)
  }

  get file(): string {
    return path.join(this.o.dir, this.o.name)
  }

  private open(): void {
    fs.mkdirSync(this.o.dir, { recursive: true })
    this.fd = fs.openSync(this.file, 'a', 0o600)
    this.size = fs.fstatSync(this.fd).size
  }

  private rotate(): void {
    if (this.fd !== undefined) {
      fs.closeSync(this.fd)
      this.fd = undefined
    }
    const f = this.file
    if (this.keep === 0) {
      fs.rmSync(f, { force: true })
    } else {
      fs.rmSync(`${f}.${this.keep}`, { force: true })
      for (let i = this.keep - 1; i >= 1; i--) {
        if (fs.existsSync(`${f}.${i}`)) fs.renameSync(`${f}.${i}`, `${f}.${i + 1}`)
      }
      if (fs.existsSync(f)) fs.renameSync(f, `${f}.1`)
    }
  }

  write(line: string): void {
    try {
      const data = line.endsWith('\n') ? line : `${line}\n`
      const bytes = Buffer.byteLength(data)
      if (this.fd === undefined) this.open()
      if (this.size > 0 && this.size + bytes > this.maxBytes) {
        this.rotate()
        this.open()
      }
      fs.writeSync(this.fd as number, data)
      this.size += bytes
    } catch (e) {
      this.lastError = (e as NodeJS.ErrnoException).code ?? 'error'
      if (this.fd !== undefined) {
        try {
          fs.closeSync(this.fd)
        } catch {
          /* ignore */
        }
        this.fd = undefined
      }
    }
  }

  close(): void {
    if (this.fd !== undefined) {
      try {
        fs.closeSync(this.fd)
      } catch {
        /* ignore */
      }
      this.fd = undefined
    }
  }
}

/** Files of one log, oldest first: name.N ... name.1, name. Missing files are skipped. */
export function logFiles(dir: string, name: string): string[] {
  let entries: string[]
  try {
    entries = fs.readdirSync(dir)
  } catch {
    return []
  }
  const rotated = entries
    .map((e) => ({ e, m: new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\.(\\d+)$`).exec(e) }))
    .filter((x) => x.m)
    .map((x) => ({ file: path.join(dir, x.e), n: Number(x.m![1]) }))
    .sort((a, b) => b.n - a.n)
    .map((x) => x.file)
  if (entries.includes(name)) rotated.push(path.join(dir, name))
  return rotated
}

// ---- hub -------------------------------------------------------------------

const SERVER_LEVEL: Array<[RegExp, LogLevel]> = [
  [/\b(ERR|ERROR|FATAL|PANIC)\b/, 'error'],
  [/\b(WRN|WARN|WARNING)\b/, 'warn'],
  [/\b(DBG|DEBUG|TRC|TRACE)\b/, 'debug'],
]

function inferLevel(line: string): LogLevel {
  for (const [re, lvl] of SERVER_LEVEL) if (re.test(line)) return lvl
  return 'info'
}

export interface LogHubOptions {
  /** Directory for the log files. See `defaultLogsDir`. */
  dir: string
  ring?: RingOptions
  maxBytes?: number
  keep?: number
  now?: () => number
}

export interface ExportResult {
  path: string
  bytes: number
  files: number
}

/** The folder the Go launcher already used: <data folder>/logs. */
export function defaultLogsDir(dataFolder: string): string {
  return path.join(dataFolder, 'logs')
}

export class LogHub implements LogSink {
  readonly ring: LogRing
  private writers = new Map<LogSource, RotatingFileWriter>()
  private readonly now: () => number

  constructor(private readonly o: LogHubOptions) {
    this.ring = new LogRing(o.ring)
    this.now = o.now ?? Date.now
    for (const s of LOG_SOURCES) {
      this.writers.set(s, new RotatingFileWriter({ dir: o.dir, name: `${s}.log`, maxBytes: o.maxBytes, keep: o.keep }))
    }
  }

  get dir(): string {
    return this.o.dir
  }

  write(source: LogSource, line: string, level?: LogLevel): void {
    const text = redact(line.replace(/\r?\n$/, ''))
    const lvl = level ?? (source === 'server' ? inferLevel(text) : 'info')
    const ts = this.now()
    this.ring.push(source, lvl, text, ts)
    this.writers.get(source)?.write(`${new Date(ts).toISOString()} ${lvl.toUpperCase()} ${text}`)
  }

  subscribe(listener: LogListener): () => void {
    return this.ring.subscribe(listener)
  }

  query(q?: LogQuery): LogLine[] {
    return this.ring.query(q)
  }

  /** Write errors of the file writers by source. Empty when all is well. */
  fileErrors(): Partial<Record<LogSource, string>> {
    const out: Partial<Record<LogSource, string>> = {}
    for (const [s, w] of this.writers) if (w.lastError) out[s] = w.lastError
    return out
  }

  exportLogs(destination: string): Promise<ExportResult> {
    return exportLogs(this.o.dir, destination, { now: this.now })
  }

  close(): void {
    for (const w of this.writers.values()) w.close()
  }
}

/**
 * Bundles the server, launcher and updater logs (rotated files included,
 * oldest first) into one text file, redacted. The file is written atomically
 * with mode 0600.
 */
export async function exportLogs(dir: string, destination: string, opts: { now?: () => number } = {}): Promise<ExportResult> {
  const now = opts.now ?? Date.now
  const parts: string[] = [
    `LocalAI launcher log export`,
    `created: ${new Date(now()).toISOString()}`,
    `platform: ${process.platform} ${os.arch()}`,
    `secrets are redacted`,
    '',
  ]
  let files = 0
  for (const source of LOG_SOURCES) {
    parts.push(`===== ${source} =====`)
    const list = logFiles(dir, `${source}.log`)
    if (list.length === 0) parts.push('(no log)')
    for (const f of list) {
      files++
      let text = ''
      try {
        text = await fs.promises.readFile(f, 'utf8')
      } catch {
        parts.push(`(could not read ${path.basename(f)})`)
        continue
      }
      parts.push(`--- ${path.basename(f)} ---`)
      parts.push(text.split(/\r?\n/).map(redact).join('\n').replace(/\n+$/, ''))
    }
    parts.push('')
  }
  const data = parts.join('\n')
  const dest = path.resolve(destination)
  await fs.promises.mkdir(path.dirname(dest), { recursive: true })
  const tmp = path.join(path.dirname(dest), `.${path.basename(dest)}.${process.pid}.${crypto.randomBytes(6).toString('hex')}.tmp`)
  try {
    const fh = await fs.promises.open(tmp, 'wx', 0o600)
    try {
      await fh.writeFile(data, 'utf8')
      await fh.sync()
    } finally {
      await fh.close()
    }
    await fs.promises.rename(tmp, dest)
  } catch (e) {
    await fs.promises.rm(tmp, { force: true }).catch(() => undefined)
    throw e
  }
  return { path: dest, bytes: Buffer.byteLength(data), files }
}
