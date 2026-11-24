import { describe, expect, it } from 'vitest'
import {
  APPARMOR_PROFILES_PATH,
  SYSCTL_PATH,
  decideSandboxState,
  parseProfiles,
  parseSysctl,
  probeSandbox,
  readSandboxInputs,
  type ProfileStatus,
  type SandboxState,
} from '../src/main/sandboxProbe'

describe('decideSandboxState matrix', () => {
  const sysctls = [null, 0, 1, 2]
  const profiles: ProfileStatus[] = ['loaded', 'not-loaded', 'unknown']
  for (const platform of ['linux', 'darwin', 'win32']) {
    for (const sysctl of sysctls) {
      for (const noSandboxFlag of [false, true]) {
        for (const profile of profiles) {
          const expected: SandboxState =
            platform !== 'linux'
              ? 'not-applicable'
              : !noSandboxFlag
                ? 'sandboxed'
                : sysctl === 1
                  ? 'unsandboxed-restricted'
                  : 'unsandboxed-other'
          it(`${platform} sysctl=${sysctl} flag=${noSandboxFlag} profile=${profile} -> ${expected}`, () => {
            expect(decideSandboxState({ platform, sysctl, noSandboxFlag, profile })).toBe(expected)
          })
        }
      }
    }
  }
  it('names the four named cases', () => {
    const base = { platform: 'linux', profile: 'unknown' as const }
    expect(decideSandboxState({ ...base, sysctl: 1, noSandboxFlag: false })).toBe('sandboxed')
    expect(decideSandboxState({ ...base, sysctl: 1, noSandboxFlag: true })).toBe('unsandboxed-restricted')
    expect(decideSandboxState({ ...base, sysctl: 0, noSandboxFlag: true })).toBe('unsandboxed-other')
    expect(decideSandboxState({ platform: 'darwin', sysctl: null, noSandboxFlag: true, profile: 'unknown' })).toBe(
      'not-applicable',
    )
  })
})

describe('parsers', () => {
  it('reads the sysctl', () => {
    expect(parseSysctl('1\n')).toBe(1)
    expect(parseSysctl('0')).toBe(0)
    expect(parseSysctl('')).toBeNull()
    expect(parseSysctl('x')).toBeNull()
    expect(parseSysctl('-1')).toBeNull()
  })
  it('finds the profile by exact name', () => {
    const text = 'docker-default (enforce)\nlocalai-launcher (unconfined)\nother (complain)\n'
    expect(parseProfiles(text, 'localai-launcher')).toBe('loaded')
    expect(parseProfiles('localai-launcher-x (enforce)\n', 'localai-launcher')).toBe('not-loaded')
    expect(parseProfiles('', 'localai-launcher')).toBe('not-loaded')
  })
})

describe('readSandboxInputs', () => {
  const files = (m: Record<string, string>) => (p: string) => {
    if (p in m) return m[p]
    throw Object.assign(new Error('nope'), { code: 'ENOENT' })
  }
  it('collects everything on Linux', () => {
    const i = readSandboxInputs({
      platform: 'linux',
      argv: ['app', '--no-sandbox'],
      readFile: files({ [SYSCTL_PATH]: '1\n', [APPARMOR_PROFILES_PATH]: 'localai-launcher (unconfined)\n' }),
    })
    expect(i).toEqual({ platform: 'linux', sysctl: 1, noSandboxFlag: true, profile: 'loaded' })
  })
  it('reports unknown when the profiles file is unreadable, and never throws', () => {
    const i = readSandboxInputs({ platform: 'linux', argv: ['app'], readFile: files({ [SYSCTL_PATH]: '1' }) })
    expect(i.profile).toBe('unknown')
    expect(i.sysctl).toBe(1)
  })
  it('reports a missing sysctl as null', () => {
    const i = readSandboxInputs({ platform: 'linux', argv: ['app'], readFile: files({}) })
    expect(i.sysctl).toBeNull()
  })
  it('reads no file off Linux', () => {
    let reads = 0
    const i = readSandboxInputs({
      platform: 'darwin',
      argv: ['app'],
      readFile: () => {
        reads++
        return '1'
      },
    })
    expect(reads).toBe(0)
    expect(i.sysctl).toBeNull()
  })
  it('does not match a flag that only contains the text', () => {
    const i = readSandboxInputs({ platform: 'linux', argv: ['--no-sandbox-x'], readFile: files({}) })
    expect(i.noSandboxFlag).toBe(false)
  })
  it('probeSandbox joins inputs and state', () => {
    const p = probeSandbox({ platform: 'linux', argv: ['a', '--no-sandbox'], readFile: files({ [SYSCTL_PATH]: '1' }) })
    expect(p.state).toBe('unsandboxed-restricted')
  })
})
