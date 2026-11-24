import { describe, expect, it } from 'vitest'
import {
  ATTACH_STRATEGIES,
  InvalidAttachPattern,
  PROFILE_PATH,
  buildRemovePlan,
  buildSetupPlan,
  generateProfile,
  validateAttachPattern,
} from '../src/main/sandboxProfile'

const GOOD = ['/opt/app/launcher', '/tmp/.mount_App*/launcher', '/home/*/x/launcher?', '/usr/lib/app-1.2/bin/run_me']
const BAD: Record<string, string> = {
  empty: '',
  relative: 'opt/app',
  space: '/opt/my app/x',
  tab: '/opt/a\tb/x',
  newline: '/opt/x\n  userns,\n',
  cr: '/opt/x\r/y',
  dquote: '/opt/"x"/y',
  squote: "/opt/'x'/y",
  brace: '/opt/{a,b}/x',
  closeBrace: '/opt/a}/x',
  bracket: '/opt/[ab]/x',
  comma: '/opt/a,b/x',
  backslash: '/opt/a\\b/x',
  hash: '/opt/a#b/x',
  semicolon: '/opt/a;b/x',
  angle: '/opt/<abi/4.0>/x',
  dollar: '/opt/$HOME/x',
  at: '/opt/@{HOME}/x',
  dotdot: '/opt/../etc/x',
  doubleSlash: '/opt//x',
  trailingSlash: '/opt/x/',
  wildFirst: '/*/launcher',
  doubleStarAll: '/**',
  wildLast: '/opt/app/*',
  nul: '/opt/a\0b/x',
  unicode: '/opt/é/x',
  closeProfile: '/x } profile evil /bin/sh { userns, } #',
}

describe('validateAttachPattern', () => {
  for (const g of GOOD) it(`accepts ${g}`, () => expect(validateAttachPattern(g)).toBe(g))
  for (const [name, p] of Object.entries(BAD)) {
    it(`rejects ${name}`, () => expect(() => validateAttachPattern(p)).toThrow(InvalidAttachPattern))
  }
  it('rejects a very long pattern', () => {
    expect(() => validateAttachPattern('/' + 'a'.repeat(600))).toThrow(InvalidAttachPattern)
  })
})

describe('generateProfile', () => {
  it('writes the profile text', () => {
    const t = generateProfile('/opt/app/launcher')
    expect(t).toContain('abi <abi/4.0>,')
    expect(t).toContain('include <tunables/global>')
    expect(t).toContain('profile localai-launcher /opt/app/launcher flags=(unconfined) {')
    expect(t).toContain('  userns,')
    expect(t).toContain('include if exists <local/localai-launcher>')
    expect(t.endsWith('}\n')).toBe(true)
  })
  it('is stable', () => {
    expect(generateProfile('/opt/app/launcher')).toBe(generateProfile('/opt/app/launcher'))
  })
  it('never emits an injected line', () => {
    for (const p of Object.values(BAD)) expect(() => generateProfile(p)).toThrow()
  })
  it('has exactly one profile block and one userns rule', () => {
    const t = generateProfile('/tmp/.mount_App*/launcher')
    expect(t.match(/^profile /gm)).toHaveLength(1)
    expect(t.match(/userns,/g)).toHaveLength(1)
  })
})

describe('plans', () => {
  it('setup: write the file, then load it, with argv arrays only', () => {
    const p = buildSetupPlan('/opt/app/launcher')
    expect(p.kind).toBe('setup')
    expect(p.profileText).toBe(generateProfile('/opt/app/launcher'))
    expect(p.steps.map((s) => s.argv)).toEqual([
      ['pkexec', '/usr/bin/tee', PROFILE_PATH],
      ['pkexec', '/usr/sbin/apparmor_parser', '-r', PROFILE_PATH],
    ])
    expect(p.steps[0].input).toBe(p.profileText)
    expect(p.terminalText).toContain(`sudo apparmor_parser -r ${PROFILE_PATH}`)
    expect(p.terminalText).toContain(p.profileText!.trim())
  })
  it('remove: unload, then delete', () => {
    const p = buildRemovePlan()
    expect(p.steps.map((s) => s.argv)).toEqual([
      ['pkexec', '/usr/sbin/apparmor_parser', '-R', PROFILE_PATH],
      ['pkexec', '/usr/bin/rm', '-f', PROFILE_PATH],
    ])
    expect(p.terminalText).toBe(`sudo apparmor_parser -R ${PROFILE_PATH}\nsudo rm -f ${PROFILE_PATH}`)
  })
  it('the profile path is fixed', () => {
    expect(PROFILE_PATH).toBe('/etc/apparmor.d/localai-launcher')
  })
  it('lists the candidate strategies, each with a valid example pattern and no chosen default', () => {
    expect(ATTACH_STRATEGIES.map((s) => s.id)).toEqual(['mount-pattern', 'stable-install-path', 'extract-and-run'])
    for (const s of ATTACH_STRATEGIES) expect(() => validateAttachPattern(s.example)).not.toThrow()
  })
})
