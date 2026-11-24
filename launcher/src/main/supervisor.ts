// Starts, watches and stops the LocalAI server. No Electron import.
//
// - Linux and macOS: the child runs in its own process group (detached), so a
//   stop reaches every process the server started, not only the direct child.
//   Stop sends SIGTERM to the group, waits a bounded time, then SIGKILL, and
//   reports `stopped` only once the group has no member left.
// - Windows: there are no process groups and no SIGTERM. A console program
//   that has no window cannot be asked to close from another process, and the
//   server has no shutdown endpoint, so the stop is forced at once:
//   `taskkill /PID <pid> /T /F` ends the server and every process it started.
//   The server also keeps its backends in a Job Object that ends them when the
//   server ends, so a backend never outlives it. The child gets no console
//   window (windowsHide).
// - Ready means the health URL answers 2xx. The log is never parsed.
import { execFile, spawn, type ChildProcess } from 'node:child_process'
import path from 'node:path'
import { EventEmitter } from 'node:events'
import http from 'node:http'
import type { ServerState } from '../shared/types'

export interface SupervisorOptions {
  command: string
  args?: string[]
  env: Record<string, string>
  cwd?: string
  healthUrl: string
  readyTimeoutMs?: number
  pollIntervalMs?: number
  /** Time between SIGTERM and SIGKILL. */
  stopGraceMs?: number
  /** Time to wait for the group to disappear after SIGKILL. */
  killWaitMs?: number
  logLines?: number
  /** Default: process.platform. Selects how the process tree is started and stopped. */
  platform?: NodeJS.Platform
  /** Test hook: replaces the process control of the platform. */
  control?: ProcessControl
}

/**
 * How the server's process tree is started and stopped on one platform.
 * `pid` is the server's pid (on Linux and macOS also its process group id).
 */
export interface ProcessControl {
  /** Extra spawn options. */
  spawn: { detached: boolean; windowsHide: boolean }
  /** True while a process of the tree may still exist. `exited` is true once the direct child has exited. */
  alive(pid: number, exited: boolean): boolean
  /** Asks the tree to end. Undefined when the platform has no polite way: the stop goes to `kill` at once. */
  terminate?(pid: number): Promise<void>
  /** Ends the tree by force. */
  kill(pid: number): Promise<void>
}

export interface SupervisorSnapshot {
  state: ServerState
  detail?: string
  logTail: string[]
}

export function probeHealth(url: string, timeoutMs = 1500): Promise<boolean> {
  return new Promise((resolve) => {
    const req = http.get(url, { timeout: timeoutMs }, (res) => {
      res.resume()
      resolve((res.statusCode ?? 0) >= 200 && (res.statusCode ?? 0) < 300)
    })
    req.on('timeout', () => {
      req.destroy()
      resolve(false)
    })
    req.on('error', () => resolve(false))
  })
}

/** True while any process of the group still exists (a zombie counts until reaped). */
export function groupAlive(pgid: number): boolean {
  try {
    process.kill(-pgid, 0)
    return true
  } catch (e) {
    return (e as NodeJS.ErrnoException).code === 'EPERM'
  }
}

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms))

function signalGroup(pgid: number, sig: NodeJS.Signals): void {
  try {
    process.kill(-pgid, sig)
  } catch {
    /* group already gone */
  }
}

/** Linux and macOS: a process group, SIGTERM, then SIGKILL. */
export const posixControl: ProcessControl = {
  spawn: { detached: true, windowsHide: false }, // new session and process group: pgid === pid
  alive: (pgid) => groupAlive(pgid),
  terminate: async (pgid) => signalGroup(pgid, 'SIGTERM'),
  kill: async (pgid) => signalGroup(pgid, 'SIGKILL'),
}

/** Runs taskkill. Resolves with false when it could not run or failed. */
export type Taskkill = (args: string[]) => Promise<boolean>

/** taskkill.exe from the Windows system folder, never from PATH. */
export function taskkillPath(env: NodeJS.ProcessEnv = process.env): string {
  const root = env.SystemRoot || env.SYSTEMROOT || 'C:\\Windows'
  return path.win32.join(root, 'System32', 'taskkill.exe')
}

export const runTaskkill: Taskkill = (args) =>
  new Promise((resolve) => {
    execFile(taskkillPath(), args, { timeout: 10_000, windowsHide: true }, (err) => resolve(!err))
  })

/** True while a process with this pid exists. */
export function pidAlive(pid: number): boolean {
  try {
    process.kill(pid, 0)
    return true
  } catch (e) {
    return (e as NodeJS.ErrnoException).code === 'EPERM'
  }
}

/**
 * Windows: no process group and no polite stop for a console program
 * without a window. `taskkill /T /F` ends the server and its child processes;
 * when taskkill cannot run, the server alone is ended (its Job Object then
 * ends the backends). The tree counts as gone once the direct child has
 * exited, so a reused pid is never mistaken for the server.
 */
