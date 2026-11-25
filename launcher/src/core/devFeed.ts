// The development build's release tag and how its identity is read from the
// release text, shared by the desktop launcher and a client-only shell. Pure,
// no Electron or Node import. The platform file names and the feed URL stay
// in main/devFeed.ts, which re-exports these names.
//
// The CI job that publishes the development build owns these names. If it
// renames the release tag or changes the release text, change this file
// together with main/devFeed.ts and scripts/fake-release-server.mjs.

export const DEV_RELEASE_TAG = 'dev-master'

// The Android app's release assets. The stable ones are attached to every
// stable release (read through `releases/latest/download/`), the dev ones to
// the `dev-master` pre-release. The names carry no version, so the feed URL
// never changes; the version is in the manifest and in the APK. The Android
// CI workflows own these names.
export const APK_STABLE = 'LocalAI-Android.apk'
export const APK_DEV = 'LocalAI-Android-dev.apk'
export const APK_MANIFEST_STABLE = 'android-update.json'
export const APK_MANIFEST_DEV = 'android-update-dev.json'

export interface DevBuild {
  /** What "newer" compares: the short commit sha, or the publish time when the text has no sha. */
  id: string
  /** The commit date as YYYY-MM-DD, when the release text has one. */
  date?: string
  /** The first line of the commit subject, when the release text has one. */
  subject?: string
}

/**
 * Reads the build identity from the release name and body. "Newer" for the
 * development channel means a different id; it is not a version compare.
 */
export function parseDevBuild(release: { name?: string; body?: string; published_at?: string }): DevBuild | undefined {
  const text = `${release.name ?? ''}\n${release.body ?? ''}`
  const sha = /\b([0-9a-f]{7,40})\b/.exec(text)
  const date = /\b(\d{4}-\d{2}-\d{2})\b/.exec(text)
  const id = sha ? sha[1].slice(0, 7) : release.published_at && /^[0-9TZ:.+-]{10,40}$/.test(release.published_at) ? release.published_at : undefined
  if (!id) return undefined
  // The subject is the line that follows the sha and the date, or the first other line.
  let subject: string | undefined
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.replace(/^[\s>*#-]+/, '').trim()
    if (!line || (sha && line.includes(sha[1])) || (date && line.includes(date[1])) || /^(commit|sha|date|subject)\b[:\s]*$/i.test(line)) continue
    subject = line.replace(/^subject:\s*/i, '').slice(0, 120)
    break
  }
  return { id, date: date?.[1], subject }
}

/** The short id as the UI shows it: the date and the sha when both are known. */
export function devBuildLabel(b: { id: string; date?: string }): string {
  return b.date ? `${b.date} (${b.id})` : b.id
}
