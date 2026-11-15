import fs from 'node:fs'
import path from 'node:path'
import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { InstallError } from '../src/main/atomicInstall'
import { ServerInstaller, classifyError, execServerVersion, nodeInstallFs, type InstallFs, type InstallState } from '../src/main/serverInstall'
import { RateLimitError } from '../src/main/updateProvider'
import {
  FeedServer,
  cleanupRigs,
  bin,
  installOld,
  ls,
  makeRig,
  publish,
  sha,
  streamResponse,
  type Rig,
} from './serverInstallHarness'

const feed = new FeedServer()
beforeAll(() => feed.start())
afterAll(() => {
  feed.stop()
  cleanupRigs()
})

function watch(inst: ServerInstaller) {
  const seen: InstallState[] = []
  inst.subscribe((s) => seen.push(s))
  return { seen, kinds: () => seen.map((s) => s.kind) }
}
const errno = (code: string) => Object.assign(new Error(`${code}: injected`), { code })
const fsWith = (o: Partial<InstallFs>): InstallFs => ({ ...nodeInstallFs, ...o })
const noLeftovers = (rig: Rig) => {
  expect(ls(rig.downloadDir).filter((f) => !f.startsWith('.'))).toEqual([])
  expect(ls(rig.binDir).filter((f) => f.endsWith('.part') || f.endsWith('.staging') || f.includes('.tmp'))).toEqual([])
}

describe('install: happy paths', () => {
  it('installs on a fresh machine and reports every step', async () => {
    const body = bin('3.1.0')
    const rel = publish(feed, 'v3.1.0', body)
    const rig = makeRig(rel)
    const inst = new ServerInstaller(rig.deps)
    const w = watch(inst)
    const end = await inst.install({ allowPrerelease: false })
    expect(end).toEqual({ kind: 'done', version: '3.1.0', adopted: false })
    expect(w.kinds()).toEqual(['checking', 'update-available', 'downloading', 'downloading', 'verifying', 'installing', 'done'])
    const last = w.seen.filter((s) => s.kind === 'downloading').pop()
    expect(last).toEqual({ kind: 'downloading', done: body.length, total: body.length })
    expect(fs.readFileSync(rig.target).equals(body)).toBe(true)
    expect(fs.statSync(rig.target).mode & 0o777).toBe(0o755)
    expect(ls(rig.binDir)).toEqual(['local-ai'])
    noLeftovers(rig)
    expect(rig.supervisor.calls).toEqual([])
  })

  it('creates the binary folder on a first install', async () => {
    const body = bin('3.1.0')
    const rel = publish(feed, 'v3.1.0', body)
    const rig = makeRig(rel)
    fs.rmSync(rig.binDir, { recursive: true })
    const end = await new ServerInstaller(rig.deps).install({ allowPrerelease: false })
    expect(end).toEqual({ kind: 'done', version: '3.1.0', adopted: false })
    expect(fs.readFileSync(rig.target).equals(body)).toBe(true)
  })

  it('updates, keeps the previous binary as the rollback copy and remembers the version', async () => {
    const rel = publish(feed, 'v3.1.0', bin('3.1.0'))
    const rig = makeRig(rel)
    const old = installOld(rig, '3.0.0')
    const inst = new ServerInstaller(rig.deps)
    const end = await inst.install({ allowPrerelease: false })
    expect(end).toMatchObject({ kind: 'done', version: '3.1.0' })
    expect(fs.readFileSync(path.join(rig.binDir, 'local-ai.previous')).equals(old)).toBe(true)
    expect(ls(rig.binDir)).toEqual(['local-ai', 'local-ai.previous'])
    // A later check reads the metadata and does not run the binary again.
    rig.versionCalls.length = 0
    const again = new ServerInstaller(rig.deps)
    expect(await again.check({ allowPrerelease: false })).toEqual({ kind: 'up-to-date', version: '3.1.0' })
    expect(rig.versionCalls.filter((f) => f === rig.target)).toEqual([])
  })

  it('reports up-to-date and downloads nothing', async () => {
    const rel = publish(feed, 'v3.1.0', bin('3.1.0'))
    const rig = makeRig(rel)
    installOld(rig, '3.1.0')
    const inst = new ServerInstaller(rig.deps)
    expect(await inst.install({ allowPrerelease: false })).toEqual({ kind: 'up-to-date', version: '3.1.0' })
    expect(feed.requestsFor(rel.id)).toEqual([])
  })

  it('check alone reports update-available and downloads nothing', async () => {
    const rel = publish(feed, 'v3.1.0', bin('3.1.0'))
    const rig = makeRig(rel)
    installOld(rig, '3.0.0')
    const end = await new ServerInstaller(rig.deps).check({ allowPrerelease: false })
    expect(end).toEqual({ kind: 'update-available', version: '3.1.0', tag: 'v3.1.0', installed: '3.0.0' })
    expect(feed.requestsFor(rel.id)).toEqual([])
  })
})

