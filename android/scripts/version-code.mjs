#!/usr/bin/env node
// SPDX-License-Identifier: MIT
// The Android versionCode of a build.
//
//   node scripts/version-code.mjs stable 4.3.0   -> 400300099
//   node scripts/version-code.mjs dev            -> minutes since 2026-01-01T00:00:00Z (at least 1)
//
// Stable: ((MAJOR*1000+MINOR)*1000+PATCH)*100+99. The last two digits leave room
// below each release (there is no RC channel in v1, so only 99 is used).
// Dev has its own application ID, so it needs no order against stable: whole
// minutes since a fixed epoch rise with every build and do not reset when the
// workflow is renamed, as run numbers would. Android needs a versionCode of
// at least 1, so the first minute counts as 1.

import { pathToFileURL } from 'node:url'

// Android refuses a versionCode above 2 100 000 000.
const MAX_VERSION_CODE = 2_100_000_000
const STABLE = /^(0|[1-9]\d{0,2})\.(0|[1-9]\d{0,2})\.(0|[1-9]\d{0,2})$/
const DEV_EPOCH_MS = Date.UTC(2026, 0, 1)

/** The versionCode of a stable version X.Y.Z. Throws on anything else. */
export function stableVersionCode(version) {
  const m = STABLE.exec(String(version))
  if (!m) throw new Error(`not a stable version MAJOR.MINOR.PATCH: "${version}"`)
  const code = ((Number(m[1]) * 1000 + Number(m[2])) * 1000 + Number(m[3])) * 100 + 99
  if (code >= MAX_VERSION_CODE) throw new Error(`versionCode ${code} of "${version}" is out of Android's range`)
  return code
}

/** The versionCode of a development build made at `now`. */
export function devVersionCode(now) {
  const minutes = Math.floor((now.getTime() - DEV_EPOCH_MS) / 60_000)
  if (!Number.isInteger(minutes) || minutes < 0) throw new Error(`build time before 2026-01-01: ${now.toISOString()}`)
  return Math.max(1, minutes)
}

function main(argv) {
  const [mode, value] = argv
  if (mode === 'stable' && value) return stableVersionCode(value)
  if (mode === 'dev') return devVersionCode(new Date())
  throw new Error('usage: version-code.mjs stable X.Y.Z | dev')
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    console.log(main(process.argv.slice(2)))
  } catch (e) {
    console.error(e instanceof Error ? e.message : String(e))
    process.exit(1)
  }
}