export function windowsControl(taskkill: Taskkill = runTaskkill): ProcessControl {
  return {
    spawn: { detached: false, windowsHide: true },
    alive: (pid, exited) => !exited && pidAlive(pid),
    kill: async (pid) => {
      if (await taskkill(['/PID', String(pid), '/T', '/F'])) return
      try {
        process.kill(pid, 'SIGKILL')
      } catch {
        /* already gone */
      }
    },
  }
}

/** The process control for a platform. */
export function processControl(platform: NodeJS.Platform): ProcessControl {
  return platform === 'win32' ? windowsControl() : posixControl
}

export class ServerSupervisor extends EventEmitter {
  private child?: ChildProcess
  private pgid?: number
  private _state: ServerState = 'stopped'
  private detail?: string
  private log: string[] = []
  private stopping = false
  private generation = 0
  private exited = false
  private readonly control: ProcessControl

  constructor(private readonly opts: SupervisorOptions) {
    super()
    this.control = opts.control ?? processControl(opts.platform ?? process.platform)
  }

  get state(): ServerState {
    return this._state
  }

  snapshot(): SupervisorSnapshot {
    return { state: this._state, detail: this.detail, logTail: [...this.log] }
  }

  private setState(state: ServerState, detail?: string) {
    this._state = state
    this.detail = detail
    this.emit('state', this.snapshot())
  }

  private pushLog(chunk: string) {
    const max = this.opts.logLines ?? 500
    for (const line of chunk.split(/\r?\n/)) {
      if (line === '') continue
      this.log.push(line)
    }
    if (this.log.length > max) this.log.splice(0, this.log.length - max)
    this.emit('log', chunk)
  }

  async start(): Promise<void> {
    if (this._state === 'starting' || this._state === 'ready' || this._state === 'stopping') return
    this.log = []
    this.stopping = false
    const gen = ++this.generation
    this.setState('starting')
    this.exited = false

    let child: ChildProcess
    try {
      child = spawn(this.opts.command, this.opts.args ?? [], {
        env: this.opts.env,
        cwd: this.opts.cwd,
        ...this.control.spawn,
        stdio: ['ignore', 'pipe', 'pipe'],
      })
    } catch (e) {
      this.setState('failed', `could not start: ${(e as Error).message}`)
      return
    }
    this.child = child
    this.pgid = child.pid
    child.stdout?.setEncoding('utf8').on('data', (d: string) => this.pushLog(d))
    child.stderr?.setEncoding('utf8').on('data', (d: string) => this.pushLog(d))

    const spawnFailed = new Promise<void>((resolve) => {
      child.once('error', (e) => {
        if (gen !== this.generation) return resolve()
        this.setState('failed', `could not start: ${e.message}`)
        resolve()
      })
    })

    child.once('exit', (code, signal) => {
      if (child === this.child) this.exited = true
      if (gen !== this.generation) return
      if (this.stopping) return // stop() owns the state change
      this.setState('failed', `server exited unexpectedly (code ${code ?? 'none'}, signal ${signal ?? 'none'})`)
      // Leftover grandchildren of a dead server are still ours to clean up.
      if (this.pgid && this.alive(this.pgid)) void this.killGroup()
    })

    void spawnFailed
    await this.waitReady(gen)
  }

  private async waitReady(gen: number) {
    const deadline = Date.now() + (this.opts.readyTimeoutMs ?? 120_000)
    const every = this.opts.pollIntervalMs ?? 500
    while (gen === this.generation && this._state === 'starting') {
      if (await probeHealth(this.opts.healthUrl)) {
        if (gen === this.generation && this._state === 'starting') this.setState('ready')
        return
      }
      if (Date.now() > deadline) {
        // Mark the kill as ours first, or the exit handler would overwrite the reason.
        this.stopping = true
        this.setState('failed', 'the server did not become ready in time')
        await this.killGroup()
        return
      }
      await sleep(every)
    }
  }

  private alive(pgid: number): boolean {
    return this.control.alive(pgid, this.exited)
  }

  private async killGroup(): Promise<boolean> {
    const pgid = this.pgid
    if (!pgid || !this.alive(pgid)) return true
    if (this.control.terminate) {
      await this.control.terminate(pgid)
      if (await this.waitGone(pgid, this.opts.stopGraceMs ?? 10_000)) return true
    }
    await this.control.kill(pgid)
    return this.waitGone(pgid, this.opts.killWaitMs ?? 3000)
  }

  private async waitGone(pgid: number, ms: number): Promise<boolean> {
    const end = Date.now() + ms
    while (Date.now() < end) {
      if (!this.alive(pgid)) return true
      await sleep(25)
    }
    return !this.alive(pgid)
  }

  async stop(): Promise<void> {
    if (this._state === 'stopped' || this._state === 'stopping') return
    this.stopping = true
    this.generation++ // ends a pending readiness wait
    this.setState('stopping')
    const gone = await this.killGroup()
    if (gone) {
      this.child = undefined
      this.pgid = undefined
      this.setState('stopped')
    } else {
      this.setState('failed', 'the server did not stop; processes are still running')
    }
  }
}
