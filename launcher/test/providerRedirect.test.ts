import fs from 'node:fs'
import http from 'node:http'
import type { AddressInfo } from 'node:net'
import path from 'node:path'
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { ServerInstaller } from '../src/main/serverInstall'
import { GithubReleasesProvider, RateLimitError, RedirectFirstProvider, resolveLatestByRedirect } from '../src/main/updateProvider'
import { FeedServer, TARGET, bin, cleanupRigs, makeRig, sha } from './serverInstallHarness'

const feed = new FeedServer()
beforeAll(() => feed.start())
afterAll(() => {
  feed.stop()
  cleanupRigs()
})

const web = () => `http://127.0.0.1:${feed.port}`
const put = (rel: string, data: string | Buffer) => {
  const f = path.join(feed.dir, rel)
  fs.mkdirSync(path.dirname(f), { recursive: true })
  fs.writeFileSync(f, data)
}
let n = 0
/** A fresh owner/repo pair in the feed, so tests do not see each other's files. */
const repo = () => ({ owner: 'example', repo: `r${++n}` })

describe('resolveLatestByRedirect', () => {
  it('reads the tag from the releases/latest redirect and builds the asset list', async () => {
    const r = repo()
    put(`${r.owner}/${r.repo}/releases/latest.redirect`, `${web()}/${r.owner}/${r.repo}/releases/tag/v3.2.0`)
    const rel = await resolveLatestByRedirect({ ...r, webBase: web() })
    expect(rel).toMatchObject({ tag: 'v3.2.0', version: '3.2.0', prerelease: false })
    const api = new GithubReleasesProvider({ ...r })
    expect(api.resolveAsset(rel, TARGET)?.url).toBe(`${web()}/${r.owner}/${r.repo}/releases/download/v3.2.0/local-ai-v3.2.0-linux-amd64`)
    expect(api.resolveChecksumFile(rel)?.name).toBe('LocalAI-v3.2.0-checksums.txt')
    expect(feed.requests().some((l) => l.includes('/repos/'))).toBe(false)
  })

  it('accepts a relative Location', async () => {
    const r = repo()
    put(`${r.owner}/${r.repo}/releases/latest.redirect`, `/${r.owner}/${r.repo}/releases/tag/v1.0.1`)
    expect((await resolveLatestByRedirect({ ...r, webBase: web() })).tag).toBe('v1.0.1')
  })

  it('rejects no redirect, a redirect elsewhere and a tag that is not a version', async () => {
    const r = repo()
    await expect(resolveLatestByRedirect({ ...r, webBase: web() })).rejects.toThrow(/no release redirect/)
    put(`${r.owner}/${r.repo}/releases/latest.redirect`, `/someone/else/releases/tag/v1.0.0`)
    await expect(resolveLatestByRedirect({ ...r, webBase: web() })).rejects.toThrow(/somewhere else/)
    put(`${r.owner}/${r.repo}/releases/latest.redirect`, `/${r.owner}/${r.repo}/releases/tag/nightly`)
    await expect(resolveLatestByRedirect({ ...r, webBase: web() })).rejects.toThrow(/no version tag/)
  })
})

describe('RedirectFirstProvider', () => {
  it('installs the latest release found by the redirect without touching the API', async () => {
    const r = repo()
    const b = bin('3.3.0')
    const name = 'local-ai-v3.3.0-linux-amd64'
    put(`${r.owner}/${r.repo}/releases/latest.redirect`, `/${r.owner}/${r.repo}/releases/tag/v3.3.0`)
    put(`${r.owner}/${r.repo}/releases/download/v3.3.0/${name}`, b)
    put(`${r.owner}/${r.repo}/releases/download/v3.3.0/LocalAI-v3.3.0-checksums.txt`, `${sha(b)}  ${name}\n`)
    const provider = new RedirectFirstProvider(
      { ...r, webBase: web() },
      new GithubReleasesProvider({ ...r, apiBase: `${web()}/api` }),
    )
    const rig = makeRig({ tag: 'unused', version: 'unused', prerelease: false, assets: [] }, { provider })
    const end = await new ServerInstaller(rig.deps).install()
    expect(end).toMatchObject({ kind: 'done', version: '3.3.0' })
    expect(fs.readFileSync(rig.target).equals(b)).toBe(true)
    expect(feed.requests().filter((l) => l.includes(`/api/repos/${r.owner}/${r.repo}`))).toEqual([])
  })

  it('falls back to the API when the redirect fails', async () => {
    const r = repo()
    put(`api/repos/${r.owner}/${r.repo}/releases/latest`, JSON.stringify({ tag_name: 'v2.0.0', assets: [] }))
    const logs: string[] = []
    const p = new RedirectFirstProvider({ ...r, webBase: web() }, new GithubReleasesProvider({ ...r, apiBase: `${web()}/api` }), (m) => logs.push(m))
    expect((await p.latestRelease()).tag).toBe('v2.0.0')
    expect(logs.join('\n')).toMatch(/trying the API/)
  })

  it('names both failures when both lookups fail', async () => {
    const r = repo()
    const p = new RedirectFirstProvider({ ...r, webBase: web() }, new GithubReleasesProvider({ ...r, apiBase: `${web()}/api` }))
    await expect(p.latestRelease()).rejects.toThrow(/HTTP 404 \(redirect: no release redirect/)
  })
})

describe('pre-release lookup', () => {
  let server: http.Server
  let base = ''
  let body = '[]'
  let rateLimited = false
  beforeAll(async () => {
    server = http.createServer((req, res) => {
      if (rateLimited) {
        res.statusCode = 403
        res.setHeader('x-ratelimit-remaining', '0')
        return res.end('{}')
      }
      if (req.url !== '/repos/o/r/releases?per_page=20') {
        res.statusCode = 404
        return res.end()
      }
      res.end(body)
    })
    await new Promise<void>((ok) => server.listen(0, '127.0.0.1', ok))
    base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`
  })
  afterAll(() => new Promise<void>((ok) => server.close(() => ok())))
  afterEach(() => {
    rateLimited = false
  })

  it('returns the newest published release, skipping drafts, through the redirect-first provider too', async () => {
    body = JSON.stringify([
      { tag_name: 'v4.0.0-rc2', draft: true, assets: [] },
      { tag_name: 'v4.0.0-rc1', prerelease: true, assets: [] },
      { tag_name: 'v3.9.0', assets: [] },
    ])
    const api = new GithubReleasesProvider({ owner: 'o', repo: 'r', apiBase: base })
    expect(await api.latestIncludingPrerelease()).toMatchObject({ tag: 'v4.0.0-rc1', prerelease: true })
    const p = new RedirectFirstProvider({ owner: 'o', repo: 'r', webBase: base }, api)
    expect((await p.latestIncludingPrerelease()).tag).toBe('v4.0.0-rc1')
  })

  it('fails on an empty or wrong list and passes the rate limit error on', async () => {
    const api = new GithubReleasesProvider({ owner: 'o', repo: 'r', apiBase: base })
    body = '[]'
    await expect(api.latestIncludingPrerelease()).rejects.toThrow(/no published release/)
    body = '{}'
    await expect(api.latestIncludingPrerelease()).rejects.toThrow(/not an array/)
    rateLimited = true
    await expect(api.latestIncludingPrerelease()).rejects.toBeInstanceOf(RateLimitError)
  })
})
