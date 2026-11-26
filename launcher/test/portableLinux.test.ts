// The portable Linux AppImage (scripts/portable-linux): the ELF interpreter
// patch, the builder configuration that wires it in, and the start script's
// choice of a private loader directory (with the planted-directory cases).
import { execFileSync, spawnSync } from 'node:child_process'
import crypto from 'node:crypto'
import fs from 'node:fs'
import { createRequire } from 'node:module'
import os from 'node:os'
import path from 'node:path'
import yaml from 'js-yaml'
import { afterEach, describe, expect, it } from 'vitest'

const root = path.resolve(__dirname, '..')
const dir = path.join(root, 'scripts/portable-linux')
const req = createRequire(__filename)
const { patchInterpreter, readInterpreter } = req(path.join(dir, 'patch-interp.cjs'))
const pins = JSON.parse(fs.readFileSync(path.join(dir, 'pins.json'), 'utf8'))
const OLD = '/lib64/ld-linux-x86-64.so.2'

const tmps: string[] = []
const tmp = (): string => {
  const d = fs.mkdtempSync(path.join(os.tmpdir(), 'portable-'))
  tmps.push(d)
  return d
}
afterEach(() => {
  for (const d of tmps.splice(0)) fs.rmSync(d, { recursive: true, force: true })
})

describe('portable AppImage configuration', () => {
  const cfg: any = yaml.load(fs.readFileSync(path.join(root, 'electron-builder.yml'), 'utf8'))

  it('runs the hook, and the hook file exists', () => {
    expect(cfg.afterPack).toBe('scripts/portable-linux/after-pack.cjs')
    expect(fs.existsSync(path.join(root, cfg.afterPack))).toBe(true)
  })

  it('uses the static AppImage runtime of the builder toolset', () => {
    expect(cfg.toolsets.appimage).toMatch(/^\d+\.\d+\.\d+$/)
  })

  it('pins the container image by digest and the package snapshot by time', () => {
    expect(pins.image).toMatch(/@sha256:[0-9a-f]{64}$/)
    expect(pins.aptSnapshot).toMatch(/^\d{8}T\d{6}Z$/)
  })

  it('has an interpreter that fits in the field it replaces', () => {
    expect(Buffer.byteLength(pins.interpreter)).toBeLessThan(Buffer.byteLength(OLD))
    expect(pins.interpreter).not.toContain('/')
  })
})

// The hosts of these tests are glibc x86_64 Linux; the interpreter test needs a
// binary that names the standard loader.
const sh = '/bin/sh'
const hasStdInterp = process.platform === 'linux' && fs.existsSync(sh) && readInterpreter(fs.realpathSync(sh)) === OLD

describe('patchInterpreter', () => {
  it.skipIf(!hasStdInterp)('changes the interpreter in place and nothing else', () => {
    const d = tmp()
    const f = path.join(d, 'prog')
    fs.copyFileSync(fs.realpathSync(sh), f)
    const before = fs.readFileSync(f)
    expect(patchInterpreter(f, OLD, pins.interpreter)).toBe(true)
    const after = fs.readFileSync(f)
    expect(after.length).toBe(before.length)
    expect(readInterpreter(f)).toBe(pins.interpreter)
    let diff = 0
    for (let i = 0; i < before.length; i++) if (before[i] !== after[i]) diff++
    expect(diff).toBeGreaterThan(0)
    expect(diff).toBeLessThanOrEqual(OLD.length + 1)
    // a second run finds another interpreter and leaves the file alone
    expect(patchInterpreter(f, OLD, pins.interpreter)).toBe(false)
  })

  it('refuses a longer interpreter and ignores files that are not ELF', () => {
    const d = tmp()
    const f = path.join(d, 'text')
    fs.writeFileSync(f, '#!/bin/sh\necho hi\n')
    expect(patchInterpreter(f, OLD, 'ld')).toBe(false)
    expect(() => patchInterpreter(f, 'ld', OLD)).toThrow(/longer/)
    expect(readInterpreter(f)).toBeNull()
  })
})

