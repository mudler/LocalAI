import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it, vi } from 'vitest'
import { API_KEY_PREFIX, apiKeyHint, generateApiKey, RevealLimiter } from '../src/main/apiKey'
import { encryptionUsable, isSecretName, SAFE_STORAGE_PROBE, SecretNameError, SecretStore, type SafeStorageLike } from '../src/main/secretStore'
import { tmpDir } from './helpers'

const SECRET = 'value-that-must-stay-secret-0123456789'

/** A stand-in for safeStorage: reversible, and never the plain bytes. */
// Like Electron, encryptString throws when encryption is not available.
function fakeSafe(o: { available?: boolean; backend?: string; tag?: string } = {}): SafeStorageLike {
  const tag = Buffer.from(o.tag ?? 'ENC1:')
  return {
    isEncryptionAvailable: () => o.available ?? true,
    getSelectedStorageBackend: () => o.backend ?? 'gnome_libsecret',
    encryptString: (s) => {
      if (o.available === false) throw new Error('Encryption is not available.')
      return Buffer.concat([tag, Buffer.from(s, 'utf8').map((b) => b ^ 0x5a)])
    },
    decryptString: (b) => {
      if (!b.subarray(0, tag.length).equals(tag)) throw new Error('cannot decrypt')
      return Buffer.from(b.subarray(tag.length).map((x) => x ^ 0x5a)).toString('utf8')
    },
  }
}

const fileOf = () => path.join(tmpDir('secrets-'), 'secrets.json')
const mode = (f: string) => fs.statSync(f).mode & 0o777
const leftovers = (f: string) => fs.readdirSync(path.dirname(f)).filter((n) => n.endsWith('.tmp'))

describe('names', () => {
  it('accepts the known names and env: names only', () => {
    for (const n of ['api_key', 'worker_token', 'env:HF_TOKEN', 'env:_A1']) expect(isSecretName(n), n).toBe(true)
    for (const n of ['', 'apikey', 'env:', 'env:1A', 'env:A B', 'env:A=B', `env:${'A'.repeat(129)}`, '../x']) expect(isSecretName(n), n).toBe(false)
  })

  it('refuses an unknown name before it touches anything', async () => {
    const file = fileOf()
    const s = await SecretStore.open({ file, safeStorage: fakeSafe(), platform: 'linux' })
    await expect(s.set('../../etc/x', 'v')).rejects.toBeInstanceOf(SecretNameError)
    await expect(s.set('env:A', 'a\u0000b')).rejects.toThrow()
    expect(fs.existsSync(file)).toBe(false)
  })
})

describe('encryptionUsable', () => {
  it('does not count the Linux fallback password as secure storage', () => {
    expect(encryptionUsable(fakeSafe({ backend: 'basic_text' }), 'linux')).toBe(false)
    expect(encryptionUsable(fakeSafe({ backend: 'unknown' }), 'linux')).toBe(false)
    expect(encryptionUsable(fakeSafe({ backend: 'kwallet6' }), 'linux')).toBe(true)
    expect(encryptionUsable(fakeSafe({ backend: 'basic_text' }), 'darwin')).toBe(true)
    expect(encryptionUsable(fakeSafe({ available: false }), 'darwin')).toBe(false)
    expect(encryptionUsable(undefined, 'darwin')).toBe(false)
  })

  it('needs a working round trip of a fixed probe string, whatever the backend', () => {
    for (const backend of ['gnome_libsecret', 'kwallet', 'kwallet5', 'kwallet6']) {
      expect(encryptionUsable(fakeSafe({ backend }), 'linux'), backend).toBe(true)
      // The Electron 45 case on a host with no keyring: the backend name says libsecret, encryption throws.
      expect(encryptionUsable(fakeSafe({ backend, available: false }), 'linux'), `${backend} throws`).toBe(false)
    }
    for (const backend of ['basic_text', 'unknown']) {
      const s = fakeSafe({ backend })
      const enc = vi.spyOn(s, 'encryptString')
      expect(encryptionUsable(s, 'linux'), backend).toBe(false)
      expect(enc).not.toHaveBeenCalled()
    }
    const broken = (over: Partial<SafeStorageLike>) => ({ ...fakeSafe(), ...over })
    for (const platform of ['linux', 'darwin', 'win32'] as const) {
      expect(encryptionUsable(broken({ encryptString: () => Buffer.alloc(0) }), platform), `${platform} empty`).toBe(false)
      expect(encryptionUsable(broken({ decryptString: () => 'something else' }), platform), `${platform} wrong`).toBe(false)
      expect(encryptionUsable(broken({ decryptString: () => { throw new Error('no') } }), platform), `${platform} decrypt throws`).toBe(false)
      expect(encryptionUsable(broken({ getSelectedStorageBackend: () => { throw new Error('no') } }), platform), `${platform} backend throws`).toBe(platform !== 'linux')
    }
    // macOS and Windows: the round trip alone decides, the flags are not asked.
    const flagsNo = { ...fakeSafe(), isEncryptionAvailable: () => false }
    expect(encryptionUsable(flagsNo, 'darwin')).toBe(true)
    expect(encryptionUsable(flagsNo, 'win32')).toBe(true)
  })

  it('probes with the fixed string only, once per safeStorage', () => {
    const s = fakeSafe()
    const enc = vi.spyOn(s, 'encryptString')
    expect(encryptionUsable(s, 'linux')).toBe(true)
    expect(encryptionUsable(s, 'linux')).toBe(true)
    expect(enc).toHaveBeenCalledTimes(1)
    expect(enc).toHaveBeenCalledWith(SAFE_STORAGE_PROBE)
  })
})

