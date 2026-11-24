import { describe, expect, it } from 'vitest'
import { migrate } from '../src/main/config'
import { registryCredentials } from '../src/main/configStore'
import { MAX_USERNAME, matchProblem, RegistryCredentials, usernameProblem, type ConfigStoreLike, type SecretStoreLike } from '../src/main/registryCredentials'
import { isSecretName } from '../src/main/secretStore'
import type { LauncherConfig } from '../src/main/config'

const SECRET = 'registry-secret-value-0123456789'

type Mode = 'ok' | 'session' | 'fails-late'

/** `session`: no secure storage. `fails-late`: encryption fails on the write and the value is kept in memory. */
/** `map` holds the stored values; `session` the in-memory ones, which win on read like the real store. */
function fakeSecrets(start: Mode = 'ok'): SecretStoreLike & { map: Map<string, string>; session: Map<string, string>; mode: Mode } {
  const map = new Map<string, string>()
  const session = new Map<string, string>()
  const fake = {
    map,
    session,
    mode: start,
    has: (n: string) => map.has(n) || session.has(n),
    get: (n: string) => session.get(n) ?? map.get(n),
    backend: () => (fake.mode === 'session' ? 'session' : 'keychain') as 'session' | 'keychain',
    set: async (n: string, v: string) => {
      if (fake.mode === 'ok') {
        map.set(n, v)
        session.delete(n)
        return { status: 'stored', backend: 'keychain' } as const
      }
      session.set(n, v)
      return { status: 'unavailable', kept: 'session' } as const
    },
    delete: async (n: string) => {
      map.delete(n)
      session.delete(n)
    },
    forgetSession: async (n: string) => void session.delete(n),
  }
  return fake
}

function fakeConfig(): ConfigStoreLike & { current: () => LauncherConfig } {
  let c = migrate({}, { home: '/h' }).config
  return {
    current: () => c,
    get: () => structuredClone(c),
    update: async (f) => {
      c = structuredClone(f(structuredClone(c)))
      return structuredClone(c)
    },
  }
}

const setup = (mode: Mode = 'ok') => {
  const config = fakeConfig()
  const secrets = fakeSecrets(mode)
  return { config, secrets, rc: new RegistryCredentials(config, secrets) }
}

describe('matchProblem', () => {
  it('accepts a host, a port, a path and an https scheme', () => {
    for (const m of ['registry.example', 'https://registry.example/team', 'HTTPS://registry.example', 'registry.example:5000/a/b', 'localhost:5000']) {
      expect(matchProblem(m), m).toBeUndefined()
    }
  })
  it('refuses user info, query, fragment, spaces and empty', () => {
    for (const m of ['user@registry.example', 'registry.example?x=1', 'registry.example#f', '', '   ', 'a b', 'ftp://registry.example']) {
      expect(typeof matchProblem(m), m).toBe('string')
    }
  })
  it('refuses plain http with a plain message, because the server would never use the rule', () => {
    for (const m of ['http://registry.example', 'HTTP://localhost:5000/team']) {
      expect(matchProblem(m), m).toBe('Use a registry address without http://; plain http is not supported.')
    }
  })
  it('refuses a registry longer than 253 characters', () => {
    expect(matchProblem(`${'a'.repeat(249)}.com`)).toBeUndefined()
    expect(matchProblem(`${'a'.repeat(250)}.com`)).toBe('The registry is too long.')
  })
  it('does not quote the input', () => {
    expect(matchProblem('user:pw-123@registry.example')).not.toContain('pw-123')
  })
})

