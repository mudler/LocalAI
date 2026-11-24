// Where releases come from. No Electron import.
import http from 'node:http'
import https from 'node:https'
import { exeSuffix, type PlatformTarget } from './platform'
import { DEV_CHECKSUMS_FALLBACK, DEV_RELEASE_TAG, devAssetName, devChecksumName, parseDevBuild, type DevBuild } from './devFeed'

export interface ReleaseAsset {
  name: string
  url: string
  size?: number
}

export interface ReleaseInfo {
  tag: string
  version: string
  prerelease: boolean
  assets: ReleaseAsset[]
  /** Set for the rolling development build: its identity, read from the release text. */
  build?: DevBuild
}

export type { PlatformTarget } from './platform'

/**
 * The release asset of the server binary, as GoReleaser names it
 * (.goreleaser.yaml, `formats: [binary]`): local-ai-v<version>-<os>-<arch>,
 * with `.exe` for Windows.
 */
export function serverAssetName(version: string, t: PlatformTarget): string {
  return `local-ai-v${version}-${t.os}-${t.arch}${exeSuffix(t)}`
}

export interface UpdateProvider {
  /** The newest stable release. */
  latestRelease(): Promise<ReleaseInfo>
  /** The server binary of `release` for `target`, or undefined. */
  resolveAsset(release: ReleaseInfo, target: PlatformTarget): ReleaseAsset | undefined
  /** The checksum list of `release` (for `target`, when the release has one per file), or undefined. */
  resolveChecksumFile(release: ReleaseInfo, target?: PlatformTarget): ReleaseAsset | undefined
}

export class RateLimitError extends Error {
  constructor(readonly resetAt?: Date) {
    super('GitHub API rate limit reached; try again later')
    this.name = 'RateLimitError'
  }
}

export interface GithubOptions {
  owner: string
  repo: string
  /** API root. Default https://api.github.com; tests point it at a local server. */
  apiBase?: string
  /** Optional token. Never required; only raises the rate limit. */
  token?: string
  userAgent?: string
}

interface GhRelease {
  tag_name?: string
  prerelease?: boolean
  draft?: boolean
  name?: string
  body?: string
  published_at?: string
  assets?: { name?: string; browser_download_url?: string; size?: number }[]
}

function getJson(url: string, headers: Record<string, string>): Promise<{ status: number; headers: http.IncomingHttpHeaders; body: string }> {
  return new Promise((resolve, reject) => {
    const mod = url.startsWith('https:') ? https : http
    const req = mod.get(url, { headers, timeout: 15000 }, (res) => {
      let body = ''
      res.setEncoding('utf8').on('data', (d) => (body += d))
      res.on('end', () => resolve({ status: res.statusCode ?? 0, headers: res.headers, body }))
    })
    req.on('timeout', () => req.destroy(new Error('request timed out')))
    req.on('error', reject)
  })
}

function toRelease(j: GhRelease): ReleaseInfo {
  if (!j.tag_name) throw new Error('release has no tag')
  const assets: ReleaseAsset[] = []
  for (const a of j.assets ?? []) {
    if (a.name && a.browser_download_url) assets.push({ name: a.name, url: a.browser_download_url, size: a.size })
  }
  return { tag: j.tag_name, version: j.tag_name.replace(/^v/, ''), prerelease: !!j.prerelease, assets }
}

export class GithubReleasesProvider implements UpdateProvider {
  constructor(private readonly o: GithubOptions) {}

  private async api(pathAndQuery: string): Promise<unknown> {
    const base = (this.o.apiBase ?? 'https://api.github.com').replace(/\/+$/, '')
    const headers: Record<string, string> = {
      Accept: 'application/vnd.github+json',
      'User-Agent': this.o.userAgent ?? 'localai-launcher',
    }
    if (this.o.token) headers.Authorization = `Bearer ${this.o.token}`
    const res = await getJson(`${base}/repos/${this.o.owner}/${this.o.repo}${pathAndQuery}`, headers)
    if ((res.status === 403 || res.status === 429) && res.headers['x-ratelimit-remaining'] === '0') {
      const reset = Number(res.headers['x-ratelimit-reset'])
      throw new RateLimitError(Number.isFinite(reset) ? new Date(reset * 1000) : undefined)
    }
    if (res.status !== 200) throw new Error(`release lookup failed: HTTP ${res.status}`)
    try {
      return JSON.parse(res.body) as unknown
    } catch {
      throw new Error('release lookup returned invalid JSON')
    }
  }

  async latestRelease(): Promise<ReleaseInfo> {
    return toRelease((await this.api('/releases/latest')) as GhRelease)
  }

  /** The newest published release, pre-releases included. Drafts are skipped. */
  async latestIncludingPrerelease(): Promise<ReleaseInfo> {
    const list = await this.api('/releases?per_page=20')
    if (!Array.isArray(list)) throw new Error('release list is not an array')
    const first = (list as GhRelease[]).find((r) => r && !r.draft && r.tag_name)
    if (!first) throw new Error('no published release found')
    return toRelease(first)
  }

  /** The rolling development build (see devFeed.ts). Fails when the release has no build identity. */
  async latestDevelopment(): Promise<ReleaseInfo> {
    const j = (await this.api(`/releases/tags/${DEV_RELEASE_TAG}`)) as GhRelease
    const rel = toRelease(j)
    const build = parseDevBuild(j)
    if (!build) throw new Error('the development release names no commit')
    return { ...rel, version: build.id, prerelease: true, build }
  }

  resolveAsset(release: ReleaseInfo, t: PlatformTarget): ReleaseAsset | undefined {
    if (release.build) return release.assets.find((a) => a.name === devAssetName(t))
    const want = serverAssetName(release.version, t)
    return release.assets.find((a) => a.name === want)
  }

