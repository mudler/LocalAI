import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import yaml from 'js-yaml'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import {
  CREDENTIALS_FILE_NAME,
  prepareRegistryCredentials,
  renderCredentialsYaml,
  writeCredentialsFile,
  type CredentialSource,
} from '../src/main/credentialsFile'
import { buildServerLaunch, userCredentialsFile } from '../src/main/serverLaunch'
import type { RegistryCredentialRule } from '../src/shared/types'

const KEYS = {
  basic: new Set(['match', 'username', 'password_env']),
  bearer: new Set(['match', 'bearer_env']),
}

/** Mirrors the server's strict parse: a list, a known key set, exactly one kind, no secret values. */
function strictParse(text: string): Array<Record<string, string>> {
  const doc = yaml.load(text)
  expect(Array.isArray(doc)).toBe(true)
  for (const e of doc as Array<Record<string, unknown>>) {
    const keys = Object.keys(e)
    const kind = 'password_env' in e ? 'basic' : 'bearer_env' in e ? 'bearer' : undefined
    expect(kind, JSON.stringify(keys)).toBeDefined()
    for (const k of keys) expect(KEYS[kind!].has(k), `unknown key ${k}`).toBe(true)
    if (kind === 'basic') expect(typeof e.username).toBe('string')
    for (const v of Object.values(e)) expect(typeof v).toBe('string')
  }
  return doc as Array<Record<string, string>>
}

const basic: RegistryCredentialRule = { id: 'a1', match: 'registry.example/team', kind: 'basic', username: 'alice' }
const bearer: RegistryCredentialRule = { id: 'b2', match: 'other.example:5000', kind: 'bearer' }
const envName = (id: string) => ({ a1: 'LOCALAI_LAUNCHER_CRED_1', b2: 'LOCALAI_LAUNCHER_CRED_2' })[id] ?? 'LOCALAI_LAUNCHER_CRED_9'

let dir: string
beforeEach(() => {
  dir = fs.mkdtempSync(path.join(process.env.TEST_TMP ?? os.tmpdir(), 'creds-'))
})
afterEach(() => fs.rmSync(dir, { recursive: true, force: true }))

describe('renderCredentialsYaml', () => {
  it('writes env references only, in the server schema', () => {
    const text = renderCredentialsYaml([basic, bearer], envName)
    expect(text).toBe(
      [
        '- match: "registry.example/team"',
        '  username: "alice"',
        '  password_env: "LOCALAI_LAUNCHER_CRED_1"',
        '- match: "other.example:5000"',
        '  bearer_env: "LOCALAI_LAUNCHER_CRED_2"',
        '',
      ].join('\n'),
    )
    expect(text).not.toMatch(/kind/)
    expect(strictParse(text)).toHaveLength(2)
  })

  it('quotes a user name with YAML syntax so it stays one string', () => {
    const odd: RegistryCredentialRule = { id: 'a1', match: 'registry.example', kind: 'basic', username: 'x"\n- match: evil #' }
    const [e] = strictParse(renderCredentialsYaml([odd], envName))
    expect(e.username).toBe('x"\n- match: evil #')
  })
})

describe('writeCredentialsFile', () => {
  it('writes mode 0600 under its own name and leaves no temp file', async () => {
    const f = await writeCredentialsFile(dir, [basic], envName)
    expect(f).toBe(path.join(dir, CREDENTIALS_FILE_NAME))
    expect(fs.statSync(f!).mode & 0o777).toBe(0o600)
    expect(fs.readdirSync(dir)).toEqual([CREDENTIALS_FILE_NAME])
  })

  it('removes the file and returns undefined with no rules', async () => {
    await writeCredentialsFile(dir, [basic], envName)
    expect(await writeCredentialsFile(dir, [], envName)).toBeUndefined()
    expect(fs.readdirSync(dir)).toEqual([])
  })

  it('never touches the credentials.yaml of the data folder', async () => {
    fs.writeFileSync(path.join(dir, 'credentials.yaml'), 'hand edited')
    await writeCredentialsFile(dir, [basic], envName)
    await writeCredentialsFile(dir, [], envName)
    expect(fs.readFileSync(path.join(dir, 'credentials.yaml'), 'utf8')).toBe('hand edited')
  })
})

