import { execFileSync, spawnSync } from 'node:child_process'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
// @ts-expect-error plain ESM script without type declarations
import { checkVersion, devVersion, versionFromTag } from '../scripts/version.mjs'

const script = path.resolve(__dirname, '../scripts/version.mjs')

describe('versionFromTag', () => {
  it.each([
    ['v4.2.1', '4.2.1'],
    ['v0.0.1', '0.0.1'],
    ['refs/tags/v4.12.0', '4.12.0'],
    ['v4.2.1-rc.1', '4.2.1-rc.1'],
    ['v4.2.1-rc1', '4.2.1-rc.1'],
    ['v4.2.1-rc.10', '4.2.1-rc.10'],
    ['v5.0.0-beta.2', '5.0.0-beta.2'],
    ['v5.0.0-alpha3', '5.0.0-alpha.3'],
  ])('%s gives %s', (tag, version) => {
    expect(versionFromTag(tag)).toBe(version)
  })

  it.each(['4.2.1', 'v4.2', 'v4', 'v.1.24.0', 'v0.2.0-windows-prebuilt', 'v4.2.1-rc', 'v4.2.1+build.5', 'v04.2.1', 'v4.2.1-RC.1', 'release-0.2', 'v4.2.1-rc.1-hotfix', ''])(
    'refuses the tag %j with a clear message',
    (tag) => {
      expect(() => versionFromTag(tag)).toThrow(/cannot make a launcher version from the tag/)
    },
  )
})

describe('devVersion', () => {
  it('takes the newest stable tag, adds one to the patch and appends the run', () => {
    expect(devVersion(['v4.1.2', 'v4.1.3', 'v4.0.9'], 42)).toEqual({ version: '4.1.4-dev.42', base: 'v4.1.3', skipped: [] })
  })

  it('compares numbers, not text', () => {
    expect(devVersion(['v4.9.0', 'v4.12.1', 'v4.10.0'], 1).version).toBe('4.12.2-dev.1')
  })

  it('skips a newer suffixed or malformed tag and lists it', () => {
    const r = devVersion(['v4.12.1', 'v4.13.0-rc.1', 'v.1.24.0', 'v99.0.0-windows-prebuilt', 'backup-pre-rewrite', 'v4'], 7)
    expect(r.version).toBe('4.12.2-dev.7')
    expect(r.skipped).toEqual(['v4.13.0-rc.1', 'v.1.24.0', 'v99.0.0-windows-prebuilt', 'backup-pre-rewrite', 'v4'])
  })

  it('reads git ls-remote lines, peeled tags and blank lines', () => {
    const lines = ['abc123\trefs/tags/v4.1.3', 'def456\trefs/tags/v4.1.3^{}', '', 'fff000\trefs/tags/v4.0.0']
    expect(devVersion(lines, 3).version).toBe('4.1.4-dev.3')
  })

  it('fails when no stable tag is present', () => {
    expect(() => devVersion(['v4.2.1-rc.1', 'nightly'], 1)).toThrow(/no release tag/)
    expect(() => devVersion([], 1)).toThrow(/no release tag/)
  })

  it('needs a positive integer run number', () => {
    for (const run of ['0', '-1', '1.5', 'abc', '']) expect(() => devVersion(['v1.0.0'], run)).toThrow(/run number/)
  })
})

describe('checkVersion', () => {
  it('accepts semver with or without a v', () => {
    expect(checkVersion('4.2.1')).toBe('4.2.1')
    expect(checkVersion('v4.2.1-test.3')).toBe('4.2.1-test.3')
  })
  it('refuses anything else', () => {
    for (const v of ['4.2', 'latest', '4.2.1+meta', '04.2.1']) expect(() => checkVersion(v)).toThrow(/not a version/)
  })
})

describe('version.mjs on the command line', () => {
  it('prints the version of a tag', () => {
    expect(execFileSync('node', [script, 'tag', 'v4.2.1-rc.2'], { encoding: 'utf8' })).toBe('4.2.1-rc.2\n')
  })

  it('prints the development version from tags on stdin', () => {
    const out = execFileSync('node', [script, 'dev', '42'], { input: 'v4.1.3\nv4.1.4-rc.1\n', encoding: 'utf8' })
    expect(out).toBe('4.1.4-dev.42\n')
  })

  it('fails with exit code 1 and a message on a bad tag', () => {
    const r = spawnSync('node', [script, 'tag', 'v4.2.1-hotfix'], { encoding: 'utf8' })
    expect(r.status).toBe(1)
    expect(r.stdout).toBe('')
    expect(r.stderr).toMatch(/version\.mjs: cannot make a launcher version from the tag "v4\.2\.1-hotfix"/)
  })
})