describe('with secure storage', () => {
  it('stores encrypted values in a 0600 file, never the plain text, and reads them back', async () => {
    const file = fileOf()
    const s = await SecretStore.open({ file, safeStorage: fakeSafe(), platform: 'linux' })
    expect(s.state()).toEqual({ backend: 'keychain', encryptionAvailable: true, plaintextOptIn: false, unreadable: 0 })
    expect(await s.set('api_key', SECRET)).toEqual({ status: 'stored', backend: 'keychain' })
    await s.set('env:HF_TOKEN', 'second-secret-value')
    expect(s.get('api_key')).toBe(SECRET)
    expect(s.list()).toEqual(['api_key', 'env:HF_TOKEN'])
    expect(s.list('env:')).toEqual(['env:HF_TOKEN'])
    expect(mode(file)).toBe(0o600)
    const text = fs.readFileSync(file, 'utf8')
    expect(text).not.toContain(SECRET)
    expect(text).not.toContain(Buffer.from(SECRET).toString('base64'))
    expect(JSON.parse(text)).toMatchObject({ version: 1, format: 'safeStorage' })
    expect(leftovers(file)).toEqual([])

    const again = await SecretStore.open({ file, safeStorage: fakeSafe(), platform: 'linux' })
    expect(again.get('api_key')).toBe(SECRET)
    await again.delete('api_key')
    await again.delete('env:HF_TOKEN')
    expect(again.list()).toEqual([])
    expect(fs.existsSync(file)).toBe(false)
  })

  it('tightens the mode of an existing file', async () => {
    const file = fileOf()
    fs.writeFileSync(file, JSON.stringify({ version: 1, format: 'safeStorage', secrets: {} }), { mode: 0o644 })
    fs.chmodSync(file, 0o644)
    await SecretStore.open({ file, safeStorage: fakeSafe(), platform: 'linux' })
    expect(mode(file)).toBe(0o600)
  })

  it('moves a plain file into secure storage once it works', async () => {
    const file = fileOf()
    const plain = await SecretStore.open({ file, platform: 'linux', plaintextOptIn: true })
    await plain.set('api_key', SECRET)
    expect(fs.readFileSync(file, 'utf8')).toContain(Buffer.from(SECRET).toString('base64'))
    const s = await SecretStore.open({ file, safeStorage: fakeSafe(), platform: 'linux' })
    expect(s.get('api_key')).toBe(SECRET)
    expect(s.state().backend).toBe('keychain')
    const text = fs.readFileSync(file, 'utf8')
    expect(JSON.parse(text).format).toBe('safeStorage')
    expect(text).not.toContain(Buffer.from(SECRET).toString('base64'))
  })

  it('keeps blobs it cannot decrypt, unchanged, and never overwrites them', async () => {
    const file = fileOf()
    const a = await SecretStore.open({ file, safeStorage: fakeSafe({ tag: 'KEY-A:' }), platform: 'linux' })
    await a.set('api_key', SECRET)
    const before = fs.readFileSync(file, 'utf8')
    // Another key (a copied home folder): nothing can be read, nothing is lost.
    const b = await SecretStore.open({ file, safeStorage: fakeSafe({ tag: 'KEY-B:' }), platform: 'linux' })
    expect(b.get('api_key')).toBeUndefined()
    expect(b.state().unreadable).toBe(1)
    expect(fs.readFileSync(file, 'utf8')).toBe(before)
    // A locked keyring: the same.
    const locked = await SecretStore.open({ file, safeStorage: fakeSafe({ available: false }), platform: 'linux', plaintextOptIn: true })
    expect(locked.state()).toMatchObject({ backend: 'session', unreadable: 1 })
    expect(await locked.set('env:A', 'x')).toEqual({ status: 'unavailable', kept: 'session' })
    expect(fs.readFileSync(file, 'utf8')).toBe(before)
  })

  it('moves a corrupt file aside without reading it into memory', async () => {
    const file = fileOf()
    fs.writeFileSync(file, `{ "secrets": "${SECRET}"`)
    const logs: string[] = []
    const s = await SecretStore.open({ file, safeStorage: fakeSafe(), platform: 'linux', log: (m) => logs.push(m) })
    expect(s.list()).toEqual([])
    expect(fs.existsSync(file)).toBe(false)
    expect(fs.readdirSync(path.dirname(file)).some((n) => n.startsWith('secrets.json.bad-'))).toBe(true)
    expect(logs.join('\n')).not.toContain(SECRET)
  })
})

