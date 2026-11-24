// Servers reached over P2P. No Electron import.
//
// The launcher does not speak P2P itself: it runs two commands of the
// installed `local-ai` binary.
//   - `local-ai p2p pair --invite <link> --identity <file> --json` redeems an
//     invite once. The server admits the peer ID of the identity file from
//     then on, and the command saves the pairing next to it.
//   - `local-ai p2p forward --peer <id> --listen 127.0.0.1:<port> --json`
//     keeps a loopback port whose connections go to the server. It re-dials by
//     itself; the supervisor below restarts it when it exits.
// The saved server is then an ordinary remote at http://127.0.0.1:<port>: the
// view, the cookies and the sign-in keep one origin, because the port is
// chosen once and kept.
import { spawn, type ChildProcess } from 'node:child_process'
import { EventEmitter } from 'node:events'
import net from 'node:net'

export const INVITE_PREFIX = 'localai://pair?'
const PEER_RE = /^[1-9A-HJ-NP-Za-km-z]{40,128}$/

/** What the launcher reads from an invite link before it uses it. */
export interface InviteInfo {
  kind: string
  peer: string
  label: string
  /** Milliseconds since the epoch. */
  expires: number
  /** The link names an open relay: anyone can use it. */
  openRelay: boolean
}

export type InviteProblem = 'invite-invalid' | 'invite-kind' | 'invite-expired'

/**
 * Reads an invite link. It checks only what the launcher must know before it
 * runs the pairing: the full check is the server's.
 */
export function readInvite(text: string, now: number): { ok: true; info: InviteInfo } | { ok: false; reason: InviteProblem } {
  const link = text.trim()
  if (!link.startsWith(INVITE_PREFIX) || link.length > 2048) return { ok: false, reason: 'invite-invalid' }
  const q = new URLSearchParams(link.slice(INVITE_PREFIX.length))
  const kind = q.get('k') ?? ''
  const peer = q.get('p') ?? ''
  const expires = Number(q.get('e') ?? '') * 1000
  if (q.get('v') !== '1' || !PEER_RE.test(peer) || !q.get('c') || !Number.isFinite(expires) || expires <= 0) {
    return { ok: false, reason: 'invite-invalid' }
  }
  // A device invite admits this computer, and the person signs in as on any
  // browser. A remote invite works too: its key is simply not used here.
  if (kind !== 'device' && kind !== 'remote') return { ok: false, reason: 'invite-kind' }
  if (expires <= now) return { ok: false, reason: 'invite-expired' }
  return { ok: true, info: { kind, peer, label: q.get('n') ?? '', expires, openRelay: (q.get('o') ?? '') !== '' } }
}

/** A saved server over P2P: how the forwarder reaches it. */
export interface P2PTarget {
  peer: string
  /** Loopback port of the forwarder; the origin of the server. */
  port: number
  addrs: string[]
  relays: string[]
}

/** The result of `local-ai p2p pair --json`. */
export interface PairResult {
  kind: string
  phrase: string
  serverName: string
  peer: string
}

export type PairFail = 'pair-failed' | 'no-binary'

/** Runs a command and collects its output. Injected in tests. */
export type RunCommand = (cmd: string, args: string[], timeoutMs: number) => Promise<{ code: number | null; stdout: string; stderr: string }>

export const runCommand: RunCommand = (cmd, args, timeoutMs) =>
  new Promise((resolve) => {
    let stdout = ''
    let stderr = ''
    let child: ChildProcess
    try {
      child = spawn(cmd, args, { stdio: ['ignore', 'pipe', 'pipe'] })
    } catch (e) {
      resolve({ code: null, stdout: '', stderr: String(e) })
      return
    }
    const timer = setTimeout(() => child.kill('SIGTERM'), timeoutMs)
    child.stdout?.on('data', (d: Buffer) => (stdout += d.toString('utf8')))
    child.stderr?.on('data', (d: Buffer) => (stderr += d.toString('utf8')))
    child.on('error', (e) => {
      clearTimeout(timer)
      resolve({ code: null, stdout, stderr: stderr + String(e) })
    })
    child.on('close', (code) => {
      clearTimeout(timer)
      resolve({ code, stdout, stderr })
    })
  })

/**
 * Redeems the invite with the identity file of this computer. On failure,
 * `detail` is the last line the command printed, which names the cause
 * (expired, used, unreachable) in plain words.
 */
export async function pair(
  run: RunCommand,
  binary: string,
  input: { link: string; identity: string; name: string; allowOpenRelay: boolean },
): Promise<{ ok: true; result: PairResult } | { ok: false; reason: PairFail; detail: string }> {
  const args = ['p2p', 'pair', '--invite', input.link, '--identity', input.identity, '--name', input.name, '--json']
  if (input.allowOpenRelay) args.push('--allow-open-relay')
  const out = await run(binary, args, 150_000)
  if (out.code !== 0) {
    const lines = out.stderr.trim().split('\n').filter((l) => l.trim() !== '')
    return { ok: false, reason: out.code === null ? 'no-binary' : 'pair-failed', detail: lines[lines.length - 1] ?? '' }
  }
  try {
    const j = JSON.parse(out.stdout.trim().split('\n').pop() ?? '') as {
      kind?: unknown
      phrase?: unknown
      server?: { name?: unknown; peer_id?: unknown }
    }
    const peer = typeof j.server?.peer_id === 'string' ? j.server.peer_id : ''
    if (typeof j.phrase !== 'string' || !PEER_RE.test(peer)) throw new Error('missing fields')
    return {
      ok: true,
      result: { kind: typeof j.kind === 'string' ? j.kind : '', phrase: j.phrase, serverName: typeof j.server?.name === 'string' ? j.server.name : '', peer },
    }
  } catch {
    return { ok: false, reason: 'pair-failed', detail: 'unexpected answer from local-ai p2p pair' }
  }
}

