import fs from 'node:fs'
import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { DEV_RELEASE_TAG, devAssetName, devChecksumName, parseDevBuild } from '../src/main/devFeed'
import { ServerInstaller } from '../src/main/serverInstall'
import { buildServerLaunch } from '../src/main/serverLaunch'
import { FeedServer, bin, cleanupRigs, devProvider, installOld, makeRig, publish, publishDev } from './serverInstallHarness'

const feed = new FeedServer()
beforeAll(() => feed.start())
afterAll(() => {
  feed.stop()
  cleanupRigs()
})

describe('the names of the development feed', () => {
  it('uses one rolling release and per-platform file names', () => {
    expect(DEV_RELEASE_TAG).toBe('dev-master')
    expect(devAssetName({ os: 'darwin', arch: 'arm64' })).toBe('local-ai-master-darwin-arm64')
    expect(devChecksumName({ os: 'linux', arch: 'amd64' })).toBe('local-ai-master-linux-amd64.sha256')
  })
  it('reads the commit, the date and the subject from the release text', () => {
    expect(parseDevBuild({ body: 'Commit: 3f9a1c2d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b\nDate: 2026-11-15\nSubject: Fix the thing\n' })).toEqual({
      id: '3f9a1c2',
      date: '2026-11-15',
      subject: 'Fix the thing',
    })
  })
  it('falls back to the publish time, and refuses a release with neither', () => {
    expect(parseDevBuild({ body: 'nightly', published_at: '2026-11-15T10:00:00Z' })?.id).toBe('2026-11-15T10:00:00Z')
    expect(parseDevBuild({ body: 'nightly' })).toBeUndefined()
  })
})

describe('the development channel installs from the rolling release', () => {
  it('installs with the same checksum check and keeps the old binary as the rollback copy', async () => {
    const stable = installOldRig()
    const dev = bin('dev-one')
    const { apiBase } = publishDev(feed, dev, { sha: 'aaaaaaa1111' })
    const rig = stable.rig
    rig.deps.provider = devProvider(apiBase) as never
    const inst = new ServerInstaller(rig.deps)
    const end = await inst.install({ dev: true, stopFirst: true })
    expect(end).toEqual({ kind: 'done', version: 'aaaaaaa', adopted: false })
    expect(fs.readFileSync(rig.target).equals(dev)).toBe(true)
    expect(fs.readFileSync(rig.target + '.previous').equals(stable.old)).toBe(true)
    expect(await inst.installedVersion()).toBe('aaaaaaa')
    expect(await inst.rollbackVersion()).toBe('3.0.0')
  })

  it('newer means a different build id, not a higher version', async () => {
    const dev = bin('dev-one')
    const { apiBase } = publishDev(feed, dev, { sha: 'bbbbbbb2222' })
    const rig = makeRig(publish(feed, 'v1.0.0', bin('1.0.0')))
    rig.deps.provider = devProvider(apiBase) as never
    const inst = new ServerInstaller(rig.deps)
    await inst.install({ dev: true })
    expect((await inst.check({ dev: true })).kind).toBe('up-to-date')
    publishDev(feed, bin('dev-two'), { sha: 'ccccccc3333' })
    const s = await inst.check({ dev: true })
    expect(s).toMatchObject({ kind: 'update-available', version: 'ccccccc', installed: 'bbbbbbb' })
  })

  it('accepts a checksum file with the hash only, or a combined list', async () => {
    for (const checksum of ['hash-only', 'combined'] as const) {
      const { apiBase } = publishDev(feed, bin(`dev-${checksum}`), { sha: 'ddddddd4444', checksum })
      const rig = makeRig(publish(feed, 'v1.0.0', bin('1.0.0')))
      rig.deps.provider = devProvider(apiBase) as never
      expect((await new ServerInstaller(rig.deps).install({ dev: true })).kind).toBe('done')
    }
  })

  it('fails closed on a bad or missing checksum and leaves the old binary alone', async () => {
    for (const [checksum, reason] of [['bad', 'checksum-mismatch'], ['missing', 'no-checksum']] as const) {
      const { apiBase } = publishDev(feed, bin('dev-x'), { sha: 'eeeeeee5555', checksum })
      const { rig, old } = installOldRig()
      rig.deps.provider = devProvider(apiBase) as never
      const end = await new ServerInstaller(rig.deps).install({ dev: true, stopFirst: true })
      expect(end).toMatchObject({ kind: 'failed', reason })
      expect(fs.readFileSync(rig.target).equals(old)).toBe(true)
    }
  })

  it('goes back to stable: the development build stays as the rollback copy', async () => {
    const dev = bin('dev-one')
    const { apiBase } = publishDev(feed, dev, { sha: 'fffffff6666' })
    const rig = makeRig(publish(feed, 'v1.0.0', bin('1.0.0')))
    const devSrc = devProvider(apiBase)
    rig.deps.provider = devSrc as never
    const inst = new ServerInstaller(rig.deps)
    await inst.install({ dev: true })
    const stableBin = bin('4.2.1')
    const rel = publish(feed, 'v4.2.1', stableBin)
    rig.provider.release = rel
    rig.deps.provider = rig.provider as never
    const end = await new ServerInstaller(rig.deps).install({})
    expect(end).toEqual({ kind: 'done', version: '4.2.1', adopted: false })
    expect(fs.readFileSync(rig.target + '.previous').equals(dev)).toBe(true)
    // rolling back to the development build restores it and its build id
    const back = await new ServerInstaller(rig.deps).rollback({})
    expect(back).toEqual({ kind: 'done', version: 'fffffff', adopted: false })
    expect(fs.readFileSync(rig.target).equals(dev)).toBe(true)
  })
})