describe('install: the checksum is required', () => {
  it.each(['missing', 'listed-404', 'malformed', 'no-entry'] as const)('fails closed when the checksum file is %s', async (mode) => {
    const rel = publish(feed, 'v3.1.0', bin('3.1.0'), { checksum: mode })
    const rig = makeRig(rel)
    const old = installOld(rig, '3.0.0')
    const end = await new ServerInstaller(rig.deps).install({ allowPrerelease: false })
    expect(end).toMatchObject({ kind: 'failed', reason: 'no-checksum', rolledBack: false })
    expect(fs.readFileSync(rig.target).equals(old)).toBe(true)
    expect(ls(rig.binDir)).toEqual(['local-ai'])
    noLeftovers(rig)
    // The binary is never even downloaded.
    expect(feed.requestsFor(rel.id).some((l) => l.includes('local-ai-v3.1.0'))).toBe(false)
  })

  it('fails closed on a fresh machine too: nothing is installed', async () => {
    const rel = publish(feed, 'v3.1.0', bin('3.1.0'), { checksum: 'missing' })
    const rig = makeRig(rel)
    const end = await new ServerInstaller(rig.deps).install({ allowPrerelease: false })
    expect(end).toMatchObject({ kind: 'failed', reason: 'no-checksum' })
    expect(ls(rig.binDir)).toEqual([])
  })

  it('rejects a download whose hash differs, before it touches the install directory', async () => {
    const rel = publish(feed, 'v3.1.0', bin('3.1.0'), { checksum: 'bad' })
    const rig = makeRig(rel)
    const old = installOld(rig, '3.0.0')
    let swaps = 0
    rig.deps.fs = fsWith({ installAtomically: (s, t) => (swaps++, nodeInstallFs.installAtomically(s, t)) })
    const end = await new ServerInstaller(rig.deps).install({ allowPrerelease: false })
    expect(end).toMatchObject({ kind: 'failed', reason: 'checksum-mismatch' })
    expect(swaps).toBe(0)
    expect(fs.readFileSync(rig.target).equals(old)).toBe(true)
    expect(ls(rig.binDir)).toEqual(['local-ai'])
    noLeftovers(rig)
  })
})

