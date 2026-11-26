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

import { backoffMs, parseForwardState, parsePairJson, parseRoutesLine, type P2PTarget, type PairOutcome, type TunnelStatus } from '../core/p2pInvite'

export {
  INVITE_PREFIX,
  backoffMs,
  linkRoutes,
  parseForwardState,
  parsePairJson,
  readInvite,
  type InviteInfo,
  type InviteProblem,
  type P2PRoutes,
  type P2PTarget,
  type PairFail,
  type PairOutcome,
  type PairResult,
  type TunnelState,
  type TunnelStatus,
} from '../core/p2pInvite'

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
): Promise<PairOutcome> {
  const args = ['p2p', 'pair', '--invite', input.link, '--identity', input.identity, '--name', input.name, '--json']
  if (input.allowOpenRelay) args.push('--allow-open-relay')
  const out = await run(binary, args, 150_000)
  if (out.code !== 0) {
    const lines = out.stderr.trim().split('\n').filter((l) => l.trim() !== '')
    return { ok: false, reason: out.code === null ? 'no-binary' : 'pair-failed', detail: lines[lines.length - 1] ?? '' }
  }
  const result = parsePairJson(out.stdout)
  if (!result) return { ok: false, reason: 'pair-failed', detail: 'unexpected answer from local-ai p2p pair' }
  return { ok: true, result }
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

/** Spawns a process. Injected in tests. */
export type SpawnFn = (cmd: string, args: string[]) => ChildProcess

const defaultSpawn: SpawnFn = (cmd, args) => spawn(cmd, args, { stdio: ['ignore', 'ignore', 'pipe'] })

/**
 * Keeps one `local-ai p2p forward` running for a saved server. It reads the
 * JSON status lines the command prints on stderr and emits 'status', and
 * 'routes' with the routes the server gave while connected. When the
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
    for (const x of this.target.hints ?? []) a.push('--hint', x)
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
    // The routes the server gives now: the owner of the saved server keeps them.
    const routes = parseRoutesLine(line)
    if (routes) {
      this.emit('routes', routes)
      return
    }
    const s = parseForwardState(line)
    if (!s) return
    if (s.state === 'ready') this.restarts = 0
    this.set(s)
  }
}
