// The Windows paths of the launcher, tested on any host: the server build
// for win32, the release and development asset names (.exe), the PE check
// of a download, the install into local-ai.exe, the environment rules
// (names without case), the process stop (taskkill), the port owner lookup
// (netstat and tasklist) and the self-update gate of the portable zip.
// Nothing here runs a Windows program.
import fs from 'node:fs'
import path from 'node:path'
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { hasExecutableMagic, installAtomically, renameWithRetry, validateExecutable } from '../src/main/atomicInstall'
import { buildChildEnv, isStrippedVar } from '../src/main/childEnv'
import { devAssetName, devChecksumName } from '../src/main/devFeed'
import { hardwareSummary, parseNetstatPids, parseTasklistName, portProcess, system32, type Run } from '../src/main/hostTools'
import { executableFormat, exeSuffix, minimalEnv, platformTarget, previousBinaryName, serverBinaryName, type PlatformTarget } from '../src/main/platform'
import { ServerInstaller } from '../src/main/serverInstall'
import { ServerSupervisor, taskkillPath, windowsControl } from '../src/main/supervisor'
import { GithubReleasesProvider, resolveLatestByRedirect, serverAssetName, type ReleaseInfo } from '../src/main/updateProvider'
import { isPortableWindows, NSIS_UNINSTALLER, selfUpdateGate } from '../src/main/updaterStatus'
import { envNameProblem } from '../src/shared/envVars'
import { FAKE_SERVER, freePort, pidDead, tmpDir } from './helpers'
import { cleanupRigs, FeedServer, ls, makeRig, sha } from './serverInstallHarness'

const WIN: PlatformTarget = { os: 'windows', arch: 'amd64' }

/** A file with a DOS header and a PE signature that says which version it is. */
function peBin(version: string): Buffer {
  const b = Buffer.alloc(4096, 0x41)
  b.write('MZ', 0, 'latin1')
  b.writeUInt32LE(0x80, 0x3c)
  b.write('PE\0\0', 0x80, 'latin1')
  Buffer.from(`\nVERSION=${version}\n`).copy(b, 200)
  return b
}

describe('platformTarget', () => {
  it.each([
    ['linux', 'x64', { os: 'linux', arch: 'amd64' }],
    ['linux', 'arm64', { os: 'linux', arch: 'arm64' }],
    ['darwin', 'arm64', { os: 'darwin', arch: 'arm64' }],
    ['darwin', 'x64', { os: 'darwin', arch: 'amd64' }],
    ['win32', 'x64', { os: 'windows', arch: 'amd64' }],
    // The release has no windows/arm64 server.
    ['win32', 'arm64', undefined],
    ['win32', 'ia32', undefined],
    ['freebsd', 'x64', undefined],
  ] as const)('%s/%s', (platform, arch, want) => {
    expect(platformTarget(platform, arch)).toEqual(want)
  })

  it('names the server binary and its rollback copy with .exe on Windows only', () => {
    expect(serverBinaryName(WIN)).toBe('local-ai.exe')
    expect(previousBinaryName(WIN)).toBe('local-ai.previous.exe')
    expect(serverBinaryName({ os: 'linux' })).toBe('local-ai')
    expect(previousBinaryName({ os: 'darwin' })).toBe('local-ai.previous')
    expect(serverBinaryName()).toBe('local-ai')
    expect(exeSuffix(WIN)).toBe('.exe')
  })

  it('expects the executable format of each OS', () => {
    expect(executableFormat('windows')).toBe('pe')
    expect(executableFormat('darwin')).toBe('macho')
    expect(executableFormat('linux')).toBe('elf')
  })
})