describe('install: download failures leave the old binary alone', () => {
  const rel = () => publish(feed, 'v3.1.0', bin('3.1.0'))

  it('maps an interrupted download to network and cleans the partial file', async () => {
    const r = rel()
    const rig = makeRig(r)
    const old = installOld(rig, '3.0.0')
    const body = bin('3.1.0')
    rig.deps.fetch = (async (url: string) => {
      if (String(url).endsWith('checksums.txt')) return fetch(url)
      return streamResponse([body.subarray(0, 1000), body.subarray(1000, 2000), body.subarray(2000)], { failAfter: 2 })
    }) as typeof fetch
    const end = await new ServerInstaller(rig.deps).install({ allowPrerelease: false })
    expect(end).toMatchObject({ kind: 'failed', reason: 'network' })
    expect(fs.readFileSync(rig.target).equals(old)).toBe(true)
    expect(ls(rig.binDir)).toEqual(['local-ai'])
    noLeftovers(rig)
  })

  it('treats a download cut short of its content length as a network failure', async () => {
    const r = rel()
    const rig = makeRig(r)
    installOld(rig, '3.0.0')
    const body = bin('3.1.0')
    rig.deps.fetch = (async (url: string) => {
      if (String(url).endsWith('checksums.txt')) return fetch(url)
      return streamResponse([body.subarray(0, 1000)], { contentLength: body.length })
    }) as typeof fetch
    const end = await new ServerInstaller(rig.deps).install({ allowPrerelease: false })
    expect(end).toMatchObject({ kind: 'failed', reason: 'network' })
    noLeftovers(rig)
  })

  it('maps disk full while downloading to disk-full', async () => {
    const r = rel()
    const rig = makeRig(r)
    const old = installOld(rig, '3.0.0')
    rig.deps.fs = fsWith({
      openWriter: async (p) => {
        const w = await nodeInstallFs.openWriter(p)
        return { write: async () => { throw errno('ENOSPC') }, close: () => w.close() }
      },
    })
    const end = await new ServerInstaller(rig.deps).install({ allowPrerelease: false })
    expect(end).toMatchObject({ kind: 'failed', reason: 'disk-full' })
    expect(fs.readFileSync(rig.target).equals(old)).toBe(true)
    noLeftovers(rig)
  })

  it('maps disk full while keeping the rollback copy to disk-full and leaves the old binary', async () => {
    const r = rel()
    const rig = makeRig(r)
    const old = installOld(rig, '3.0.0')
    rig.deps.fs = fsWith({ copyFile: async () => { throw errno('ENOSPC') } })
    const end = await new ServerInstaller(rig.deps).install({ allowPrerelease: false })
    expect(end).toMatchObject({ kind: 'failed', reason: 'disk-full' })
    expect(fs.readFileSync(rig.target).equals(old)).toBe(true)
    expect(ls(rig.binDir)).toEqual(['local-ai'])
    noLeftovers(rig)
  })

  it('maps disk full at the swap to disk-full and leaves the old binary', async () => {
    const r = rel()
    const rig = makeRig(r)
    const old = installOld(rig, '3.0.0')
    rig.deps.fs = fsWith({ installAtomically: () => { throw new InstallError('install failed at stage', 'stage', { cause: errno('ENOSPC') }) } })
    const end = await new ServerInstaller(rig.deps).install({ allowPrerelease: false })
    expect(end).toMatchObject({ kind: 'failed', reason: 'disk-full', rolledBack: false })
    expect(fs.readFileSync(rig.target).equals(old)).toBe(true)
    noLeftovers(rig)
  })

  it('maps permission denied at the swap to permission and leaves the old binary', async () => {
    const r = rel()
    const rig = makeRig(r)
    const old = installOld(rig, '3.0.0')
    rig.deps.fs = fsWith({ installAtomically: () => { throw new InstallError('install failed at stage', 'stage', { cause: errno('EACCES') }) } })
    const end = await new ServerInstaller(rig.deps).install({ allowPrerelease: false })
    expect(end).toMatchObject({ kind: 'failed', reason: 'permission' })
    expect(fs.readFileSync(rig.target).equals(old)).toBe(true)
    noLeftovers(rig)
  })

  it.skipIf(process.getuid?.() === 0)('maps a read-only install directory to permission (real file system)', async () => {
    const r = rel()
    const rig = makeRig(r)
    const old = installOld(rig, '3.0.0')
    fs.chmodSync(rig.binDir, 0o555)
    try {
      const end = await new ServerInstaller(rig.deps).install({ allowPrerelease: false })
      expect(end).toMatchObject({ kind: 'failed', reason: 'permission' })
    } finally {
      fs.chmodSync(rig.binDir, 0o755)
    }
    expect(fs.readFileSync(rig.target).equals(old)).toBe(true)
    noLeftovers(rig)
  })
})

