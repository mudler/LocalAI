import http from 'node:http'
import fs from 'node:fs'
import path from 'node:path'
import type { AddressInfo } from 'node:net'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { downloadToFile } from '../src/main/atomicInstall'
import { GithubReleasesProvider, RateLimitError } from '../src/main/updateProvider'
import { listModels } from '../src/main/models'
import { FAKE_SERVER, tmpDir } from './helpers'

let server: http.Server
let base: string
let seen: { url: string; headers: http.IncomingHttpHeaders }[]
let handler: (req: http.IncomingMessage, res: http.ServerResponse) => void

beforeEach(async () => {
  seen = []
  handler = () => undefined
  server = http.createServer((req, res) => {
    seen.push({ url: req.url ?? '', headers: req.headers })
    handler(req, res)
  })
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r))
  base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`
})
afterEach(() => new Promise<void>((r) => server.close(() => r())))

const release = (extra = {}) => ({
  tag_name: 'v4.2.1',
  prerelease: false,
  assets: [
    { name: 'local-ai-v4.2.1-linux-amd64', browser_download_url: `${base}/dl/local-ai-v4.2.1-linux-amd64`, size: 10 },
    { name: 'local-ai-v4.2.1-darwin-arm64', browser_download_url: `${base}/dl/local-ai-v4.2.1-darwin-arm64`, size: 10 },
    { name: 'LocalAI-v4.2.1-checksums.txt', browser_download_url: `${base}/dl/LocalAI-v4.2.1-checksums.txt`, size: 5 },
  ],
  ...extra,
})

describe('GithubReleasesProvider', () => {
  it('finds the latest release, the platform asset and the checksum file', async () => {
    handler = (_q, res) => res.end(JSON.stringify(release()))
    const p = new GithubReleasesProvider({ owner: 'o', repo: 'r', apiBase: base })
    const rel = await p.latestRelease()
    expect(seen[0].url).toBe('/repos/o/r/releases/latest')
    expect(seen[0].headers.authorization).toBeUndefined() // no token needed
    expect(seen[0].headers['user-agent']).toBe('localai-launcher')
    expect(rel.version).toBe('4.2.1')
    expect(p.resolveAsset(rel, { os: 'linux', arch: 'amd64' })?.name).toBe('local-ai-v4.2.1-linux-amd64')
    expect(p.resolveAsset(rel, { os: 'linux', arch: 'arm64' })).toBeUndefined()
    expect(p.resolveChecksumFile(rel)?.name).toBe('LocalAI-v4.2.1-checksums.txt')
  })

  it('reports no checksum file when the release has none', async () => {
    handler = (_q, res) => res.end(JSON.stringify(release({ assets: [] })))
    const p = new GithubReleasesProvider({ owner: 'o', repo: 'r', apiBase: base })
    expect(p.resolveChecksumFile(await p.latestRelease())).toBeUndefined()
  })

  it('sends a token only when one is configured', async () => {
    handler = (_q, res) => res.end(JSON.stringify(release()))
    await new GithubReleasesProvider({ owner: 'o', repo: 'r', apiBase: base, token: 't' }).latestRelease()
    expect(seen[0].headers.authorization).toBe('Bearer t')
  })

  it('raises a typed error on the rate limit', async () => {
    handler = (_q, res) => {
      res.statusCode = 403
      res.setHeader('x-ratelimit-remaining', '0')
      res.setHeader('x-ratelimit-reset', '1900000000')
      res.end('{}')
    }
    const p = new GithubReleasesProvider({ owner: 'o', repo: 'r', apiBase: base })
    const err = await p.latestRelease().catch((e) => e)
    expect(err).toBeInstanceOf(RateLimitError)
    expect((err as RateLimitError).resetAt?.getTime()).toBe(1900000000 * 1000)
  })

  it('fails on other statuses and bad JSON', async () => {
    const p = new GithubReleasesProvider({ owner: 'o', repo: 'r', apiBase: base })
    handler = (_q, res) => { res.statusCode = 404; res.end() }
    await expect(p.latestRelease()).rejects.toThrow(/HTTP 404/)
    handler = (_q, res) => res.end('<html>')
    await expect(p.latestRelease()).rejects.toThrow(/invalid JSON/)
    handler = (_q, res) => res.end('{}')
    await expect(p.latestRelease()).rejects.toThrow(/no tag/)
  })
})

describe('downloadToFile and listModels', () => {
  it('downloads through a part file and reports progress', async () => {
    const body = Buffer.alloc(100_000, 7)
    handler = (_q, res) => { res.setHeader('content-length', String(body.length)); res.end(body) }
    const dest = path.join(tmpDir(), 'f')
    let last = 0
    await downloadToFile(`${base}/x`, dest, (d) => (last = d))
    expect(fs.readFileSync(dest).equals(body)).toBe(true)
    expect(last).toBe(body.length)
    expect(fs.existsSync(`${dest}.part`)).toBe(false)
  })

  it('leaves no file behind on an HTTP error', async () => {
    handler = (_q, res) => { res.statusCode = 500; res.end('no') }
    const dest = path.join(tmpDir(), 'f')
    await expect(downloadToFile(`${base}/x`, dest)).rejects.toThrow(/HTTP 500/)
    expect(fs.readdirSync(path.dirname(dest))).toEqual([])
  })

  it('lists model ids, and returns [] for a server that is not there', async () => {
    handler = (_q, res) => res.end(JSON.stringify({ data: [{ id: 'a' }, { id: 'b/c' }, {}] }))
    expect(await listModels(base)).toEqual(['a', 'b/c'])
    expect(await listModels('http://127.0.0.1:1')).toEqual([])
    expect(FAKE_SERVER).toBeTruthy()
  })
})
