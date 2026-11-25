// SPDX-License-Identifier: MIT
import { describe, expect, it } from 'vitest'
import { APK_DEV, APK_MANIFEST_DEV, APK_MANIFEST_STABLE, APK_STABLE } from '@launcher/core/devFeed'
import {
  AUTO_CHECK_INTERVAL_MS,
  DEV_APP_URL,
  checkDue,
  checkManifest,
  checksumAgrees,
  feedUrls,
  type AppSelf,
} from '../src/core/apkUpdate'
import { stableVersionCode } from '../scripts/version-code.mjs'

const BASE = 'https://github.com/mudler/LocalAI/releases'
const SHA = 'a'.repeat(64)
const OTHER_SHA = 'b'.repeat(64)

const stableSelf: AppSelf = {
  applicationId: 'io.localai.client',
  versionCode: stableVersionCode('4.2.1'),
  versionName: '4.2.1',
  sdkInt: 34,
  feedBase: BASE,
  channel: 'stable',
}

const devSelf: AppSelf = {
  applicationId: 'io.localai.client.dev',
  versionCode: 473328,
  versionName: '5.0.2-dev.1',
  sdkInt: 34,
  feedBase: BASE,
  channel: 'dev',
}

function stableManifest(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    schema: 1,
    applicationId: 'io.localai.client',
    versionName: '4.3.0',
    versionCode: stableVersionCode('4.3.0'),
    minSdk: 26,
    apk: 'LocalAI-Android.apk',
    size: 41_234_567,
    sha256: SHA,
    commit: '0123456789abcdef0123456789abcdef01234567',
    notesUrl: 'https://github.com/mudler/LocalAI/releases/tag/v4.3.0',
    ...over,
  }
}

function devManifest(over: Record<string, unknown> = {}): Record<string, unknown> {
  return stableManifest({
    applicationId: 'io.localai.client.dev',
    versionName: '5.0.2-dev.7',
    versionCode: 480000,
    apk: 'LocalAI-Android-dev.apk',
    notesUrl: 'https://github.com/mudler/LocalAI/releases/tag/dev-master',
    ...over,
  })
}

describe('asset names', () => {
  it('names the APK and manifest of each channel', () => {
    expect(APK_STABLE).toBe('LocalAI-Android.apk')
    expect(APK_DEV).toBe('LocalAI-Android-dev.apk')
    expect(APK_MANIFEST_STABLE).toBe('android-update.json')
    expect(APK_MANIFEST_DEV).toBe('android-update-dev.json')
    expect(DEV_APP_URL).toBe('https://github.com/mudler/LocalAI/releases/download/dev-master/LocalAI-Android-dev.apk')
  })
})

describe('feedUrls', () => {
  it('reads the stable feed from the latest release', () => {
    expect(feedUrls(stableSelf)).toEqual({
      manifest: `${BASE}/latest/download/android-update.json`,
      apk: `${BASE}/latest/download/LocalAI-Android.apk`,
      checksum: `${BASE}/latest/download/LocalAI-Android.apk.sha256`,
    })
  })

  it('reads the dev feed from the dev-master pre-release', () => {
    expect(feedUrls(devSelf)).toEqual({
      manifest: `${BASE}/download/dev-master/android-update-dev.json`,
      apk: `${BASE}/download/dev-master/LocalAI-Android-dev.apk`,
      checksum: `${BASE}/download/dev-master/LocalAI-Android-dev.apk.sha256`,
    })
  })

  it('ignores a trailing slash on the feed base', () => {
    expect(feedUrls({ ...stableSelf, feedBase: `${BASE}/` }).manifest).toBe(`${BASE}/latest/download/android-update.json`)
  })

  it('gives the dev app URL for the default feed base', () => {
    expect(feedUrls({ ...devSelf }).apk).toBe(DEV_APP_URL)
  })
})

