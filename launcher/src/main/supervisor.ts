// Starts, watches and stops the LocalAI server. No Electron import.
//
// - The child runs in its own process group (detached), so a stop reaches
//   every process the server started, not only the direct child.
// - Ready means the health URL answers 2xx. The log is never parsed.
// - Stop sends SIGTERM to the group, waits a bounded time, then SIGKILL, and
//   reports `stopped` only once the group has no member left.
import { spawn, type ChildProcess } from 'node:child_process'
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

export class ServerSupervisor extends EventEmitter {
  private child?: ChildProcess
  private pgid?: number
  private _state: ServerState = 'stopped'
  private detail?: string
  private log: string[] = []
  private stopping = false
  private generation = 0

  constructor(private readonly opts: SupervisorOptions) {
    super()
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

    let child: ChildProcess
    try {
      child = spawn(this.opts.command, this.opts.args ?? [], {
        env: this.opts.env,
        cwd: this.opts.cwd,
        detached: true, // new session and process group: pgid === pid
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
      if (gen !== this.generation) return
      if (this.stopping) return // stop() owns the state change
      this.setState('failed', `server exited unexpectedly (code ${code ?? 'none'}, signal ${signal ?? 'none'})`)
      // Leftover grandchildren of a dead server are still ours to clean up.
      if (this.pgid && groupAlive(this.pgid)) void this.killGroup()
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

  private async killGroup(): Promise<boolean> {
    const pgid = this.pgid
    if (!pgid || !groupAlive(pgid)) return true
    const grace = this.opts.stopGraceMs ?? 10_000
    const send = (sig: NodeJS.Signals) => {
      try {
        process.kill(-pgid, sig)
      } catch {
        /* group already gone */
      }
    }
    send('SIGTERM')
    if (await this.waitGone(pgid, grace)) return true
    send('SIGKILL')
    return this.waitGone(pgid, this.opts.killWaitMs ?? 3000)
  }

  private async waitGone(pgid: number, ms: number): Promise<boolean> {
    const end = Date.now() + ms
    while (Date.now() < end) {
      if (!groupAlive(pgid)) return true
      await sleep(25)
    }
    return !groupAlive(pgid)
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
