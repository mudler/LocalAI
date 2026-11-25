// SPDX-License-Identifier: MIT
// The connection test's HTTP transport on Android. A WebView fetch cannot read
// a cross-origin answer or a TLS error, so the request runs in the NativeHttp
// plugin (OkHttp) and this file adapts it to the shared `HttpGet`.
import type { HttpAnswer, HttpGet, TestResult, TlsReason, UnreachableReason } from '@launcher/core/testClassify'
import type { NativeGetResult, NativeHttpPlugin } from './definitions'

export type NativeGetFailure = Extract<NativeGetResult, { ok: false }>

/** A request the plugin answered with a TLS, network or cancel result. */
export class NativeHttpError extends Error {
  constructor(readonly result: NativeGetFailure) {
    super(result.error === 'cancelled' ? 'cancelled' : `${result.error}: ${result.reason}`)
    this.name = 'NativeHttpError'
  }
}

const TLS_REASONS: readonly TlsReason[] = ['expired', 'not-yet-valid', 'self-signed', 'name-mismatch', 'untrusted']
const UNREACHABLE_REASONS: readonly UnreachableReason[] = ['timeout', 'refused', 'dns', 'network']

let seq = 0
const nextId = () => `nh-${Date.now().toString(36)}-${(++seq).toString(36)}`

export function nativeHttpGet(plugin: NativeHttpPlugin): HttpGet {
  return async (url, { timeoutMs, signal }) => {
    if (signal?.aborted) throw new NativeHttpError({ ok: false, error: 'cancelled' })
    const id = nextId()
    const onAbort = () => {
      // The native call then answers `cancelled`; a failed cancel changes nothing.
      plugin.cancel({ id }).catch(() => {})
    }
    signal?.addEventListener('abort', onAbort, { once: true })
    try {
      const r = await plugin.get({ id, url, timeoutMs })
      if (!r.ok) throw new NativeHttpError(r)
      const a: HttpAnswer = { status: r.status, contentType: r.contentType, body: r.body }
      if (r.location !== undefined) a.location = r.location
      return a
    } finally {
      signal?.removeEventListener('abort', onAbort)
    }
  }
}

/** The server checks of the client core: requests and their error classes, both from the plugin. */
export function nativeServerChecks(plugin: NativeHttpPlugin): { http: HttpGet; classifyError(e: unknown): TestResult } {
  return { http: nativeHttpGet(plugin), classifyError: classifyNativeError }
}

export function classifyNativeError(e: unknown): TestResult {
  if (e instanceof NativeHttpError) {
    const r = e.result
    if (r.error === 'cancelled') return { status: 'cancelled' }
    if (r.error === 'tls') return { status: 'tls', reason: TLS_REASONS.includes(r.reason) ? r.reason : 'untrusted' }
    return { status: 'unreachable', reason: UNREACHABLE_REASONS.includes(r.reason) ? r.reason : 'network' }
  }
  return { status: 'unreachable', reason: 'network' }
}
