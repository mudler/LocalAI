'use strict'
// electron-builder afterPack hook: make the x86_64 Linux AppImage start on
// hosts without glibc (musl: Alpine, Hadron, Void-musl) as well as on glibc
// hosts. See launcher/README.md ("Portable Linux AppImage") and the comment
// block in ./AppRun for the design.
//
// For the Linux x64 target only (every other target returns at once), in the
// unpacked app directory, before the builder packs it:
//   1. collect-libs.sh runs in a pinned Debian container and adds glibc/,
//      lib/ and etc/ (the loader, the libraries and a CA bundle)
//   2. the ELF interpreter of every binary is changed from
//      /lib64/ld-linux-x86-64.so.2 to the relative path "ld"
//   3. AppRun is replaced by ./AppRun; the builder's own script is kept as
//      AppRun.electron-builder and still starts the app
// The builder copies the unpacked directory over its own AppRun when it packs
// the image, and `assertPortable` (run by the tests and by the smoke script)
// checks the result.
//
// LAUNCHER_PORTABLE=off skips the step (a local build without Docker).
const fs = require('node:fs')
const path = require('node:path')
const os = require('node:os')
const { execFileSync } = require('node:child_process')
const { patchInterpreter, readInterpreter } = require('./patch-interp.cjs')
const pins = require('./pins.json')

const OLD_INTERP = '/lib64/ld-linux-x86-64.so.2'

// The libraries we add keep their interpreter: they are never started as programs.
const SKIP_DIRS = new Set(['glibc', 'lib', 'etc'])

function* walk(dir, top = true) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name)
    if (e.isDirectory()) {
      if (!(top && SKIP_DIRS.has(e.name))) yield* walk(p, false)
    } else if (e.isFile()) {
      yield p
    }
  }
}

/** Patch every ELF file under dir that has the system interpreter. */
function patchAll(dir) {
  const patched = []
  for (const f of walk(dir)) {
    if (patchInterpreter(f, OLD_INTERP, pins.interpreter)) patched.push(path.relative(dir, f))
  }
  return patched
}

module.exports = async function afterPack(context) {
  if (context.electronPlatformName !== 'linux') return
  // Arch.x64 is 1 in electron-builder.
  if (context.arch !== 1) return
  if (process.env.LAUNCHER_PORTABLE === 'off') {
    console.log('  • portable AppImage step skipped (LAUNCHER_PORTABLE=off)')
    return
  }
  const dir = context.appOutDir
  const here = __dirname
  const uid = String(os.userInfo().uid)
  const gid = String(os.userInfo().gid)

  console.log(`  • portable AppImage: collecting libraries in ${pins.image.split('@')[0]}`)
  try {
    execFileSync(
      'docker',
      [
        'run', '--rm',
        '-e', `APT_SNAPSHOT=${pins.aptSnapshot}`,
        '-e', `HOST_UID=${uid}`,
        '-e', `HOST_GID=${gid}`,
        '-v', `${dir}:/app`,
        '-v', `${path.join(here, 'collect-libs.sh')}:/collect-libs.sh:ro`,
        pins.image,
        'bash', '/collect-libs.sh',
      ],
      { stdio: 'inherit' },
    )
  } catch (err) {
    throw new Error(
      `the portable AppImage step needs Docker and network access (${err.message}). ` +
        'Set LAUNCHER_PORTABLE=off to build without it.',
    )
  }

  const patched = patchAll(dir)
  if (!patched.includes(context.packager.executableName)) {
    throw new Error(`the launcher binary has no ${OLD_INTERP} interpreter to change (patched: ${patched.join(', ') || 'nothing'})`)
  }
  console.log(`  • portable AppImage: interpreter changed in ${patched.join(', ')}`)

  // The builder's own start script, kept as it is.
  const { generateAppRunScript } = require('app-builder-lib/out/targets/appimage/appImageUtil')
  fs.writeFileSync(path.join(dir, 'AppRun.electron-builder'), generateAppRunScript({ ExecutableName: context.packager.executableName }), { mode: 0o755 })
  fs.copyFileSync(path.join(here, 'AppRun'), path.join(dir, 'AppRun'))
  fs.chmodSync(path.join(dir, 'AppRun'), 0o755)
}

module.exports.patchAll = patchAll
module.exports.OLD_INTERP = OLD_INTERP
module.exports.readInterpreter = readInterpreter