describe('release assets for Windows', () => {
  const api = new GithubReleasesProvider({ owner: 'o', repo: 'r' })
  const release = (names: string[]): ReleaseInfo => ({
    tag: 'v4.3.0',
    version: '4.3.0',
    prerelease: false,
    assets: names.map((name) => ({ name, url: `https://example.invalid/${name}` })),
  })

  it('takes the GoReleaser name with .exe, never the name without it', () => {
    expect(serverAssetName('4.3.0', WIN)).toBe('local-ai-v4.3.0-windows-amd64.exe')
    const r = release(['local-ai-v4.3.0-windows-amd64', 'local-ai-v4.3.0-windows-amd64.exe', 'local-ai-v4.3.0-linux-amd64', 'LocalAI-v4.3.0-checksums.txt'])
    expect(api.resolveAsset(r, WIN)?.name).toBe('local-ai-v4.3.0-windows-amd64.exe')
    expect(api.resolveAsset(r, { os: 'linux', arch: 'amd64' })?.name).toBe('local-ai-v4.3.0-linux-amd64')
    expect(api.resolveChecksumFile(r, WIN)?.name).toBe('LocalAI-v4.3.0-checksums.txt')
  })

  it('finds no asset when the release has no Windows build', () => {
    expect(api.resolveAsset(release(['local-ai-v4.3.0-linux-amd64']), WIN)).toBeUndefined()
  })

  it('names the development build with .exe and its checksum file after it', () => {
    expect(devAssetName(WIN)).toBe('local-ai-master-windows-amd64.exe')
    expect(devChecksumName(WIN)).toBe('local-ai-master-windows-amd64.exe.sha256')
    expect(devAssetName({ os: 'linux', arch: 'arm64' })).toBe('local-ai-master-linux-arm64')
    const dev: ReleaseInfo = { ...release(['local-ai-master-windows-amd64.exe', 'local-ai-master-windows-amd64.exe.sha256']), build: { id: 'abc1234' } }
    expect(api.resolveAsset(dev, WIN)?.name).toBe('local-ai-master-windows-amd64.exe')
    expect(api.resolveChecksumFile(dev, WIN)?.name).toBe('local-ai-master-windows-amd64.exe.sha256')
  })

  it('lists the Windows binary when the release comes from the releases/latest redirect', async () => {
    const http = await import('node:http')
    const srv = http.createServer((_req, res) => {
      res.statusCode = 302
      res.setHeader('location', '/o/r/releases/tag/v4.3.0')
      res.end()
    })
    await new Promise<void>((r) => srv.listen(0, '127.0.0.1', () => r()))
    try {
      const port = (srv.address() as import('node:net').AddressInfo).port
      const rel = await resolveLatestByRedirect({ owner: 'o', repo: 'r', webBase: `http://127.0.0.1:${port}` })
      const names = rel.assets.map((a) => a.name)
      expect(names).toContain('local-ai-v4.3.0-windows-amd64.exe')
      expect(names).not.toContain('local-ai-v4.3.0-windows-arm64.exe')
      expect(api.resolveAsset(rel, WIN)?.url).toBe(`http://127.0.0.1:${port}/o/r/releases/download/v4.3.0/local-ai-v4.3.0-windows-amd64.exe`)
    } finally {
      srv.close()
    }
  })
})