describe('usernameProblem', () => {
  it('accepts plain and non-ASCII names', () => {
    for (const u of ['ann', 'robot$team+ci', 'José', 'ユーザー', 'emoji\u{1f600}', '\u00a0nbsp', 'a'.repeat(MAX_USERNAME)]) {
      expect(usernameProblem(u), u).toBeUndefined()
    }
  })
  it('refuses what the server YAML reader refuses or changes', () => {
    for (const u of ['', ' ', 42]) expect(usernameProblem(u), JSON.stringify(u)).toBe('Enter a user name.')
    const bad = [
      'a:b', 'a\u0000b', 'a\tb', 'a\u007fb', 'a\u0080b', 'a\u0085b', 'a\u009fb', 'a\ufffeb', 'a\uffffb',
      'a\ud800b', 'a\udc00b', 'a\ud83d', '\ude00a', 'a'.repeat(MAX_USERNAME + 1),
    ]
    for (const u of bad) expect(usernameProblem(u), JSON.stringify(u)).toBe('The user name is not valid.')
  })
})

describe('RegistryCredentials', () => {
  it('saves a bearer rule and lists it without the secret', async () => {
    const { rc } = setup()
    const r = await rc.save({ match: 'registry.example', kind: 'bearer', secret: SECRET })
    expect(r.ok).toBe(true)
    const list = rc.list()
    expect(list).toHaveLength(1)
    expect(list[0]).toMatchObject({ match: 'registry.example', kind: 'bearer', secretSet: true })
    expect(JSON.stringify(list)).not.toContain(SECRET)
    expect(list[0]).not.toHaveProperty('secret')
  })

  it('keeps the secret out of launcher.json', async () => {
    const { rc, config } = setup()
    await rc.save({ match: 'registry.example', kind: 'basic', username: 'ann', secret: SECRET })
    expect(JSON.stringify(config.current())).not.toContain(SECRET)
  })

  it('needs a user name for basic and ignores it for bearer', async () => {
    const { rc } = setup()
    expect((await rc.save({ match: 'registry.example', kind: 'basic', secret: 'x' })).ok).toBe(false)
    const r = await rc.save({ match: 'registry.example', kind: 'bearer', username: 'ann', secret: 'x' })
    expect(r.ok).toBe(true)
    expect(rc.list()[0].username).toBeUndefined()
  })

  it('refuses a bad match and a duplicate match', async () => {
    const { rc } = setup()
    expect((await rc.save({ match: 'a b', kind: 'bearer', secret: 'x' })).ok).toBe(false)
    expect((await rc.save({ match: 'registry.example', kind: 'bearer', secret: 'x' })).ok).toBe(true)
    const dup = await rc.save({ match: 'registry.example', kind: 'bearer', secret: 'y' })
    expect(dup.ok).toBe(false)
    expect((await rc.save({ match: 'https://Registry.Example/', kind: 'bearer', secret: 'x' })).ok).toBe(false)
    expect(rc.list()).toHaveLength(1)
  })

  it('keeps the stored secret when an edit gives none, and replaces it when given', async () => {
    const { rc, secrets } = setup()
    const r = await rc.save({ match: 'registry.example', kind: 'basic', username: 'ann', secret: SECRET })
    if (!r.ok) throw new Error('save')
    expect((await rc.save({ id: r.id, match: 'registry.example/team', kind: 'basic', username: 'bob' })).ok).toBe(true)
    expect(await rc.secretFor(r.id)).toBe(SECRET)
    expect(rc.list()[0]).toMatchObject({ match: 'registry.example/team', username: 'bob', secretSet: true })
    await rc.save({ id: r.id, match: 'registry.example/team', kind: 'basic', username: 'bob', secret: 'new-one' })
    expect(secrets.map.get(`cred:${r.id}`)).toBe('new-one')
  })

  it('refuses a basic rule whose user name the server cannot read', async () => {
    const { rc, config } = setup()
    for (const username of ['a\u0085b', 'a\u0090b', 'a\uffffb', 'a\ud800b', 'a:b']) {
      const r = await rc.save({ match: 'registry.example', kind: 'basic', username, secret: SECRET })
      expect(r, JSON.stringify(username)).toEqual({ ok: false, reason: 'The user name is not valid.' })
    }
    expect(registryCredentials(config.current())).toEqual([])
  })

  it('refuses a plain http rule', async () => {
    const { rc } = setup()
    expect(await rc.save({ match: 'http://registry.example', kind: 'bearer', secret: SECRET })).toEqual({
      ok: false,
      reason: 'Use a registry address without http://; plain http is not supported.',
    })
  })

  it('needs a new secret when an edit changes the kind or the host', async () => {
    const { rc, secrets } = setup()
    const r = await rc.save({ match: 'registry.example/team', kind: 'basic', username: 'ann', secret: SECRET })
    if (!r.ok) throw new Error('save')
    const again = { ok: false, reason: 'Enter the secret again: the kind or the registry changed.' }
    expect(await rc.save({ id: r.id, match: 'registry.example/team', kind: 'bearer' })).toEqual(again)
    expect(await rc.save({ id: r.id, match: 'other.example/team', kind: 'basic', username: 'ann' })).toEqual(again)
    expect(await rc.save({ id: r.id, match: 'registry.example:5000/team', kind: 'basic', username: 'ann' })).toEqual(again)
    expect(rc.list()[0]).toMatchObject({ match: 'registry.example/team', kind: 'basic' })
    // Same host, another path or case: the secret still goes to the same place.
    expect((await rc.save({ id: r.id, match: 'https://REGISTRY.example/other', kind: 'basic', username: 'ann' })).ok).toBe(true)
    // With a new secret both changes are fine.
    expect((await rc.save({ id: r.id, match: 'other.example', kind: 'bearer', secret: 'token-two' })).ok).toBe(true)
    expect(secrets.map.get(`cred:${r.id}`)).toBe('token-two')
  })

  it('an edit whose new secret cannot be kept leaves the old stored secret in use', async () => {
    const { rc, secrets } = setup()
    const r = await rc.save({ match: 'registry.example', kind: 'bearer', secret: SECRET })
    if (!r.ok) throw new Error('save')
    secrets.mode = 'fails-late'
    const edit = await rc.save({ id: r.id, match: 'registry.example', kind: 'bearer', secret: 'new-value-1234' })
    expect(edit.ok).toBe(false)
    expect(secrets.session.size).toBe(0)
    expect(await rc.secretFor(r.id)).toBe(SECRET)
  })

  it('an edit whose rule cannot be saved gives the rule its old secret back', async () => {
    const { rc, config, secrets } = setup()
    const r = await rc.save({ match: 'registry.example', kind: 'basic', username: 'ann', secret: SECRET })
    if (!r.ok) throw new Error('save')
    config.update = async () => {
      throw new Error('disk')
    }
    const edit = await rc.save({ id: r.id, match: 'registry.example', kind: 'bearer', secret: 'token-value-1234' })
    expect(edit).toEqual({ ok: false, reason: 'The rule could not be saved.' })
    expect(await rc.secretFor(r.id)).toBe(SECRET)
    expect(rc.list()[0]).toMatchObject({ kind: 'basic', username: 'ann' })
  })

  it('refuses an edit of an unknown id', async () => {
    const { rc } = setup()
    expect((await rc.save({ id: 'nope', match: 'registry.example', kind: 'bearer', secret: 'x' })).ok).toBe(false)
  })

  it('removes the rule and its secret', async () => {
    const { rc, secrets } = setup()
    const r = await rc.save({ match: 'registry.example', kind: 'bearer', secret: SECRET })
    if (!r.ok) throw new Error('save')
    await rc.remove(r.id)
    expect(rc.list()).toEqual([])
    expect(secrets.map.size).toBe(0)
  })

  it('gives each rule a stable env name that hides the registry', async () => {
    const { rc } = setup()
    const a = await rc.save({ match: 'one.example', kind: 'bearer', secret: 'x' })
    const b = await rc.save({ match: 'two.example', kind: 'bearer', secret: 'x' })
    if (!a.ok || !b.ok) throw new Error('save')
    const na = rc.secretEnvName(a.id)
    const nb = rc.secretEnvName(b.id)
    expect(na).toMatch(/^LOCALAI_LAUNCHER_CRED_[1-9][0-9]*$/)
    expect(nb).not.toBe(na)
    await rc.save({ id: a.id, match: 'one.example/x', kind: 'bearer' })
    expect(rc.secretEnvName(a.id)).toBe(na)
    expect(() => rc.secretEnvName('nope')).toThrow()
  })

  it('refuses a new rule without a secret', async () => {
    const { rc, config } = setup()
    for (const kind of ['basic', 'bearer'] as const) {
      expect((await rc.save({ match: 'registry.example', kind, username: 'ann' })).ok).toBe(false)
    }
    expect(rc.list()).toEqual([])
    expect(registryCredentials(config.current())).toEqual([])
  })

  it('refuses and keeps nothing when there is no secure storage', async () => {
    const { rc, config, secrets } = setup('session')
    const r = await rc.save({ match: 'registry.example', kind: 'bearer', secret: SECRET })
    expect(r.ok).toBe(false)
    expect(JSON.stringify(r)).not.toContain(SECRET)
    expect(rc.list()).toEqual([])
    expect(registryCredentials(config.current())).toEqual([])
    expect(secrets.map.size).toBe(0)
  })

  it('refuses and keeps nothing when the store answers unavailable on the write', async () => {
    const { rc, config, secrets } = setup('fails-late')
    const r = await rc.save({ match: 'registry.example', kind: 'basic', username: 'ann', secret: SECRET })
    expect(r.ok).toBe(false)
    expect(registryCredentials(config.current())).toEqual([])
    expect(secrets.map.size).toBe(0)
  })

  it('leaves no secret behind when the rule is refused', async () => {
    const { rc, secrets } = setup()
    await rc.save({ match: 'registry.example', kind: 'bearer', secret: 'x' })
    await rc.save({ match: 'registry.example', kind: 'bearer', secret: 'y' })
    expect([...secrets.map.values()]).toEqual(['x'])
  })

  it('keeps the rule when deleting its secret throws', async () => {
    const { rc, secrets } = setup()
    const r = await rc.save({ match: 'registry.example', kind: 'bearer', secret: 'x' })
    secrets.delete = async () => {
      throw new Error('disk')
    }
    await expect(rc.remove(r.ok ? r.id : '')).rejects.toThrow()
    expect(rc.list()).toHaveLength(1)
  })

  it('refuses an invalid secret without echoing it', async () => {
    const { rc } = setup()
    const r = await rc.save({ match: 'registry.example', kind: 'bearer', secret: 'a\u0000b' })
    expect(r.ok).toBe(false)
    expect(JSON.stringify(r)).not.toContain('a\u0000b')
  })

  it('survives a round trip through launcher.json and drops bad entries', () => {
    const raw = {
      registry_credentials: [
        { id: 'a1', match: 'registry.example', kind: 'basic', username: 'ann', index: 1 },
        { id: 'a1', match: 'dup.example', kind: 'basic', index: 2 },
        { id: 'b2', match: 'x.example', kind: 'other', index: 3 },
        { id: 'c3', match: 'y.example', kind: 'bearer', index: 1 },
        'junk',
        // Hand-edited entries that a save would refuse.
        { id: 'd4', match: 'http://plain.example', kind: 'bearer', index: 4 },
        { id: 'e5', match: 'a b', kind: 'bearer', index: 5 },
        { id: 'f6', match: 'c1.example', kind: 'basic', username: 'a\u0085b', index: 6 },
        { id: 'f7', match: 'lone.example', kind: 'basic', username: 'a\ud800', index: 7 },
        { id: 'f8', match: 'nouser.example', kind: 'basic', index: 8 },
        { id: 'g9', match: 'tok.example', kind: 'bearer', username: 'ignored', index: 9 },
      ],
    }
    const c = migrate(raw, { home: '/h' }).config
    const rules = registryCredentials(c)
    expect(rules.map((r) => r.id)).toEqual(['a1', 'g9'])
    expect(rules[1].username).toBeUndefined()
  })
})

describe('secret name', () => {
  it('accepts cred:<id> only', () => {
    expect(isSecretName('cred:ab12')).toBe(true)
    for (const n of ['cred:', 'cred:a b', 'cred:../x', `cred:${'a'.repeat(65)}`]) expect(isSecretName(n), n).toBe(false)
  })
})