describe('install: rollback', () => {
  it('restores the previous binary and restarts it when the new one fails its health start', async () => {
    const rel = publish(feed, 'v3.1.0', bin('3.1.0', 'BROKEN'))
    const rig = makeRig(rel)
    const old = installOld(rig, '3.0.0')
    rig.supervisor.state = 'ready'
    rig.supervisor.onStart = () => (fs.readFileSync(rig.target, 'latin1').includes('BROKEN') ? 'failed' : 'ready')
    const inst = new ServerInstaller(rig.deps)
    const w = watch(inst)
    const end = await inst.install({ allowPrerelease: false, stopFirst: true })
    expect(end).toMatchObject({ kind: 'failed', reason: 'start-failed', rolledBack: true })
    expect(w.kinds()).toContain('rolling-back')
    expect(fs.readFileSync(rig.target).equals(old)).toBe(true)
    expect(rig.supervisor.calls).toEqual(['stop', 'start', 'stop', 'start'])
    expect(rig.supervisor.state).toBe('ready')
    noLeftovers(rig)
  })

  it('restores the previous binary when the new binary reports a different version', async () => {
    const rel = publish(feed, 'v3.1.0', bin('9.9.9'))
    const rig = makeRig(rel)
    const old = installOld(rig, '3.0.0')
    const end = await new ServerInstaller(rig.deps).install({ allowPrerelease: false })
    // A wrong version is not a failed start: only a health start that fails is.
    expect(end).toMatchObject({ kind: 'failed', reason: 'unknown', rolledBack: true })
    expect(fs.readFileSync(rig.target).equals(old)).toBe(true)
    expect(rig.supervisor.calls).toEqual([])
  })

  it('restores the previous binary when the new binary cannot report a version at all', async () => {
    const rel = publish(feed, 'v3.1.0', bin('3.1.0'))
    const rig = makeRig(rel)
    const old = installOld(rig, '3.0.0')
    rig.deps.runVersion = async (f) => {
      if (fs.readFileSync(f, 'latin1').includes('3.1.0')) throw new Error('exec format error')
      return 'v3.0.0'
    }
    const end = await new ServerInstaller(rig.deps).install({ allowPrerelease: false })
    expect(end).toMatchObject({ kind: 'failed', rolledBack: true })
    expect(fs.readFileSync(rig.target).equals(old)).toBe(true)
  })

  it('removes a bad first install instead of leaving it', async () => {
    const rel = publish(feed, 'v3.1.0', bin('9.9.9'))
    const rig = makeRig(rel)
    const end = await new ServerInstaller(rig.deps).install({ allowPrerelease: false })
    expect(end).toMatchObject({ kind: 'failed', rolledBack: true })
    expect(ls(rig.binDir)).toEqual([])
  })

  it('restarts the old server when the swap itself fails after the server was stopped', async () => {
    const rel = publish(feed, 'v3.1.0', bin('3.1.0'))
    const rig = makeRig(rel)
    const old = installOld(rig, '3.0.0')
    rig.supervisor.state = 'ready'
    rig.deps.fs = fsWith({ installAtomically: () => { throw new InstallError('install failed at rename', 'rename', { cause: errno('EACCES') }) } })
    const end = await new ServerInstaller(rig.deps).install({ allowPrerelease: false, stopFirst: true })
    expect(end).toMatchObject({ kind: 'failed', reason: 'permission' })
    expect(fs.readFileSync(rig.target).equals(old)).toBe(true)
    expect(rig.supervisor.calls).toEqual(['stop', 'start'])
  })

  it('rolls back on request to the previous version', async () => {
    const rel = publish(feed, 'v3.1.0', bin('3.1.0'))
    const rig = makeRig(rel)
    const old = installOld(rig, '3.0.0')
    const inst = new ServerInstaller(rig.deps)
    await inst.install({ allowPrerelease: false })
    const w = watch(inst)
    const end = await inst.rollback({})
    expect(end).toEqual({ kind: 'done', version: '3.0.0', adopted: false })
    expect(w.kinds()).toEqual(['rolling-back', 'done'])
    expect(fs.readFileSync(rig.target).equals(old)).toBe(true)
    // A later check sees the restored version, not stale metadata.
    expect(await new ServerInstaller(rig.deps).check({ allowPrerelease: false })).toMatchObject({ kind: 'update-available', installed: '3.0.0' })
  })

  it('fails a requested rollback when there is no previous version', async () => {
    const rig = makeRig(publish(feed, 'v3.1.0', bin('3.1.0')))
    installOld(rig, '3.0.0')
    const end = await new ServerInstaller(rig.deps).rollback({})
    expect(end).toMatchObject({ kind: 'failed', reason: 'unknown' })
  })

  it('does not roll back under a running server unless told to stop it', async () => {
    const rel = publish(feed, 'v3.1.0', bin('3.1.0'))
    const rig = makeRig(rel)
    installOld(rig, '3.0.0')
    const inst = new ServerInstaller(rig.deps)
    await inst.install({ allowPrerelease: false })
    rig.supervisor.state = 'ready'
    expect(await inst.rollback({})).toMatchObject({ kind: 'failed', reason: 'supervisor-busy' })
    expect(rig.supervisor.calls).toEqual([])
    expect(await inst.rollback({ stopFirst: true })).toMatchObject({ kind: 'done', version: '3.0.0' })
    expect(rig.supervisor.calls).toEqual(['stop', 'start'])
  })
})

