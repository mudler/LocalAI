import fs from 'node:fs'
import path from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { buildChildEnv } from '../src/main/childEnv'
import { groupAlive, ServerSupervisor } from '../src/main/supervisor'
import { FAKE_SERVER, freePort, pidDead, tmpDir } from './helpers'

const live: ServerSupervisor[] = []
afterEach(async () => {
  for (const s of live.splice(0)) await s.stop().catch(() => undefined)
})

async function make(extra: Record<string, string> = {}, opts: Partial<ConstructorParameters<typeof ServerSupervisor>[0]> = {}) {
  const port = await freePort()
  const dir = tmpDir()
  const pidsFile = path.join(dir, 'pids.json')
  const sup = new ServerSupervisor({
    command: process.execPath,
    args: [FAKE_SERVER],
    env: buildChildEnv(process.env, { FAKE_PORT: String(port), FAKE_PIDS_FILE: pidsFile, ...extra }),
    healthUrl: `http://127.0.0.1:${port}/readyz`,
    pollIntervalMs: 50,
    stopGraceMs: 600,
    killWaitMs: 3000,
    ...opts,
  })
  live.push(sup)
  const pids = () => JSON.parse(fs.readFileSync(pidsFile, 'utf8')) as { server: number; grandchild: number }
  return { sup, pids, port }
}

describe('ServerSupervisor', () => {
  it('goes starting -> ready by polling the health URL, not the log', async () => {
    const { sup } = await make({ FAKE_READY_DELAY_MS: '400' })
    const seen: string[] = []
    sup.on('state', (s) => seen.push(s.state))
    await sup.start()
    expect(sup.state).toBe('ready')
    expect(seen).toEqual(['starting', 'ready'])
  })

  it('stops the whole process group on SIGTERM and reports stopped only when it is gone', async () => {
    const { sup, pids } = await make()
    await sup.start()
    const { server, grandchild } = pids()
    expect(pidDead(grandchild)).toBe(false)
    await sup.stop()
    expect(sup.state).toBe('stopped')
    expect(groupAlive(server)).toBe(false)
    expect(pidDead(grandchild)).toBe(true)
  })

  it('escalates to SIGKILL when the group ignores SIGTERM', async () => {
    const { sup, pids } = await make({ FAKE_IGNORE_TERM: '1' })
    await sup.start()
    const { server, grandchild } = pids()
    const t0 = Date.now()
    await sup.stop()
    expect(Date.now() - t0).toBeGreaterThanOrEqual(550) // waited for the grace period
    expect(sup.state).toBe('stopped')
    expect(groupAlive(server)).toBe(false)
    expect(pidDead(grandchild)).toBe(true)
  })

  it('fails when the server dies by itself and cleans up the grandchild', async () => {
    const { sup, pids } = await make({ FAKE_EXIT_AFTER_MS: '300' })
    await sup.start()
    await new Promise((r) => setTimeout(r, 1200))
    expect(sup.state).toBe('failed')
    expect(sup.snapshot().detail).toMatch(/exited unexpectedly/)
    expect(pidDead(pids().grandchild)).toBe(true)
  })

  it('fails and kills the group when readiness times out', async () => {
    const { sup, pids } = await make({ FAKE_READY_DELAY_MS: '60000' }, { readyTimeoutMs: 600 })
    await sup.start()
    expect(sup.state).toBe('failed')
    expect(sup.snapshot().detail).toMatch(/did not become ready/)
    expect(pidDead(pids().grandchild)).toBe(true)
  })

  it('fails with a clear message when the binary cannot be started', async () => {
    const sup = new ServerSupervisor({ command: '/nonexistent/local-ai', env: {}, healthUrl: 'http://127.0.0.1:1/readyz', pollIntervalMs: 20 })
    await sup.start()
    expect(sup.state).toBe('failed')
    expect(sup.snapshot().detail).toMatch(/could not start/)
  })

  it('keeps a tail of the server log', async () => {
    const { sup } = await make()
    await sup.start()
    expect(sup.snapshot().logTail.join('\n')).toContain('fake-server: listening')
  })
})

describe('buildChildEnv', () => {
  it('strips Electron and AppImage variables and keeps the rest', () => {
    const env = buildChildEnv({
      PATH: '/usr/bin',
      HOME: '/home/x',
      ELECTRON_RUN_AS_NODE: '1',
      ELECTRON_ENABLE_LOGGING: '1',
      APPDIR: '/tmp/.mount_x',
      APPIMAGE: '/opt/LocalAI-Launcher-x86_64.AppImage',
      ARGV0: 'x',
      LD_LIBRARY_PATH: '/tmp/.mount_x/usr/lib',
      APPIMAGE_EXTRACT_AND_RUN: '1',
      NODE_OPTIONS: '--inspect',
    })
    expect(env).toEqual({ PATH: '/usr/bin', HOME: '/home/x' })
  })

  it('applies explicit extra variables on top', () => {
    expect(buildChildEnv({ A: '1' }, { A: '2', B: '3' })).toEqual({ A: '2', B: '3' })
  })

  it('never lets a user variable replace a stripped or launcher-controlled one', () => {
    const env = buildChildEnv(
      { PATH: '/usr/bin', API_KEY: 'from-the-desktop', LOCALAI_API_KEY: 'inherited', ADDRESS: 'x' },
      { PATH: '/evil', LD_LIBRARY_PATH: '/evil', ELECTRON_RUN_AS_NODE: '1', LOCALAI_API_KEY: 'user', 'BAD NAME': '1', OK: '1' },
      { LOCALAI_API_KEY: 'launcher' },
    )
    expect(env).toEqual({ PATH: '/usr/bin', OK: '1', LOCALAI_API_KEY: 'launcher' })
  })
})