// The start script, with a fake bundle: a loader that only answers --version,
// and a fake electron-builder script that reports where it runs.
describe.skipIf(process.platform !== 'linux')('AppRun', () => {
  const appRun = path.join(dir, 'AppRun')

  function bundle(): { appdir: string; loaderHash: string } {
    const appdir = tmp()
    fs.mkdirSync(path.join(appdir, 'glibc'))
    const loader = '#!/bin/sh\necho "ld.so (fake GLIBC)"\n'
    fs.writeFileSync(path.join(appdir, 'glibc/ld-linux-x86-64.so.2'), loader, { mode: 0o755 })
    fs.writeFileSync(path.join(appdir, 'AppRun.electron-builder'), '#!/bin/bash\necho "cwd=$PWD"\necho "args=$*"\necho "ldpath=$LD_LIBRARY_PATH"\n', { mode: 0o755 })
    return { appdir, loaderHash: crypto.createHash('sha256').update(loader).digest('hex').slice(0, 16) }
  }

  function run(appdir: string, home: string, extra: NodeJS.ProcessEnv = {}) {
    const env: NodeJS.ProcessEnv = { PATH: process.env.PATH, HOME: home, APPDIR: appdir, LOCALAI_LAUNCHER_BUNDLED_GLIBC: '1', ...extra }
    const r = spawnSync('sh', [appRun, '--flag', 'x'], { env, encoding: 'utf8' })
    const out = Object.fromEntries(r.stdout.split('\n').filter(Boolean).map((l) => [l.slice(0, l.indexOf('=')), l.slice(l.indexOf('=') + 1)]))
    return { status: r.status, stderr: r.stderr, ...out } as { status: number; stderr: string; cwd?: string; args?: string; ldpath?: string }
  }

  it('is valid POSIX sh', () => {
    execFileSync('sh', ['-n', appRun])
  })

  it('runs in a private directory that holds a copy of the bundled loader', () => {
    const { appdir, loaderHash } = bundle()
    const home = tmp()
    const r = run(appdir, home)
    expect(r.status).toBe(0)
    expect(r.cwd).toBe(fs.realpathSync(path.join(home, '.cache/localai-launcher', `ld-${loaderHash}`)))
    expect(r.args).toBe('--disable-gpu --flag x')
    expect(r.ldpath).toContain(`${appdir}/glibc:${appdir}/lib`)
    const st = fs.statSync(r.cwd!)
    expect(st.mode & 0o777).toBe(0o700)
    expect(st.uid).toBe(process.getuid!())
    const ld = path.join(r.cwd!, 'ld')
    expect(fs.lstatSync(ld).isFile()).toBe(true)
    expect(fs.readFileSync(ld, 'utf8')).toBe(fs.readFileSync(path.join(appdir, 'glibc/ld-linux-x86-64.so.2'), 'utf8'))
  })

  it('finds the same directory from another mount point of the image', () => {
    const a = bundle()
    const b = bundle()
    const home = tmp()
    expect(run(a.appdir, home).cwd).toBe(run(b.appdir, home).cwd)
  })

  it('replaces a loader copy that was changed', () => {
    const { appdir } = bundle()
    const home = tmp()
    const first = run(appdir, home)
    const ld = path.join(first.cwd!, 'ld')
    fs.writeFileSync(ld, '#!/bin/sh\necho tampered\n', { mode: 0o755 })
    const second = run(appdir, home)
    expect(second.status).toBe(0)
    expect(fs.readFileSync(ld, 'utf8')).toContain('fake GLIBC')
  })

  it('does not follow a planted link, and does not use a directory others can write', () => {
    const { appdir, loaderHash } = bundle()
    // First candidate: $XDG_RUNTIME_DIR/localai-launcher, planted as a link.
    const home = tmp()
    const runtime = tmp()
    const evil = tmp()
    fs.symlinkSync(evil, path.join(runtime, 'localai-launcher'))
    const planted = run(appdir, home, { XDG_RUNTIME_DIR: runtime })
    expect(planted.status).toBe(0)
    expect(fs.readdirSync(evil)).toEqual([])
    expect(planted.cwd).toBe(fs.realpathSync(path.join(home, '.cache/localai-launcher', `ld-${loaderHash}`)))

    // Planted as a directory that others can write: refused, never used.
    const home2 = tmp()
    const runtime2 = tmp()
    fs.mkdirSync(path.join(runtime2, 'localai-launcher'))
    fs.chmodSync(path.join(runtime2, 'localai-launcher'), 0o777)
    const open = run(appdir, home2, { XDG_RUNTIME_DIR: runtime2 })
    expect(open.status).toBe(0)
    expect(fs.readdirSync(path.join(runtime2, 'localai-launcher'))).toEqual([])
    expect(open.cwd).toBe(fs.realpathSync(path.join(home2, '.cache/localai-launcher', `ld-${loaderHash}`)))
  })

  it('accepts its directory when stat prints translated file types', () => {
    // A stat first on PATH that prints the German file type unless LC_ALL=C,
    // so the test does not need a German locale installed.
    const realStat = execFileSync('sh', ['-c', 'command -v stat'], { encoding: 'utf8' }).trim()
    const bin = tmp()
    fs.writeFileSync(
      path.join(bin, 'stat'),
      `#!/bin/sh\nout=$('${realStat}' "$@") || exit $?\nif [ "\${LC_ALL:-}" = C ]; then printf '%s\\n' "$out"; else printf '%s\\n' "$out" | sed 's/directory$/Verzeichnis/'; fi\n`,
      { mode: 0o755 },
    )
    expect(execFileSync(path.join(bin, 'stat'), ['-c', '%F', bin], { encoding: 'utf8', env: { PATH: process.env.PATH, LANG: 'de_DE.UTF-8' } }).trim()).toBe('Verzeichnis')
    const { appdir, loaderHash } = bundle()
    const home = tmp()
    const r = run(appdir, home, { PATH: `${bin}:${process.env.PATH}`, LANG: 'de_DE.UTF-8' })
    expect(r.stderr).toBe('')
    expect(r.status).toBe(0)
    expect(r.cwd).toBe(fs.realpathSync(path.join(home, '.cache/localai-launcher', `ld-${loaderHash}`)))
  })

  it('uses the system loader through a link on a glibc host', () => {
    // Only on a host that has the standard glibc loader.
    if (!fs.existsSync(OLD)) return
    const { appdir } = bundle()
    const home = tmp()
    const r = run(appdir, home, { LOCALAI_LAUNCHER_BUNDLED_GLIBC: '0' })
    expect(r.status).toBe(0)
    expect(path.basename(r.cwd!)).toBe('ld-system')
    expect(fs.readlinkSync(path.join(r.cwd!, 'ld'))).toBe(OLD)
    expect(r.args).toBe('--flag x')
  })
})
