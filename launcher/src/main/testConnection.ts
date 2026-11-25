// Checks a server address from the main process. No Electron import.
//
// The renderer never makes this request: the address comes from a saved
// server or from what the user typed, already reduced to an origin. The
// decisions live in core/testClassify.ts; this file brings the Node HTTP
// transport, which never skips certificate errors and reads at most 64 KiB.
import http from 'node:http'
import https from 'node:https'
import { runServerTest, type HttpAnswer, type HttpGet, type TestResult, type TlsReason } from '../core/testClassify'

export type { HttpAnswer, HttpGet, TestResult, TlsReason, UnreachableReason } from '../core/testClassify'
export { isInsecureRemote, TEST_TIMEOUT_MS } from '../core/testClassify'

const MAX_BODY = 64 * 1024

export interface TestOptions {
  timeoutMs?: number
  signal?: AbortSignal
  /** Extra trusted certificate, for tests. It adds trust for a test CA; it never turns checking off. */
  extraCa?: string
}

export const TLS_CODES: Record<string, TlsReason> = {
  CERT_HAS_EXPIRED: 'expired',
  CERT_NOT_YET_VALID: 'not-yet-valid',
  DEPTH_ZERO_SELF_SIGNED_CERT: 'self-signed',
  SELF_SIGNED_CERT_IN_CHAIN: 'self-signed',
  ERR_TLS_CERT_ALTNAME_INVALID: 'name-mismatch',
  UNABLE_TO_VERIFY_LEAF_SIGNATURE: 'untrusted',
  UNABLE_TO_GET_ISSUER_CERT_LOCALLY: 'untrusted',
  UNABLE_TO_GET_ISSUER_CERT: 'untrusted',
  CERT_UNTRUSTED: 'untrusted',
  CERT_REVOKED: 'untrusted',
  ERR_SSL_WRONG_VERSION_NUMBER: 'untrusted',
}

const code = (e: unknown): string => (e as NodeJS.ErrnoException)?.code ?? ''

export function classifyNetworkError(e: unknown): TestResult {
  const c = code(e)
  if (c in TLS_CODES) return { status: 'tls', reason: TLS_CODES[c] }
  if (c.startsWith('ERR_TLS') || c.startsWith('ERR_SSL') || /^CERT_|^UNABLE_TO/.test(c)) return { status: 'tls', reason: 'untrusted' }
  if (c === 'ECONNREFUSED') return { status: 'unreachable', reason: 'refused' }
  if (c === 'ENOTFOUND' || c === 'EAI_AGAIN') return { status: 'unreachable', reason: 'dns' }
  if (c === 'ETIMEDOUT' || c === 'ESOCKETTIMEDOUT') return { status: 'unreachable', reason: 'timeout' }
  return { status: 'unreachable', reason: 'network' }
}

/** The Node transport: an `HttpGet` plus an optional extra trusted CA. */
export function nodeGet(url: string, o: Parameters<HttpGet>[1] & { extraCa?: string }): Promise<HttpAnswer> {
  return new Promise((resolve, reject) => {
    const mod = url.startsWith('https:') ? https : http
    // No Authorization, no cookies; rejectUnauthorized stays at its default (on).
    const req = mod.get(
      url,
      {
        headers: { Accept: 'application/json, text/html;q=0.5', 'User-Agent': 'localai-launcher' },
        agent: false,
        ...(o.extraCa ? { ca: o.extraCa } : {}),
      },
      (res) => {
        const chunks: Buffer[] = []
        let n = 0
        res.on('data', (d: Buffer) => {
          n += d.length
          if (n <= MAX_BODY) chunks.push(d)
          else res.destroy()
        })
        const done = () =>
          resolve({
            status: res.statusCode ?? 0,
            location: res.headers.location,
            contentType: String(res.headers['content-type'] ?? ''),
            body: Buffer.concat(chunks).toString('utf8'),
          })
        res.on('end', done)
        res.on('close', done)
        res.on('error', reject)
      },
    )
    const timer = setTimeout(() => req.destroy(Object.assign(new Error('timeout'), { code: 'ETIMEDOUT' })), o.timeoutMs)
    const abort = () => req.destroy(Object.assign(new Error('cancelled'), { code: 'ABORTED' }))
    if (o.signal) {
      if (o.signal.aborted) abort()
      else o.signal.addEventListener('abort', abort, { once: true })
    }
    req.on('error', (e) => {
      clearTimeout(timer)
      reject(e)
    })
    req.on('close', () => {
      clearTimeout(timer)
      o.signal?.removeEventListener('abort', abort)
    })
  })
}

export function testServer(origin: string, opts: TestOptions = {}): Promise<TestResult> {
  return runServerTest(
    origin,
    (u, o) => nodeGet(u, { ...o, extraCa: opts.extraCa }),
    (e) => (code(e) === 'ABORTED' ? { status: 'cancelled' } : classifyNetworkError(e)),
    opts,
  )
}
