// Small questions about this computer that the start-failed and first-run
// screens ask: a free port, what listens on a port, and the hardware. No
// Electron import. Platform commands run as argv arrays (never a shell
// string), with fixed arguments; nothing in them comes from the renderer
// except a port number checked by the caller.
import { execFile } from 'node:child_process'
import net from 'node:net'
import path from 'node:path'
import { minimalEnv } from './platform'
import type { HardwareSummaryView, PortProcessView } from '../shared/types'

// ---- free port ----------------------------------------------------------------

/** How many ports findFreePort tries before it gives up. */
export const FREE_PORT_TRIES = 100

/** Binds the port on loopback and lets it go again. False when it is in use (or not allowed). */
export function canBindLoopback(port: number): Promise<boolean> {
  return new Promise((resolve) => {
    const srv = net.createServer()
    srv.unref()
    srv.once('error', () => resolve(false))
    srv.listen({ port, host: '127.0.0.1', exclusive: true }, () => srv.close(() => resolve(true)))
  })
}

/** The first port from `from` upward that can be bound on loopback, or null. */
export async function findFreePort(from: number, canBind: (port: number) => Promise<boolean> = canBindLoopback): Promise<number | null> {
  const last = Math.min(65535, from + FREE_PORT_TRIES - 1)
  for (let p = from; p <= last; p++) if (await canBind(p)) return p
  return null
}

// ---- what listens on a port ---------------------------------------------------

export type RunResult = { ok: true; stdout: string } | { ok: false; missing: boolean }
export type Run = (file: string, args: string[]) => Promise<RunResult>

/** Runs a fixed command with a short timeout and a small output limit. */
export const runCommand: Run = (file, args) =>
  new Promise((resolve) => {
    const env = process.platform === 'win32' ? minimalEnv() : { PATH: process.env.PATH ?? '/usr/bin:/bin:/usr/sbin:/sbin', LC_ALL: 'C' }
    execFile(file, args, { timeout: 3000, maxBuffer: 256 * 1024, windowsHide: true, env }, (err, stdout) => {
      if (err && (err as NodeJS.ErrnoException).code === 'ENOENT') return resolve({ ok: false, missing: true })
      // ss and lsof exit non-zero when nothing matches; the output still counts.
      if (err && !stdout) return resolve(typeof (err as { code?: unknown }).code === 'number' ? { ok: true, stdout: '' } : { ok: false, missing: false })
      resolve({ ok: true, stdout: String(stdout) })
    })
  })

/** A process name as shown: printable characters only, short. */
function cleanName(s: string): string {
  return s.replace(/[^\x20-\x7e]/g, '').trim().slice(0, 64)
}

const MAX_PROCESSES = 5

/**
 * Reads `ss -H -ltnp sport = :PORT`. Each line is a listening socket; the
 * users:(("name",pid=N,fd=M)) part is there only for processes of this user.
 */
export function parseSs(out: string): PortProcessView {
  const lines = out.split('\n').filter((l) => l.trim() !== '')
  if (lines.length === 0) return { status: 'none' }
  const seen = new Map<number, string>()
  for (const l of lines) {
    for (const m of l.matchAll(/\("((?:[^"\\]|\\.){1,200})",pid=(\d{1,10}),/g)) {
      const pid = Number(m[2])
      if (!seen.has(pid)) seen.set(pid, cleanName(m[1]))
    }
  }
  if (seen.size === 0) return { status: 'hidden' }
  return { status: 'found', processes: [...seen].slice(0, MAX_PROCESSES).map(([pid, name]) => ({ pid, name })) }
}

/** Reads `lsof -nP -iTCP:PORT -sTCP:LISTEN -Fpc`: lines `p<pid>` then `c<command>`. */
export function parseLsof(out: string): PortProcessView {
  const seen = new Map<number, string>()
  let pid: number | undefined
  for (const l of out.split('\n')) {
    if (/^p\d{1,10}$/.test(l)) pid = Number(l.slice(1))
    else if (l.startsWith('c') && pid !== undefined && !seen.has(pid)) seen.set(pid, cleanName(l.slice(1)))
  }
  if (seen.size === 0) return { status: 'none' }
  return { status: 'found', processes: [...seen].slice(0, MAX_PROCESSES).map(([p, name]) => ({ pid: p, name })) }
}

/**
 * Reads `netstat -ano`: the pids of the TCP sockets that listen on `port`.
 * The state column is translated on Windows in other languages, so a
 * listening socket is found by its foreign address, which is `*:0`
 * (0.0.0.0:0 or [::]:0) only while it listens.
 */