describe('executable check of a download', () => {
  const dir = tmpDir('win-exe-')
  afterAll(() => fs.rmSync(dir, { recursive: true, force: true }))
  const write = (name: string, b: Buffer) => {
    const f = path.join(dir, name)
    fs.writeFileSync(f, b)
    return f
  }

  it('accepts a PE file only when PE is asked for', () => {
    const f = write('pe.exe', peBin('4.3.0'))
    expect(() => validateExecutable(f, { format: 'pe' })).not.toThrow()
    expect(() => validateExecutable(f)).toThrow(/not an ELF/)
  })

  it('refuses a file that starts with MZ but has no PE signature, and an HTML page', () => {
    const fake = Buffer.alloc(4096, 0x20)
    fake.write('MZ', 0, 'latin1')
    expect(() => validateExecutable(write('mz-only.exe', fake), { format: 'pe' })).toThrow(/not a Windows \(PE\) executable/)
    expect(() => validateExecutable(write('page.exe', Buffer.from('<html>' + 'x'.repeat(5000))), { format: 'pe' })).toThrow(/not a Windows/)
  })

  it('knows the Mach-O magic numbers, so a macOS server binary installs', () => {
    for (const hex of ['cffaedfe', 'cafebabe']) {
      const b = Buffer.alloc(4096, 0)
      Buffer.from(hex, 'hex').copy(b, 0)
      expect(hasExecutableMagic(b, 'macho')).toBe(true)
      expect(hasExecutableMagic(b, 'elf')).toBe(false)
      expect(hasExecutableMagic(b, 'pe')).toBe(false)
    }
  })

  it('installs a PE file over the target', () => {
    const src = write('src.exe', peBin('4.3.0'))
    const target = path.join(dir, 'local-ai.exe')
    installAtomically(src, target, { format: 'pe' })
    expect(fs.readFileSync(target).equals(fs.readFileSync(src))).toBe(true)
  })
})

describe('renameWithRetry', () => {
  const busy = (code: string) => Object.assign(new Error(code), { code })

  it('retries a rename that a scanner holds for a moment on Windows', () => {
    let calls = 0
    const waits: number[] = []
    renameWithRetry(() => {
      if (++calls < 3) throw busy('EPERM')
    }, 'a', 'b', { platform: 'win32', sleep: (ms) => waits.push(ms) })
    expect(calls).toBe(3)
    expect(waits).toEqual([50, 100])
  })

  it('gives up after the last attempt and passes on the error', () => {
    let calls = 0
    expect(() => renameWithRetry(() => (calls++, (() => { throw busy('EBUSY') })()), 'a', 'b', { platform: 'win32', attempts: 4, sleep: () => undefined })).toThrow('EBUSY')
    expect(calls).toBe(4)
  })

  it('does not retry an error that is not transient, nor on Linux and macOS', () => {
    for (const [platform, code] of [['win32', 'ENOENT'], ['linux', 'EPERM'], ['darwin', 'EBUSY']] as const) {
      let calls = 0
      expect(() => renameWithRetry(() => (calls++, (() => { throw busy(code) })()), 'a', 'b', { platform, sleep: () => undefined })).toThrow(code)
      expect(calls).toBe(1)
    }
  })
})

describe('server install on Windows', () => {
  const feed = new FeedServer()
  beforeAll(() => feed.start())
  afterAll(() => {
    feed.stop()
    cleanupRigs()
  })

  function winRelease(binary: Buffer): ReleaseInfo {
    const name = 'local-ai-v4.3.0-windows-amd64.exe'
    const dir = path.join(feed.dir, 'win')
    fs.mkdirSync(dir, { recursive: true })
    fs.writeFileSync(path.join(dir, name), binary)
    const sums = 'LocalAI-v4.3.0-checksums.txt'
    fs.writeFileSync(path.join(dir, sums), `${sha(Buffer.from('linux'))}  local-ai-v4.3.0-linux-amd64\n${sha(binary)}  ${name}\n`)
    const base = `http://127.0.0.1:${feed.port}/win`
    return {
      tag: 'v4.3.0',
      version: '4.3.0',
      prerelease: false,
      assets: [
        { name, url: `${base}/${name}`, size: binary.length },
        { name: sums, url: `${base}/${sums}` },
      ],
    }
  }

  it('downloads the .exe asset, checks it as PE and installs it as local-ai.exe', async () => {
    const rig = makeRig(winRelease(peBin('4.3.0')), { target: WIN })
    const end = await new ServerInstaller(rig.deps).install()
    expect(end).toEqual({ kind: 'done', version: '4.3.0', adopted: false })
    expect(ls(rig.binDir)).toEqual(['local-ai.exe'])
    expect(rig.versionCalls).toEqual([path.join(rig.binDir, 'local-ai.exe')])
  })

  it('keeps the rollback copy as local-ai.previous.exe on an update', async () => {
    const rig = makeRig(winRelease(peBin('4.3.0')), { target: WIN })
    fs.writeFileSync(path.join(rig.binDir, 'local-ai.exe'), peBin('4.2.0'))
    const end = await new ServerInstaller(rig.deps).install()
    expect(end.kind).toBe('done')
    expect(ls(rig.binDir)).toEqual(['local-ai.exe', 'local-ai.previous.exe'])
  })

  it('refuses a download that is not a Windows executable', async () => {
    const elf = Buffer.alloc(4096, 0x41)
    Buffer.from([0x7f, 0x45, 0x4c, 0x46]).copy(elf, 0)
    const rig = makeRig(winRelease(elf), { target: WIN })
    const end = await new ServerInstaller(rig.deps).install()
    expect(end.kind).toBe('failed')
    expect(end.kind === 'failed' && end.message).toMatch(/not a Windows \(PE\) executable/)
    expect(ls(rig.binDir)).toEqual([])
  })
})

