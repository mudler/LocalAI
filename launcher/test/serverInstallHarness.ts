// Fakes for the server install tests: a feed served by the fake release
// server, a fake provider, a fake supervisor and a few fetch fakes.
import { spawn, type ChildProcess } from 'node:child_process'
import { createHash } from 'node:crypto'
import fs from 'node:fs'
import net from 'node:net'
import path from 'node:path'
import { GithubReleasesProvider, type PlatformTarget, type ReleaseInfo } from '../src/main/updateProvider'
import type { InstallDeps, SupervisorHandle } from '../src/main/serverInstall'
import { nodeInstallFs } from '../src/main/serverInstall'
import type { ServerState } from '../src/shared/types'
import { freePort, tmpDir } from './helpers'

export const TARGET: PlatformTarget = { os: 'linux', arch: 'amd64' }

/** An ELF-looking file that says which version it is. `extra` can add markers such as BROKEN. */
export function bin(version: string, extra = ''): Buffer {
  const b = Buffer.alloc(4096, 0x41)
  Buffer.from([0x7f, 0x45, 0x4c, 0x46]).copy(b, 0)
  b.fill(0, 8, 11)
  Buffer.from(`\nVERSION=${version}\n${extra}\n`).copy(b, 100)
  return b
}

export const sha = (b: Buffer) => createHash('sha256').update(b).digest('hex')

export function readVersionFromFile(file: string): string {
  const m = /VERSION=(\S+)/.exec(fs.readFileSync(file, 'latin1'))
  if (!m) throw new Error('no version in binary')
  return `v${m[1]} (test)`
}

export class FeedServer {
  readonly dir = tmpDir('feed-')
  private child?: ChildProcess
  port = 0
  private log: string
  constructor() {
    this.log = path.join(this.dir, '..', `${path.basename(this.dir)}.log`)
  }
  async start(): Promise<void> {
    this.port = await freePort()
    fs.writeFileSync(this.log, '')
    const script = path.resolve(__dirname, '../scripts/fake-release-server.mjs')
    this.child = spawn(process.execPath, [script, String(this.port), this.dir, this.log], { stdio: 'ignore' })
    for (let i = 0; i < 100; i++) {
      const ok = await new Promise<boolean>((r) => {
        const s = net.connect(this.port, '127.0.0.1', () => (s.destroy(), r(true)))
        s.on('error', () => r(false))
      })
      if (ok) return
      await new Promise((r) => setTimeout(r, 50))
    }
    throw new Error('fake release server did not start')
  }
  stop(): void {
    this.child?.kill()
    fs.rmSync(this.dir, { recursive: true, force: true })
    fs.rmSync(this.log, { force: true })
  }
  requests(): string[] {
    return fs.readFileSync(this.log, 'utf8').split('\n').filter(Boolean)
  }
  requestsFor(id: string): string[] {
    return this.requests().filter((l) => l.includes(`/${id}/`))
  }
}

export type ChecksumMode = 'good' | 'bad' | 'missing' | 'listed-404' | 'malformed' | 'no-entry'

let counter = 0

/** Publish one release into the feed directory and describe it. */
export function publish(feed: FeedServer, tag: string, binary: Buffer, o: { checksum?: ChecksumMode; prerelease?: boolean } = {}): ReleaseInfo & { id: string } {
  const id = `r${++counter}`
  const version = tag.replace(/^v/, '')
  const name = `local-ai-v${version}-linux-amd64`
  const dir = path.join(feed.dir, id)
  fs.mkdirSync(dir, { recursive: true })
  fs.writeFileSync(path.join(dir, name), binary)
  const base = `http://127.0.0.1:${feed.port}/${id}`
  const assets = [{ name, url: `${base}/${name}`, size: binary.length }]
  const mode = o.checksum ?? 'good'
  const csName = `LocalAI-${tag}-checksums.txt`
  const sumFor = (b: Buffer) => `${sha(b)}  ${name}\n`
  if (mode === 'good') fs.writeFileSync(path.join(dir, csName), `${'0'.repeat(64)}  other-file\n${sumFor(binary)}`)
  if (mode === 'bad') fs.writeFileSync(path.join(dir, csName), sumFor(Buffer.from('something else')))
  if (mode === 'malformed') fs.writeFileSync(path.join(dir, csName), 'this is not a checksum list\n')
  if (mode === 'no-entry') fs.writeFileSync(path.join(dir, csName), `${sha(binary)}  some-other-asset\n`)
  if (mode !== 'missing') assets.push({ name: csName, url: `${base}/${csName}`, size: 100 })
  return { id, tag, version, prerelease: o.prerelease ?? false, assets }
}

export class FakeProvider {
  lookups = 0
  failures: Error[] = []
  private real = new GithubReleasesProvider({ owner: 'o', repo: 'r' })
  constructor(public release: ReleaseInfo) {}
  async latestRelease(): Promise<ReleaseInfo> {
    this.lookups++
    const f = this.failures.shift()
    if (f) throw f
    return this.release
  }
  resolveAsset = (r: ReleaseInfo, t: PlatformTarget) => this.real.resolveAsset(r, t)
  resolveChecksumFile = (r: ReleaseInfo) => this.real.resolveChecksumFile(r)
}

