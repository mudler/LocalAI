import http from 'node:http'
import https from 'node:https'
import fs from 'node:fs'
import path from 'node:path'
import { execFileSync } from 'node:child_process'
import type { AddressInfo } from 'node:net'
import { afterEach, describe, expect, it } from 'vitest'
import { classifyNetworkError, isInsecureRemote, testServer } from '../src/main/testConnection'
import { tmpDir } from './helpers'

type Handler = (req: http.IncomingMessage, res: http.ServerResponse) => void
const closers: (() => void)[] = []
afterEach(() => closers.splice(0).forEach((c) => c()))

function serve(h: Handler): Promise<{ origin: string; seen: string[] }> {
  const seen: string[] = []
  const s = http.createServer((req, res) => {
    seen.push(`${req.method} ${req.url} auth=${req.headers.authorization ? 'yes' : 'no'} cookie=${req.headers.cookie ? 'yes' : 'no'}`)
    h(req, res)
  })
  return new Promise((r) => s.listen(0, '127.0.0.1', () => {
    closers.push(() => s.close())
    s.closeAllConnections?.()
    r({ origin: `http://127.0.0.1:${(s.address() as AddressInfo).port}`, seen })
  }))
}
const json = (res: http.ServerResponse, code: number, body: unknown) => {
  res.writeHead(code, { 'content-type': 'application/json' })
  res.end(JSON.stringify(body))
}
const localai: Handler = (req, res) => {
  if (req.url === '/.well-known/localai.json') return json(res, 200, { version: 'v4.2.1', endpoints: { chat_completions: '/v1/chat/completions' } })
  res.writeHead(404).end()
}

describe('testServer', () => {
  it('knows LocalAI and reads its version, sending no credentials', async () => {
    const s = await serve(localai)
    const r = await testServer(s.origin)
    expect(r).toEqual({ status: 'ok', version: '4.2.1', secure: false })
    expect(s.seen.every((l) => l.includes('auth=no') && l.includes('cookie=no'))).toBe(true)
  })

  it('accepts an older server by its version route', async () => {
    const s = await serve((req, res) => (req.url === '/version' ? json(res, 200, { version: 'v3.9.0' }) : res.writeHead(404).end()))
    expect(await testServer(s.origin)).toMatchObject({ status: 'ok', version: '3.9.0' })
  })

  it('accepts a server that is still starting', async () => {
    const s = await serve((req, res) => (req.url === '/readyz' ? json(res, 503, { status: 'starting', reason: 'x' }) : res.writeHead(404).end()))
    expect(await testServer(s.origin)).toMatchObject({ status: 'ok' })
  })

  it('says not LocalAI for another web page', async () => {
    const s = await serve((_q, res) => res.writeHead(200, { 'content-type': 'text/html' }).end('<html>hello</html>'))
    expect(await testServer(s.origin)).toEqual({ status: 'not-localai', reason: 'answer' })
  })

  it('treats 401 and 403 as reachable and needing sign-in', async () => {
    for (const code of [401, 403]) {
      const s = await serve((_q, res) => res.writeHead(code).end())
      expect(await testServer(s.origin)).toEqual({ status: 'needs-signin' })
    }
  })

  it('treats a redirect to the WebUI login page as needing sign-in', async () => {
    const s = await serve((req, res) => (req.url === '/login' ? res.writeHead(200).end('<html>login</html>') : res.writeHead(302, { location: '/login' }).end()))
    expect(await testServer(s.origin)).toEqual({ status: 'needs-signin' })
  })

  it('does not follow a redirect off the origin', async () => {
    const other = await serve((_q, res) => json(res, 200, { endpoints: { chat_completions: 'x' } }))
    const s = await serve((_q, res) => res.writeHead(302, { location: `${other.origin}/.well-known/localai.json` }).end())
    expect(await testServer(s.origin)).toEqual({ status: 'not-localai', reason: 'redirect' })
    expect(other.seen).toEqual([])
  })

  it('reports a refused connection', async () => {
    const s = await serve(localai)
    closers.splice(0).forEach((c) => c())
    await new Promise((r) => setTimeout(r, 50))
    expect(await testServer(s.origin)).toEqual({ status: 'unreachable', reason: 'refused' })
  })

  it('times out', async () => {
    const s = await serve(() => undefined)
    expect(await testServer(s.origin, { timeoutMs: 150 })).toEqual({ status: 'unreachable', reason: 'timeout' })
  })

  it('can be cancelled', async () => {
    const s = await serve(() => undefined)
    const ac = new AbortController()
    const p = testServer(s.origin, { signal: ac.signal })
    setTimeout(() => ac.abort(), 50)
    expect(await p).toEqual({ status: 'cancelled' })
  })

  it('flags http on an address that is not this computer', () => {
    expect(isInsecureRemote('http://192.0.2.40:8080')).toBe(true)
    expect(isInsecureRemote('http://127.0.0.1:8080')).toBe(false)
    expect(isInsecureRemote('http://localhost:8080')).toBe(false)
    expect(isInsecureRemote('https://gpu.example.com')).toBe(false)
  })
})

describe('classifyNetworkError', () => {
  it('maps certificate and network codes to plain reasons', () => {
    const e = (code: string) => Object.assign(new Error('x'), { code })
    expect(classifyNetworkError(e('CERT_HAS_EXPIRED'))).toEqual({ status: 'tls', reason: 'expired' })
    expect(classifyNetworkError(e('ERR_TLS_CERT_ALTNAME_INVALID'))).toEqual({ status: 'tls', reason: 'name-mismatch' })
    expect(classifyNetworkError(e('ENOTFOUND'))).toEqual({ status: 'unreachable', reason: 'dns' })
    expect(classifyNetworkError(e('EHOSTUNREACH'))).toEqual({ status: 'unreachable', reason: 'network' })
  })
})

const hasOpenssl = (() => {
  try {
    execFileSync('openssl', ['version'], { stdio: 'ignore' })
    return true
  } catch {
    return false
  }
})()

describe.skipIf(!hasOpenssl)('testServer over TLS', () => {
  function cert(days: number, cn: string) {
    const dir = tmpDir('tls-')
    const key = path.join(dir, 'k.pem')
    const crt = path.join(dir, 'c.pem')
    execFileSync('openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', key, '-out', crt, '-days', String(days), '-subj', `/CN=${cn}`, '-addext', `subjectAltName=DNS:${cn},IP:127.0.0.1`], { stdio: 'ignore' })
    const out = { key: fs.readFileSync(key, 'utf8'), cert: fs.readFileSync(crt, 'utf8') }
    fs.rmSync(dir, { recursive: true, force: true })
    return out
  }
  const tls = (c: { key: string; cert: string }, h: Handler) =>
    new Promise<string>((r) => {
      const s = https.createServer(c, h)
      s.listen(0, '127.0.0.1', () => {
        closers.push(() => s.close())
        r(`https://127.0.0.1:${(s.address() as AddressInfo).port}`)
      })
    })

  it('never bypasses a self-signed certificate', async () => {
    const origin = await tls(cert(5, 'localhost'), localai)
    expect(await testServer(origin)).toMatchObject({ status: 'tls', reason: 'self-signed' })
  })

  it('connects when the certificate chain is trusted (test CA only)', async () => {
    const c = cert(5, 'localhost')
    const origin = await tls(c, localai)
    expect(await testServer(origin, { extraCa: c.cert })).toMatchObject({ status: 'ok', secure: true })
  })
})