  resolveChecksumFile(release: ReleaseInfo, t?: PlatformTarget): ReleaseAsset | undefined {
    if (release.build) {
      const own = t ? release.assets.find((a) => a.name === devChecksumName(t)) : undefined
      return own ?? release.assets.find((a) => a.name === DEV_CHECKSUMS_FALLBACK)
    }
    return release.assets.find((a) => /checksums\.txt$/i.test(a.name))
  }
}

// ---- releases/latest redirect ----------------------------------------------

export interface RedirectOptions {
  owner: string
  repo: string
  /** Web root. Default https://github.com; tests point it at a local server. */
  webBase?: string
  userAgent?: string
  /** All server binaries a release carries, so the asset list can be built without the API. */
  targets?: PlatformTarget[]
}

const ALL_TARGETS: PlatformTarget[] = [
  { os: 'linux', arch: 'amd64' },
  { os: 'linux', arch: 'arm64' },
  { os: 'darwin', arch: 'amd64' },
  { os: 'darwin', arch: 'arm64' },
  // The release has no windows/arm64 server build.
  { os: 'windows', arch: 'amd64' },
]

function getRedirect(url: string, headers: Record<string, string>): Promise<{ status: number; location?: string }> {
  return new Promise((resolve, reject) => {
    const mod = url.startsWith('https:') ? https : http
    // GET, not HEAD: the redirect is the same and some proxies mishandle HEAD.
    const req = mod.get(url, { headers, timeout: 15000 }, (res) => {
      res.resume()
      resolve({ status: res.statusCode ?? 0, location: res.headers.location })
    })
    req.on('timeout', () => req.destroy(new Error('request timed out')))
    req.on('error', reject)
  })
}

/**
 * Port of the Go launcher's first lookup step. `<web>/<owner>/<repo>/releases/latest`
 * answers with a redirect to `.../releases/tag/<tag>`. That page is not under
 * the API's unauthenticated limit of 60 requests per hour per IP, which a
 * shared or NAT address reaches quickly. The asset list is built from the
 * release naming rules, because the redirect carries only the tag.
 */
export async function resolveLatestByRedirect(o: RedirectOptions): Promise<ReleaseInfo> {
  const web = (o.webBase ?? 'https://github.com').replace(/\/+$/, '')
  const from = `${web}/${o.owner}/${o.repo}/releases/latest`
  const res = await getRedirect(from, { 'User-Agent': o.userAgent ?? 'localai-launcher' })
  if (res.status < 300 || res.status > 399 || !res.location) throw new Error(`no release redirect (HTTP ${res.status})`)
  const to = new URL(res.location, from)
  const prefix = `/${o.owner}/${o.repo}/releases/tag/`
  if (!to.pathname.startsWith(prefix)) throw new Error('release redirect points somewhere else')
  const tag = decodeURIComponent(to.pathname.slice(prefix.length))
  if (!/^v?\d+\.\d+\.\d+[0-9A-Za-z.+-]*$/.test(tag)) throw new Error('release redirect has no version tag')
  const version = tag.replace(/^v/, '')
  const dl = `${web}/${o.owner}/${o.repo}/releases/download/${encodeURIComponent(tag)}`
  const assets: ReleaseAsset[] = (o.targets ?? ALL_TARGETS).map((t) => {
    const name = serverAssetName(version, t)
    return { name, url: `${dl}/${name}` }
  })
  const sums = `LocalAI-${tag}-checksums.txt`
  assets.push({ name: sums, url: `${dl}/${sums}` })
  // github.com never redirects releases/latest to a pre-release.
  return { tag, version, prerelease: false, assets }
}

/**
 * The provider the launcher uses: the redirect first, the API when the
 * redirect fails. Pre-releases need the API (the redirect skips them).
 */
export class RedirectFirstProvider implements UpdateProvider {
  constructor(
    private readonly redirect: RedirectOptions,
    private readonly api: GithubReleasesProvider,
    private readonly log: (message: string) => void = () => undefined,
  ) {}

  async latestRelease(): Promise<ReleaseInfo> {
    let redirectErr: unknown
    try {
      return await resolveLatestByRedirect(this.redirect)
    } catch (e) {
      redirectErr = e
      this.log(`release redirect failed (${(e as Error).message}); trying the API`)
    }
    try {
      return await this.api.latestRelease()
    } catch (e) {
      if (e instanceof RateLimitError) throw e
      throw new Error(`${(e as Error).message} (redirect: ${(redirectErr as Error).message})`)
    }
  }

  latestIncludingPrerelease(): Promise<ReleaseInfo> {
    return this.api.latestIncludingPrerelease()
  }

  resolveAsset(release: ReleaseInfo, target: PlatformTarget): ReleaseAsset | undefined {
    return this.api.resolveAsset(release, target)
  }

  resolveChecksumFile(release: ReleaseInfo, target?: PlatformTarget): ReleaseAsset | undefined {
    return this.api.resolveChecksumFile(release, target)
  }

  latestDevelopment(): Promise<ReleaseInfo> {
    return this.api.latestDevelopment()
  }
}

/** A release version as the UI shows it: digits and dots, an optional pre-release suffix, an optional leading v. */
const RELEASE_VERSION = /^v?(\d{1,6}\.\d{1,6}\.\d{1,6}(?:-[0-9A-Za-z.-]{1,64})?)$/

/** The release page of `version` on GitHub, or undefined when `version` is not a plain release version. */
export function releasePageUrl(repo: { owner: string; repo: string }, version: string): string | undefined {
  const m = RELEASE_VERSION.exec(version)
  if (!m) return undefined
  return `https://github.com/${repo.owner}/${repo.repo}/releases/tag/v${m[1]}`
}
