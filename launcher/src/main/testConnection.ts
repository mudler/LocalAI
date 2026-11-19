// Checks a server address from the main process. No Electron import.
//
// The renderer never makes this request: the address comes from a saved
// server or from what the user typed, already reduced to an origin. The
// check sends no credentials and no cookies, never skips certificate
// errors, never follows a redirect off the origin, and gives up after 8 s.
import http from 'node:http'
import https from 'node:https'
import { isLoopbackHost } from './network'

export const TEST_TIMEOUT_MS = 8000
const MAX_BODY = 64 * 1024
const MAX_HOPS = 3

export type TlsReason = 'expired' | 'not-yet-valid' | 'self-signed' | 'name-mismatch' | 'untrusted'
export type UnreachableReason = 'timeout' | 'refused' | 'dns' | 'network'

export type TestResult =
  /** It answered and it is LocalAI. `secure` is false for plain http. */
  | { status: 'ok'; version?: string; secure: boolean }
  /** It answered and asks for a sign-in (401, 403 or the WebUI login page). Still reachable. */
  | { status: 'needs-signin' }
  | { status: 'not-localai'; reason: 'answer' | 'redirect' }
  | { status: 'tls'; reason: TlsReason }
  | { status: 'unreachable'; reason: UnreachableReason }
  | { status: 'cancelled' }

export interface TestOptions {
  timeoutMs?: number
  signal?: AbortSignal
  /** Extra trusted certificate, for tests. It adds trust for a test CA; it never turns checking off. */
  extraCa?: string
}

/** Plain http to an address that is not this computer: the traffic is readable on the network. */
export function isInsecureRemote(origin: string): boolean {
  try {
    const u = new URL(origin)
    return u.protocol === 'http:' && !isLoopbackHost(u.hostname)
  } catch {
    return false
  }
}

interface Answer {
  status: number
  location?: string
  contentType: string
  body: string
}

class Failed extends Error {
  constructor(readonly result: TestResult) {
    super('test failed')
  }
}

const TLS_CODES: Record<string, TlsReason> = {
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

export function classifyNetworkError(e: unknown): TestResult {
  const code = (e as NodeJS.ErrnoException)?.code ?? ''
  if (code in TLS_CODES) return { status: 'tls', reason: TLS_CODES[code] }
  if (code.startsWith('ERR_TLS') || code.startsWith('ERR_SSL') || /^CERT_|^UNABLE_TO/.test(code)) return { status: 'tls', reason: 'untrusted' }
  if (code === 'ECONNREFUSED') return { status: 'unreachable', reason: 'refused' }
  if (code === 'ENOTFOUND' || code === 'EAI_AGAIN') return { status: 'unreachable', reason: 'dns' }
  if (code === 'ETIMEDOUT' || code === 'ESOCKETTIMEDOUT') return { status: 'unreachable', reason: 'timeout' }
  return { status: 'unreachable', reason: 'network' }
}

function get(url: string, o: { timeoutMs: number; signal?: AbortSignal; extraCa?: string }): Promise<Answer> {
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

const LOGIN_PATH = /login|sign-?in|auth/i

function parseJson(text: string): Record<string, unknown> | undefined {
  try {
    const v = JSON.parse(text) as unknown
    return typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : undefined
  } catch {
    return undefined
  }
}

function cleanVersion(v: unknown): string | undefined {
  if (typeof v !== 'string') return undefined
  const m = /v?(\d+\.\d+\.\d+[0-9A-Za-z.+-]*)/.exec(v)
  return m ? m[1] : undefined
}

export async function testServer(origin: string, opts: TestOptions = {}): Promise<TestResult> {
  const secure = origin.startsWith('https:')
  const base = new URL(origin).origin
  const o = { timeoutMs: opts.timeoutMs ?? TEST_TIMEOUT_MS, signal: opts.signal, extraCa: opts.extraCa }

  // One request, following redirects that stay on the origin. A redirect to
  // a login page is a sign-in; any other place is refused.
  const fetchPath = async (p: string): Promise<Answer | 'signin'> => {
    let url = base + p
    for (let hop = 0; hop <= MAX_HOPS; hop++) {
      const a = await get(url, o)
      if (a.status < 300 || a.status > 399 || !a.location) return a
      let next: URL
      try {
        next = new URL(a.location, url)
      } catch {
        throw new Failed({ status: 'not-localai', reason: 'redirect' })
      }
      if (next.origin !== base) throw new Failed({ status: 'not-localai', reason: 'redirect' })
      if (LOGIN_PATH.test(next.pathname)) return 'signin'
      url = next.href
    }
    throw new Failed({ status: 'not-localai', reason: 'redirect' })
  }

  try {
    let signin = false
    const probe = async (p: string): Promise<Answer | undefined> => {
      const a = await fetchPath(p)
      if (a === 'signin') {
        signin = true
        return undefined
      }
      if (a.status === 401 || a.status === 403) {
        signin = true
        return undefined
      }
      return a
    }

    // 1. The public discovery document (no sign-in needed on a LocalAI server).
    const wk = await probe('/.well-known/localai.json')
    if (signin) return { status: 'needs-signin' }
    if (wk && wk.status === 200) {
      const j = parseJson(wk.body)
      const ep = j?.endpoints
      if (typeof ep === 'object' && ep !== null && typeof (ep as Record<string, unknown>).chat_completions === 'string') {
        return { status: 'ok', version: cleanVersion(j?.version), secure }
      }
    }
    // 2. Older servers: the version route.
    const ver = await probe('/version')
    if (signin) return { status: 'needs-signin' }
    if (ver && ver.status === 200) {
      const j = parseJson(ver.body)
      const v = cleanVersion(j?.version)
      if (v) return { status: 'ok', version: v, secure }
    }
    // 3. The readiness route answers JSON while the server starts.
    const rz = await probe('/readyz')
    if (signin) return { status: 'needs-signin' }
    if (rz && rz.status === 503) {
      const s = parseJson(rz.body)?.status
      if (s === 'starting' || s === 'draining') return { status: 'ok', secure }
    }
    return { status: 'not-localai', reason: 'answer' }
  } catch (e) {
    if (e instanceof Failed) return e.result
    if ((e as NodeJS.ErrnoException)?.code === 'ABORTED') return { status: 'cancelled' }
    return classifyNetworkError(e)
  }
}
