// The connection test decisions over a scripted transport: no sockets.
import { describe, expect, it } from 'vitest'
import { runServerTest, isInsecureRemote, type HttpAnswer, type HttpGet, type TestResult } from '../src/core/testClassify'

const ORIGIN = 'https://example.com'

type Step = HttpAnswer | Error

/** A transport that answers from a map of URL to answer or error. Unknown URLs answer 404. */
function scripted(routes: Record<string, Step>): { get: HttpGet; calls: string[] } {
  const calls: string[] = []
  const get: HttpGet = async (url) => {
    calls.push(url)
    const r = routes[url]
    if (r instanceof Error) throw r
    return r ?? notFound
  }
  return { get, calls }
}

const notFound: HttpAnswer = { status: 404, contentType: 'text/plain', body: 'not found' }
const json = (status: number, body: unknown): HttpAnswer => ({ status, contentType: 'application/json', body: JSON.stringify(body) })
const redirect = (location: string): HttpAnswer => ({ status: 302, location, contentType: 'text/html', body: '' })

const unreachable = (): TestResult => ({ status: 'unreachable', reason: 'network' })

describe('runServerTest', () => {
  it('reads the version from the discovery document', async () => {
    const { get } = scripted({
      [`${ORIGIN}/.well-known/localai.json`]: json(200, { version: 'v4.3.0', endpoints: { chat_completions: '/v1/chat/completions' } }),
    })
    expect(await runServerTest(ORIGIN, get, unreachable)).toEqual({ status: 'ok', version: '4.3.0', secure: true })
  })

  it('falls back to the version route', async () => {
    const { get, calls } = scripted({ [`${ORIGIN}/version`]: json(200, { version: 'v4.2.1' }) })
    expect(await runServerTest(ORIGIN, get, unreachable)).toEqual({ status: 'ok', version: '4.2.1', secure: true })
    expect(calls).toEqual([`${ORIGIN}/.well-known/localai.json`, `${ORIGIN}/version`])
  })

  it('reports plain http as not secure', async () => {
    const o = 'http://192.0.2.10:8080'
    const { get } = scripted({ [`${o}/version`]: json(200, { version: 'v4.2.1' }) })
    expect(await runServerTest(o, get, unreachable)).toEqual({ status: 'ok', version: '4.2.1', secure: false })
  })

  it('treats a redirect to a login page as a sign-in', async () => {
    const { get } = scripted({ [`${ORIGIN}/.well-known/localai.json`]: redirect('/login') })
    expect(await runServerTest(ORIGIN, get, unreachable)).toEqual({ status: 'needs-signin' })
  })

  it('treats 401 as a sign-in', async () => {
    const { get } = scripted({ [`${ORIGIN}/.well-known/localai.json`]: { status: 401, contentType: 'text/plain', body: '' } })
    expect(await runServerTest(ORIGIN, get, unreachable)).toEqual({ status: 'needs-signin' })
  })

  it('refuses a redirect to another origin', async () => {
    const { get } = scripted({ [`${ORIGIN}/.well-known/localai.json`]: redirect('https://other.example.com/') })
    expect(await runServerTest(ORIGIN, get, unreachable)).toEqual({ status: 'not-localai', reason: 'redirect' })
  })

  it('refuses more than three same-origin hops', async () => {
    const { get, calls } = scripted({
      [`${ORIGIN}/.well-known/localai.json`]: redirect('/a'),
      [`${ORIGIN}/a`]: redirect('/b'),
      [`${ORIGIN}/b`]: redirect('/c'),
      [`${ORIGIN}/c`]: redirect('/d'),
      [`${ORIGIN}/d`]: json(200, { version: 'v4.3.0', endpoints: { chat_completions: '/v1/chat/completions' } }),
    })
    expect(await runServerTest(ORIGIN, get, unreachable)).toEqual({ status: 'not-localai', reason: 'redirect' })
    expect(calls).toHaveLength(4)
  })

  it('accepts a starting server on the readiness route, with no version', async () => {
    const { get } = scripted({ [`${ORIGIN}/readyz`]: json(503, { status: 'starting' }) })
    expect(await runServerTest(ORIGIN, get, unreachable)).toEqual({ status: 'ok', secure: true })
  })

  it('reports another server when every probe is 404', async () => {
    const { get, calls } = scripted({})
    expect(await runServerTest(ORIGIN, get, unreachable)).toEqual({ status: 'not-localai', reason: 'answer' })
    expect(calls).toEqual([`${ORIGIN}/.well-known/localai.json`, `${ORIGIN}/version`, `${ORIGIN}/readyz`])
  })

  it('maps a transport error through the injected classifier', async () => {
    const marker = new Error('marker')
    const { get } = scripted({ [`${ORIGIN}/.well-known/localai.json`]: marker })
    const seen: unknown[] = []
    const classify = (e: unknown): TestResult => {
      seen.push(e)
      return { status: 'tls', reason: 'self-signed' }
    }
    expect(await runServerTest(ORIGIN, get, classify)).toEqual({ status: 'tls', reason: 'self-signed' })
    expect(seen).toEqual([marker])
  })

  it('gives cancelled when the transport fails after the signal aborted', async () => {
    const ac = new AbortController()
    const get: HttpGet = async () => {
      ac.abort()
      throw new Error('socket closed')
    }
    expect(await runServerTest(ORIGIN, get, unreachable, { signal: ac.signal })).toEqual({ status: 'cancelled' })
  })

  it('passes the timeout and the signal to the transport', async () => {
    const ac = new AbortController()
    const seen: { timeoutMs: number; signal?: AbortSignal }[] = []
    const get: HttpGet = async (_u, o) => {
      seen.push(o)
      return notFound
    }
    await runServerTest(ORIGIN, get, unreachable, { timeoutMs: 1234, signal: ac.signal })
    expect(seen[0]).toEqual({ timeoutMs: 1234, signal: ac.signal })
    seen.length = 0
    await runServerTest(ORIGIN, get, unreachable)
    expect(seen[0].timeoutMs).toBe(8000)
  })
})

describe('isInsecureRemote', () => {
  it('flags plain http to another host only', () => {
    expect(isInsecureRemote('http://192.0.2.10:8080')).toBe(true)
    expect(isInsecureRemote('http://127.0.0.1:8080')).toBe(false)
    expect(isInsecureRemote('https://example.com')).toBe(false)
    expect(isInsecureRemote('not a url')).toBe(false)
  })
})
