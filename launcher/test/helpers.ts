import net from 'node:net'
import path from 'node:path'
import fs from 'node:fs'
import os from 'node:os'
import { execFileSync } from 'node:child_process'

export function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const s = net.createServer()
    s.listen(0, '127.0.0.1', () => {
      const p = (s.address() as net.AddressInfo).port
      s.close(() => resolve(p))
    })
    s.on('error', reject)
  })
}

export const FAKE_SERVER = path.resolve(__dirname, 'fixtures/fake-server.cjs')

export function tmpDir(prefix = 'launcher-test-'): string {
  return fs.mkdtempSync(path.join(process.env.LAUNCHER_TEST_TMP ?? os.tmpdir(), prefix))
}

export function pidAlive(pid: number): boolean {
  try {
    process.kill(pid, 0)
    return true
  } catch (e) {
    return (e as NodeJS.ErrnoException).code === 'EPERM'
  }
}

/** True when the pid is gone or only a zombie awaiting reaping. */
export function pidDead(pid: number): boolean {
  if (!pidAlive(pid)) return true
  try {
    if (process.platform === 'linux') {
      const stat = fs.readFileSync(`/proc/${pid}/stat`, 'utf8')
      return stat.slice(stat.lastIndexOf(')') + 2, stat.lastIndexOf(')') + 3) === 'Z'
    }
    // No /proc on macOS: ask ps. It exits non-zero when the pid is gone.
    const state = execFileSync('ps', ['-o', 'stat=', '-p', String(pid)], { encoding: 'utf8' }).trim()
    return state === '' || state.startsWith('Z')
  } catch {
    return true
  }
}

/** The command line of a process, as the system shows it to every user. */
export function commandLine(pid: number): string {
  if (process.platform === 'linux') return fs.readFileSync(`/proc/${pid}/cmdline`, 'utf8')
  return execFileSync('ps', ['-ww', '-o', 'args=', '-p', String(pid)], { encoding: 'utf8' })
}