describe('prepareRegistryCredentials', () => {
  const source = (secrets: Record<string, string>): CredentialSource => ({
    list: () => [basic, bearer],
    secretEnvName: envName,
    secretFor: async (id) => secrets[id],
  })

  it('returns the file and the secret variables', async () => {
    const p = await prepareRegistryCredentials(dir, source({ a1: 'pw-one', b2: 'tok-two' }), false)
    expect(p.file).toBe(path.join(dir, CREDENTIALS_FILE_NAME))
    expect(p.env).toEqual({ LOCALAI_LAUNCHER_CRED_1: 'pw-one', LOCALAI_LAUNCHER_CRED_2: 'tok-two' })
    expect(fs.readFileSync(p.file!, 'utf8')).not.toMatch(/pw-one|tok-two/)
  })

  it('skips a rule whose secret is missing, in the file and the variables', async () => {
    const p = await prepareRegistryCredentials(dir, source({ b2: 'tok-two' }), false)
    expect(p.env).toEqual({ LOCALAI_LAUNCHER_CRED_2: 'tok-two' })
    const entries = strictParse(fs.readFileSync(p.file!, 'utf8'))
    expect(entries.map((e) => e.match)).toEqual(['other.example:5000'])
  })

  it('writes nothing and removes a stale file when no rule has a secret', async () => {
    await writeCredentialsFile(dir, [basic], envName)
    const p = await prepareRegistryCredentials(dir, source({}), false)
    expect(p).toEqual({ env: {}, userOverride: false })
    expect(fs.readdirSync(dir)).toEqual([])
  })

  it('writes nothing and removes an earlier generated file when the user set their own', async () => {
    await writeCredentialsFile(dir, [basic], envName)
    fs.writeFileSync(path.join(dir, 'credentials.yaml'), 'hand edited')
    const p = await prepareRegistryCredentials(dir, source({ a1: 'pw-one' }), true)
    expect(p).toEqual({ env: {}, userOverride: true })
    expect(fs.readdirSync(dir)).toEqual(['credentials.yaml'])
  })
})

describe('buildServerLaunch with registry credentials', () => {
  const c = { models_path: '/m', backends_path: '/b', address: '127.0.0.1:8080', log_level: 'info' }
  const reg = { file: '/cfg/registry-credentials.yaml', env: { LOCALAI_LAUNCHER_CRED_1: 'pw-one-secret' } }

  it('sets the file and the secret variables, and keeps the secrets out of the arguments', () => {
    const l = buildServerLaunch(c, '/d', { PATH: '/usr/bin' }, undefined, {}, 'stable', reg)
    expect(l.env.LOCALAI_CREDENTIALS_FILE).toBe(reg.file)
    expect(l.env.LOCALAI_LAUNCHER_CRED_1).toBe('pw-one-secret')
    expect(l.credentialsUserOverride).toBe(false)
    expect(l.args.join(' ')).not.toMatch(/pw-one-secret|credentials/)
  })

  it('passes a file the user set in the system environment and ignores the launcher file', () => {
    const l = buildServerLaunch(c, '/d', { LOCALAI_CREDENTIALS_FILE: '/mine.yaml' }, undefined, {}, 'stable', reg)
    expect(l.env.LOCALAI_CREDENTIALS_FILE).toBe('/mine.yaml')
    expect(l.env.LOCALAI_LAUNCHER_CRED_1).toBeUndefined()
    expect(l.credentialsUserOverride).toBe(true)
  })

  it('sets nothing without registry credentials', () => {
    const l = buildServerLaunch(c, '/d', {}, undefined, {})
    expect(l.env.LOCALAI_CREDENTIALS_FILE).toBeUndefined()
    expect(l.credentialsUserOverride).toBe(false)
  })

  it('passes a file that comes from the Settings variables when the system has none', () => {
    const l = buildServerLaunch(c, '/d', {}, undefined, { env: { LOCALAI_CREDENTIALS_FILE: '/from-settings.yaml' } }, 'stable', reg)
    expect(l.env.LOCALAI_CREDENTIALS_FILE).toBe('/from-settings.yaml')
    expect(l.env.LOCALAI_LAUNCHER_CRED_1).toBeUndefined()
    expect(l.credentialsUserOverride).toBe(true)
  })

  it('prefers the Settings value over the system one, and skips an empty value', () => {
    expect(userCredentialsFile({ LOCALAI_CREDENTIALS_FILE: '/sys.yaml' }, { LOCALAI_CREDENTIALS_FILE: '/settings.yaml' })).toBe('/settings.yaml')
    expect(userCredentialsFile({ LOCALAI_CREDENTIALS_FILE: '/sys.yaml' }, { LOCALAI_CREDENTIALS_FILE: '' })).toBe('/sys.yaml')
  })

  it('finds a user value by name, case-blind on Windows', () => {
    expect(userCredentialsFile({ localai_credentials_file: '/x' }, {}, 'win32')).toBe('/x')
    expect(userCredentialsFile({ localai_credentials_file: '/x' }, {}, 'linux')).toBeUndefined()
    expect(userCredentialsFile({ LOCALAI_CREDENTIALS_FILE: '' })).toBeUndefined()
  })
})