describe('install: cancel', () => {
  it('cancels mid-download, leaves no partial file and keeps the old binary', async () => {
    const rel = publish(feed, 'v3.1.0', bin('3.1.0'))
    const rig = makeRig(rel)
    const old = installOld(rig, '3.0.0')
    const body = bin('3.1.0')
    let release!: () => void
    const gate = new Promise<void>((r) => (release = r))
    rig.deps.fetch = (async (url: string, init?: RequestInit) => {
      if (String(url).endsWith('checksums.txt')) return fetch(url)
      const res = streamResponse([body.subarray(0, 1000), body.subarray(1000)], { gate })
      init?.signal?.addEventListener('abort', () => release())
      return res
    }) as typeof fetch
    const inst = new ServerInstaller(rig.deps)
    const p = inst.install({ allowPrerelease: false })
    // Progress events are throttled, so poll the state instead of waiting for one.
    while (!(inst.state.kind === 'downloading' && inst.state.done > 0)) await new Promise((r) => setTimeout(r, 5))
    expect(inst.cancel()).toBe(true)
    const end = await p
    expect(end).toMatchObject({ kind: 'failed', reason: 'cancelled' })
    expect(fs.readFileSync(rig.target).equals(old)).toBe(true)
    expect(ls(rig.binDir)).toEqual(['local-ai'])
    noLeftovers(rig)
  })

  it('cancels during the release lookup', async () => {
    const rig = makeRig(publish(feed, 'v3.1.0', bin('3.1.0')))
    let release!: () => void
    rig.provider.latestRelease = () => new Promise((_, rej) => (release = () => rej(new Error('late'))))
    const inst = new ServerInstaller(rig.deps)
    const p = inst.install({ allowPrerelease: false })
    await new Promise((r) => setTimeout(r, 10))
    expect(inst.cancel()).toBe(true)
    release()
    expect(await p).toMatchObject({ kind: 'failed', reason: 'cancelled' })
  })

  it('refuses to cancel once the swap has begun and finishes the install', async () => {
    const rel = publish(feed, 'v3.1.0', bin('3.1.0'))
    const rig = makeRig(rel)
    installOld(rig, '3.0.0')
    rig.supervisor.state = 'ready'
    const inst = new ServerInstaller(rig.deps)
    let answer: boolean | undefined
    rig.supervisor.onStop = () => (answer = inst.cancel())
    const end = await inst.install({ allowPrerelease: false, stopFirst: true })
    expect(answer).toBe(false)
    expect(end).toMatchObject({ kind: 'done', version: '3.1.0' })
  })

  it('has nothing to cancel when idle', () => {
    expect(new ServerInstaller(makeRig(publish(feed, 'v3.1.0', bin('3.1.0'))).deps).cancel()).toBe(false)
  })
})