describe('server environment on Windows', () => {
  const base = { Path: 'C:\\Windows', SystemRoot: 'C:\\Windows', Api_Key: 'stray', electron_run_as_node: '1', Foo: 'base' }

  it('drops controlled and launcher-only names in any case and keeps Path', () => {
    const env = buildChildEnv(base, {}, {}, 'win32')
    expect(env).toEqual({ Path: 'C:\\Windows', SystemRoot: 'C:\\Windows', Foo: 'base' })
  })

  it('lets a later layer replace a name of an earlier one whatever its case', () => {
    const env = buildChildEnv(base, { FOO: 'user' }, { LOCALAI_API_KEY: 'k' }, 'win32')
    expect(env.FOO).toBe('user')
    expect('Foo' in env).toBe(false)
    expect(env.LOCALAI_API_KEY).toBe('k')
  })

  it('refuses a user variable that is a controlled name in another case', () => {
    expect(buildChildEnv({}, { path: 'x', api_key: 'y', localai_address: 'z' }, {}, 'win32')).toEqual({})
    expect(envNameProblem('Path', true)).toBe('controlled')
    expect(envNameProblem('appimage', true)).toBe('controlled')
  })

  it('keeps names case-sensitive on Linux and macOS, as before', () => {
    expect(envNameProblem('Path')).toBeUndefined()
    expect(buildChildEnv({ Api_Key: 'kept', API_KEY: 'dropped' }, {}, {}, 'linux')).toEqual({ Api_Key: 'kept' })
    expect(isStrippedVar('electron_run_as_node', 'linux')).toBe(false)
    expect(isStrippedVar('electron_run_as_node', 'win32')).toBe(true)
  })

  it('gives helper commands the variables a Windows program needs', () => {
    const env = { PATH: 'p', SystemRoot: 'C:\\Windows', TEMP: 't', SECRET: 's' }
    expect(minimalEnv(env, 'win32')).toEqual({ PATH: 'p', SystemRoot: 'C:\\Windows', TEMP: 't' })
    expect(minimalEnv(env, 'linux')).toEqual({ PATH: 'p' })
  })
})