function installOldRig() {
  const rig = makeRig(publish(feed, 'v3.0.0', bin('3.0.0')))
  const old = installOld(rig, '3.0.0')
  return { rig, old }
}

describe('the server environment of the development channel', () => {
  const cfg = { models_path: '/m', backends_path: '/b', address: '127.0.0.1:8080', log_level: 'info' }
  it('asks for development backend images only on dev', () => {
    const stable = buildServerLaunch(cfg, '/d', {}, undefined, {}, 'stable')
    const rc = buildServerLaunch(cfg, '/d', {}, undefined, {}, 'rc')
    const dev = buildServerLaunch(cfg, '/d', {}, undefined, {}, 'dev')
    expect(stable.env.LOCALAI_PREFER_DEV_BACKENDS).toBeUndefined()
    expect(rc.env.LOCALAI_PREFER_DEV_BACKENDS).toBeUndefined()
    expect(dev.env.LOCALAI_PREFER_DEV_BACKENDS).toBe('true')
  })
  it('sets nothing else: the tag and suffix defaults of the server already mean master and development', () => {
    const dev = buildServerLaunch(cfg, '/d', {}, undefined, {}, 'dev')
    const stable = buildServerLaunch(cfg, '/d', {}, undefined, {}, 'stable')
    expect(Object.keys(dev.env).filter((k) => !(k in stable.env))).toEqual(['LOCALAI_PREFER_DEV_BACKENDS'])
  })
  it('wins over a user variable of the same name while the channel is dev', () => {
    const dev = buildServerLaunch(cfg, '/d', {}, undefined, { env: { LOCALAI_PREFER_DEV_BACKENDS: 'false' } }, 'dev')
    expect(dev.env.LOCALAI_PREFER_DEV_BACKENDS).toBe('true')
  })
  it('leaves a user variable alone on stable', () => {
    const s = buildServerLaunch(cfg, '/d', {}, undefined, { env: { LOCALAI_PREFER_DEV_BACKENDS: 'true' } }, 'stable')
    expect(s.env.LOCALAI_PREFER_DEV_BACKENDS).toBe('true')
  })
})
