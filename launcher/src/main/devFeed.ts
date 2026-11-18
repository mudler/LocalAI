// Where the development build of the LocalAI server is published, and how the
// launcher reads it. No Electron import.
//
// ONE PLACE TO CHANGE. The CI job that publishes the development build owns
// these names. If it renames the release tag or the files, change only the
// constants and functions in this file (and the fake release server in
// scripts/fake-release-server.mjs, which serves the same names in tests).
//
// Today: one rolling pre-release of github.com/mudler/LocalAI with the tag
// `dev-master`. It carries, for each platform, a binary
// `local-ai-master-<os>-<arch>` (os: linux, darwin or windows; arch: amd64 or
// arm64; `.exe` added for windows) and a checksum file `<binary>.sha256`, for
// example `local-ai-master-windows-amd64.exe.sha256`, in `sha256sum`
// format (hash, two spaces, file name). The release body holds the commit
// sha, the commit date and the commit subject.
import { exeSuffix, type PlatformTarget } from './platform'

export const DEV_RELEASE_TAG = 'dev-master'

export function devAssetName(t: PlatformTarget): string {
  return `local-ai-master-${t.os}-${t.arch}${exeSuffix(t)}`
}

export function devChecksumName(t: PlatformTarget): string {
  return `${devAssetName(t)}.sha256`
}

/** A combined checksum list, used when the per-file one is missing. */
export const DEV_CHECKSUMS_FALLBACK = 'SHA256SUMS'

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
