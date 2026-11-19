import fs from 'node:fs'
import path from 'node:path'
import { createHash } from 'node:crypto'
import { describe, expect, it } from 'vitest'
import { installAtomically, InstallError, validateExecutable } from '../src/main/atomicInstall'
import { ChecksumError, parseChecksums, verifyChecksum } from '../src/main/checksum'
import { tmpDir } from './helpers'

function elf(size = 4096, fill = 0x41, appImage = false): Buffer {
  const b = Buffer.alloc(size, fill)
  Buffer.from([0x7f, 0x45, 0x4c, 0x46]).copy(b, 0)
  if (appImage) Buffer.from([0x41, 0x49, 0x02]).copy(b, 8)
  else b.fill(0, 8, 11)
  return b
}

function setup() {
  const dir = tmpDir()
  const src = path.join(dir, 'download.bin')
  const target = path.join(dir, 'LocalAI-Launcher-x86_64.AppImage')
  const old = elf(4096, 0x4f)
  fs.writeFileSync(target, old, { mode: 0o755 })
  return { dir, src, target, old }
}
const leftovers = (dir: string) => fs.readdirSync(dir).filter((f) => f.endsWith('.staging') || f.endsWith('.part'))

describe('installAtomically', () => {
  it('replaces the target in place, keeps the name, sets mode 755 and leaves no staging file', () => {
    const { dir, src, target } = setup()
    const next = elf(8192, 0x4e)
    fs.writeFileSync(src, next, { mode: 0o600 })
    installAtomically(src, target)
    expect(fs.readFileSync(target).equals(next)).toBe(true)
    expect(fs.statSync(target).mode & 0o777).toBe(0o755)
    expect(fs.readdirSync(dir).sort()).toEqual(['LocalAI-Launcher-x86_64.AppImage', 'download.bin'])
  })

  it('can replace a binary that is running', async () => {
    const { dir, target } = setup()
    const { spawn } = await import('node:child_process')
    const exe = path.join(dir, 'sleeper')
    fs.copyFileSync('/bin/sleep', exe)
    const child = spawn(exe, ['5'])
    try {
      const src = path.join(dir, 'new.bin')
      fs.writeFileSync(src, elf())
      installAtomically(src, exe) // a plain overwrite would fail with ETXTBSY
      expect(fs.readFileSync(exe).equals(elf())).toBe(true)
    } finally {
      child.kill()
    }
    expect(target).toBeTruthy()
  })

  describe('failure paths leave the old file untouched and clean up', () => {
    const cases: [string, (s: ReturnType<typeof setup>) => void, Partial<Parameters<typeof installAtomically>[2]>, RegExp, string][] = [
      ['missing download', (s) => fs.rmSync(s.src, { force: true }), {}, /missing/, 'validate'],
      ['not an ELF file', (s) => fs.writeFileSync(s.src, Buffer.alloc(4096, 0x20)), {}, /not an ELF/, 'validate'],
      ['empty download', (s) => fs.writeFileSync(s.src, ''), {}, /too small/, 'validate'],
      ['an HTML error page', (s) => fs.writeFileSync(s.src, '<html>' + 'x'.repeat(5000)), {}, /not an ELF/, 'validate'],
      ['ELF that is not an AppImage', (s) => fs.writeFileSync(s.src, elf()), { appImage: true }, /not an AppImage/, 'validate'],
      ['copy fails (disk full)', (s) => fs.writeFileSync(s.src, elf()), { ops: { copyFile: () => { throw Object.assign(new Error('ENOSPC: no space left on device'), { code: 'ENOSPC' }) } } }, /ENOSPC/, 'stage'],
      ['chmod fails', (s) => fs.writeFileSync(s.src, elf()), { ops: { chmod: () => { throw new Error('EPERM') } } }, /EPERM/, 'chmod'],
      ['fsync fails', (s) => fs.writeFileSync(s.src, elf()), { ops: { fsync: () => { throw new Error('EIO') } } }, /EIO/, 'fsync'],
      ['rename fails', (s) => fs.writeFileSync(s.src, elf()), { ops: { rename: () => { throw new Error('EXDEV') } } }, /EXDEV/, 'rename'],
    ]
    for (const [name, prepare, opts, msg, step] of cases) {
      it(name, () => {
        const s = setup()
        prepare(s)
        let err: unknown
        try {
          installAtomically(s.src, s.target, opts)
        } catch (e) {
          err = e
        }
        expect(err).toBeInstanceOf(InstallError)
        expect((err as InstallError).message).toMatch(msg)
        expect((err as InstallError).step).toBe(step)
        expect(fs.readFileSync(s.target).equals(s.old)).toBe(true)
        expect(fs.statSync(s.target).mode & 0o777).toBe(0o755)
        expect(leftovers(s.dir)).toEqual([])
      })
    }

    it('target directory is read-only', () => {
      if (process.getuid?.() === 0) return // root ignores directory modes
      const s = setup()
      fs.writeFileSync(s.src, elf())
      const ro = path.join(s.dir, 'ro')
      fs.mkdirSync(ro)
      const t = path.join(ro, 'app')
      fs.writeFileSync(t, s.old, { mode: 0o755 })
      fs.chmodSync(ro, 0o555)
      try {
        expect(() => installAtomically(s.src, t)).toThrow(InstallError)
        expect(fs.readFileSync(t).equals(s.old)).toBe(true)
      } finally {
        fs.chmodSync(ro, 0o755)
      }
    })
  })

  it('validateExecutable accepts an AppImage marker', () => {
    const { src } = setup()
    fs.writeFileSync(src, elf(4096, 0x41, true))
    expect(() => validateExecutable(src, { appImage: true })).not.toThrow()
  })
})

describe('verifyChecksum fails closed', () => {
  const body = Buffer.from('server-binary-content')
  const sum = createHash('sha256').update(body).digest('hex')
  const name = 'local-ai-v1.2.3-linux-amd64'
  const file = () => {
    const f = path.join(tmpDir(), name)
    fs.writeFileSync(f, body)
    return f
  }

  it('passes on a matching entry, both sha256sum formats', async () => {
    await expect(verifyChecksum(file(), name, `${sum}  ${name}\n`)).resolves.toBeUndefined()
    await expect(verifyChecksum(file(), name, `${sum.toUpperCase()} *${name}\r\n`)).resolves.toBeUndefined()
  })
  it('aborts when the checksum file is missing or empty', async () => {
    await expect(verifyChecksum(file(), name, undefined)).rejects.toThrow(ChecksumError)
    await expect(verifyChecksum(file(), name, '  \n')).rejects.toThrow(/no checksum file/)
  })
  it('aborts when the list is garbage', async () => {
    await expect(verifyChecksum(file(), name, '<html>404</html>')).rejects.toThrow(/no valid entries/)
  })
  it('aborts when the asset is not listed', async () => {
    await expect(verifyChecksum(file(), name, `${sum}  other-file\n`)).rejects.toThrow(/no checksum listed/)
  })
  it('aborts on a mismatch', async () => {
    await expect(verifyChecksum(file(), name, `${'0'.repeat(64)}  ${name}\n`)).rejects.toThrow(/mismatch/)
  })
  it('parses entries', () => {
    expect([...parseChecksums(`${sum}  a\nbad line\n${sum} *b`).keys()]).toEqual(['a', 'b'])
  })
})
