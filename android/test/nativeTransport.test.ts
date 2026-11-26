// SPDX-License-Identifier: MIT
import { describe, expect, it, vi } from 'vitest'
import { runServerTest } from '@launcher/core/testClassify'
import type { NativeGetResult, NativeHttpPlugin } from '../src/plugins/definitions'
import { NativeHttpError, classifyNativeError, nativeHttpGet } from '../src/plugins/nativeTransport'

function fakePlugin(answer: (url: string) => NativeGetResult | Promise<NativeGetResult>) {
  const calls: { id: string; url: string; timeoutMs: number }[] = []
  const cancels: string[] = []
  const plugin: NativeHttpPlugin = {
    get: vi.fn(async (o: { id: string; url: string; timeoutMs: number }) => {
      calls.push(o)
      return answer(o.url)
    }),
    cancel: vi.fn(async (o: { id: string }) => {
      cancels.push(o.id)
    }),
  }
  return { plugin, calls, cancels }
}

describe('nativeHttpGet', () => {
  it('passes the url and timeout and returns the answer', async () => {
    const { plugin, calls } = fakePlugin(() => ({ ok: true, status: 302, location: '/login', contentType: 'text/html', body: 'x' }))
    const a = await nativeHttpGet(plugin)('https://example.com/version', { timeoutMs: 8000 })
    expect(a).toEqual({ status: 302, location: '/login', contentType: 'text/html', body: 'x' })
    expect(calls).toHaveLength(1)
    expect(calls[0]).toMatchObject({ url: 'https://example.com/version', timeoutMs: 8000 })
    expect(calls[0].id).toMatch(/\S/)
  })

  it('leaves location out when the plugin sends none', async () => {
    const { plugin } = fakePlugin(() => ({ ok: true, status: 200, contentType: '', body: '' }))
    const a = await nativeHttpGet(plugin)('http://192.0.2.1:8080/', { timeoutMs: 100 })
    expect('location' in a).toBe(false)
  })

  it('gives every request its own id', async () => {
    const { plugin, calls } = fakePlugin(() => ({ ok: true, status: 200, contentType: '', body: '' }))
    const get = nativeHttpGet(plugin)
    await get('http://127.0.0.1/a', { timeoutMs: 100 })
    await get('http://127.0.0.1/b', { timeoutMs: 100 })
    expect(calls[0].id).not.toBe(calls[1].id)
  })

  it('rejects with a NativeHttpError for a failed request', async () => {
    const { plugin } = fakePlugin(() => ({ ok: false, error: 'tls', reason: 'self-signed' }))
    const p = nativeHttpGet(plugin)('https://example.com/', { timeoutMs: 100 })
    await expect(p).rejects.toBeInstanceOf(NativeHttpError)
    await expect(p).rejects.toMatchObject({ result: { ok: false, error: 'tls', reason: 'self-signed' } })
  })

  it('cancels the native call with the same id when the signal aborts', async () => {
    let finish: (r: NativeGetResult) => void = () => {}
    const { plugin, calls, cancels } = fakePlugin(() => new Promise<NativeGetResult>((r) => (finish = r)))
    const ac = new AbortController()
    const p = nativeHttpGet(plugin)('https://example.com/', { timeoutMs: 8000, signal: ac.signal })
    await Promise.resolve()
    ac.abort()
    expect(cancels).toEqual([calls[0].id])
    finish({ ok: false, error: 'cancelled' })
    await expect(p).rejects.toMatchObject({ result: { error: 'cancelled' } })
  })

  it('does not call the plugin when the signal already aborted', async () => {
    const { plugin } = fakePlugin(() => ({ ok: true, status: 200, contentType: '', body: '' }))
    const ac = new AbortController()
    ac.abort()
    await expect(nativeHttpGet(plugin)('https://example.com/', { timeoutMs: 100, signal: ac.signal })).rejects.toMatchObject({
      result: { error: 'cancelled' },
    })
    expect(plugin.get).not.toHaveBeenCalled()
  })

  it('does not cancel after the request finished', async () => {
    const { plugin, cancels } = fakePlugin(() => ({ ok: true, status: 200, contentType: '', body: '' }))
    const ac = new AbortController()
    await nativeHttpGet(plugin)('https://example.com/', { timeoutMs: 100, signal: ac.signal })
    ac.abort()
    expect(cancels).toEqual([])
  })
})