export function parseNetstatPids(out: string, port: number): number[] {
  const pids: number[] = []
  for (const l of out.split(/\r?\n/)) {
    const f = l.trim().split(/\s+/)
    if (f.length < 5 || f[0].toUpperCase() !== 'TCP') continue
    const local = f[1]
    const foreign = f[2]
    if (!/^(?:0\.0\.0\.0|\[::\]|\*):0$/.test(foreign)) continue
    if (local.slice(local.lastIndexOf(':') + 1) !== String(port)) continue
    const pid = Number(f[f.length - 1])
    if (Number.isInteger(pid) && pid > 0 && !pids.includes(pid)) pids.push(pid)
  }
  return pids
}

/** Reads `tasklist /FO CSV /NH`: the image name of the first row, or undefined. */
export function parseTasklistName(out: string): string | undefined {
  for (const l of out.split(/\r?\n/)) {
    const m = /^"((?:[^"]|"")*)","(\d+)"/.exec(l.trim())
    if (m) return cleanName(m[1].replace(/""/g, '"'))
  }
  return undefined
}

/** A program in the Windows system folder, never one from PATH. */
export function system32(file: string, env: NodeJS.ProcessEnv = process.env): string {
  return path.win32.join(env.SystemRoot || env.SYSTEMROOT || 'C:\\Windows', 'System32', file)
}

async function windowsPortProcess(port: number, run: Run): Promise<PortProcessView> {
  const r = await run(system32('netstat.exe'), ['-ano'])
  if (!r.ok) return { status: 'unavailable' }
  const pids = parseNetstatPids(r.stdout, port).slice(0, MAX_PROCESSES)
  if (pids.length === 0) return { status: 'none' }
  const processes: { pid: number; name: string }[] = []
  for (const pid of pids) {
    const t = await run(system32('tasklist.exe'), ['/FI', `PID eq ${pid}`, '/FO', 'CSV', '/NH'])
    processes.push({ pid, name: (t.ok && parseTasklistName(t.stdout)) || '' })
  }
  // No name at all: the processes ended meanwhile, or tasklist could not read them.
  if (processes.every((p) => p.name === '')) return { status: 'hidden' }
  return { status: 'found', processes }
}

/**
 * Best effort: which process listens on `port`. Only the process name and
 * pid are returned, never the raw output. A stable `unavailable` when the
 * platform has no tool for it or the tool is missing.
 */
export async function portProcess(port: number, platform: NodeJS.Platform, run: Run = runCommand): Promise<PortProcessView> {
  if (platform === 'linux') {
    const r = await run('ss', ['-H', '-ltnp', 'sport', '=', `:${port}`])
    if (r.ok) return parseSs(r.stdout)
    if (!r.missing) return { status: 'unavailable' }
  }
  if (platform === 'linux' || platform === 'darwin') {
    const r = await run('lsof', ['-nP', `-iTCP:${port}`, '-sTCP:LISTEN', '-Fpc'])
    if (r.ok) return parseLsof(r.stdout)
  }
  if (platform === 'win32') return windowsPortProcess(port, run)
  return { status: 'unavailable' }
}

// ---- hardware -------------------------------------------------------------------

export interface HardwareFacts {
  platform: NodeJS.Platform
  arch: string
  cpus: { model: string }[]
  totalMem: number
  /** Whether a path exists; only fixed paths are asked. */
  exists(p: string): boolean
  /** For SystemRoot on Windows. Default: process.env. */
  env?: NodeJS.ProcessEnv
}

/** CPU, memory and a GPU hint that costs one file check. No commands, no network. */
export function hardwareSummary(f: HardwareFacts): HardwareSummaryView {
  let gpu: HardwareSummaryView['gpu']
  if (f.platform === 'darwin' && f.arch === 'arm64') gpu = 'apple'
  else if (f.platform === 'linux') {
    if (f.exists('/proc/driver/nvidia/version')) gpu = 'nvidia'
    else if (f.exists('/dev/kfd')) gpu = 'amd'
  } else if (f.platform === 'win32') {
    // Files that the NVIDIA and the AMD display drivers put in the system folder.
    if (f.exists(system32('nvapi64.dll', f.env))) gpu = 'nvidia'
    else if (f.exists(system32('atiadlxx.dll', f.env))) gpu = 'amd'
  }
  const model = cleanName((f.cpus[0]?.model ?? '').replace(/\s+/g, ' '))
  return { cpuModel: model || undefined, cpuCount: f.cpus.length, memoryBytes: f.totalMem, gpu }
}
