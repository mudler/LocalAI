// SPDX-License-Identifier: MIT
// The fake server the emulator smoke test connects to. The app must accept it
// as a LocalAI server, so the check here is the app's own server test.
import type { AddressInfo } from 'node:net'
import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { runServerTest, type HttpGet } from '@launcher/core/testClassify'
import { startFakeLocalAI } from './fixtures/fake-localai.mjs'

let server: Awaited<ReturnType<typeof startFakeLocalAI>>
let origin = ''

beforeAll(async () => {
  server = await startFakeLocalAI(0)
  const a = server.address() as AddressInfo
  origin = `http://127.0.0.1:${a.port}`
})

afterAll(async () => {
  await new Promise<void>((resolve) => server.close(() => resolve()))
})

const get: HttpGet = async (url, o) => {
  const r = await fetch(url, { redirect: 'manual', signal: o.signal ?? AbortSignal.timeout(o.timeoutMs) })
  return { status: r.status, location: r.headers.get('location') ?? undefined, contentType: r.headers.get('content-type') ?? '', body: await r.text() }
}

describe('fake-localai', () => {
  it('listens on loopback only', () => {
    expect((server.address() as AddressInfo).address).toBe('127.0.0.1')
  })

  it('passes the app server test as LocalAI 9.9.9 over plain http', async () => {
    const r = await runServerTest(origin, get, () => ({ status: 'unreachable', reason: 'network' }))
    expect(r).toEqual({ status: 'ok', version: '9.9.9', secure: false })
  })

  it('serves the discovery document', async () => {
    const r = await fetch(`${origin}/.well-known/localai.json`)
    expect(r.status).toBe(200)
    expect(r.headers.get('content-type')).toMatch(/^application\/json/)
    expect(await r.json()).toEqual({ version: 'v9.9.9', endpoints: { chat_completions: '/v1/chat/completions' } })
  })

  it('serves an HTML page titled Fake LocalAI at /app', async () => {
    const r = await fetch(`${origin}/app`)
    expect(r.status).toBe(200)
    expect(r.headers.get('content-type')).toMatch(/^text\/html/)
    expect(await r.text()).toContain('<title>Fake LocalAI</title>')
  })

  it('answers 404 for anything else, and for other methods', async () => {
    expect((await fetch(`${origin}/`)).status).toBe(404)
    expect((await fetch(`${origin}/version`)).status).toBe(404)
    expect((await fetch(`${origin}/app/chat`)).status).toBe(404)
    expect((await fetch(`${origin}/app`, { method: 'POST' })).status).toBe(404)
  })
})
