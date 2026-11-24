import { afterEach, beforeAll, describe, expect, it } from 'vitest'
import fs from 'node:fs'
import http from 'node:http'
import https from 'node:https'
import path from 'node:path'
import { execFileSync } from 'node:child_process'
import type { AddressInfo } from 'node:net'
import { createProbeRequest, probeRegistry, probeUrl, type ProbeFetch } from '../src/main/registryProbe'
import { tmpDir } from './helpers'

const SECRET = 'probe-secret-value-0001'
const servers: https.Server[] = []

const hasOpenssl = (() => {
  try {
    execFileSync('openssl', ['version'], { stdio: 'ignore' })
    return true
  } catch {
    return false
  }
})()

/** A self-signed certificate for 127.0.0.1, made once for this file. */
let cert: { key: string; cert: string }
beforeAll(() => {
  if (!hasOpenssl) return
  const dir = tmpDir('probe-tls-')
  const key = path.join(dir, 'k.pem')
  const crt = path.join(dir, 'c.pem')
  execFileSync('openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', key, '-out', crt, '-days', '5', '-subj', '/CN=localhost', '-addext', 'subjectAltName=DNS:localhost,IP:127.0.0.1'], { stdio: 'ignore' })
  cert = { key: fs.readFileSync(key, 'utf8'), cert: fs.readFileSync(crt, 'utf8') }
  fs.rmSync(dir, { recursive: true, force: true })
})