describe('classifyNativeError', () => {
  const err = (r: ConstructorParameters<typeof NativeHttpError>[0]) => new NativeHttpError(r)

  it.each([
    [err({ ok: false, error: 'tls', reason: 'expired' }), { status: 'tls', reason: 'expired' }],
    [err({ ok: false, error: 'tls', reason: 'not-yet-valid' }), { status: 'tls', reason: 'not-yet-valid' }],
    [err({ ok: false, error: 'tls', reason: 'self-signed' }), { status: 'tls', reason: 'self-signed' }],
    [err({ ok: false, error: 'tls', reason: 'name-mismatch' }), { status: 'tls', reason: 'name-mismatch' }],
    [err({ ok: false, error: 'tls', reason: 'untrusted' }), { status: 'tls', reason: 'untrusted' }],
    [err({ ok: false, error: 'unreachable', reason: 'timeout' }), { status: 'unreachable', reason: 'timeout' }],
    [err({ ok: false, error: 'unreachable', reason: 'refused' }), { status: 'unreachable', reason: 'refused' }],
    [err({ ok: false, error: 'unreachable', reason: 'dns' }), { status: 'unreachable', reason: 'dns' }],
    [err({ ok: false, error: 'unreachable', reason: 'network' }), { status: 'unreachable', reason: 'network' }],
    [err({ ok: false, error: 'cancelled' }), { status: 'cancelled' }],
  ])('maps %o', (e, want) => {
    expect(classifyNativeError(e)).toEqual(want)
  })

  it('treats a plugin rejection or any other error as a network failure', () => {
    expect(classifyNativeError(new Error('plugin not implemented'))).toEqual({ status: 'unreachable', reason: 'network' })
    expect(classifyNativeError('boom')).toEqual({ status: 'unreachable', reason: 'network' })
  })

  it('does not pass on a reason the contract does not know', () => {
    const tls = err({ ok: false, error: 'tls', reason: 'weird' as never })
    const net = err({ ok: false, error: 'unreachable', reason: 'weird' as never })
    expect(classifyNativeError(tls)).toEqual({ status: 'tls', reason: 'untrusted' })
    expect(classifyNativeError(net)).toEqual({ status: 'unreachable', reason: 'network' })
  })
})

describe('runServerTest over the native transport', () => {
  it('reports a LocalAI server', async () => {
    const { plugin } = fakePlugin((url) =>
      url.endsWith('/.well-known/localai.json')
        ? { ok: true, status: 200, contentType: 'application/json', body: '{"version":"v4.3.0","endpoints":{"chat_completions":"/v1/chat/completions"}}' }
        : { ok: true, status: 404, contentType: '', body: '' },
    )
    const r = await runServerTest('https://example.com', nativeHttpGet(plugin), classifyNativeError)
    expect(r).toEqual({ status: 'ok', version: '4.3.0', secure: true })
  })

  it('reports the TLS reason', async () => {
    const { plugin } = fakePlugin(() => ({ ok: false, error: 'tls', reason: 'name-mismatch' }))
    const r = await runServerTest('https://example.com', nativeHttpGet(plugin), classifyNativeError)
    expect(r).toEqual({ status: 'tls', reason: 'name-mismatch' })
  })

  it('reports cancelled when the user aborts', async () => {
    // Like the Kotlin side: a cancelled call answers { error: 'cancelled' }.
    let finish: (r: NativeGetResult) => void = () => {}
    const { plugin } = fakePlugin(() => new Promise<NativeGetResult>((r) => (finish = r)))
    plugin.cancel = vi.fn(async () => finish({ ok: false, error: 'cancelled' }))
    const ac = new AbortController()
    const p = runServerTest('https://example.com', nativeHttpGet(plugin), classifyNativeError, { signal: ac.signal })
    await Promise.resolve()
    ac.abort()
    expect(await p).toEqual({ status: 'cancelled' })
    expect(plugin.cancel).toHaveBeenCalledTimes(1)
  })
})