describe('install: the supervisor', () => {
  it.each(['ready', 'starting', 'stopping'] as const)('returns supervisor-busy when the server is %s, before downloading', async (state) => {
    const rel = publish(feed, 'v3.1.0', bin('3.1.0'))
    const rig = makeRig(rel)
    const old = installOld(rig, '3.0.0')
    rig.supervisor.state = state
    const end = await new ServerInstaller(rig.deps).install({ allowPrerelease: false })
    expect(end).toMatchObject({ kind: 'failed', reason: 'supervisor-busy' })
    expect(rig.supervisor.calls).toEqual([])
    expect(fs.readFileSync(rig.target).equals(old)).toBe(true)
    expect(feed.requestsFor(rel.id).some((l) => l.includes('local-ai-v3.1.0'))).toBe(false)
  })

  it('refuses to swap when the server starts during the download', async () => {
    const rel = publish(feed, 'v3.1.0', bin('3.1.0'))
    const rig = makeRig(rel)
    const old = installOld(rig, '3.0.0')
    rig.deps.sleep = async () => {}
    const real = rig.deps.fetch
    rig.deps.fetch = (async (u: string, i?: RequestInit) => {
      const r = await real(u, i)
      if (!String(u).endsWith('checksums.txt')) rig.supervisor.state = 'starting'
      return r
    }) as typeof fetch
    const end = await new ServerInstaller(rig.deps).install({ allowPrerelease: false })
    expect(end).toMatchObject({ kind: 'failed', reason: 'supervisor-busy' })
    expect(fs.readFileSync(rig.target).equals(old)).toBe(true)
    noLeftovers(rig)
  })

  it('stops the server first only when asked, and starts it again on the new binary', async () => {
    const rel = publish(feed, 'v3.1.0', bin('3.1.0'))
    const rig = makeRig(rel)
    installOld(rig, '3.0.0')
    rig.supervisor.state = 'ready'
    const end = await new ServerInstaller(rig.deps).install({ allowPrerelease: false, stopFirst: true })
    expect(end).toMatchObject({ kind: 'done', version: '3.1.0' })
    expect(rig.supervisor.calls).toEqual(['stop', 'start'])
    expect(rig.supervisor.state).toBe('ready')
  })

  it('does not start a server that was stopped', async () => {
    const rel = publish(feed, 'v3.1.0', bin('3.1.0'))
    const rig = makeRig(rel)
    installOld(rig, '3.0.0')
    await new ServerInstaller(rig.deps).install({ allowPrerelease: false })
    expect(rig.supervisor.calls).toEqual([])
  })

  it('treats failed and not-installed as not busy', async () => {
    for (const state of ['failed', 'not-installed'] as const) {
      const rig = makeRig(publish(feed, 'v3.1.0', bin('3.1.0')))
      rig.supervisor.state = state
      expect(await new ServerInstaller(rig.deps).install({ allowPrerelease: false })).toMatchObject({ kind: 'done' })
    }
  })
})

describe('install: adopt an existing binary', () => {
  it('adopts a binary whose hash matches the release, without a download or a backup', async () => {
    const body = bin('3.1.0')
    const rel = publish(feed, 'v3.1.0', body)
    const rig = makeRig(rel)
    fs.writeFileSync(rig.target, body, { mode: 0o755 })
    rig.deps.runVersion = async () => { throw new Error('cannot tell') }
    const inst = new ServerInstaller(rig.deps)
    const w = watch(inst)
    const end = await inst.install({ allowPrerelease: false })
    expect(end).toEqual({ kind: 'done', version: '3.1.0', adopted: true })
    expect(w.kinds()).toEqual(['checking', 'update-available', 'verifying', 'done'])
    expect(ls(rig.binDir)).toEqual(['local-ai'])
    expect(feed.requestsFor(rel.id).some((l) => l.includes('local-ai-v3.1.0'))).toBe(false)
    // Adopted means remembered: the next check is up to date without running the binary.
    rig.deps.runVersion = async () => { throw new Error('must not run') }
    expect(await new ServerInstaller(rig.deps).check({ allowPrerelease: false })).toEqual({ kind: 'up-to-date', version: '3.1.0' })
  })

  it('replaces an unknown binary whose hash differs', async () => {
    const rel = publish(feed, 'v3.1.0', bin('3.1.0'))
    const rig = makeRig(rel)
    fs.writeFileSync(rig.target, bin('mystery'), { mode: 0o755 })
    const realRun = rig.deps.runVersion
    rig.deps.runVersion = async (f) => (fs.readFileSync(f, 'latin1').includes('mystery') ? Promise.reject(new Error('no')) : realRun(f))
    const end = await new ServerInstaller(rig.deps).install({ allowPrerelease: false })
    expect(end).toEqual({ kind: 'done', version: '3.1.0', adopted: false })
    expect(fs.readFileSync(rig.target).equals(bin('3.1.0'))).toBe(true)
  })
})

