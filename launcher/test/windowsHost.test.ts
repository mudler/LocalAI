// Runs only on a Windows host (the Windows job of the launcher build
// workflow): the real taskkill stop of a process tree and the real netstat
// and tasklist lookup. Skipped everywhere else; windows.test.ts covers the
// same logic with fakes on every host.
import fs from 'node:fs'
import net from 'node:net'
import path from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { portProcess } from '../src/main/hostTools'
import { pidAlive, ServerSupervisor } from '../src/main/supervisor'
import { FAKE_SERVER, freePort, tmpDir } from './helpers'

const onWindows = process.platform === 'win32'

describe.runIf(onWindows)('on a Windows host', () => {
  const live: ServerSupervisor[] = []
  afterEach(async () => {
    for (const s of live.splice(0)) await s.stop().catch(() => undefined)
  })

  it('stops the server and the process it started with taskkill /T /F', async () => {
    const port = await freePort()
    const pidsFile = path.join(tmpDir(), 'pids.json')
    const sup = new ServerSupervisor({
      command: process.execPath,
      args: [FAKE_SERVER],
      env: { ...(process.env as Record<string, string>), FAKE_PORT: String(port), FAKE_PIDS_FILE: pidsFile },
      healthUrl: `http://127.0.0.1:${port}/readyz`,
      pollIntervalMs: 50,
      killWaitMs: 10_000,
    })
    live.push(sup)
    await sup.start()
    expect(sup.state).toBe('ready')
    const { server, grandchild } = JSON.parse(fs.readFileSync(pidsFile, 'utf8')) as { server: number; grandchild: number }
    expect(pidAlive(grandchild)).toBe(true)
    await sup.stop()
    expect(sup.state).toBe('stopped')
    expect(pidAlive(server)).toBe(false)
    for (let i = 0; i < 50 && pidAlive(grandchild); i++) await new Promise((r) => setTimeout(r, 100))
    expect(pidAlive(grandchild)).toBe(false)
  })

  it('names the process that listens on a port', async () => {
    const srv = net.createServer()
    await new Promise<void>((r) => srv.listen(0, '127.0.0.1', () => r()))
    try {
      const port = (srv.address() as net.AddressInfo).port
      const view = await portProcess(port, 'win32')
      expect(view.status).toBe('found')
      if (view.status === 'found') expect(view.processes).toContainEqual({ pid: process.pid, name: path.basename(process.execPath) })
    } finally {
      srv.close()
    }
  })
})
