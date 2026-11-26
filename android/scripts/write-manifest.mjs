#!/usr/bin/env node
// SPDX-License-Identifier: MIT
// Writes the update manifest (android-update.json or android-update-dev.json)
// that describes one APK. CI runs it after the APK is built and signed.
//
//   node scripts/write-manifest.mjs --app-id io.localai.client \
//     --version-name 5.0.0 --version-code 500000099 \
//     --apk out/LocalAI-Android.apk --name LocalAI-Android.apk \
//     --commit <40 hex> --notes-url https://github.com/mudler/LocalAI/releases/tag/v5.0.0 \
//     --out out/android-update.json
//
// Size and SHA-256 come from the file itself. The input is checked with the
// same limits the app applies (src/core/apkUpdate.ts), so CI fails here
// instead of publishing a manifest every installed app would refuse.

import { createHash } from 'node:crypto'
import { createReadStream } from 'node:fs'
import { stat, writeFile } from 'node:fs/promises'
import { pathToFileURL } from 'node:url'

export const MIN_SDK = 26
const MAX_VERSION_CODE = 2_100_000_000
const APP_ID = /^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$/
const VERSION_NAME = /^[0-9A-Za-z.+-]{1,64}$/
const ASSET_NAME = /^[A-Za-z0-9][A-Za-z0-9._-]*\.apk$/
const COMMIT = /^[0-9a-f]{40}$/

const FLAGS = {
  '--app-id': 'appId',
  '--version-name': 'versionName',
  '--version-code': 'versionCode',
  '--apk': 'apk',
  '--name': 'name',
  '--commit': 'commit',
  '--notes-url': 'notesUrl',
  '--out': 'out',
}

/** Parses the command line. Every flag is required and takes one value. */
export function parseArgs(argv) {
  const out = {}
  for (let i = 0; i < argv.length; i += 2) {
    const key = FLAGS[argv[i]]
    if (!key) throw new Error(`unknown argument: ${argv[i]}`)
    const value = argv[i + 1]
    if (value === undefined) throw new Error(`${argv[i]} needs a value`)
    out[key] = value
  }
  const missing = Object.entries(FLAGS).filter(([, key]) => out[key] === undefined).map(([flag]) => flag)
  if (missing.length > 0) throw new Error(`missing: ${missing.join(', ')}`)
  if (!/^[1-9][0-9]{0,9}$/.test(out.versionCode)) throw new Error(`--version-code is not a whole number: ${out.versionCode}`)
  out.versionCode = Number(out.versionCode)
  return out
}

function sha256Of(file) {
  return new Promise((resolve, reject) => {
    const hash = createHash('sha256')
    createReadStream(file)
      .on('error', reject)
      .on('data', (chunk) => hash.update(chunk))
      .on('end', () => resolve(hash.digest('hex')))
  })
}

function isHttpsUrl(v) {
  try {
    return new URL(v).protocol === 'https:'
  } catch {
    return false
  }
}

/** The manifest object for the APK at `input.apk`. Throws on input the app would refuse. */
export async function buildManifest(input) {
  const { appId, versionName, versionCode, apk, name, commit, notesUrl } = input
  if (typeof appId !== 'string' || !APP_ID.test(appId)) throw new Error(`not an application ID: ${appId}`)
  if (typeof versionName !== 'string' || !VERSION_NAME.test(versionName)) throw new Error(`not a version name: ${versionName}`)
  if (!Number.isInteger(versionCode) || versionCode < 1 || versionCode > MAX_VERSION_CODE) {
    throw new Error(`versionCode out of range 1..${MAX_VERSION_CODE}: ${versionCode}`)
  }
  if (typeof name !== 'string' || !ASSET_NAME.test(name)) throw new Error(`not a plain .apk asset name: ${name}`)
  if (typeof commit !== 'string' || !COMMIT.test(commit)) throw new Error(`not a full commit SHA: ${commit}`)
  if (!isHttpsUrl(notesUrl)) throw new Error(`notes URL is not https: ${notesUrl}`)
  const info = await stat(apk)
  if (!info.isFile() || info.size < 1) throw new Error(`not a non-empty file: ${apk}`)
  return {
    schema: 1,
    applicationId: appId,
    versionName,
    versionCode,
    minSdk: MIN_SDK,
    apk: name,
    size: info.size,
    sha256: await sha256Of(apk),
    commit,
    notesUrl,
  }
}

/** Builds the manifest and writes it to `input.out`. Returns the manifest. */
export async function writeManifest(input) {
  const manifest = await buildManifest(input)
  await writeFile(input.out, `${JSON.stringify(manifest, null, 2)}\n`)
  return manifest
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const manifest = await writeManifest(parseArgs(process.argv.slice(2)))
    console.log(JSON.stringify(manifest))
  } catch (e) {
    console.error(`write-manifest: ${e instanceof Error ? e.message : String(e)}`)
    process.exit(1)
  }
}
