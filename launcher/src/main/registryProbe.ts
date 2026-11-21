// Checks one registry credential rule from the main process. No Electron import.
//
// Unlike testConnection.ts, this request carries the rule's credential, so it
// is stricter about where it goes: only the origin named by the rule, never a
// redirect to another origin, never a skipped certificate check. The secret is
// read in the main process; the result and every error stay free of it, of the
// header and of the response body.
import http from 'node:http'
import https from 'node:https'
import type { RegistryCredentialRule } from '../shared/types'
import { classifyNetworkError, TEST_TIMEOUT_MS } from './testConnection'

const MAX_BODY = 64 * 1024
const MAX_HOPS = 3

export type RegistryProbeResult = {
  result: 'ok' | 'unauthorized' | 'unreachable' | 'tls' | 'other'
  status?: number
}

/** What the probe needs from the network: one request, one answer. Never follows a redirect. */
export type ProbeFetch = (url: string, headers: Record<string, string>) => Promise<{ status: number; location?: string }>

/** The default request: node http(s), certificate checking on, 8 s limit, body capped at 64 KB. */
export const probeRequest: ProbeFetch = (url, headers) =>
  new Promise((resolve, reject) => {
    const mod = url.startsWith('https:') ? https : http
    const req = mod.get(url, { headers: { ...headers, Accept: 'application/json', 'User-Agent': 'localai-launcher' }, agent: false }, (res) => {
      let n = 0
      let settled = false
      const done = () => {
        if (settled) return
        settled = true
        resolve({ status: res.statusCode ?? 0, location: res.headers.location })
      }
      res.on('data', (d: Buffer) => {
        n += d.length
        if (n > MAX_BODY) res.destroy()
      })
      res.on('end', done)
      res.on('close', done)
      res.on('error', done)
    })
    const timer = setTimeout(() => req.destroy(Object.assign(new Error('timeout'), { code: 'ETIMEDOUT' })), TEST_TIMEOUT_MS)
    req.on('error', (e) => {
      clearTimeout(timer)
      reject(e)
    })
    req.on('close', () => clearTimeout(timer))
  })

/** `<scheme>://<host[:port]>/v2/` from a rule's `match`; the path of the match is ignored. */
export function probeUrl(match: string): string | undefined {
  const plain = /^http:\/\//i.test(match)
  const rest = match.trim().replace(/^https?:\/\//i, '')
  const hostPort = rest.split('/')[0]
  if (hostPort === '') return undefined
  try {
    const u = new URL(`${plain ? 'http' : 'https'}://${hostPort}/v2/`)
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

export async function probeRegistry(rule: RegistryCredentialRule, secret: string | undefined, fetchImpl: ProbeFetch = probeRequest): Promise<RegistryProbeResult> {
  const first = probeUrl(rule.match)
  if (!first) return { result: 'other' }
  const origin = new URL(first).origin
  const headers = authorization(rule, secret)
  try {
    let url = first
    for (let hop = 0; hop <= MAX_HOPS; hop++) {
      const a = await fetchImpl(url, headers)
      if (a.status >= 300 && a.status <= 399 && a.location) {
        let next: URL
        try {
          next = new URL(a.location, url)
        } catch {
          return { result: 'other', status: a.status }
        }
        // The credential goes to the origin of the rule and nowhere else.
        if (next.origin !== origin) return { result: 'other', status: a.status }
        url = next.href
        continue
      }
      if (a.status >= 200 && a.status <= 299) return { result: 'ok', status: a.status }
      if (a.status === 401 || a.status === 403) return { result: 'unauthorized', status: a.status }
      return { result: 'other', status: a.status }
    }
    return { result: 'other' }
  } catch (e) {
    // Only the error code is read; the message may hold anything.
    const c = classifyNetworkError(e)
    return c.status === 'tls' ? { result: 'tls' } : c.status === 'unreachable' ? { result: 'unreachable' } : { result: 'other' }
  }
}
