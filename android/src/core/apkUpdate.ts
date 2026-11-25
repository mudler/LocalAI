// SPDX-License-Identifier: MIT
// Decides whether the release feed offers an APK update for this app. Pure, no
// Capacitor import: the plugin fetches and installs, this file only judges.
//
// The feed can lie (a wrong upload, a hijacked release): every field the
// update depends on is checked, and the APK and checksum URLs are always
// built from the app's own feed base and the channel's fixed asset name, so a
// manifest can never send the app to another file or host.

import { parseChecksums } from '@launcher/core/checksums'
import { APK_DEV, APK_MANIFEST_DEV, APK_MANIFEST_STABLE, APK_STABLE, DEV_RELEASE_TAG } from '@launcher/core/devFeed'

/** v1 has no release candidate channel: stable, or the separate dev app. */
export type ApkChannel = 'stable' | 'dev'

export interface AppSelf {
  applicationId: string
  versionCode: number
  versionName: string
  sdkInt: number
  /** The releases URL of the repository, e.g. https://github.com/mudler/LocalAI/releases */
  feedBase: string
  channel: ApkChannel
}

export interface ApkManifest {
  schema: 1
  applicationId: string
  versionName: string
  versionCode: number
  minSdk: number
  apk: string
  size: number
  sha256: string
  commit: string
  notesUrl: string
}

export interface ApkOffer {
  versionName: string
  versionCode: number
  apkUrl: string
  checksumUrl: string
  size: number
  sha256: string
  notesUrl: string
}

export type ManifestCheck =
  | { ok: true; offer: ApkOffer }
  | { ok: false; reason: 'schema' | 'app-id' | 'min-sdk' | 'not-newer' | 'apk-name' | 'size' | 'sha256' }

export const AUTO_CHECK_INTERVAL_MS = 86_400_000

export const DEV_APP_URL = 'https://github.com/mudler/LocalAI/releases/download/dev-master/LocalAI-Android-dev.apk'

// Android refuses a versionCode above this.
const MAX_VERSION_CODE = 2_100_000_000
// Far above a real APK; stops a manifest from asking for an unbounded download.
const MAX_APK_SIZE = 300_000_000
const SHA256 = /^[0-9a-f]{64}$/
// Shown in the UI ("Update to 4.3.0"): keep it to version characters.
const VERSION_NAME = /^[0-9A-Za-z.+-]{1,64}$/

function names(channel: ApkChannel): { manifest: string; apk: string } {
  return channel === 'dev' ? { manifest: APK_MANIFEST_DEV, apk: APK_DEV } : { manifest: APK_MANIFEST_STABLE, apk: APK_STABLE }
}

/**
 * The feed URLs of the app's channel. Stable reads the newest stable release
 * (GitHub's `latest` skips pre-releases); dev reads the `dev-master` pre-release.
 */
export function feedUrls(self: AppSelf): { manifest: string; apk: string; checksum: string } {
  const base = self.feedBase.replace(/\/+$/, '')
  const dir = self.channel === 'dev' ? `${base}/download/${DEV_RELEASE_TAG}` : `${base}/latest/download`
  const n = names(self.channel)
  return { manifest: `${dir}/${n.manifest}`, apk: `${dir}/${n.apk}`, checksum: `${dir}/${n.apk}.sha256` }
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function isHttpsUrl(v: unknown): v is string {
  if (typeof v !== 'string') return false
  try {
    return new URL(v).protocol === 'https:'
  } catch {
    return false
  }
}

/** Judges a parsed manifest against the running app. Checks run in a fixed order; the first failure is the reason. */
export function checkManifest(raw: unknown, self: AppSelf): ManifestCheck {
  if (!isRecord(raw) || raw.schema !== 1) return { ok: false, reason: 'schema' }
  const m = raw
  if (
    typeof m.applicationId !== 'string' ||
    typeof m.versionName !== 'string' ||
    !VERSION_NAME.test(m.versionName) ||
    !Number.isInteger(m.versionCode) ||
    (m.versionCode as number) < 1 ||
    (m.versionCode as number) > MAX_VERSION_CODE ||
    !Number.isInteger(m.minSdk) ||
    (m.minSdk as number) < 1 ||
    typeof m.apk !== 'string' ||
    typeof m.commit !== 'string' ||
    !isHttpsUrl(m.notesUrl)
  ) {
    return { ok: false, reason: 'schema' }
  }
  const versionCode = m.versionCode as number
  if (m.applicationId !== self.applicationId) return { ok: false, reason: 'app-id' }
  if ((m.minSdk as number) > self.sdkInt) return { ok: false, reason: 'min-sdk' }
  if (versionCode <= self.versionCode) return { ok: false, reason: 'not-newer' }
  if (m.apk !== names(self.channel).apk) return { ok: false, reason: 'apk-name' }
  if (typeof m.size !== 'number' || !Number.isInteger(m.size) || m.size < 1 || m.size > MAX_APK_SIZE) return { ok: false, reason: 'size' }
  if (typeof m.sha256 !== 'string' || !SHA256.test(m.sha256)) return { ok: false, reason: 'sha256' }
  const urls = feedUrls(self)
  return {
    ok: true,
    offer: {
      versionName: m.versionName,
      versionCode,
      apkUrl: urls.apk,
      checksumUrl: urls.checksum,
      size: m.size,
      sha256: m.sha256,
      notesUrl: m.notesUrl as string,
    },
  }
}

/**
 * True when the release's `.sha256` file (sha256sum format) gives `apkName`
 * exactly the manifest's hash. Manifest and checksum file must agree, or the
 * update is refused before anything is downloaded.
 */
export function checksumAgrees(sha256: string, checksumText: string, apkName: string): boolean {
  if (!SHA256.test(sha256)) return false
  return parseChecksums(checksumText).get(apkName) === sha256
}

/** The daily automatic check: due with no earlier check, after a day, or when the clock went back. */
export function checkDue(autoCheck: boolean, lastCheck: number | undefined, now: number): boolean {
  if (!autoCheck) return false
  return lastCheck === undefined || now - lastCheck >= AUTO_CHECK_INTERVAL_MS || lastCheck > now
}