describe('server stop on Windows', () => {
  const live: ServerSupervisor[] = []
  afterEach(async () => {
    for (const s of live.splice(0)) await s.stop().catch(() => undefined)
  })

  it('runs taskkill from the system folder, never from PATH', () => {
    expect(taskkillPath({ SystemRoot: 'D:\\Win' })).toBe('D:\\Win\\System32\\taskkill.exe')
    expect(taskkillPath({})).toBe('C:\\Windows\\System32\\taskkill.exe')
  })

  it('starts the server with no console window and no process group', () => {
    expect(windowsControl(async () => true).spawn).toEqual({ detached: false, windowsHide: true })
  })

  // The real supervisor with the Windows control. The fake taskkill ends the
  // server and its child the way `taskkill /T /F` does.
  async function run(taskkillWorks: boolean) {
    const port = await freePort()
    const dir = tmpDir()
    const pidsFile = path.join(dir, 'pids.json')
    const calls: string[][] = []
    const control = windowsControl(async (args) => {
      calls.push(args)
      if (!taskkillWorks) return false
      const { server, grandchild } = JSON.parse(fs.readFileSync(pidsFile, 'utf8'))
      for (const p of [grandchild, server]) {
        try {
          process.kill(p, 'SIGKILL')
        } catch {
          /* gone */
        }
      }
      return true
    })
    const sup = new ServerSupervisor({
      command: process.execPath,
      args: [FAKE_SERVER],
      env: { ...(process.env as Record<string, string>), FAKE_PORT: String(port), FAKE_PIDS_FILE: pidsFile, FAKE_IGNORE_TERM: '1' },
      healthUrl: `http://127.0.0.1:${port}/readyz`,
      pollIntervalMs: 50,
      stopGraceMs: 5000,
      killWaitMs: 3000,
      control,
    })
    live.push(sup)
    await sup.start()
    expect(sup.state).toBe('ready')
    const pids = JSON.parse(fs.readFileSync(pidsFile, 'utf8')) as { server: number; grandchild: number }
    const t0 = Date.now()
    await sup.stop()
    return { sup, calls, pids, ms: Date.now() - t0 }
  }

  it('stops the tree at once with taskkill /T /F: there is no grace period to wait', async () => {
    const { sup, calls, pids, ms } = await run(true)
    expect(calls).toEqual([['/PID', String(pids.server), '/T', '/F']])
    expect(sup.state).toBe('stopped')
    expect(ms).toBeLessThan(2500)
    expect(pidDead(pids.server)).toBe(true)
    expect(pidDead(pids.grandchild)).toBe(true)
    try {
      process.kill(pids.grandchild, 'SIGKILL')
    } catch {
      /* already gone */
    }
  })

  it('ends the server itself when taskkill cannot run', async () => {
    const { sup, calls, pids } = await run(false)
    expect(calls.length).toBe(1)
    expect(sup.state).toBe('stopped')
    expect(pidDead(pids.server)).toBe(true)
    // Not the fake's grandchild: on Windows the server's Job Object ends its backends.
    try {
      process.kill(pids.grandchild, 'SIGKILL')
    } catch {
      /* already gone */
    }
  })
})

