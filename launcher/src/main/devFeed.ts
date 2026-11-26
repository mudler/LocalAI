// Where the development build of the LocalAI server is published, and how the
// launcher reads it. No Electron import.
//
// ONE PLACE TO CHANGE. The CI job that publishes the development build owns
// these names. If it renames the release tag or the files, change only the
// constants and functions in this file and in core/devFeed.ts (the release
// tag and the release text parsing, shared with a client-only shell), and
// the fake release server in scripts/fake-release-server.mjs, which serves
// the same names in tests.
//
// Today: one rolling pre-release of github.com/mudler/LocalAI with the tag
// `dev-master`. It carries, for each platform, a binary
// `local-ai-master-<os>-<arch>` (os: linux, darwin or windows; arch: amd64 or
// arm64; `.exe` added for windows) and a checksum file `<binary>.sha256`, for
// example `local-ai-master-windows-amd64.exe.sha256`, in `sha256sum`
// format (hash, two spaces, file name). The release body holds the commit
// sha, the commit date and the commit subject.
//
// The same release also carries the development build of the launcher
// (`LocalAI-Launcher-dev-*` packages) and the update feed files that
// electron-updater reads (`latest-linux.yml`, `latest-linux-arm64.yml`,
// `latest-mac.yml`, `latest.yml`). See `devLauncherFeedUrl`.
import { DEV_RELEASE_TAG } from '../core/devFeed'
import { exeSuffix, type PlatformTarget } from './platform'

export { DEV_RELEASE_TAG, devBuildLabel, parseDevBuild, type DevBuild } from '../core/devFeed'

export function devAssetName(t: PlatformTarget): string {
  return `local-ai-master-${t.os}-${t.arch}${exeSuffix(t)}`
}

export function devChecksumName(t: PlatformTarget): string {
  return `${devAssetName(t)}.sha256`
}

/**
 * The base URL of the launcher's own development feed: the folder of the
 * `dev-master` release downloads. electron-updater's generic provider reads
 * the `latest-*.yml` file there and resolves the package names against it.
 * The feed files carry the SHA-512 of each package, which electron-updater
 * checks before it reports a download as done.
 */
export function devLauncherFeedUrl(repo: { owner: string; repo: string }): string {
  return `https://github.com/${repo.owner}/${repo.repo}/releases/download/${DEV_RELEASE_TAG}/`
}

/** A combined checksum list, used when the per-file one is missing. */
export const DEV_CHECKSUMS_FALLBACK = 'SHA256SUMS'
