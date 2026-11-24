// Checks one registry credential rule from the main process. No Electron import.
//
// Unlike testConnection.ts, this request carries the rule's credential, so it
// is stricter about where it goes: only the origin named by the rule, never a
// redirect to another origin, never a skipped certificate check. The secret is
// read in the main process; the result and every error stay free of it, of the
// header and of the response body.
import https from 'node:https'
import type { RegistryCredentialRule, RegistryCredentialTestResult } from '../shared/types'
import { classifyNetworkError, TEST_TIMEOUT_MS } from './testConnection'

const MAX_BODY = 64 * 1024
const MAX_HOPS = 3

export type RegistryProbeResult = RegistryCredentialTestResult

/** One answer: the status, and the two headers the probe reads. */
export interface ProbeAnswer {
  status: number
  location?: string
  /** The `WWW-Authenticate` header of a 401. */
  authenticate?: string
}

/**
 * What the probe needs from the network: one request, one answer. Never
 * follows a redirect. `signal` ends the request when the probe's single
 * deadline passes.
 */
export type ProbeFetch = (url: string, headers: Record<string, string>, signal: AbortSignal) => Promise<ProbeAnswer>

/**
 * A node https request, certificate checking on, body capped at 64 KB. `ca`
 * adds a trusted certificate, for tests only; the default trusts the system.
 */
export function createProbeRequest(o: { ca?: string } = {}): ProbeFetch {
  return (url, headers, signal) =>
    new Promise((resolve, reject) => {
      if (!url.startsWith('https:')) return reject(Object.assign(new Error('https only'), { code: 'ERR_INVALID_PROTOCOL' }))
      const req = https.get(
        url,
        { headers: { ...headers, Accept: 'application/json', 'User-Agent': 'localai-launcher' }, agent: false, signal, ...(o.ca ? { ca: o.ca } : {}) },
        (res) => {
          let n = 0
          let settled = false
          const done = () => {
            if (settled) return
            settled = true
            // A body cut off by the deadline is a timeout, not an answer.
            if (signal.aborted) return reject(Object.assign(new Error('timeout'), { code: 'ETIMEDOUT' }))
            const auth = res.headers['www-authenticate']
            resolve({ status: res.statusCode ?? 0, location: res.headers.location, authenticate: Array.isArray(auth) ? auth[0] : auth })
          }
          res.on('data', (d: Buffer) => {
            n += d.length
            if (n > MAX_BODY) res.destroy()
          })
          res.on('end', done)
          res.on('close', done)
          res.on('error', done)
        },
      )
      req.on('error', reject)
    })
}

/** The default request: the system's trusted certificates. */
export const probeRequest: ProbeFetch = createProbeRequest()

/**
 * `https://<host[:port]>/v2/` from a rule's `match`; the path of the match is
 * ignored. Plain http is never probed: the server does not use such a rule.
 */
export function probeUrl(match: string): string | undefined {
  if (/^http:\/\//i.test(match.trim())) return undefined
  const rest = match.trim().replace(/^https:\/\//i, '')
  const hostPort = rest.split('/')[0]
  if (hostPort === '') return undefined
  try {
    const u = new URL(`https://${hostPort}/v2/`)
    return u.username || u.password ? undefined : u.href
  } catch {
    return undefined
  }
}

function authorization(rule: RegistryCredentialRule, secret: string | undefined): Record<string, string> {
  if (secret === undefined) return {}
  if (rule.kind === 'bearer') return { Authorization: `Bearer ${secret}` }
  return { Authorization: `Basic ${Buffer.from(`${rule.username ?? ''}:${secret}`, 'utf8').toString('base64')}` }
}

export async function probeRegistry(
  rule: RegistryCredentialRule,
  secret: string | undefined,
  fetchImpl: ProbeFetch = probeRequest,
  o: { timeoutMs?: number } = {},
): Promise<RegistryProbeResult> {
  const first = probeUrl(rule.match)
  if (!first) return { result: 'other' }
  const origin = new URL(first).origin
  const headers = authorization(rule, secret)
  // One deadline for the whole probe, redirects included.
  const deadline = new AbortController()
  const timer = setTimeout(() => deadline.abort(), o.timeoutMs ?? TEST_TIMEOUT_MS)
  try {
    let url = first
    for (let hop = 0; hop <= MAX_HOPS; hop++) {
      const a = await fetchImpl(url, headers, deadline.signal)
      if (deadline.signal.aborted) return { result: 'unreachable' }
      if (a.status >= 300 && a.status <= 399 && a.location) {
        let next: URL
        try {
          next = new URL(a.location, url)
        } catch {
          return { result: 'other', status: a.status }
        }
        // The credential goes to the origin of the rule and nowhere else; a
        // move from https to http is another origin too.
        if (next.origin !== origin) return { result: 'other', status: a.status }
        url = next.href
        continue
      }
      if (a.status >= 200 && a.status <= 299) return { result: 'ok', status: a.status }
      // A token registry answers every /v2/ request without a token with 401 and
      // a Bearer challenge. The server gets the token from the realm the
      // challenge names; the probe does not send the credential there.
      if (a.status === 401 && /^\s*bearer\b/i.test(a.authenticate ?? '')) return { result: 'token', status: a.status }
      if (a.status === 401 || a.status === 403) return { result: 'unauthorized', status: a.status }
      return { result: 'other', status: a.status }
    }
    return { result: 'other' }
  } catch (e) {
    if (deadline.signal.aborted) return { result: 'unreachable' }
    // Only the error code is read; the message may hold anything.
    const c = classifyNetworkError(e)
    return c.status === 'tls' ? { result: 'tls' } : c.status === 'unreachable' ? { result: 'unreachable' } : { result: 'other' }
  } finally {
    clearTimeout(timer)
  }
}
