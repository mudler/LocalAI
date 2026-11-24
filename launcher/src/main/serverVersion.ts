// Version policy for the LocalAI server binary. No Electron import.
//
// Rules:
// - A tag or a `--version` output is read with semver. Anything that does not
//   parse is never offered as an update (fail closed).
// - A tag with a pre-release part (-rc1, -beta.2, -cuda12 ...) is a
//   pre-release, and so is a release the host marks as one. A pre-release is
//   offered only when the caller opted in.
// - Only a newer version is an update. The Go launcher offered any different
//   version, which also offered downgrades; that is not ported.
import semver from 'semver'

/** Pull the first semver-looking token out of a tag or of `local-ai --version` output. */
export function parseVersion(text: string | undefined): string | undefined {
  if (!text) return undefined
  const m = /v?(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)/.exec(text)
  if (!m) return undefined
  return semver.valid(m[1]) ?? undefined
}

export function isPrereleaseVersion(version: string): boolean {
  return (semver.prerelease(version)?.length ?? 0) > 0
}

export type UpdateDecision =
  | { kind: 'update-available'; version: string }
  | { kind: 'up-to-date'; version: string }
  | { kind: 'ignored'; reason: 'prerelease' | 'invalid-tag' }

export interface DecideInput {
  /** Installed server version, or undefined when none is installed. */
  installed: string | undefined
  /** Release tag, for example v3.1.0 or v3.1.0-rc1. */
  tag: string
  /** The host's own pre-release flag for the release. */
  prerelease: boolean
  /** The user opted into pre-releases. Taken from config by the caller. */
  allowPrerelease: boolean
}

export function decideUpdate(i: DecideInput): UpdateDecision {
  const candidate = parseVersion(i.tag)
  if (!candidate) return { kind: 'ignored', reason: 'invalid-tag' }
  const pre = i.prerelease || isPrereleaseVersion(candidate)
  if (pre && !i.allowPrerelease) return { kind: 'ignored', reason: 'prerelease' }
  const installed = parseVersion(i.installed)
  if (!installed) return { kind: 'update-available', version: candidate }
  return semver.gt(candidate, installed)
    ? { kind: 'update-available', version: candidate }
    : { kind: 'up-to-date', version: installed }
}