describe('what listens on a port, on Windows', () => {
  const NETSTAT = [
    '',
    'Active Connections',
    '',
    '  Proto  Local Address          Foreign Address        State           PID',
    '  TCP    0.0.0.0:135            0.0.0.0:0              LISTENING       1000',
    '  TCP    0.0.0.0:8080           0.0.0.0:0              LISTENING       4242',
    '  TCP    127.0.0.1:8080         127.0.0.1:50000        ESTABLISHED     4242',
    '  TCP    127.0.0.1:50000        127.0.0.1:8080         ESTABLISHED     777',
    '  TCP    [::]:8080              [::]:0                 ABHÖREN         4242',
    '  TCP    [::1]:18080            [::]:0                 LISTENING       5555',
    '  UDP    0.0.0.0:8080           *:*                                    999',
  ].join('\r\n')

  it('finds the listening pids by the foreign address, in any language', () => {
    expect(parseNetstatPids(NETSTAT, 8080)).toEqual([4242])
    expect(parseNetstatPids(NETSTAT, 18080)).toEqual([5555])
    expect(parseNetstatPids(NETSTAT, 9999)).toEqual([])
  })

  it('reads the image name from tasklist CSV output', () => {
    expect(parseTasklistName('"local-ai.exe","4242","Console","1","123,456 K"\r\n')).toBe('local-ai.exe')
    expect(parseTasklistName('INFO: No tasks are running which match the specified criteria.\r\n')).toBeUndefined()
  })

  it('asks netstat and tasklist from the system folder and returns names and pids', async () => {
    const calls: string[] = []
    const run: Run = async (file, args) => {
      calls.push(`${file} ${args.join(' ')}`)
      if (file.endsWith('netstat.exe')) return { ok: true, stdout: NETSTAT }
      return { ok: true, stdout: '"local-ai.exe","4242","Console","1","1 K"' }
    }
    expect(await portProcess(8080, 'win32', run)).toEqual({ status: 'found', processes: [{ pid: 4242, name: 'local-ai.exe' }] })
    expect(calls[0]).toBe(`${system32('netstat.exe')} -ano`)
    expect(calls[1]).toBe(`${system32('tasklist.exe')} /FI PID eq 4242 /FO CSV /NH`)
    expect(await portProcess(9999, 'win32', run)).toEqual({ status: 'none' })
  })

  it('is unavailable when netstat cannot run, and hidden when no name can be read', async () => {
    expect(await portProcess(8080, 'win32', async () => ({ ok: false, missing: true }))).toEqual({ status: 'unavailable' })
    const noNames: Run = async (file) => (file.endsWith('netstat.exe') ? { ok: true, stdout: NETSTAT } : { ok: false, missing: false })
    expect(await portProcess(8080, 'win32', noNames)).toEqual({ status: 'hidden' })
  })

  it('gives a GPU hint from a driver file in the system folder', () => {
    const facts = (files: string[]) => ({ platform: 'win32' as const, arch: 'x64', cpus: [{ model: 'CPU' }], totalMem: 1, env: { SystemRoot: 'C:\\Windows' }, exists: (p: string) => files.includes(p) })
    expect(hardwareSummary(facts(['C:\\Windows\\System32\\nvapi64.dll'])).gpu).toBe('nvidia')
    expect(hardwareSummary(facts(['C:\\Windows\\System32\\atiadlxx.dll'])).gpu).toBe('amd')
    expect(hardwareSummary(facts([])).gpu).toBeUndefined()
  })
})

describe('self-update of the Windows builds', () => {
  const exe = 'C:\\Users\\u\\AppData\\Local\\Programs\\localai-launcher\\local-ai-launcher.exe'

  it('treats a launcher without the installer next to it as the portable zip', () => {
    const installed = (d: string) => (d === 'C:\\Users\\u\\AppData\\Local\\Programs\\localai-launcher' ? ['local-ai-launcher.exe', 'Uninstall localai-launcher.exe'] : [])
    expect(isPortableWindows('win32', exe, installed)).toBe(false)
    expect(isPortableWindows('win32', exe, () => ['local-ai-launcher.exe'])).toBe(true)
    expect(isPortableWindows('linux', '/opt/x', () => [])).toBe(false)
    expect(isPortableWindows('darwin', '/Applications/x', () => [])).toBe(false)
    expect(NSIS_UNINSTALLER.test('Uninstall LocalAI Launcher.exe')).toBe(true)
  })

  it('turns self-update off in the portable zip, unless a test feed is set', () => {
    expect(selfUpdateGate({ packaged: true, version: '4.3.0', portable: true })).toEqual({ enabled: false, reason: 'portable' })
    expect(selfUpdateGate({ packaged: true, version: '4.3.0', portable: true, feedUrl: 'http://127.0.0.1:1/' })).toEqual({ enabled: true, feedUrl: 'http://127.0.0.1:1/' })
    expect(selfUpdateGate({ packaged: true, version: '4.3.0', portable: false })).toEqual({ enabled: true })
    // A development build stays a development build.
    expect(selfUpdateGate({ packaged: true, version: '4.3.1-dev.7', portable: true })).toEqual({ enabled: false, reason: 'dev-build' })
  })
})
