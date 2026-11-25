// Decides what a server address answers, shared by the desktop check and a
// client-only shell. Pure, no Electron or Node import: the HTTP request is
// injected, so each side brings its own transport.
//
// The check sends no credentials and no cookies, never follows a redirect
// off the origin, and gives up after 8 s.
import { isLoopbackHost } from './address'

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

export const TEST_TIMEOUT_MS = 8000
const MAX_HOPS = 3

export interface HttpAnswer {
  status: number
  location?: string
  contentType: string
  body: string
}

/** One GET with no redirects, no cookies, no credentials. Rejects on a network or TLS error. */
export type HttpGet = (url: string, o: { timeoutMs: number; signal?: AbortSignal }) => Promise<HttpAnswer>

/** Plain http to an address that is not this computer: the traffic is readable on the network. */
export function isInsecureRemote(origin: string): boolean {
  try {
    const u = new URL(origin)
    return u.protocol === 'http:' && !isLoopbackHost(u.hostname)
  } catch {
    return false
  }
}

class Failed extends Error {
  constructor(readonly result: TestResult) {
    super('test failed')
  }
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

/**
 * Checks that `origin` is a LocalAI server. `get` does the requests;
 * `classifyError` turns a rejected request into a result. A request that
 * fails after `opts.signal` aborted is `cancelled`, whatever the error.
 */
export async function runServerTest(
  origin: string,
  get: HttpGet,
  classifyError: (e: unknown) => TestResult,
  opts: { timeoutMs?: number; signal?: AbortSignal } = {},
): Promise<TestResult> {
  const secure = origin.startsWith('https:')
  const base = new URL(origin).origin
  const o = { timeoutMs: opts.timeoutMs ?? TEST_TIMEOUT_MS, signal: opts.signal }

  // One request, following redirects that stay on the origin. A redirect to
  // a login page is a sign-in; any other place is refused.
  const fetchPath = async (p: string): Promise<HttpAnswer | 'signin'> => {
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
    const probe = async (p: string): Promise<HttpAnswer | undefined> => {
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
    // The transport's error after a cancel can be anything (a closed socket,
    // a fetch AbortError); the signal is what says the user cancelled.
    if (opts.signal?.aborted) return { status: 'cancelled' }
    return classifyError(e)
  }
}
