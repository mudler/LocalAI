import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it, vi } from 'vitest'
import { applyLoginItem, AUTOSTART_FILE, autostartDir, desktopEntry, execArg, type LoginItemFs } from '../src/main/loginItem'
import { tmpDir } from './helpers'

const errno = (code: string) => Object.assign(new Error(code), { code })

/** An in-memory file system: files by path, with one call made to fail when asked. */
function fakeFs(fail: Partial<Record<keyof LoginItemFs, string>> = {}) {
  const files = new Map<string, { data: string; mode: number }>()
  const dirs = new Set<string>()
  const calls: string[] = []
  const ops: LoginItemFs = {
    mkdirp: (d) => {
      calls.push(`mkdirp ${d}`)
      if (fail.mkdirp) throw errno(fail.mkdirp)
      dirs.add(d)
    },
    writeNew: (f, data, mode) => {
      calls.push(`writeNew ${path.basename(f)}`)
      if (files.has(f)) throw errno('EEXIST')
      if (fail.writeNew) {
        files.set(f, { data: data.slice(0, 5), mode }) // a cut-short write leaves a partial file behind
        throw errno(fail.writeNew)
      }
      files.set(f, { data, mode })
    },
    rename: (a, b) => {
      calls.push(`rename ${path.basename(a)} ${path.basename(b)}`)
      if (fail.rename) throw errno(fail.rename)
      const v = files.get(a)
      if (!v) throw errno('ENOENT')
      files.delete(a)
      files.set(b, v)
    },
    remove: (f) => {
      calls.push(`remove ${path.basename(f)}`)
      if (fail.remove) throw errno(fail.remove)
      files.delete(f)
    },
  }
  return { ops, files, dirs, calls }
}

const HOME = '/home/user'
const APP = '/home/user/Applications/LocalAI-Launcher-x86_64.AppImage'
const ENTRY = path.join(HOME, '.config', 'autostart', AUTOSTART_FILE)
const linux = (f: ReturnType<typeof fakeFs>, env: NodeJS.ProcessEnv = { APPIMAGE: APP }) => ({ platform: 'linux' as const, env, home: HOME, fs: f.ops })

