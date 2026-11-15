import { describe, expect, it } from 'vitest'
import { selfUpdateGate, updaterSettings } from '../src/main/updaterStatus'

describe('self-update gate', () => {
  it('is off in a run from source, also with a feed set', () => {
    expect(selfUpdateGate({ packaged: false, version: '4.2.1' })).toEqual({ enabled: false, reason: 'unpackaged' })
    expect(selfUpdateGate({ packaged: false, version: '4.2.1', feedUrl: 'http://127.0.0.1:18081/' })).toEqual({ enabled: false, reason: 'unpackaged' })
  })

  it('is off in a development build from master, which never checks the stable feed', () => {
    for (const version of ['4.1.4-dev.42', '4.1.4-dev.1', '0.1.1-dev.7+build.3']) {
      expect(selfUpdateGate({ packaged: true, version })).toEqual({ enabled: false, reason: 'dev-build' })
    }
  })

  it('a development build checks only an explicit test feed', () => {
    expect(selfUpdateGate({ packaged: true, version: '4.1.4-dev.42', feedUrl: 'http://127.0.0.1:18081/' })).toEqual({
      enabled: true,
      feedUrl: 'http://127.0.0.1:18081/',
    })
    expect(selfUpdateGate({ packaged: true, version: '4.1.4-dev.42', feedUrl: '' })).toEqual({ enabled: false, reason: 'dev-build' })
  })

  it('is on in a packaged release, release candidates included', () => {
    expect(selfUpdateGate({ packaged: true, version: '4.2.1' })).toEqual({ enabled: true })
    expect(selfUpdateGate({ packaged: true, version: '4.2.1-rc1' })).toEqual({ enabled: true })
    // "dev" elsewhere in the version is not a development build.
    expect(selfUpdateGate({ packaged: true, version: '4.2.1-devices' })).toEqual({ enabled: true })
  })

  it('a release candidate takes stable updates only, unless the user opts in', () => {
    // electron-updater turns allowPrerelease on when the running version has a pre-release part (an rc
    // build), so the setting is always written; drafts are not visible to an unauthenticated feed at all.
    expect(updaterSettings(false)).toEqual({ autoDownload: false, autoInstallOnAppQuit: false, allowPrerelease: false, allowDowngrade: false })
    expect(updaterSettings(true)).toEqual({ autoDownload: false, autoInstallOnAppQuit: false, allowPrerelease: true, allowDowngrade: false })
  })

  it('every combination of packaged, version kind, test feed and the release-candidate setting', () => {
    const versions = { release: '4.2.1', rc: '4.2.1-rc.1', dev: '4.12.2-dev.42' } as const
    for (const packaged of [false, true]) {
      for (const [kind, version] of Object.entries(versions)) {
        for (const feedUrl of [undefined, 'http://127.0.0.1:18081/']) {
          for (const pre of [false, true]) {
            const gate = selfUpdateGate({ packaged, version, feedUrl })
            const label = JSON.stringify({ packaged, kind, feedUrl, pre })
            // Never from source; a development build only against an explicit test feed.
            const on = packaged && (kind !== 'dev' || feedUrl !== undefined)
            expect(gate.enabled, label).toBe(on)
            if (!gate.enabled) expect(gate.reason, label).toBe(packaged ? 'dev-build' : 'unpackaged')
            // When it runs, pre-releases follow the setting only: never the running version's suffix.
            if (on) expect(updaterSettings(pre).allowPrerelease, label).toBe(pre)
            expect(updaterSettings(pre).allowDowngrade, label).toBe(false)
          }
        }
      }
    }
  })
})
