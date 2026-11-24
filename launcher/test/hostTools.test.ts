import net from 'node:net'
import { describe, expect, it } from 'vitest'
import { canBindLoopback, findFreePort, FREE_PORT_TRIES, hardwareSummary, parseLsof, parseSs, portProcess, type Run } from '../src/main/hostTools'

describe('findFreePort', () => {
  it('skips ports in use and returns the next free one', async () => {
    const busy = new Set([8081, 8082])
    expect(await findFreePort(8081, async (p) => !busy.has(p))).toBe(8083)
  })

  it('gives up after a bounded number of tries and never passes 65535', async () => {
    const tried: number[] = []
    expect(await findFreePort(9000, async (p) => (tried.push(p), false))).toBeNull()
    expect(tried).toHaveLength(FREE_PORT_TRIES)
    tried.length = 0
    expect(await findFreePort(65530, async (p) => (tried.push(p), false))).toBeNull()
    expect(tried.at(-1)).toBe(65535)
  })

  it('tests a real bind on loopback', async () => {
    const srv = net.createServer()
    await new Promise<void>((r) => srv.listen({ port: 0, host: '127.0.0.1' }, () => r()))
    const port = (srv.address() as net.AddressInfo).port
    try {
      expect(await canBindLoopback(port)).toBe(false)
      const free = await findFreePort(port)
      expect(free).not.toBe(port)
      expect(free).toBeGreaterThan(port)
    } finally {
      srv.close()
    }
  })
})

describe('port process', () => {
  const SS = [
    'LISTEN 0      4096   127.0.0.1:8080 0.0.0.0:* users:(("local-ai",pid=48213,fd=7),("local-ai",pid=48213,fd=9))',
    'LISTEN 0      4096       [::1]:8080    [::]:* users:(("other\\u001b[31m",pid=7,fd=3))',
  ].join('\n')

  it('reads names and pids from ss, once per pid, and drops control characters', () => {
    expect(parseSs(SS)).toEqual({ status: 'found', processes: [{ pid: 48213, name: 'local-ai' }, { pid: 7, name: 'other\\u001b[31m' }] })
    expect(parseSs('LISTEN 0 1 127.0.0.1:8080 0.0.0.0:* users:(("x\u0007y",pid=5,fd=1))')).toEqual({ status: 'found', processes: [{ pid: 5, name: 'xy' }] })
  })

  it('tells a socket of another user from no socket at all', () => {
    expect(parseSs('LISTEN 0 4096 0.0.0.0:8080 0.0.0.0:*\n')).toEqual({ status: 'hidden' })
    expect(parseSs('')).toEqual({ status: 'none' })
  })

  it('reads lsof field output', () => {
    expect(parseLsof('p123\ncnode\nf3\np456\ncPython\n')).toEqual({ status: 'found', processes: [{ pid: 123, name: 'node' }, { pid: 456, name: 'Python' }] })
    expect(parseLsof('')).toEqual({ status: 'none' })
  })

  it('runs fixed argv arrays, falls back to lsof, and is stable when no tool exists', async () => {
    const calls: [string, string[]][] = []
    const none: Run = async (f, a) => (calls.push([f, a]), { ok: false, missing: true })
    expect(await portProcess(8080, 'linux', none)).toEqual({ status: 'unavailable' })
    expect(calls).toEqual([
      ['ss', ['-H', '-ltnp', 'sport', '=', ':8080']],
      ['lsof', ['-nP', '-iTCP:8080', '-sTCP:LISTEN', '-Fpc']],
    ])
    const lsofOnly: Run = async (f) => (f === 'lsof' ? { ok: true, stdout: 'p9\ncfoo\n' } : { ok: false, missing: true })
    expect(await portProcess(8080, 'darwin', lsofOnly)).toEqual({ status: 'found', processes: [{ pid: 9, name: 'foo' }] })
    expect(await portProcess(8080, 'win32', lsofOnly)).toEqual({ status: 'unavailable' })
    const broken: Run = async () => ({ ok: false, missing: false })
    expect(await portProcess(8080, 'linux', broken)).toEqual({ status: 'unavailable' })
  })
})

describe('hardwareSummary', () => {
  const base = { platform: 'linux' as const, arch: 'x64', cpus: Array(8).fill({ model: '  Example   CPU  ' }), totalMem: 16e9 }
  it('reports CPU, memory and a GPU hint from one file check', () => {
    expect(hardwareSummary({ ...base, exists: (p) => p === '/proc/driver/nvidia/version' })).toEqual({ cpuModel: 'Example CPU', cpuCount: 8, memoryBytes: 16e9, gpu: 'nvidia' })
    expect(hardwareSummary({ ...base, exists: (p) => p === '/dev/kfd' }).gpu).toBe('amd')
    expect(hardwareSummary({ ...base, exists: () => false }).gpu).toBeUndefined()
    expect(hardwareSummary({ ...base, platform: 'darwin', arch: 'arm64', exists: () => false }).gpu).toBe('apple')
    expect(hardwareSummary({ ...base, cpus: [], exists: () => false })).toEqual({ cpuModel: undefined, cpuCount: 0, memoryBytes: 16e9, gpu: undefined })
  })
})
