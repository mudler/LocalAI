#!/usr/bin/env node
// The launcher version, from a release tag or for a development build.
//
//   node scripts/version.mjs tag v4.2.1          -> 4.2.1
//   node scripts/version.mjs tag v4.2.1-rc.1     -> 4.2.1-rc.1
//   git tag --list 'v*' | node scripts/version.mjs dev 42
//                                               -> <newest vX.Y.Z, patch + 1>-dev.42
//   node scripts/version.mjs check 4.2.1-test.3  -> 4.2.1-test.3 (manual runs)
//
// `dev` reads tag names from standard input, one per line. It also accepts
// the output of `git ls-remote --tags` ("<sha>\trefs/tags/<name>").
// The launcher version equals the LocalAI release tag. An unknown tag shape
// fails, so a bad tag never makes a launcher with a wrong version.

const STABLE = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/
// Release candidates and the other suffixes the release flow accepts.
// "-rc1" is accepted and written as "-rc.1", so that rc.10 sorts after rc.9.
const PRERELEASE = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)-(rc|beta|alpha)\.?(0|[1-9]\d*)$/
// Semantic Versioning 2.0.0, without build metadata (electron-updater drops it).
const SEMVER = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?$/

function tagName(raw) {
  const line = String(raw).trim()
  const ref = line.includes('\t') ? line.split('\t').pop() : line
  return ref.replace(/^refs\/tags\//, '').replace(/\^\{\}$/, '')
}

/** The launcher version of a release tag. Throws on a tag of unknown shape. */
export function versionFromTag(raw) {
  const tag = tagName(raw)
  let m = STABLE.exec(tag)
  if (m) return `${m[1]}.${m[2]}.${m[3]}`
  m = PRERELEASE.exec(tag)
  if (m) return `${m[1]}.${m[2]}.${m[3]}-${m[4]}.${m[5]}`
  throw new Error(
    `cannot make a launcher version from the tag "${tag}". ` +
      'Expected vMAJOR.MINOR.PATCH, or a release candidate vMAJOR.MINOR.PATCH-rc.N ' +
      '(also -rcN, -beta.N, -alpha.N).',
  )
}

/**
 * The version of a development build: the newest stable tag (vX.Y.Z) with
 * the patch number plus one, and "-dev.<run>". It sorts above the last
 * release and below the next one. Tags of another shape (release
 * candidates, suffixed or malformed tags) are skipped and listed in
 * `skipped`, so one odd tag cannot break every development build.
 */
export function devVersion(rawTags, run) {
  const runText = String(run).trim()
  if (!/^[1-9]\d*$/.test(runText)) throw new Error(`the run number must be a positive integer, not "${runText}"`)
  let best = null
  const skipped = []
  for (const raw of rawTags) {
    if (!String(raw).trim()) continue
    const tag = tagName(raw)
    const m = STABLE.exec(tag)
    if (!m) {
      skipped.push(tag)
      continue
    }
    const v = [Number(m[1]), Number(m[2]), Number(m[3])]
    if (!best || v[0] > best.v[0] || (v[0] === best.v[0] && (v[1] > best.v[1] || (v[1] === best.v[1] && v[2] > best.v[2])))) {
      best = { tag, v }
    }
  }
  if (!best) {
    throw new Error(
      'no release tag of the form vMAJOR.MINOR.PATCH was found. ' +
        'Pass the tags on standard input (for example from git ls-remote --tags).',
    )
  }
  return { version: `${best.v[0]}.${best.v[1]}.${best.v[2] + 1}-dev.${runText}`, base: best.tag, skipped }
}

/** A version given by hand (manual workflow runs). Throws unless it is semver. */
export function checkVersion(raw) {
  const v = String(raw).trim().replace(/^v/, '')
  if (!SEMVER.test(v)) throw new Error(`"${raw}" is not a version of the form MAJOR.MINOR.PATCH[-PRERELEASE]`)
  return v
}

async function readStdin() {
  const chunks = []
  for await (const c of process.stdin) chunks.push(c)
  return Buffer.concat(chunks).toString('utf8').split('\n')
}

async function main(argv) {
  const [cmd, arg] = argv
  if (cmd === 'tag' && arg) return versionFromTag(arg)
  if (cmd === 'check' && arg) return checkVersion(arg)
  if (cmd === 'dev' && arg) {
    const r = devVersion(await readStdin(), arg)
    process.stderr.write(`version.mjs: base tag ${r.base}; ${r.skipped.length} tag(s) of another shape skipped\n`)
    return r.version
  }
  throw new Error('usage: version.mjs tag <tag> | dev <run number> (tags on stdin) | check <version>')
}

const isMain = process.argv[1] && import.meta.url === new URL(`file://${process.argv[1]}`).href
if (isMain) {
  main(process.argv.slice(2)).then(
    (v) => process.stdout.write(`${v}\n`),
    (e) => {
      process.stderr.write(`version.mjs: ${e.message}\n`)
      process.exit(1)
    },
  )
}
