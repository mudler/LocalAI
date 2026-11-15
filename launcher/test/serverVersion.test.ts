import { describe, expect, it } from 'vitest'
import { decideUpdate, isPrereleaseVersion, parseVersion } from '../src/main/serverVersion'

const d = (installed: string | undefined, tag: string, over: { prerelease?: boolean; allowPrerelease?: boolean } = {}) =>
  decideUpdate({ installed, tag, prerelease: over.prerelease ?? false, allowPrerelease: over.allowPrerelease ?? false })

describe('parseVersion', () => {
  it('reads tags and version output', () => {
    expect(parseVersion('v3.1.0')).toBe('3.1.0')
    expect(parseVersion('v3.1.0-rc1')).toBe('3.1.0-rc1')
    expect(parseVersion('v3.1.0 (abcdef1)')).toBe('3.1.0')
    expect(parseVersion('LocalAI version v3.2.1-beta.2 (abc)')).toBe('3.2.1-beta.2')
  })
  it('rejects text with no version', () => {
    expect(parseVersion('latest')).toBeUndefined()
    expect(parseVersion('')).toBeUndefined()
    expect(parseVersion(undefined)).toBeUndefined()
  })
  it('flags pre-release versions', () => {
    expect(isPrereleaseVersion('3.1.0-rc1')).toBe(true)
    expect(isPrereleaseVersion('3.1.0')).toBe(false)
  })
})

describe('decideUpdate', () => {
  it('offers the latest when nothing is installed', () => {
    expect(d(undefined, 'v3.1.0')).toEqual({ kind: 'update-available', version: '3.1.0' })
  })
  it('offers a newer stable and compares numerically, not as text', () => {
    expect(d('3.9.0', 'v3.10.0')).toEqual({ kind: 'update-available', version: '3.10.0' })
  })
  it('is up to date on the same or an older release (no downgrade offer)', () => {
    expect(d('3.1.0', 'v3.1.0')).toEqual({ kind: 'up-to-date', version: '3.1.0' })
    expect(d('3.2.0', 'v3.1.0')).toEqual({ kind: 'up-to-date', version: '3.2.0' })
  })
  it('never offers a release candidate to a stable install by default', () => {
    expect(d('3.0.0', 'v3.1.0-rc1')).toEqual({ kind: 'ignored', reason: 'prerelease' })
    expect(d(undefined, 'v3.1.0-rc1')).toEqual({ kind: 'ignored', reason: 'prerelease' })
  })
  it('treats a host-flagged pre-release as a pre-release even with a plain tag', () => {
    expect(d('3.0.0', 'v3.1.0', { prerelease: true })).toEqual({ kind: 'ignored', reason: 'prerelease' })
  })
  it('treats other suffixed tags as pre-releases', () => {
    expect(d('3.0.0', 'v3.1.0-cuda12')).toEqual({ kind: 'ignored', reason: 'prerelease' })
  })
  it('offers a release candidate when the user opted in', () => {
    expect(d('3.0.0', 'v3.1.0-rc1', { allowPrerelease: true })).toEqual({ kind: 'update-available', version: '3.1.0-rc1' })
    expect(d('3.1.0-rc1', 'v3.1.0-rc2', { allowPrerelease: true })).toEqual({ kind: 'update-available', version: '3.1.0-rc2' })
  })
  it('does not offer an older rc over a newer stable even when opted in', () => {
    expect(d('3.1.0', 'v3.1.0-rc2', { allowPrerelease: true })).toEqual({ kind: 'up-to-date', version: '3.1.0' })
  })
  it('offers the stable release to a user on its release candidate', () => {
    expect(d('3.1.0-rc2', 'v3.1.0')).toEqual({ kind: 'update-available', version: '3.1.0' })
  })
  it('ignores a tag that is not a version', () => {
    expect(d('3.0.0', 'nightly')).toEqual({ kind: 'ignored', reason: 'invalid-tag' })
  })
  it('treats an unreadable installed version as not installed', () => {
    expect(d('garbage', 'v3.1.0')).toEqual({ kind: 'update-available', version: '3.1.0' })
  })
})