/** Reads the relays and addresses of a link, as the pair command saved them. */
export function linkRoutes(link: string): { addrs: string[]; relays: string[] } {
  const q = new URLSearchParams(link.trim().slice(INVITE_PREFIX.length))
  return { addrs: q.getAll('a').slice(0, 2), relays: q.getAll('r').slice(0, 3) }
}

/** A free loopback port, chosen by the system. */
export function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer()
    srv.unref()
    srv.on('error', reject)
    srv.listen(0, '127.0.0.1', () => {
      const a = srv.address()
      const port = typeof a === 'object' && a ? a.port : 0
      srv.close(() => (port ? resolve(port) : reject(new Error('no port'))))
    })
  })
}

export type TunnelState = 'starting' | 'ready' | 'up' | 'down' | 'stopped' | 'failed'

export interface TunnelStatus {
  state: TunnelState
  /** direct, hole_punched or relayed, after the first forwarded connection. */
  path?: string
  error?: string
}

/** Spawns a process. Injected in tests. */
export type SpawnFn = (cmd: string, args: string[]) => ChildProcess

const defaultSpawn: SpawnFn = (cmd, args) => spawn(cmd, args, { stdio: ['ignore', 'ignore', 'pipe'] })

/** Delay before the n-th restart (n from 0): 1 s doubling up to 60 s. */
export function backoffMs(n: number): number {
  return Math.min(60_000, 1000 * 2 ** Math.min(n, 6))
}

/**
 * Keeps one `local-ai p2p forward` running for a saved server. It reads the
 * JSON status lines the command prints on stderr and emits 'status'. When the
 * command exits it starts it again after a growing delay; a run that reached
 * "ready" resets the delay.
 */
export class TunnelSupervisor extends EventEmitter {
  private child?: ChildProcess
  private restarts = 0
  private timer?: NodeJS.Timeout
  private stopped = true
  private _status: TunnelStatus = { state: 'stopped' }
  private buf = ''

  constructor(
    private readonly binary: string,
    private readonly identity: string,
    private readonly target: P2PTarget,
    private readonly spawnFn: SpawnFn = defaultSpawn,
  ) {
    super()
  }

  get status(): TunnelStatus {
    return this._status
  }

  args(): string[] {
    const a = ['p2p', 'forward', '--peer', this.target.peer, '--listen', `127.0.0.1:${this.target.port}`, '--identity', this.identity, '--json']
    for (const r of this.target.relays) a.push('--relay', r)
    for (const x of this.target.addrs) a.push('--addr', x)
    return a
  }

  start(): void {
    if (!this.stopped) return
    this.stopped = false
    this.spawnOnce()
  }

  stop(): void {
    this.stopped = true
    if (this.timer) clearTimeout(this.timer)
    this.timer = undefined
    this.child?.kill('SIGTERM')
    this.child = undefined
    this.set({ state: 'stopped' })
  }

  private set(s: TunnelStatus): void {
    this._status = s
    this.emit('status', s)
  }

  private spawnOnce(): void {
    this.set({ state: 'starting' })
    this.buf = ''
    let child: ChildProcess
    try {
      child = this.spawnFn(this.binary, this.args())
    } catch (e) {
      this.set({ state: 'failed', error: String(e) })
      this.scheduleRestart()
      return
    }
    this.child = child
    child.stderr?.on('data', (d: Buffer) => this.onData(d.toString('utf8')))
    child.on('error', (e) => this.set({ state: 'failed', error: String(e) }))
    child.on('exit', () => {
      if (this.child !== child) return
      this.child = undefined
      if (this.stopped) return
      if (this._status.state !== 'failed') this.set({ state: 'failed', error: this._status.error ?? 'the forwarder exited' })
      this.scheduleRestart()
    })
  }

  private scheduleRestart(): void {
    if (this.stopped) return
    const wait = backoffMs(this.restarts++)
    this.timer = setTimeout(() => {
      this.timer = undefined
      if (!this.stopped) this.spawnOnce()
    }, wait)
  }

  private onData(chunk: string): void {
    this.buf += chunk
    let nl: number
    while ((nl = this.buf.indexOf('\n')) >= 0) {
      const line = this.buf.slice(0, nl).trim()
      this.buf = this.buf.slice(nl + 1)
      if (line.startsWith('{')) this.onLine(line)
    }
    if (this.buf.length > 64 * 1024) this.buf = ''
  }

  private onLine(line: string): void {
    let j: { state?: unknown; path?: unknown; error?: unknown }
    try {
      j = JSON.parse(line) as typeof j
    } catch {
      return
    }
    const path = typeof j.path === 'string' && j.path !== '' && j.path !== 'none' ? j.path : undefined
    const error = typeof j.error === 'string' && j.error !== '' ? j.error : undefined
    switch (j.state) {
      case 'ready':
      case 'listening':
        this.restarts = 0
        this.set({ state: 'ready' })
        break
      case 'up':
        this.set({ state: 'up', path })
        break
      case 'down':
        this.set({ state: 'down', error })
        break
    }
  }
}