/** An https registry on 127.0.0.1 with the test certificate. */
async function serve(handler: http.RequestListener): Promise<{ port: number; seen: http.IncomingMessage[] }> {
  const seen: http.IncomingMessage[] = []
  const s = https.createServer(cert, (req, res) => {
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

/** The real request, trusting the test certificate only. */
const trusted = (): ProbeFetch => createProbeRequest({ ca: cert.cert })

describe('probeUrl', () => {
  it('uses host and port of the match over https and ignores the path', () => {
    expect(probeUrl('registry.example')).toBe('https://registry.example/v2/')
    expect(probeUrl('registry.example:5000/team/app')).toBe('https://registry.example:5000/v2/')
    expect(probeUrl('https://registry.example/team')).toBe('https://registry.example/v2/')
  })
  it('refuses a match that is not a host, and plain http', () => {
    expect(probeUrl('')).toBeUndefined()
    expect(probeUrl('https://')).toBeUndefined()
    expect(probeUrl('user@registry.example')).toBeUndefined()
    expect(probeUrl('http://registry.example:5000/x')).toBeUndefined()
  })
})

describe('probeRegistry with injected answers', () => {
  it('never probes plain http', async () => {
    const seen: string[] = []
    const f: ProbeFetch = async (url) => {
      seen.push(url)
      return { status: 200 }
    }
    await probeRegistry({ id: 'a', match: 'registry.example:5000', kind: 'bearer' }, SECRET, f)
    await probeRegistry({ id: 'a', match: 'https://registry.example', kind: 'bearer' }, SECRET, f)
    expect(await probeRegistry({ id: 'a', match: 'http://registry.example', kind: 'bearer' }, SECRET, f)).toEqual({ result: 'other' })
    expect(seen).toEqual(['https://registry.example:5000/v2/', 'https://registry.example/v2/'])
  })

  it('the default request refuses an http URL without a connection', async () => {
    await expect(createProbeRequest()('http://127.0.0.1:1/v2/', {}, new AbortController().signal)).rejects.toMatchObject({ code: 'ERR_INVALID_PROTOCOL' })
  })

  it('reports token for a 401 with a Bearer challenge, and unauthorized for Basic or none', async () => {
    const answer = (authenticate?: string): ProbeFetch => async () => ({ status: 401, ...(authenticate ? { authenticate } : {}) })
    const rule = { id: 'a', match: 'registry.example', kind: 'basic' as const, username: 'u' }
    expect(await probeRegistry(rule, SECRET, answer('Bearer realm="https://auth.registry.example/token",service="registry.example"'))).toEqual({ result: 'token', status: 401 })
    expect(await probeRegistry(rule, SECRET, answer('bearer realm="x"'))).toEqual({ result: 'token', status: 401 })
    expect(await probeRegistry(rule, SECRET, answer('Basic realm="registry"'))).toEqual({ result: 'unauthorized', status: 401 })
    expect(await probeRegistry(rule, SECRET, answer())).toEqual({ result: 'unauthorized', status: 401 })
    // A 403 is a refusal whatever the challenge says.
    expect(await probeRegistry(rule, SECRET, async () => ({ status: 403, authenticate: 'Bearer realm="x"' }))).toEqual({ result: 'unauthorized', status: 403 })
  })

  it('does not follow an https to http redirect on the same host, so the credential is not sent', async () => {
    const seen: string[] = []
    const f: ProbeFetch = async (url) => {
      seen.push(url)
      return { status: 301, location: 'http://registry.example/v2/' }
    }
    expect(await probeRegistry({ id: 'a', match: 'registry.example', kind: 'bearer' }, SECRET, f)).toEqual({ result: 'other', status: 301 })
    expect(seen).toEqual(['https://registry.example/v2/'])
  })

  it('reports unreachable when the connection is refused', async () => {
    const f: ProbeFetch = async () => {
      throw Object.assign(new Error('connect ECONNREFUSED'), { code: 'ECONNREFUSED' })
    }
    expect(await probeRegistry({ id: 'a', match: 'registry.example', kind: 'bearer' }, SECRET, f)).toEqual({ result: 'unreachable' })
  })

  it('has one deadline for the whole probe, not one per redirect', async () => {
    let hops = 0
    // Each hop answers in 60 ms, so a per-hop limit of 100 ms would never fire.
    const f: ProbeFetch = (_u, _h, signal) =>
      new Promise((resolve, reject) => {
        hops++
        const t = setTimeout(() => resolve({ status: 302, location: `/v2/${hops}` }), 60)
        signal.addEventListener('abort', () => {
          clearTimeout(t)
          reject(Object.assign(new Error('aborted'), { code: 'ABORT_ERR' }))
        })
      })
    const started = Date.now()
    expect(await probeRegistry({ id: 'a', match: 'registry.example', kind: 'bearer' }, SECRET, f, { timeoutMs: 100 })).toEqual({ result: 'unreachable' })
    expect(Date.now() - started).toBeLessThan(200)
    expect(hops).toBe(2)
  })

  it('maps a certificate error to tls', async () => {
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
    expect(await probeRegistry({ id: 'a', match: 'https://', kind: 'bearer' }, SECRET, f)).toEqual({ result: 'other' })
  })
})

describe.skipIf(!hasOpenssl)('probeRegistry against a local https registry', () => {
  const at = (port: number, rest = '') => `127.0.0.1:${port}${rest}`

  it('sends Basic credentials and reports ok on 200', async () => {
    const { port, seen } = await serve((_q, r) => r.writeHead(200).end('{}'))
    const out = await probeRegistry({ id: 'a', match: `https://${at(port, '/ignored/path')}`, kind: 'basic', username: 'user' }, SECRET, trusted())
    expect(out).toEqual({ result: 'ok', status: 200 })
    expect(seen[0].url).toBe('/v2/')
    expect(seen[0].headers.authorization).toBe(`Basic ${Buffer.from(`user:${SECRET}`).toString('base64')}`)
  })

  it('sends a Bearer secret', async () => {
    const { port, seen } = await serve((_q, r) => r.writeHead(200).end())
    await probeRegistry({ id: 'a', match: at(port), kind: 'bearer' }, SECRET, trusted())
    expect(seen[0].headers.authorization).toBe(`Bearer ${SECRET}`)
  })

  it('sends no Authorization header without a secret', async () => {
    const { port, seen } = await serve((_q, r) => r.writeHead(401).end())
    expect(await probeRegistry({ id: 'a', match: at(port), kind: 'bearer' }, undefined, trusted())).toEqual({ result: 'unauthorized', status: 401 })
    expect(seen[0].headers.authorization).toBeUndefined()
  })

  it('reads the challenge of a token registry, and other on an unexpected status', async () => {
    const a = await serve((_q, r) => r.writeHead(401, { 'WWW-Authenticate': 'Bearer realm="https://auth.registry.example/token"' }).end())
    expect(await probeRegistry({ id: 'a', match: at(a.port), kind: 'bearer' }, SECRET, trusted())).toEqual({ result: 'token', status: 401 })
    const b = await serve((_q, r) => r.writeHead(404).end())
    expect(await probeRegistry({ id: 'a', match: at(b.port), kind: 'bearer' }, SECRET, trusted())).toEqual({ result: 'other', status: 404 })
  })

  it('does not follow a redirect to another origin', async () => {
    const target = await serve((_q, r) => r.writeHead(200).end())
    const { port } = await serve((_q, r) => r.writeHead(302, { Location: `https://${at(target.port)}/v2/` }).end())
    expect(await probeRegistry({ id: 'a', match: at(port), kind: 'bearer' }, SECRET, trusted())).toEqual({ result: 'other', status: 302 })
    expect(target.seen).toHaveLength(0)
  })

  it('does not follow a redirect from https to http on the same host and port', async () => {
    const { port, seen } = await serve((_q, r) => r.writeHead(301, { Location: `http://${at(port)}/v2/` }).end())
    expect(await probeRegistry({ id: 'a', match: at(port), kind: 'bearer' }, SECRET, trusted())).toEqual({ result: 'other', status: 301 })
    expect(seen).toHaveLength(1)
  })

  it('follows a redirect on the same origin', async () => {
    const { port, seen } = await serve((q, r) => (q.url === '/v2/' ? r.writeHead(301, { Location: '/v2/ok' }).end() : r.writeHead(200).end()))
    expect(await probeRegistry({ id: 'a', match: at(port), kind: 'bearer' }, SECRET, trusted())).toEqual({ result: 'ok', status: 200 })
    expect(seen[1].headers.authorization).toBe(`Bearer ${SECRET}`)
  })

  it('reports tls for a certificate the system does not trust, and sends nothing', async () => {
    const { port, seen } = await serve((_q, r) => r.writeHead(200).end())
    expect(await probeRegistry({ id: 'a', match: at(port), kind: 'bearer' }, SECRET)).toEqual({ result: 'tls' })
    expect(seen).toHaveLength(0)
  })

  it('reports unreachable when the registry does not answer in time', async () => {
    const { port } = await serve(() => undefined)
    expect(await probeRegistry({ id: 'a', match: at(port), kind: 'bearer' }, SECRET, trusted(), { timeoutMs: 200 })).toEqual({ result: 'unreachable' })
  })
})