describe('install: release candidates', () => {
  it('is up to date for a stable install when only a release candidate is newer', async () => {
    const rel = publish(feed, 'v3.2.0-rc1', bin('3.2.0-rc1'), { prerelease: true })
    const rig = makeRig(rel)
    installOld(rig, '3.1.0')
    const inst = new ServerInstaller(rig.deps)
    expect(await inst.install({ allowPrerelease: false })).toEqual({ kind: 'up-to-date', version: '3.1.0' })
    expect(feed.requestsFor(rel.id)).toEqual([])
  })

  it('installs the release candidate when the caller opted in', async () => {
    const rel = publish(feed, 'v3.2.0-rc1', bin('3.2.0-rc1'), { prerelease: true })
    const rig = makeRig(rel)
    installOld(rig, '3.1.0')
    const end = await new ServerInstaller(rig.deps).install({ allowPrerelease: true })
    expect(end).toEqual({ kind: 'done', version: '3.2.0-rc1', adopted: false })
  })

  it('asks the provider for pre-releases only when opted in and the provider can', async () => {
    const stable = publish(feed, 'v3.1.0', bin('3.1.0'))
    const rc = publish(feed, 'v3.2.0-rc1', bin('3.2.0-rc1'), { prerelease: true })
    const rig = makeRig(stable)
    installOld(rig, '3.0.0')
    ;(rig.provider as unknown as { latestIncludingPrerelease: () => Promise<typeof rc> }).latestIncludingPrerelease = async () => rc
    expect(await new ServerInstaller(rig.deps).check({ allowPrerelease: false })).toMatchObject({ kind: 'update-available', version: '3.1.0' })
    expect(await new ServerInstaller(rig.deps).check({ allowPrerelease: true })).toMatchObject({ kind: 'update-available', version: '3.2.0-rc1' })
  })

  it('fails with a clear reason when nothing is installed and only a pre-release exists', async () => {
    const rel = publish(feed, 'v3.2.0-rc1', bin('3.2.0-rc1'), { prerelease: true })
    const rig = makeRig(rel)
    const end = await new ServerInstaller(rig.deps).install({ allowPrerelease: false })
    expect(end).toMatchObject({ kind: 'failed', reason: 'unknown' })
    expect(ls(rig.binDir)).toEqual([])
  })
})

describe('release lookup', () => {
  it('retries with growing backoff and then succeeds', async () => {
    const rig = makeRig(publish(feed, 'v3.1.0', bin('3.1.0')), { retry: { attempts: 3, baseMs: 500 } })
    rig.provider.failures = [new Error('boom'), new Error('boom')]
    const end = await new ServerInstaller(rig.deps).check({ allowPrerelease: false })
    expect(end.kind).toBe('update-available')
    expect(rig.provider.lookups).toBe(3)
    expect(rig.sleeps).toEqual([500, 1000])
  })

  it('gives up after the bounded number of attempts with a network reason', async () => {
    const rig = makeRig(publish(feed, 'v3.1.0', bin('3.1.0')), { retry: { attempts: 3, baseMs: 500 } })
    rig.provider.failures = [new Error('a'), new Error('b'), new Error('c'), new Error('d')]
    const end = await new ServerInstaller(rig.deps).check({ allowPrerelease: false })
    expect(end).toMatchObject({ kind: 'failed', reason: 'network' })
    expect(rig.provider.lookups).toBe(3)
    expect(rig.sleeps).toEqual([500, 1000])
  })

  it('does not retry when rate limited', async () => {
    const rig = makeRig(publish(feed, 'v3.1.0', bin('3.1.0')))
    rig.provider.failures = [new RateLimitError()]
    const end = await new ServerInstaller(rig.deps).check({ allowPrerelease: false })
    expect(end).toMatchObject({ kind: 'failed', reason: 'network' })
    expect(rig.provider.lookups).toBe(1)
    expect(rig.sleeps).toEqual([])
  })
})

