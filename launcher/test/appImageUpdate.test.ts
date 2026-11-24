import fs from 'node:fs'
import path from 'node:path'
import { createHash } from 'node:crypto'
import { describe, expect, it } from 'vitest'
import { appImageEntry, installPath, swapAppImage, type DownloadedAppImage } from '../src/main/appImageUpdate'
import { sha512Base64Sync } from '../src/main/checksum'
import { tmpDir } from './helpers'

/** A small file with the ELF magic and the AppImage type 2 marker. */
function appImage(size = 8192, fill = 0x4e): Buffer {
  const b = Buffer.alloc(size, fill)
  Buffer.from([0x7f, 0x45, 0x4c, 0x46]).copy(b, 0)
  Buffer.from([0x41, 0x49, 0x02]).copy(b, 8)
  return b
}
const sha512 = (b: Buffer) => createHash('sha512').update(b).digest('base64')
const errno = (code: string) => Object.assign(new Error(`${code}: simulated`), { code })

function setup(o: { mode?: number; next?: Buffer } = {}) {
  const dir = tmpDir('swap-')
  const apps = path.join(dir, 'Applications')
  const cache = path.join(dir, 'cache', 'pending')
  fs.mkdirSync(apps, { recursive: true })
  fs.mkdirSync(cache, { recursive: true })
  const target = path.join(apps, 'LocalAI-Launcher-x86_64.AppImage')
  const old = appImage(4096, 0x4f)
  fs.writeFileSync(target, old)
  fs.chmodSync(target, o.mode ?? 0o755)
  const next = o.next ?? appImage()
  const file = path.join(cache, 'LocalAI-Launcher-x86_64.AppImage')
  fs.writeFileSync(file, next, { mode: 0o755 })
  const download: DownloadedAppImage = { file, sha512: sha512(next), size: next.length, version: '4.2.1' }
  return { dir, apps, target, old, next, file, download }
}
const leftovers = (dir: string) => fs.readdirSync(dir).filter((f) => f !== 'LocalAI-Launcher-x86_64.AppImage')