export class FakeSupervisor implements SupervisorHandle {
  state: ServerState = 'stopped'
  calls: string[] = []
  /** Decides the state after start(). Default: ready. */
  onStart: () => ServerState = () => 'ready'
  onStop?: () => void
  async start() {
    this.calls.push('start')
    this.state = this.onStart()
  }
  async stop() {
    this.calls.push('stop')
    this.onStop?.()
    this.state = 'stopped'
  }
}

export function streamResponse(chunks: Buffer[], o: { failAfter?: number; gate?: Promise<void>; contentLength?: number } = {}): Response {
  let i = 0
  const body = new ReadableStream<Uint8Array>({
    async pull(c) {
      if (o.gate && i === 1) await o.gate
      if (o.failAfter !== undefined && i >= o.failAfter) return c.error(Object.assign(new Error('connection reset'), { code: 'ECONNRESET' }))
      if (i >= chunks.length) return c.close()
      c.enqueue(chunks[i++])
    },
  })
  const total = o.contentLength ?? chunks.reduce((n, b) => n + b.length, 0)
  return new Response(body, { status: 200, headers: { 'content-length': String(total) } })
}

export interface Rig {
  root: string
  binDir: string
  metaDir: string
  downloadDir: string
  target: string
  deps: InstallDeps
  provider: FakeProvider
  supervisor: FakeSupervisor
  sleeps: number[]
  versionCalls: string[]
  clock: { t: number }
}

const rigRoots: string[] = []
/** Remove every rig directory made so far. */
export function cleanupRigs(): void {
  for (const r of rigRoots.splice(0)) fs.rmSync(r, { recursive: true, force: true })
}

export function makeRig(release: ReleaseInfo, over: Partial<InstallDeps> = {}): Rig {
  const root = tmpDir('install-')
  rigRoots.push(root)
  const binDir = path.join(root, 'bin')
  const metaDir = path.join(root, 'meta')
  const downloadDir = path.join(root, 'dl')
  fs.mkdirSync(binDir)
  const provider = new FakeProvider(release)
  const supervisor = new FakeSupervisor()
  const sleeps: number[] = []
  const versionCalls: string[] = []
  const clock = { t: 0 }
  const deps: InstallDeps = {
    provider,
    fetch,
    fs: nodeInstallFs,
    sleep: async (ms) => void sleeps.push(ms),
    now: () => clock.t,
    supervisor,
    target: TARGET,
    paths: { binDir, metaDir, downloadDir },
    runVersion: async (f) => (versionCalls.push(f), readVersionFromFile(f)),
    ...over,
  }
  return { root, binDir, metaDir, downloadDir, target: path.join(binDir, 'local-ai'), deps, provider, supervisor, sleeps, versionCalls, clock }
}

export function installOld(rig: Rig, version: string, extra = ''): Buffer {
  const b = bin(version, extra)
  fs.writeFileSync(rig.target, b, { mode: 0o755 })
  return b
}

export const ls = (dir: string) => (fs.existsSync(dir) ? fs.readdirSync(dir).sort() : [])

export interface DevPublish {
  sha: string
  date?: string
  subject?: string
  /** `per-file`: name.sha256 (hash and name); `hash-only`; `combined`: SHA256SUMS; `missing`; `bad`. */
  checksum?: 'per-file' | 'hash-only' | 'combined' | 'missing' | 'bad'
}

/** Publish the rolling development release into the feed the way the CI job does, and return the API base. */
export function publishDev(feed: FeedServer, binary: Buffer, o: DevPublish): { apiBase: string } {
  const owner = 'o'
  const repo = 'r'
  const name = `local-ai-master-${TARGET.os}-${TARGET.arch}`
  const rel = path.join(feed.dir, 'download', 'dev-master')
  fs.mkdirSync(rel, { recursive: true })
  fs.writeFileSync(path.join(rel, name), binary)
  const base = `http://127.0.0.1:${feed.port}/download/dev-master`
  const assets = [{ name, browser_download_url: `${base}/${name}`, size: binary.length }]
  const mode = o.checksum ?? 'per-file'
  const hash = mode === 'bad' ? sha(Buffer.from('other')) : sha(binary)
  const add = (n: string, text: string) => {
    fs.writeFileSync(path.join(rel, n), text)
    assets.push({ name: n, browser_download_url: `${base}/${n}`, size: text.length })
  }
  if (mode === 'per-file' || mode === 'bad') add(`${name}.sha256`, `${hash}  ${name}\n`)
  if (mode === 'hash-only') add(`${name}.sha256`, `${hash}\n`)
  if (mode === 'combined') add('SHA256SUMS', `${hash}  ${name}\n`)
  const body = `Commit: ${o.sha}\nDate: ${o.date ?? '2026-11-15'}\nSubject: ${o.subject ?? 'Fix a thing'}\n`
  const api = path.join(feed.dir, 'repos', owner, repo, 'releases', 'tags')
  fs.mkdirSync(api, { recursive: true })
  fs.writeFileSync(path.join(api, 'dev-master'), JSON.stringify({ tag_name: 'dev-master', name: 'Development build', body, prerelease: true, draft: false, assets }))
  return { apiBase: `http://127.0.0.1:${feed.port}` }
}

export function devProvider(apiBase: string): GithubReleasesProvider {
  return new GithubReleasesProvider({ owner: 'o', repo: 'r', apiBase })
}