describe('without secure storage', () => {
  it('keeps values for this session only and says so; writes no file', async () => {
    const file = fileOf()
    const s = await SecretStore.open({ file, safeStorage: fakeSafe({ backend: 'basic_text' }), platform: 'linux' })
    expect(s.state()).toEqual({ backend: 'session', encryptionAvailable: false, plaintextOptIn: false, unreadable: 0 })
    expect(await s.set('api_key', SECRET)).toEqual({ status: 'unavailable', kept: 'session' })
    expect(s.get('api_key')).toBe(SECRET)
    expect(s.isSessionOnly('api_key')).toBe(true)
    expect(fs.existsSync(file)).toBe(false)
    const next = await SecretStore.open({ file, platform: 'linux' })
    expect(next.get('api_key')).toBeUndefined()
  })

  it('writes a 0600 plain file only after the opt-in, and removes it when the opt-in is turned off', async () => {
    const file = fileOf()
    const s = await SecretStore.open({ file, platform: 'linux' })
    await s.set('env:HF_TOKEN', SECRET)
    expect(fs.existsSync(file)).toBe(false)
    expect((await s.setPlaintextOptIn(true)).backend).toBe('plaintext-file')
    expect(mode(file)).toBe(0o600)
    expect(JSON.parse(fs.readFileSync(file, 'utf8')).format).toBe('plaintext')
    expect(await s.set('api_key', 'k'.repeat(20))).toEqual({ status: 'stored', backend: 'plaintext-file' })
    const reopened = await SecretStore.open({ file, platform: 'linux', plaintextOptIn: true })
    expect(reopened.get('env:HF_TOKEN')).toBe(SECRET)
    // A reset config (opt-in missing) does not destroy the file the user chose.
    const reset = await SecretStore.open({ file, platform: 'linux' })
    expect(reset.state().plaintextOptIn).toBe(true)
    expect(reset.get('env:HF_TOKEN')).toBe(SECRET)
    // Turning it off is explicit: the file goes, the values stay for this session.
    expect((await reset.setPlaintextOptIn(false)).backend).toBe('session')
    expect(fs.existsSync(file)).toBe(false)
    expect(reset.get('env:HF_TOKEN')).toBe(SECRET)
  })
})

