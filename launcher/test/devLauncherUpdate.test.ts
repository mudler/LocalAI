// The launcher's own update on the Development channel: which feed it reads,
// when it is on, what "newer" means, and that Stable and Release candidates
// are unchanged. Runs electron-updater's own generic provider against a fake
// HTTP executor (no network, no Electron import).
import { describe, expect, it } from 'vitest'
import semver from 'semver'
import { devVersion } from '../scripts/version.mjs'
import { devLauncherFeedUrl } from '../src/main/devFeed'
import { launcherFeed, launcherUpdaterSettings, selfUpdateGate, updaterSettings } from '../src/main/updaterStatus'
import type { UpdateChannelName } from '../src/shared/types'
// eslint-disable-next-line @typescript-eslint/no-require-imports
const { GenericProvider } = require('electron-updater/out/providers/GenericProvider') as typeof import('electron-updater/out/providers/GenericProvider')

const repo = { owner: 'mudler', repo: 'LocalAI' }
const DEV_FEED = 'https://github.com/mudler/LocalAI/releases/download/dev-master/'
const channels: UpdateChannelName[] = ['stable', 'rc', 'dev']

describe('the development feed of the launcher', () => {
  it('is the download folder of the dev-master release', () => {
    expect(devLauncherFeedUrl(repo)).toBe(DEV_FEED)
  })

  it('reads latest-linux.yml from that folder and resolves the package next to it', async () => {
    const realArch = Object.getOwnPropertyDescriptor(process, 'arch')
    Object.defineProperty(process, 'arch', { value: 'x64', configurable: true })
    try {
      const seen: string[] = []
      const executor = {
        request: async (o: { hostname?: string; path?: string }) => {
          seen.push(`${o.hostname}${o.path}`)
          return `version: 4.2.1-dev.57
files:
  - url: LocalAI-Launcher-dev-x86_64.AppImage
    sha512: abc
    size: 1
path: LocalAI-Launcher-dev-x86_64.AppImage
sha512: abc
releaseDate: '2026-11-23T00:00:00.000Z'
`
        },
      }
      const updater = { channel: null, isAddNoCacheQuery: false }
      const p = new GenericProvider({ provider: 'generic', url: DEV_FEED }, updater as never, { executor: executor as never, isUseMultipleRangeRequest: false, platform: 'linux' } as never)
      const info = await p.getLatestVersion()
      expect(seen).toEqual(['github.com/mudler/LocalAI/releases/download/dev-master/latest-linux.yml'])
      expect(info.version).toBe('4.2.1-dev.57')
      expect(p.resolveFiles(info)[0].url.href).toBe(`${DEV_FEED}LocalAI-Launcher-dev-x86_64.AppImage`)
      // The checksum comes from the feed, so a package without one is never installed.
      expect(info.files[0].sha512).toBe('abc')
    } finally {
      if (realArch) Object.defineProperty(process, 'arch', realArch)
    }
  })
})

describe('when the launcher updates itself on each channel', () => {
  const dev = '4.2.1-dev.57'
  const release = '4.2.0'

  it('a development build on the Development channel follows the development feed', () => {
    const gate = selfUpdateGate({ packaged: true, version: dev, channel: 'dev' })
    expect(gate).toEqual({ enabled: true, dev: true })
    if (gate.enabled) expect(launcherFeed(gate, repo)).toEqual({ kind: 'generic', url: DEV_FEED })
  })

  it('a development build on Stable or Release candidates never updates, and never reads the stable feed', () => {
    for (const channel of ['stable', 'rc'] as const) {
      expect(selfUpdateGate({ packaged: true, version: dev, channel })).toEqual({ enabled: false, reason: 'dev-build' })
    }
    expect(selfUpdateGate({ packaged: true, version: dev })).toEqual({ enabled: false, reason: 'dev-build' })
  })

  it('a release build on the Development channel moves to the development feed, and back on Stable', () => {
    const on = selfUpdateGate({ packaged: true, version: release, channel: 'dev' })
    expect(on).toEqual({ enabled: true, dev: true })
    for (const channel of ['stable', 'rc'] as const) {
      const gate = selfUpdateGate({ packaged: true, version: release, channel })
      expect(gate).toEqual({ enabled: true })
      if (gate.enabled) expect(launcherFeed(gate, repo)).toEqual({ kind: 'default' })
    }
  })

  it('never from source, never the portable zip, and a test feed wins on every channel', () => {
    for (const channel of channels) {
      for (const version of [release, dev]) {
        expect(selfUpdateGate({ packaged: false, version, channel })).toEqual({ enabled: false, reason: 'unpackaged' })
        // A development build off the Development channel is off for its own reason first.
        const reason = version === dev && channel !== 'dev' ? 'dev-build' : 'portable'
        expect(selfUpdateGate({ packaged: true, version, channel, portable: true })).toEqual({ enabled: false, reason })
        const gate = selfUpdateGate({ packaged: true, version, channel, feedUrl: 'http://127.0.0.1:18081/', portable: true })
        expect(gate).toEqual({ enabled: true, feedUrl: 'http://127.0.0.1:18081/' })
        if (gate.enabled) expect(launcherFeed(gate, repo)).toEqual({ kind: 'generic', url: 'http://127.0.0.1:18081/' })
      }
    }
  })

  it('Stable and Release candidates keep their settings; only Development turns differential download off', () => {
    expect(launcherUpdaterSettings('stable')).toEqual({ ...updaterSettings(false), disableDifferentialDownload: false })
    expect(launcherUpdaterSettings('rc')).toEqual({ ...updaterSettings(true), disableDifferentialDownload: false })
    expect(launcherUpdaterSettings('dev')).toEqual({ ...updaterSettings(false), disableDifferentialDownload: true })
    // Nothing downloads or installs by itself, and downgrades are never offered, on any channel.
    for (const ch of channels) {
      expect(launcherUpdaterSettings(ch)).toMatchObject({ autoDownload: false, autoInstallOnAppQuit: false, allowDowngrade: false })
    }
  })
})

describe('what "newer" means on the Development channel', () => {
  // electron-updater offers an update when the feed version is greater by semver and downgrades are off.
  const newer = (feed: string, current: string) => semver.gt(feed, current)

  it('a later development build is newer, also across digit counts', () => {
    expect(newer('4.2.1-dev.10', '4.2.1-dev.9')).toBe(true)
    expect(newer('4.2.1-dev.100', '4.2.1-dev.99')).toBe(true)
    expect(newer('4.2.1-dev.57', '4.2.1-dev.57')).toBe(false)
    expect(newer('4.2.1-dev.56', '4.2.1-dev.57')).toBe(false)
  })

  it('the versions that CI makes rise with the run number and with a new release', () => {
    const v = (tags: string[], run: number) => devVersion(tags, run).version
    expect(newer(v(['v4.2.0'], 58), v(['v4.2.0'], 57))).toBe(true)
    // A new stable release moves the base up, so the next development build is newer than the last one.
    expect(newer(v(['v4.2.0', 'v4.2.1'], 59), v(['v4.2.0'], 58))).toBe(true)
  })

  it('a development build is newer than the last release and older than the next one', () => {
    expect(newer('4.2.1-dev.57', '4.2.0')).toBe(true)
    expect(newer('4.2.1', '4.2.1-dev.57')).toBe(true)
    // A launcher that is already on the release of the same number is not pulled back to a development build.
    expect(newer('4.2.1-dev.57', '4.2.1')).toBe(false)
  })
})
