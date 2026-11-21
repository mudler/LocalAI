import { describe, expect, it } from 'vitest'
import { migrate } from '../src/main/config'
import { registryCredentials } from '../src/main/configStore'
import { matchProblem, RegistryCredentials, type ConfigStoreLike, type SecretStoreLike } from '../src/main/registryCredentials'
import { isSecretName } from '../src/main/secretStore'
import type { LauncherConfig } from '../src/main/config'

const SECRET = 'registry-secret-value-0123456789'

function fakeSecrets(): SecretStoreLike & { map: Map<string, string> } {
  const map = new Map<string, string>()
  return {
    map,
    has: (n) => map.has(n),
    get: (n) => map.get(n),
    set: async (n, v) => void map.set(n, v),
    delete: async (n) => void map.delete(n),
  }
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

const setup = () => {
  const config = fakeConfig()
  const secrets = fakeSecrets()
  return { config, secrets, rc: new RegistryCredentials(config, secrets) }
}

describe('matchProblem', () => {
  it('accepts a host, a port, a path and an http(s) scheme', () => {
    for (const m of ['registry.example', 'https://registry.example/team', 'registry.example:5000/a/b', 'http://localhost:5000']) {
      expect(matchProblem(m), m).toBeUndefined()
    }
  })
  it('refuses user info, query, fragment, spaces and empty', () => {
    for (const m of ['user@registry.example', 'registry.example?x=1', 'registry.example#f', '', '   ', 'a b', 'ftp://registry.example']) {
      expect(typeof matchProblem(m), m).toBe('string')
    }
  })
  it('does not quote the input', () => {
    expect(matchProblem('user:pw-123@registry.example')).not.toContain('pw-123')
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
    expect((await rc.save({ match: 'a b', kind: 'bearer' })).ok).toBe(false)
    expect((await rc.save({ match: 'registry.example', kind: 'bearer', secret: 'x' })).ok).toBe(true)
    const dup = await rc.save({ match: 'registry.example', kind: 'bearer', secret: 'y' })
    expect(dup.ok).toBe(false)
    expect((await rc.save({ match: 'https://Registry.Example/', kind: 'bearer' })).ok).toBe(false)
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

  it('refuses an edit of an unknown id', async () => {
    const { rc } = setup()
    expect((await rc.save({ id: 'nope', match: 'registry.example', kind: 'bearer' })).ok).toBe(false)
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
    const a = await rc.save({ match: 'one.example', kind: 'bearer' })
    const b = await rc.save({ match: 'two.example', kind: 'bearer' })
    if (!a.ok || !b.ok) throw new Error('save')
    const na = rc.secretEnvName(a.id)
    const nb = rc.secretEnvName(b.id)
    expect(na).toMatch(/^LOCALAI_LAUNCHER_CRED_[1-9][0-9]*$/)
    expect(nb).not.toBe(na)
    await rc.save({ id: a.id, match: 'one.example/x', kind: 'bearer' })
    expect(rc.secretEnvName(a.id)).toBe(na)
    expect(() => rc.secretEnvName('nope')).toThrow()
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
      ],
    }
    const c = migrate(raw, { home: '/h' }).config
    expect(registryCredentials(c).map((r) => r.id)).toEqual(['a1'])
  })
})

describe('secret name', () => {
  it('accepts cred:<id> only', () => {
    expect(isSecretName('cred:ab12')).toBe(true)
    for (const n of ['cred:', 'cred:a b', 'cred:../x', `cred:${'a'.repeat(65)}`]) expect(isSecretName(n), n).toBe(false)
  })
})