describe('autostart entry on Linux', () => {
  it('writes the entry with the AppImage path and --hidden, through a temporary file and a rename', () => {
    const f = fakeFs()
    expect(applyLoginItem(true, linux(f))).toEqual({ status: 'ok' })
    expect([...f.files.keys()]).toEqual([ENTRY])
    const { data, mode } = f.files.get(ENTRY)!
    expect(mode).toBe(0o644)
    expect(data).toContain(`Exec="${APP}" --hidden\n`)
    expect(data).toMatch(/^\[Desktop Entry\]\nType=Application\n/)
    expect(f.calls[0]).toBe(`mkdirp ${path.dirname(ENTRY)}`)
    expect(f.calls[1]).toMatch(/^writeNew \.localai-launcher\.desktop\.[0-9a-f]{12}\.tmp$/)
    expect(f.calls[2]).toMatch(/^rename \.localai-launcher\.desktop\.[0-9a-f]{12}\.tmp localai-launcher\.desktop$/)
  })

  it('replaces an older entry (the AppImage moved) and removes it when turned off', () => {
    const f = fakeFs()
    applyLoginItem(true, linux(f, { APPIMAGE: '/old/place/app.AppImage' }))
    applyLoginItem(true, linux(f))
    expect(f.files.get(ENTRY)!.data).toContain(APP)
    expect(f.files.size).toBe(1)
    expect(applyLoginItem(false, linux(f))).toEqual({ status: 'ok' })
    expect(f.files.size).toBe(0)
    // Off again: nothing there is fine.
    expect(applyLoginItem(false, linux(f))).toEqual({ status: 'ok' })
  })

  it('uses XDG_CONFIG_HOME when it is absolute', () => {
    expect(autostartDir({ XDG_CONFIG_HOME: '/cfg' }, HOME)).toBe('/cfg/autostart')
    expect(autostartDir({ XDG_CONFIG_HOME: 'relative' }, HOME)).toBe(path.join(HOME, '.config', 'autostart'))
    expect(autostartDir({}, HOME)).toBe(path.join(HOME, '.config', 'autostart'))
    const f = fakeFs()
    applyLoginItem(true, linux(f, { APPIMAGE: APP, XDG_CONFIG_HOME: '/cfg' }))
    expect([...f.files.keys()]).toEqual([`/cfg/autostart/${AUTOSTART_FILE}`])
  })

  it('is unavailable without APPIMAGE (a run from source), and writes nothing', () => {
    for (const env of [{}, { APPIMAGE: '' }, { APPIMAGE: 'relative.AppImage' }, { APPIMAGE: '/a\0b' }]) {
      const f = fakeFs()
      expect(applyLoginItem(true, linux(f, env))).toEqual({ status: 'unavailable', reason: 'not-appimage' })
      expect(f.calls).toEqual([])
    }
    // Turning it off still removes an entry a packaged run wrote.
    const f = fakeFs()
    f.files.set(ENTRY, { data: 'x', mode: 0o644 })
    expect(applyLoginItem(false, linux(f, {}))).toEqual({ status: 'ok' })
    expect(f.files.size).toBe(0)
  })

  it.each([
    ['read-only folder', { mkdirp: 'EROFS' }, 'read-only'],
    ['no permission', { writeNew: 'EACCES' }, 'read-only'],
    ['disk full', { writeNew: 'ENOSPC' }, 'no-space'],
    ['rename fails', { rename: 'EIO' }, 'write-failed'],
  ] as const)('%s: reports a stable reason, keeps an existing entry and leaves no temporary file', (_name, fail, reason) => {
    const f = fakeFs(fail)
    f.files.set(ENTRY, { data: 'old entry', mode: 0o644 })
    expect(applyLoginItem(true, linux(f))).toEqual({ status: 'failed', reason })
    expect([...f.files.keys()]).toEqual([ENTRY])
    expect(f.files.get(ENTRY)!.data).toBe('old entry')
  })

  it('reports a failed removal', () => {
    const f = fakeFs({ remove: 'EACCES' })
    expect(applyLoginItem(false, linux(f))).toEqual({ status: 'failed', reason: 'read-only' })
  })

  it('quotes the path for the Exec key', () => {
    expect(execArg('/a b/c')).toBe('"/a b/c"')
    expect(execArg('/x"$`\\y')).toBe('"/x\\"\\$\\`\\\\y"')
    expect(execArg('/100%/app')).toBe('"/100%%/app"')
    // The backslashes of the quoting are escaped again for the value, as the spec says.
    expect(desktopEntry('/x$y')).toContain('Exec="/x\\\\$y" --hidden\n')
    // One line, whatever the path holds.
    expect(desktopEntry('/a\nb').split('\n').filter((l) => l.startsWith('Exec='))).toEqual(['Exec="/a\\nb" --hidden'])
  })

  it('writes a real file atomically', () => {
    const home = tmpDir('login-')
    const env = { APPIMAGE: path.join(home, 'app.AppImage'), XDG_CONFIG_HOME: path.join(home, 'cfg') }
    expect(applyLoginItem(true, { platform: 'linux', env, home })).toEqual({ status: 'ok' })
    const file = path.join(home, 'cfg', 'autostart', AUTOSTART_FILE)
    expect(fs.readFileSync(file, 'utf8')).toBe(desktopEntry(env.APPIMAGE))
    expect(fs.statSync(file).mode & 0o777).toBe(0o644)
    expect(fs.readdirSync(path.dirname(file))).toEqual([AUTOSTART_FILE])
    expect(applyLoginItem(false, { platform: 'linux', env, home })).toEqual({ status: 'ok' })
    expect(fs.existsSync(file)).toBe(false)
  })
})

describe('login item on macOS and Windows', () => {
  it('uses Electron with --hidden', () => {
    const set = vi.fn(() => ({ status: 'enabled' }))
    expect(applyLoginItem(true, { platform: 'darwin', env: {}, home: HOME, setElectronLoginItem: set })).toEqual({ status: 'ok' })
    expect(applyLoginItem(false, { platform: 'win32', env: {}, home: HOME, setElectronLoginItem: set })).toEqual({ status: 'ok' })
    expect(set.mock.calls).toEqual([
      [true, ['--hidden']],
      [false, ['--hidden']],
    ])
  })

  it('says when macOS wants the user to approve it, and when Electron throws', () => {
    expect(applyLoginItem(true, { platform: 'darwin', env: {}, home: HOME, setElectronLoginItem: () => ({ status: 'requires-approval' }) })).toEqual({
      status: 'failed',
      reason: 'requires-approval',
    })
    expect(applyLoginItem(true, { platform: 'darwin', env: {}, home: HOME, setElectronLoginItem: () => { throw new Error('x') } })).toEqual({ status: 'failed', reason: 'error' })
  })

  it('is unavailable on other platforms', () => {
    expect(applyLoginItem(true, { platform: 'freebsd', env: {}, home: HOME })).toEqual({ status: 'unavailable', reason: 'platform' })
  })
})
