// SPDX-License-Identifier: MIT
// The client core checks servers through the NativeHttp plugin: the app entry
// wires nativeServerChecks, and the core reports the plugin's answers.
import fs from 'node:fs'
import path from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { ClientCore } from '../src/core/clientCore'
import type { NativeGetResult, NativeHttpPlugin } from '../src/plugins/definitions'
import { nativeServerChecks } from '../src/plugins/nativeTransport'
import { fakePlatform, flush } from './fakes/plugins'

const DISCOVERY = JSON.stringify({ version: 'v4.1.3', endpoints: { chat_completions: '/v1/chat/completions' } })

/** Answers per origin: a TLS refusal, a network failure, or a LocalAI server. */
function plugin(answers: Record<string, NativeGetResult | 'localai'>): NativeHttpPlugin {
  return {
    get: async ({ url }) => {
      const a = answers[new URL(url).origin]
      if (a === undefined) return { ok: false, error: 'unreachable', reason: 'refused' }
      if (a !== 'localai') return a
      if (new URL(url).pathname === '/.well-known/localai.json') return { ok: true, status: 200, contentType: 'application/json', body: DISCOVERY }
      return { ok: true, status: 404, contentType: 'text/plain', body: '' }
    },
    cancel: async () => undefined,
  }
}

let open: ClientCore[] = []
afterEach(async () => {
  for (const c of open) await c.shutdown()
  open = []
})

async function start(p: NativeHttpPlugin): Promise<ClientCore> {
  const core = await ClientCore.open({ ...fakePlatform(), ...nativeServerChecks(p) })
  open.push(core)
  await flush()
  return core
}

describe('server checks over NativeHttp', () => {
  it('reports a LocalAI server the plugin reached', async () => {
    const core = await start(plugin({ 'https://ai.example.com': 'localai' }))
    const t = await core.servers.testServer({ address: 'https://ai.example.com' })
    expect(t.ok && t.result).toMatchObject({ status: 'ok', version: '4.1.3' })
  })

  it('reports the TLS reason the plugin gave', async () => {
    const core = await start(plugin({ 'https://self.example.com': { ok: false, error: 'tls', reason: 'self-signed' } }))
    const t = await core.servers.testServer({ address: 'https://self.example.com' })
    expect(t.ok && t.result).toEqual({ status: 'tls', reason: 'self-signed' })
  })

  it('reports the unreachable reason the plugin gave', async () => {
    const core = await start(plugin({ 'https://dns.example.com': { ok: false, error: 'unreachable', reason: 'dns' } }))
    const t = await core.servers.testServer({ address: 'https://dns.example.com' })
    expect(t.ok && t.result).toEqual({ status: 'unreachable', reason: 'dns' })
  })

  it('is what the app entry wires, with no placeholder left', () => {
    const main = fs.readFileSync(path.resolve(__dirname, '../src/main.tsx'), 'utf8')
    expect(main).toContain('...nativeServerChecks(NativeHttp)')
    expect(main).not.toMatch(/not wired/)
  })
})
