import { afterEach, describe, expect, it } from 'vitest'
import http from 'node:http'
import type { AddressInfo } from 'node:net'
import { probeRegistry, probeUrl, type ProbeFetch } from '../src/main/registryProbe'

const SECRET = 'probe-secret-value-0001'
const servers: http.Server[] = []

async function serve(handler: http.RequestListener): Promise<{ port: number; seen: http.IncomingMessage[] }> {
  const seen: http.IncomingMessage[] = []
  const s = http.createServer((req, res) => {
    seen.push(req)
    handler(req, res)
  })
  servers.push(s)
  await new Promise<void>((r) => s.listen(0, '127.0.0.1', r))
  return { port: (s.address() as AddressInfo).port, seen }
}

afterEach(async () => {
  await Promise.all(servers.splice(0).map((s) => new Promise((r) => s.close(r))))
})

describe('probeUrl', () => {
  it('uses host and port of the match and ignores the path', () => {
    expect(probeUrl('registry.example')).toBe('https://registry.example/v2/')
    expect(probeUrl('registry.example:5000/team/app')).toBe('https://registry.example:5000/v2/')
    expect(probeUrl('https://registry.example/team')).toBe('https://registry.example/v2/')
    expect(probeUrl('http://registry.example:5000/x')).toBe('http://registry.example:5000/v2/')
  })
  it('refuses a match that is not a host', () => {
    expect(probeUrl('')).toBeUndefined()
    expect(probeUrl('http://')).toBeUndefined()
    expect(probeUrl('user@registry.example')).toBeUndefined()
  })
})

describe('probeRegistry', () => {
  it('sends Basic credentials and reports ok on 200', async () => {
    const { port, seen } = await serve((_q, r) => r.writeHead(200).end('{}'))
    const out = await probeRegistry({ id: 'a', match: `http://127.0.0.1:${port}/ignored/path`, kind: 'basic', username: 'user' }, SECRET)
    expect(out).toEqual({ result: 'ok', status: 200 })
    expect(seen[0].url).toBe('/v2/')
    expect(seen[0].headers.authorization).toBe(`Basic ${Buffer.from(`user:${SECRET}`).toString('base64')}`)
  })

  it('sends a Bearer secret', async () => {
    const { port, seen } = await serve((_q, r) => r.writeHead(200).end())
    await probeRegistry({ id: 'a', match: `http://127.0.0.1:${port}`, kind: 'bearer' }, SECRET)
    expect(seen[0].headers.authorization).toBe(`Bearer ${SECRET}`)
  })

  it('sends no Authorization header without a secret', async () => {
    const { port, seen } = await serve((_q, r) => r.writeHead(401).end())
    const out = await probeRegistry({ id: 'a', match: `http://127.0.0.1:${port}`, kind: 'bearer' }, undefined)
    expect(out).toEqual({ result: 'unauthorized', status: 401 })
    expect(seen[0].headers.authorization).toBeUndefined()
  })

  it('reports unauthorized on 401 and other on an unexpected status', async () => {
    const a = await serve((_q, r) => r.writeHead(401).end())
    expect(await probeRegistry({ id: 'a', match: `http://127.0.0.1:${a.port}`, kind: 'bearer' }, SECRET)).toEqual({ result: 'unauthorized', status: 401 })
    const b = await serve((_q, r) => r.writeHead(404).end())
    expect(await probeRegistry({ id: 'a', match: `http://127.0.0.1:${b.port}`, kind: 'bearer' }, SECRET)).toEqual({ result: 'other', status: 404 })
  })

  it('does not follow a redirect to another origin', async () => {
    const target = await serve((_q, r) => r.writeHead(200).end())
    const { port } = await serve((_q, r) => r.writeHead(302, { Location: `http://127.0.0.1:${target.port}/v2/` }).end())
    const out = await probeRegistry({ id: 'a', match: `http://127.0.0.1:${port}`, kind: 'bearer' }, SECRET)
    expect(out).toEqual({ result: 'other', status: 302 })
    expect(target.seen).toHaveLength(0)
  })

  it('follows a redirect on the same origin', async () => {
    const { port, seen } = await serve((q, r) => (q.url === '/v2/' ? r.writeHead(301, { Location: '/v2/ok' }).end() : r.writeHead(200).end()))
    const out = await probeRegistry({ id: 'a', match: `http://127.0.0.1:${port}`, kind: 'bearer' }, SECRET)
    expect(out).toEqual({ result: 'ok', status: 200 })
    expect(seen[1].headers.authorization).toBe(`Bearer ${SECRET}`)
  })

  it('reports unreachable when nothing listens', async () => {
    const s = http.createServer()
    await new Promise<void>((r) => s.listen(0, '127.0.0.1', r))
    const port = (s.address() as AddressInfo).port
    await new Promise((r) => s.close(r))
    expect(await probeRegistry({ id: 'a', match: `http://127.0.0.1:${port}`, kind: 'bearer' }, SECRET)).toEqual({ result: 'unreachable' })
  })

  it('probes plain http only when the match says http://', async () => {
    const seen: string[] = []
    const f: ProbeFetch = async (url) => {
      seen.push(url)
      return { status: 200 }
    }
    await probeRegistry({ id: 'a', match: 'registry.example:5000', kind: 'bearer' }, SECRET, f)
    await probeRegistry({ id: 'a', match: 'https://registry.example', kind: 'bearer' }, SECRET, f)
    await probeRegistry({ id: 'a', match: 'http://registry.example', kind: 'bearer' }, SECRET, f)
    expect(seen).toEqual(['https://registry.example:5000/v2/', 'https://registry.example/v2/', 'http://registry.example/v2/'])
  })

  it('maps a certificate error to tls and never skips the check', async () => {
    const f: ProbeFetch = async () => {
      throw Object.assign(new Error(`bad cert for ${SECRET}`), { code: 'DEPTH_ZERO_SELF_SIGNED_CERT' })
    }
    expect(await probeRegistry({ id: 'a', match: 'registry.example', kind: 'bearer' }, SECRET, f)).toEqual({ result: 'tls' })
  })

  it('never surfaces an error whose message holds the secret', async () => {
    const f: ProbeFetch = async (_u, h) => {
      throw new Error(`failed with ${SECRET} and ${h.Authorization}`)
    }
    const out = await probeRegistry({ id: 'a', match: 'registry.example', kind: 'basic', username: 'u' }, SECRET, f)
    expect(JSON.stringify(out)).not.toContain(SECRET)
    expect(JSON.stringify(out)).not.toContain('Basic')
    expect(out.result).toBe('unreachable')
  })

  it('refuses a match with no host without a request', async () => {
    const f: ProbeFetch = async () => {
      throw new Error('must not be called')
    }
    expect(await probeRegistry({ id: 'a', match: 'http://', kind: 'bearer' }, SECRET, f)).toEqual({ result: 'other' })
  })
})
