// Small questions about this computer that the start-failed and first-run
// screens ask: a free port, what listens on a port, and the hardware. No
// Electron import. Platform commands run as argv arrays (never a shell
// string), with fixed arguments; nothing in them comes from the renderer
// except a port number checked by the caller.
import { execFile } from 'node:child_process'
import net from 'node:net'
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
    execFile(file, args, { timeout: 3000, maxBuffer: 64 * 1024, windowsHide: true, env: { PATH: process.env.PATH ?? '/usr/bin:/bin:/usr/sbin:/sbin', LC_ALL: 'C' } }, (err, stdout) => {
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
}

/** CPU, memory and a GPU hint that costs one file check. No commands, no network. */
export function hardwareSummary(f: HardwareFacts): HardwareSummaryView {
  let gpu: HardwareSummaryView['gpu']
  if (f.platform === 'darwin' && f.arch === 'arm64') gpu = 'apple'
  else if (f.platform === 'linux') {
    if (f.exists('/proc/driver/nvidia/version')) gpu = 'nvidia'
    else if (f.exists('/dev/kfd')) gpu = 'amd'
  }
  const model = cleanName((f.cpus[0]?.model ?? '').replace(/\s+/g, ' '))
  return { cpuModel: model || undefined, cpuCount: f.cpus.length, memoryBytes: f.totalMem, gpu }
}