describe('when encryption fails after the probe', () => {
  /** Passes the start probe, then throws (or returns nothing) for every real value. */
  function failsLater(mode: 'throw' | 'empty'): SafeStorageLike {
    const s = fakeSafe()
    const real = s.encryptString
    return {
      ...s,
      encryptString: (v) => {
        if (v === SAFE_STORAGE_PROBE) return real(v)
        if (mode === 'empty') return Buffer.alloc(0)
        throw new Error('Encryption is not available.')
      },
    }
  }

  for (const mode of ['throw', 'empty'] as const) {
    it(`keeps the value for this session and switches the store to session-only (${mode})`, async () => {
      const file = fileOf()
      // One value stored for good while encryption worked.
      const before = await SecretStore.open({ file, safeStorage: fakeSafe(), platform: 'linux' })
      await before.set('env:OLD', 'old-value-kept-on-disk-0001')
      const disk = fs.readFileSync(file, 'utf8')

      const logs: string[] = []
      const safe = failsLater(mode)
      const s = await SecretStore.open({ file, safeStorage: safe, platform: 'linux', log: (m) => logs.push(m) })
      expect(s.state().backend).toBe('keychain')
      expect(await s.set('api_key', SECRET)).toEqual({ status: 'unavailable', kept: 'session' })
      expect(s.get('api_key')).toBe(SECRET)
      expect(s.isSessionOnly('api_key')).toBe(true)
      expect(s.state()).toEqual({ backend: 'session', encryptionAvailable: false, plaintextOptIn: false, unreadable: 0 })
      // The whole store is session-only now: no second try, the file is not touched.
      const enc = vi.spyOn(safe, 'encryptString')
      expect(await s.set('env:HF_TOKEN', 'second-secret-value')).toEqual({ status: 'unavailable', kept: 'session' })
      expect(enc).not.toHaveBeenCalled()
      // A new value of a stored name wins for this session; the stored one stays on disk.
      expect(await s.set('env:OLD', 'new-value-for-this-session')).toEqual({ status: 'unavailable', kept: 'session' })
      expect(s.get('env:OLD')).toBe('new-value-for-this-session')
      expect(s.list()).toEqual(['api_key', 'env:HF_TOKEN', 'env:OLD'])
      expect(fs.readFileSync(file, 'utf8')).toBe(disk)
      // The plain-file opt-in does not mix plain values into a file of encrypted blobs.
      expect((await s.setPlaintextOptIn(true)).backend).toBe('session')
      expect(fs.readFileSync(file, 'utf8')).toBe(disk)
      const text = logs.join('\n')
      expect(text).toContain('secrets: secure storage could not encrypt api_key; values are kept for this session only')
      for (const v of [SECRET, 'second-secret-value', 'new-value-for-this-session']) expect(text).not.toContain(v)
    })
  }

  it('leaves a plain file as it is when its values cannot be moved into secure storage', async () => {
    const file = fileOf()
    const plain = await SecretStore.open({ file, platform: 'linux', plaintextOptIn: true })
    await plain.set('api_key', SECRET)
    const disk = fs.readFileSync(file, 'utf8')
    const s = await SecretStore.open({ file, safeStorage: failsLater('throw'), platform: 'linux' })
    expect(s.get('api_key')).toBe(SECRET)
    expect(s.state()).toMatchObject({ backend: 'plaintext-file', encryptionAvailable: false })
    expect(fs.readFileSync(file, 'utf8')).toBe(disk)
  })
})

describe('API key', () => {
  it('is 32 random bytes in URL-safe base64 after the prefix', () => {
    const k = generateApiKey()
    expect(k.startsWith(API_KEY_PREFIX)).toBe(true)
    expect(k).toMatch(/^lai-[A-Za-z0-9_-]{43}$/)
    expect(Buffer.from(k.slice(4), 'base64url')).toHaveLength(32)
    expect(generateApiKey()).not.toBe(k)
    expect(generateApiKey(() => Buffer.alloc(32, 0xff))).toBe(`lai-${Buffer.alloc(32, 0xff).toString('base64url')}`)
    expect(() => generateApiKey(() => Buffer.alloc(8))).toThrow()
  })

  it('hint shows the prefix and the last 4 characters, nothing of a short key', () => {
    expect(apiKeyHint('lai-abcdefghijklmnopqrstuvwxyz3f9a')).toEqual({ prefix: 'lai-', last4: '3f9a' })
    expect(apiKeyHint('short-key')).toEqual({ prefix: '', last4: '' })
  })

  it('reveal limit: a gap between requests and a cap per minute', () => {
    const l = new RevealLimiter(3, 60_000, 1_000)
    expect(l.take(0)).toBe(true)
    expect(l.take(500)).toBe(false)
    expect(l.take(1_000)).toBe(true)
    expect(l.take(2_000)).toBe(true)
    expect(l.take(3_000)).toBe(false)
    expect(l.take(61_000)).toBe(true)
  })
})