describe('swapAppImage', () => {
  it('replaces the AppImage in place: same name, same mode, new bytes, nothing left over', () => {
    const s = setup({ mode: 0o750 })
    const r = swapAppImage(s.download, s.target)
    expect(r).toEqual({ ok: true, target: s.target })
    expect(fs.readFileSync(s.target).equals(s.next)).toBe(true)
    expect(fs.statSync(s.target).mode & 0o777).toBe(0o750)
    expect(leftovers(s.apps)).toEqual([])
    // The download stays where electron-updater keeps it; it is copied, not moved.
    expect(fs.existsSync(s.file)).toBe(true)
  })

  it('keeps a symlink to the AppImage working', () => {
    const s = setup()
    const link = path.join(s.dir, 'launcher')
    fs.symlinkSync(s.target, link)
    expect(swapAppImage(s.download, s.target).ok).toBe(true)
    expect(fs.readFileSync(link).equals(s.next)).toBe(true)
  })

  it('hashes the staged copy, read from disk, once', () => {
    const s = setup()
    const hashed: string[] = []
    const r = swapAppImage(s.download, s.target, { hash: (f) => (hashed.push(f), sha512Base64Sync(f)) })
    expect(r.ok).toBe(true)
    expect(hashed).toHaveLength(1)
    expect(path.dirname(hashed[0])).toBe(s.apps)
    expect(path.basename(hashed[0])).toMatch(/^\.LocalAI-Launcher-x86_64\.AppImage\.[0-9a-f]+\.staging$/)
  })

  describe('failures leave the old AppImage untouched, remove the staged copy and give a stable reason', () => {
    const cases: [string, (s: ReturnType<typeof setup>) => Parameters<typeof swapAppImage>, string][] = [
      ['not an AppImage run (APPIMAGE unset)', (s) => [s.download, undefined], 'not-appimage'],
      ['APPIMAGE not absolute', (s) => [s.download, 'LocalAI.AppImage'], 'not-appimage'],
      ['no download', (s) => [undefined, s.target], 'no-download'],
      ['no checksum in the feed', (s) => [{ ...s.download, sha512: undefined }, s.target], 'no-download'],
      ['download missing from the cache', (s) => (fs.rmSync(s.file), [s.download, s.target]), 'no-download'],
      ['wrong size', (s) => [{ ...s.download, size: s.next.length + 1 }, s.target], 'size-mismatch'],
      ['wrong magic: not ELF', (s) => (fs.writeFileSync(s.file, Buffer.alloc(8192, 0x3c)), [{ ...s.download, size: undefined }, s.target]), 'not-an-appimage'],
      [
        'wrong magic: ELF but not an AppImage',
        (s) => {
          const b = appImage()
          b.fill(0, 8, 11)
          fs.writeFileSync(s.file, b)
          return [s.download, s.target]
        },
        'not-an-appimage',
      ],
      ['checksum differs', (s) => [{ ...s.download, sha512: sha512(Buffer.from('other')) }, s.target], 'checksum-mismatch'],
      ['disk full while staging', (s) => [s.download, s.target, { ops: { copyFile: () => { throw errno('ENOSPC') } } }], 'no-space'],
      [
        'interrupted copy (the copy stops half way with an error)',
        (s) => [
          s.download,
          s.target,
          {
            ops: {
              copyFile: (src: string, dst: string) => {
                fs.writeFileSync(dst, fs.readFileSync(src).subarray(0, 100))
                throw errno('EIO')
              },
            },
          },
        ],
        'failed',
      ],
      [
        'silently short copy (caught by the checksum of the staged file)',
        (s) => [s.download, s.target, { ops: { copyFile: (src: string, dst: string) => fs.writeFileSync(dst, fs.readFileSync(src).subarray(0, 4000), { flag: 'wx' }) } }],
        'checksum-mismatch',
      ],
      ['fsync fails', (s) => [s.download, s.target, { ops: { fsync: () => { throw errno('EIO') } } }], 'failed'],
      ['rename fails', (s) => [s.download, s.target, { ops: { rename: () => { throw errno('EXDEV') } } }], 'failed'],
    ]
    for (const [name, args, reason] of cases) {
      it(name, () => {
        const s = setup()
        const r = swapAppImage(...args(s))
        expect(r).toMatchObject({ ok: false, reason })
        expect(fs.readFileSync(s.target).equals(s.old)).toBe(true)
        expect(fs.statSync(s.target).mode & 0o777).toBe(0o755)
        expect(leftovers(s.apps)).toEqual([])
      })
    }

    it('read-only folder', () => {
      if (process.getuid?.() === 0) return // root ignores folder modes
      const s = setup()
      fs.chmodSync(s.apps, 0o555)
      try {
        expect(swapAppImage(s.download, s.target)).toMatchObject({ ok: false, reason: 'read-only' })
        expect(fs.readFileSync(s.target).equals(s.old)).toBe(true)
        expect(leftovers(s.apps)).toEqual([])
      } finally {
        fs.chmodSync(s.apps, 0o755)
      }
    })

    it('the AppImage file is gone', () => {
      const s = setup()
      fs.rmSync(s.target)
      expect(swapAppImage(s.download, s.target)).toMatchObject({ ok: false, reason: 'not-appimage' })
      expect(fs.readdirSync(s.apps)).toEqual([])
    })
  })

  it('never installs a file nobody can run, and drops setuid bits', () => {
    const s = setup({ mode: 0o644 })
    expect(swapAppImage(s.download, s.target).ok).toBe(true)
    expect(fs.statSync(s.target).mode & 0o7777).toBe(0o744)
  })
})

describe('feed entry and install path', () => {
  it('takes the AppImage checksum and size from the feed', () => {
    expect(
      appImageEntry({
        files: [
          { url: 'LocalAI-Launcher-x86_64.zip', sha512: 'zip', size: 5 },
          { url: 'LocalAI-Launcher-x86_64.AppImage', sha512: 'abc', size: 123 },
        ],
      }),
    ).toEqual({ sha512: 'abc', size: 123 })
    expect(appImageEntry({ path: 'LocalAI-Launcher-x86_64.AppImage', sha512: 'top' })).toEqual({ sha512: 'top' })
    expect(appImageEntry({ files: [{ url: 'a.AppImage', sha512: '' }] })).toBeUndefined()
    expect(appImageEntry({})).toBeUndefined()
  })

  it('swaps only an AppImage on Linux; macOS keeps electron-updater', () => {
    expect(installPath('linux', { APPIMAGE: '/x/a.AppImage' })).toBe('appimage-swap')
    expect(installPath('linux', {})).toBe('electron-updater')
    expect(installPath('darwin', { APPIMAGE: '/x/a.AppImage' })).toBe('electron-updater')
  })
})