describe('runner behaviour', () => {
  it('lets a subscriber unsubscribe and survives a throwing subscriber', async () => {
    const rig = makeRig(publish(feed, 'v3.1.0', bin('3.1.0')))
    const inst = new ServerInstaller(rig.deps)
    const seen: string[] = []
    inst.subscribe(() => { throw new Error('bad listener') })
    const off = inst.subscribe((s) => seen.push(s.kind))
    await inst.check({ allowPrerelease: false })
    off()
    inst.reset()
    expect(seen).toEqual(['checking', 'update-available'])
    expect(inst.state.kind).toBe('idle')
  })

  it('shares one run between two concurrent install calls', async () => {
    const rig = makeRig(publish(feed, 'v3.1.0', bin('3.1.0')))
    const inst = new ServerInstaller(rig.deps)
    const [a, b] = await Promise.all([inst.install({ allowPrerelease: false }), inst.install({ allowPrerelease: false })])
    expect(a).toEqual(b)
    expect(rig.provider.lookups).toBe(1)
  })

  it('throttles progress events by the injected clock but always reports completion', async () => {
    const rel = publish(feed, 'v3.1.0', bin('3.1.0'))
    const rig = makeRig(rel)
    const body = bin('3.1.0')
    const chunks = Array.from({ length: 8 }, (_, i) => body.subarray(i * 512, (i + 1) * 512))
    rig.deps.fetch = (async (url: string) => (String(url).endsWith('checksums.txt') ? fetch(url) : streamResponse(chunks))) as typeof fetch
    const inst = new ServerInstaller(rig.deps)
    const w = watch(inst)
    await inst.install({ allowPrerelease: false }) // the clock never moves
    const progress = w.seen.filter((s) => s.kind === 'downloading')
    expect(progress.length).toBeLessThan(chunks.length)
    expect(progress[progress.length - 1]).toEqual({ kind: 'downloading', done: body.length, total: body.length })
  })
})

describe('classifyError', () => {
  it('reads the code through the cause chain', () => {
    expect(classifyError(new InstallError('x', 'stage', { cause: errno('ENOSPC') }), 'unknown')).toBe('disk-full')
    expect(classifyError(new InstallError('x', 'rename', { cause: errno('EROFS') }), 'unknown')).toBe('permission')
    expect(classifyError(errno('EPERM'), 'unknown')).toBe('permission')
    expect(classifyError(errno('ECONNRESET'), 'unknown')).toBe('network')
    expect(classifyError(new TypeError('fetch failed'), 'unknown')).toBe('network')
    expect(classifyError(new Error('who knows'), 'unknown')).toBe('unknown')
    expect(classifyError(new Error('who knows'), 'network')).toBe('network')
  })
  it('keeps the sha helper honest', () => {
    expect(sha(Buffer.from('a'))).toHaveLength(64)
  })
})

describe('execServerVersion', () => {
  it('returns the output of --version and fails for a binary that cannot run', async () => {
    const dir = fs.mkdtempSync(path.join(makeRig(publish(feed, 'v1.0.0', bin('1.0.0'))).root, 'x-'))
    const script = path.join(dir, 'fake-local-ai')
    fs.writeFileSync(script, '#!/bin/sh\necho "LocalAI version v3.1.0 (abc123)"\n', { mode: 0o755 })
    expect(await execServerVersion(script)).toBe('LocalAI version v3.1.0 (abc123)')
    await expect(execServerVersion(path.join(dir, 'missing'))).rejects.toThrow()
  })
})