describe('checkManifest', () => {
  it('offers a newer stable build with URLs built from the feed base', () => {
    expect(checkManifest(stableManifest(), stableSelf)).toEqual({
      ok: true,
      offer: {
        versionName: '4.3.0',
        versionCode: 400300099,
        apkUrl: `${BASE}/latest/download/LocalAI-Android.apk`,
        checksumUrl: `${BASE}/latest/download/LocalAI-Android.apk.sha256`,
        size: 41_234_567,
        sha256: SHA,
        notesUrl: 'https://github.com/mudler/LocalAI/releases/tag/v4.3.0',
      },
    })
  })

  it('offers a newer dev build from dev-master', () => {
    const r = checkManifest(devManifest(), devSelf)
    expect(r.ok).toBe(true)
    if (r.ok) {
      expect(r.offer.apkUrl).toBe(DEV_APP_URL)
      expect(r.offer.checksumUrl).toBe(`${DEV_APP_URL}.sha256`)
    }
  })

  it('accepts a manifest whose minSdk equals the device', () => {
    expect(checkManifest(stableManifest({ minSdk: 34 }), stableSelf).ok).toBe(true)
  })

  it('refuses another application id', () => {
    expect(checkManifest(stableManifest({ applicationId: 'com.example.other' }), stableSelf)).toEqual({ ok: false, reason: 'app-id' })
    // The stable manifest must not update the dev app, and the reverse.
    expect(checkManifest(stableManifest({ versionCode: 999_999_999 }), devSelf)).toEqual({ ok: false, reason: 'app-id' })
    expect(checkManifest(devManifest({ versionCode: 999_999_999 }), stableSelf)).toEqual({ ok: false, reason: 'app-id' })
  })

  it('refuses a minSdk above the device', () => {
    expect(checkManifest(stableManifest({ minSdk: 99 }), stableSelf)).toEqual({ ok: false, reason: 'min-sdk' })
    expect(checkManifest(stableManifest({ minSdk: 35 }), stableSelf)).toEqual({ ok: false, reason: 'min-sdk' })
  })

  it('refuses an equal or lower versionCode', () => {
    expect(checkManifest(stableManifest({ versionName: '4.2.1', versionCode: stableVersionCode('4.2.1') }), stableSelf)).toEqual({ ok: false, reason: 'not-newer' })
    expect(checkManifest(stableManifest({ versionName: '4.2.0', versionCode: stableVersionCode('4.2.0') }), stableSelf)).toEqual({ ok: false, reason: 'not-newer' })
    expect(checkManifest(devManifest({ versionCode: devSelf.versionCode }), devSelf)).toEqual({ ok: false, reason: 'not-newer' })
  })

  it('refuses an apk name other than the channel asset name', () => {
    for (const apk of ['evil.apk', 'LocalAI-Android-dev.apk', 'localai-android.apk', '../LocalAI-Android.apk', 'https://example.com/LocalAI-Android.apk', '']) {
      expect(checkManifest(stableManifest({ apk }), stableSelf)).toEqual({ ok: false, reason: 'apk-name' })
    }
    expect(checkManifest(devManifest({ apk: 'LocalAI-Android.apk' }), devSelf)).toEqual({ ok: false, reason: 'apk-name' })
  })

  it('refuses a bad size', () => {
    for (const size of [0, -1, 1.5, 300_000_001, '41234567', Number.NaN]) {
      expect(checkManifest(stableManifest({ size }), stableSelf)).toEqual({ ok: false, reason: 'size' })
    }
    expect(checkManifest(stableManifest({ size: 300_000_000 }), stableSelf).ok).toBe(true)
  })

  it('refuses a bad sha256', () => {
    for (const sha256 of ['A'.repeat(64), 'a'.repeat(63), 'a'.repeat(65), 'g'.repeat(64), '', 42]) {
      expect(checkManifest(stableManifest({ sha256 }), stableSelf)).toEqual({ ok: false, reason: 'sha256' })
    }
  })

  it('refuses another schema or a malformed manifest', () => {
    expect(checkManifest(stableManifest({ schema: 2 }), stableSelf)).toEqual({ ok: false, reason: 'schema' })
    expect(checkManifest(stableManifest({ schema: '1' }), stableSelf)).toEqual({ ok: false, reason: 'schema' })
    for (const raw of [null, undefined, 'text', 42, [], [stableManifest()]]) {
      expect(checkManifest(raw, stableSelf)).toEqual({ ok: false, reason: 'schema' })
    }
    for (const over of [
      { versionCode: '400300099' },
      { versionCode: 4.5 },
      { versionCode: 2_100_000_001 },
      { minSdk: '26' },
      { versionName: '' },
      { versionName: '<b>4.3.0</b>' },
      { applicationId: 7 },
      { commit: 7 },
      { notesUrl: 'javascript:alert(1)' },
      { notesUrl: 'http://example.com/notes' },
    ]) {
      expect(checkManifest(stableManifest(over), stableSelf)).toEqual({ ok: false, reason: 'schema' })
    }
  })
})

describe('checksumAgrees', () => {
  it('agrees when the .sha256 file names the channel APK with the manifest hash', () => {
    expect(checksumAgrees(SHA, `${SHA}  LocalAI-Android.apk\n`, APK_STABLE)).toBe(true)
    expect(checksumAgrees(SHA, `${SHA.toUpperCase()} *LocalAI-Android.apk`, APK_STABLE)).toBe(true)
  })

  it('disagrees on another hash, another name, or an empty file', () => {
    expect(checksumAgrees(SHA, `${OTHER_SHA}  LocalAI-Android.apk\n`, APK_STABLE)).toBe(false)
    expect(checksumAgrees(SHA, `${SHA}  LocalAI-Android-dev.apk\n`, APK_STABLE)).toBe(false)
    expect(checksumAgrees(SHA, `${SHA}  evil.apk\n${OTHER_SHA}  LocalAI-Android.apk\n`, APK_STABLE)).toBe(false)
    expect(checksumAgrees(SHA, '', APK_STABLE)).toBe(false)
    expect(checksumAgrees(SHA, '<html>Not Found</html>', APK_STABLE)).toBe(false)
  })

  it('disagrees when the manifest hash itself is not 64 lowercase hex', () => {
    expect(checksumAgrees('', `${SHA}  LocalAI-Android.apk`, APK_STABLE)).toBe(false)
    expect(checksumAgrees(SHA.toUpperCase(), `${SHA}  LocalAI-Android.apk`, APK_STABLE)).toBe(false)
  })
})

describe('checkDue', () => {
  const now = Date.UTC(2026, 10, 25, 12)
  const hour = 3_600_000

  it('is one day', () => {
    expect(AUTO_CHECK_INTERVAL_MS).toBe(86_400_000)
  })

  it('is due when there was no check yet', () => {
    expect(checkDue(true, undefined, now)).toBe(true)
  })

  it('is not due 23 hours after the last check', () => {
    expect(checkDue(true, now - 23 * hour, now)).toBe(false)
  })

  it('is due 24 and 25 hours after the last check', () => {
    expect(checkDue(true, now - 24 * hour, now)).toBe(true)
    expect(checkDue(true, now - 25 * hour, now)).toBe(true)
  })

  it('is due when the clock went back past the last check', () => {
    expect(checkDue(true, now + hour, now)).toBe(true)
  })

  it('is never due when the automatic check is off', () => {
    expect(checkDue(false, undefined, now)).toBe(false)
    expect(checkDue(false, now - 25 * hour, now)).toBe(false)
  })
})
