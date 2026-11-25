// SPDX-License-Identifier: MIT
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { buildManifest, parseArgs, writeManifest } from '../scripts/write-manifest.mjs'
import { checkManifest } from '../src/core/apkUpdate'

// "hello\n": 6 bytes, SHA-256 known from `printf 'hello\n' | sha256sum`.
const BYTES = 'hello\n'
const SHA = '5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03'
const COMMIT = '0123456789abcdef0123456789abcdef01234567'

let dir = ''
let apk = ''

beforeAll(async () => {
  dir = await mkdtemp(path.join(tmpdir(), 'write-manifest-'))
  apk = path.join(dir, 'app-stable-release.apk')
  await writeFile(apk, BYTES)
})

afterAll(async () => {
  await rm(dir, { recursive: true, force: true })
})

const base = () => ({
  appId: 'io.localai.client',
  versionName: '5.0.0',
  versionCode: 500000099,
  apk,
  name: 'LocalAI-Android.apk',
  commit: COMMIT,
  notesUrl: 'https://github.com/mudler/LocalAI/releases/tag/v5.0.0',
})

describe('buildManifest', () => {
  it('describes the file: size and SHA-256 from its bytes, the given asset name, minSdk 26', async () => {
    const m = await buildManifest(base())
    expect(m).toEqual({
      schema: 1,
      applicationId: 'io.localai.client',
      versionName: '5.0.0',
      versionCode: 500000099,
      minSdk: 26,
      apk: 'LocalAI-Android.apk',
      size: 6,
      sha256: SHA,
      commit: COMMIT,
      notesUrl: 'https://github.com/mudler/LocalAI/releases/tag/v5.0.0',
    })
  })

  it('writes a manifest the app accepts', async () => {
    const m = await buildManifest(base())
    const check = checkManifest(JSON.parse(JSON.stringify(m)), {
      applicationId: 'io.localai.client',
      versionCode: 400200199,
      versionName: '4.2.1',
      sdkInt: 34,
      feedBase: 'https://github.com/mudler/LocalAI/releases',
      channel: 'stable',
    })
    expect(check.ok).toBe(true)
  })

  it('refuses bad input instead of writing a manifest the app would refuse', async () => {
    await expect(buildManifest({ ...base(), versionCode: 0 })).rejects.toThrow()
    await expect(buildManifest({ ...base(), versionCode: 2_100_000_001 })).rejects.toThrow()
    await expect(buildManifest({ ...base(), versionName: '5.0.0 beta' })).rejects.toThrow()
    await expect(buildManifest({ ...base(), appId: 'not an id' })).rejects.toThrow()
    await expect(buildManifest({ ...base(), name: '../LocalAI-Android.apk' })).rejects.toThrow()
    await expect(buildManifest({ ...base(), commit: 'main' })).rejects.toThrow()
    await expect(buildManifest({ ...base(), notesUrl: 'http://example.com/notes' })).rejects.toThrow()
    await expect(buildManifest({ ...base(), apk: path.join(dir, 'missing.apk') })).rejects.toThrow()
  })
})

describe('the command line', () => {
  it('parses every flag and refuses unknown or missing ones', () => {
    const args = parseArgs([
      '--app-id', 'io.localai.client.dev',
      '--version-name', '5.0.1-dev.12',
      '--version-code', '473328',
      '--apk', 'out/x.apk',
      '--name', 'LocalAI-Android-dev.apk',
      '--commit', COMMIT,
      '--notes-url', 'https://github.com/mudler/LocalAI/releases/tag/dev-master',
      '--out', 'out/android-update-dev.json',
    ])
    expect(args).toEqual({
      appId: 'io.localai.client.dev',
      versionName: '5.0.1-dev.12',
      versionCode: 473328,
      apk: 'out/x.apk',
      name: 'LocalAI-Android-dev.apk',
      commit: COMMIT,
      notesUrl: 'https://github.com/mudler/LocalAI/releases/tag/dev-master',
      out: 'out/android-update-dev.json',
    })
    expect(() => parseArgs(['--app-id', 'io.localai.client'])).toThrow()
    expect(() => parseArgs(['--colour', 'red'])).toThrow()
    expect(() => parseArgs(['--version-code', '12a'])).toThrow()
  })

  it('writes the manifest file as JSON with a final newline', async () => {
    const out = path.join(dir, 'android-update.json')
    await writeManifest({ ...base(), out })
    const text = await readFile(out, 'utf8')
    expect(text.endsWith('\n')).toBe(true)
    expect(JSON.parse(text).sha256).toBe(SHA)
  })
})
