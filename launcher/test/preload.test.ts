// The IPC contract test. shared/ipc.ts is the list; the preload script and the
// main-process handler table must agree with it channel by channel, including
// the direction of each channel.
import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it, vi } from 'vitest'
import { CHANNEL_KIND, IPC, type ChannelKind } from '../src/shared/ipc'
import { createIpcHandlers, type CoreApi, type HandlerDeps } from '../src/main/ipcHandlers'
import { IpcInputError } from '../src/main/ipcValidate'

const src = fs.readFileSync(path.resolve(__dirname, '../src/preload/index.ts'), 'utf8')
const mainSrc = fs
  .readdirSync(path.resolve(__dirname, '../src/main'))
  .filter((f) => f.endsWith('.ts'))
  .map((f) => fs.readFileSync(path.resolve(__dirname, '../src/main', f), 'utf8'))
  .join('\n')

const channels = (kind: ChannelKind) =>
  new Set(Object.entries(CHANNEL_KIND).filter(([, k]) => k === kind).map(([c]) => c))

/** Channels the preload passes to one of its helpers, by helper name. */
const preloadUses = (helper: string) => new Set([...src.matchAll(new RegExp(`\\b${helper}\\('(launcher:[a-z:-]+)'`, 'g'))].map((m) => m[1]))

function fakeDeps(): { deps: HandlerDeps; core: Record<string, ReturnType<typeof vi.fn>> } {
  const names: (keyof CoreApi)[] = [
    'snapshot', 'startServer', 'stopServer', 'restartServer', 'installState', 'checkServerUpdate', 'installServer',
    'cancelInstall', 'rollbackServer', 'getSettings', 'saveSettings', 'queryLogs', 'exportLogs', 'sandboxView',
    'sandboxPlan', 'runSandboxSetup', 'runSandboxRemove', 'dismissSandboxNotice', 'statusBarVisible', 'setStatusBarVisible',
    'webuiState', 'setShellScreen', 'setWebuiOverlay', 'setShellInsets', 'openWebuiInBrowser', 'connectRemote', 'backToLocal', 'reloadWebui',
    'resetWebuiPermissions', 'windowSettings', 'setOpenInBrowser', 'setDesktopNotifications', 'updateInfo', 'setAutoCheck', 'setUpdateChannel', 'backToStable', 'installDetails',
    'serversView', 'testServer', 'addServer', 'updateServer', 'removeServer', 'setDefaultServer', 'cancelSwitch', 'retryRemote', 'setKeepLocalRunning',
    'networkView', 'networkPlan', 'setNetworkExposed', 'regenerateApiKey', 'dismissKeyNotice', 'revealApiKey', 'envVarsView', 'setEnvVar',
    'deleteEnvVar', 'secretStorageView',
  ]
  const core: Record<string, ReturnType<typeof vi.fn>> = {}
  for (const n of names) core[n] = vi.fn(() => 'ok')
  // Only versions on offer have a release page.
  core.releaseNotesUrl = vi.fn((v: string) => (v === '4.2.1' ? 'https://github.com/mudler/LocalAI/releases/tag/v4.2.1' : undefined))
  core.apiKeyCopyText = vi.fn((f: string) => (f === 'key' ? 'lai-test-key' : `example with lai-test-key (${f})`))
  const deps: HandlerDeps = {
    core: { ...(core as unknown as CoreApi), endpoint: 'http://127.0.0.1:8080' },
    info: vi.fn(),
    listModels: vi.fn(async () => []),
    openChat: vi.fn(async () => undefined),
    writeClipboard: vi.fn(),
    writeSecretClipboard: vi.fn(),
    chooseLogExportPath: vi.fn(async () => undefined),
    getUpdater: vi.fn(() => ({ phase: 'idle' as const })),
    checkForLauncherUpdate: vi.fn(async () => undefined),
    installLauncherUpdate: vi.fn(async () => undefined),
    relaunch: vi.fn(async () => undefined),
    openExternal: vi.fn(),
    findFreePort: vi.fn(async () => 8081),
    portProcess: vi.fn(async () => ({ status: 'none' as const })),
    pickFolder: vi.fn(async () => null),
    hardwareSummary: vi.fn(() => ({ cpuCount: 4, memoryBytes: 8e9 })),
    firstRender: vi.fn(),
  }
  return { deps, core }
}

describe('IPC contract', () => {
  it('lists every channel of IPC exactly once, with a direction', () => {
    expect(new Set(Object.keys(CHANNEL_KIND))).toEqual(new Set(Object.values(IPC)))
    expect(Object.values(IPC).length).toBe(new Set(Object.values(IPC)).size)
  })

  it('the preload uses exactly the channel names of shared/ipc.ts', () => {
    const used = new Set([...src.matchAll(/'(launcher:[a-z:-]+)'/g)].map((m) => m[1]))
    expect(used).toEqual(new Set(Object.values(IPC)))
  })

  it('the preload uses each channel in its direction', () => {
    expect(preloadUses('invoke')).toEqual(channels('invoke'))
    expect(preloadUses('listen')).toEqual(channels('event'))
    expect(preloadUses('send')).toEqual(channels('send'))
  })

  it('the preload sends nothing outside its typed helpers', () => {
    // Exactly one raw call each, inside the helpers.
    expect(src.match(/ipcRenderer\.invoke\(/g)).toHaveLength(1)
    expect(src.match(/ipcRenderer\.on\(/g)).toHaveLength(1)
    expect(src.match(/ipcRenderer\.send\(/g)).toHaveLength(1)
  })

  it('the main process has a handler for every invoke and send channel, and no other', () => {
    const { invoke, send } = createIpcHandlers(fakeDeps().deps)
    expect(new Set(Object.keys(invoke))).toEqual(channels('invoke'))
    expect(new Set(Object.keys(send))).toEqual(channels('send'))
  })

  it('the main process sends every event channel', () => {
    for (const c of channels('event')) expect(mainSrc, c).toContain(`'${c}'`)
  })

  it('requires nothing but electron (a sandboxed preload cannot load other files)', () => {
    const imports = [...src.matchAll(/^import (type )?.*from '([^']+)'/gm)].map((m) => ({ typeOnly: !!m[1], from: m[2] }))
    for (const i of imports) expect(i.from === 'electron' || i.typeOnly, `${i.from} must be a type-only import`).toBe(true)
  })
})

describe('IPC handlers check their input', () => {
  const { deps, core } = fakeDeps()
  const { invoke, send } = createIpcHandlers(deps)

  it('refuses extra arguments on calls that take none', () => {
    for (const [c, h] of Object.entries(invoke)) {
      if (['launcher:open-chat', 'launcher:copy', 'launcher:install:check', 'launcher:install:run', 'launcher:install:rollback',
        'launcher:settings:save', 'launcher:logs:query', 'launcher:sandbox:plan', 'launcher:status-bar:set',
        'launcher:webui:set-screen', 'launcher:webui:set-overlay', 'launcher:webui:connect',
        'launcher:window:set-open-in-browser', 'launcher:window:set-desktop-notifications', 'launcher:updates:set-auto-check', 'launcher:updates:release-notes',
        'launcher:port:find-free', 'launcher:port:process', 'launcher:settings:pick-folder',
        'launcher:network:set-exposed', 'launcher:api-key:copy', 'launcher:env:set', 'launcher:env:delete',
        'launcher:updates:set-channel', 'launcher:servers:test', 'launcher:servers:add', 'launcher:servers:update', 'launcher:servers:remove',
        'launcher:servers:set-default', 'launcher:servers:set-keep-local'].includes(c)) continue
      expect(() => h('extra'), c).toThrow(IpcInputError)
    }
  })

  it('refuses bad input before the core sees it', async () => {
    const bad: [string, unknown[]][] = [
      ['launcher:status-bar:set', ['yes']],
      ['launcher:status-bar:set', []],
      // The data folders come only from the main-process picker: no path from the renderer, valid or not.
      ['launcher:settings:save', [{ modelsPath: 'relative' }]],
      ['launcher:settings:save', [{ modelsPath: '/srv/localai/models' }]],
      ['launcher:settings:save', [{ backendsPath: '/srv/localai/backends' }]],
      ['launcher:settings:save', [{ unknownKey: 1 }]],
      ['launcher:install:run', [{ stopFirst: 1 }]],
      ['launcher:install:rollback', [{ target: '/x' }]],
      ['launcher:logs:query', [{ limit: 1e9 }]],
      ['launcher:sandbox:plan', ['/etc/apparmor.d/x']],
      ['launcher:open-chat', ['a\u0000b']],
      ['launcher:copy', [42]],
      ['launcher:webui:set-screen', ['models']],
      ['launcher:webui:set-screen', []],
      ['launcher:webui:set-screen', [undefined]],
      ['launcher:webui:set-overlay', ['true']],
      ['launcher:webui:connect', [42]],
      ['launcher:webui:connect', ['x'.repeat(3000)]],
      ['launcher:webui:connect', ['a', 'b']],
      // The renderer sends a saved server id or a typed address, never an origin of its own choosing.
      ['launcher:webui:connect', ['https://evil.example.test']],
      ['launcher:servers:set-default', ['https://evil.example.test']],
      ['launcher:servers:set-default', []],
      ['launcher:servers:remove', ['a b']],
      ['launcher:servers:test', [{}]],
      ['launcher:servers:test', [{ id: 'srv_00000001', address: 'https://a.example.com' }]],
      ['launcher:servers:test', [{ origin: 'https://a.example.com' }]],
      ['launcher:servers:test', [{ address: 42 }]],
      ['launcher:servers:test', [{ address: 'x'.repeat(3000) }]],
      ['launcher:servers:add', [{ name: 'A' }]],
      ['launcher:servers:add', [{ name: 'A', address: 'https://a.example.com', id: 'srv_00000001' }]],
      ['launcher:servers:add', [{ name: 'A', address: 'https://a.example.com', skipTest: 'yes' }]],
      ['launcher:servers:add', [{ name: 'a\u0000', address: 'https://a.example.com' }]],
      ['launcher:servers:update', [{ name: 'A' }]],
      ['launcher:servers:update', [{ id: 'srv_00000001', url: 'https://a.example.com' }]],
      ['launcher:servers:set-keep-local', ['true']],
      ['launcher:updates:set-channel', ['nightly']],
      ['launcher:updates:set-channel', ['dev', 'yes']],
      ['launcher:updates:set-channel', []],
      ['launcher:updates:back-to-stable', ['x']],
      ['launcher:servers:cancel-switch', ['x']],
      ['launcher:settings:save', [{ remoteServerUrl: 'http://remote.example.test' }]],
      ['launcher:settings:save', [{ remoteServerUrl: 'file:///etc/passwd' }]],
      ['launcher:settings:save', [{ openWebuiInBrowser: 'yes' }]],
      ['launcher:window:set-open-in-browser', ['true']],
      ['launcher:window:set-desktop-notifications', ['true']],
      ['launcher:window:set-desktop-notifications', []],
      ['launcher:window:set-open-in-browser', []],
      ['launcher:updates:set-auto-check', [1]],
      ['launcher:updates:release-notes', [42]],
      ['launcher:updates:release-notes', ['x'.repeat(81)]],
      ['launcher:updates:release-notes', ['4.2.1', 'https://example.test/']],
      ['launcher:port:find-free', [80]],
      ['launcher:port:find-free', [70000]],
      ['launcher:port:find-free', ['8080']],
      ['launcher:port:find-free', [8080.5]],
      ['launcher:port:process', ['8080; rm -rf ~']],
      ['launcher:port:process', [8080, 1]],
      ['launcher:settings:pick-folder', ['/etc']],
      ['launcher:settings:pick-folder', []],
      ['launcher:hardware', ['x']],
      ['launcher:network:get', ['x']],
      ['launcher:network:plan', [{}]],
      ['launcher:network:set-exposed', []],
      ['launcher:network:set-exposed', ['true']],
      ['launcher:network:set-exposed', [true, 'not-a-plan']],
      ['launcher:network:set-exposed', [true, 'A'.repeat(32)]],
      ['launcher:network:set-exposed', [true, '0'.repeat(32), 'x']],
      ['launcher:api-key:regenerate', [true]],
      ['launcher:api-key:dismiss-notice', ['x']],
      ['launcher:api-key:reveal', ['now']],
      ['launcher:api-key:copy', ['ssh-key']],
      ['launcher:api-key:copy', [1]],
      ['launcher:api-key:copy', ['key', 'x']],
      ['launcher:env:get', ['x']],
      ['launcher:env:set', ['HF_TOKEN']],
      ['launcher:env:set', ['BAD NAME', 'v']],
      ['launcher:env:set', ['1A', 'v']],
      ['launcher:env:set', ['A'.repeat(129), 'v']],
      ['launcher:env:set', ['A', 42]],
      ['launcher:env:set', ['A', 'a\u0000b']],
      ['launcher:env:set', ['A', 'x'.repeat(32 * 1024 + 1)]],
      ['launcher:env:set', ['A', 'v', 'extra']],
      ['launcher:env:delete', []],
      ['launcher:env:delete', ['A=B']],
      ['launcher:env:delete', [{ name: 'A' }]],
      ['launcher:secrets:state', [1]],
    ]
    for (const [c, args] of bad) {
      expect(() => (invoke as Record<string, (...a: unknown[]) => unknown>)[c](...args), c).toThrow(IpcInputError)
    }
    for (const [n, fn] of Object.entries(core)) if (n !== 'releaseNotesUrl') expect(fn, n).not.toHaveBeenCalled()
    expect(deps.writeSecretClipboard).not.toHaveBeenCalled()
    expect(deps.openExternal).not.toHaveBeenCalled()
    for (const fn of [deps.findFreePort, deps.portProcess, deps.pickFolder, deps.hardwareSummary]) expect(fn).not.toHaveBeenCalled()
    expect(deps.openChat).not.toHaveBeenCalled()
    expect(deps.writeClipboard).not.toHaveBeenCalled()
    expect(() => send['launcher:first-render']('x'.repeat(65))).toThrow(IpcInputError)
    for (const bad of [[], [{ top: 1 }], [{ top: -1, bottom: 0 }], [{ top: 0, bottom: 401 }], [{ top: NaN, bottom: 0 }], [{ top: '1', bottom: 0 }], [{ top: 0, bottom: 0, overlay: true }], [null], [{ top: 0, bottom: 0 }, 1]]) {
      expect(() => send['launcher:webui:set-insets'](...bad), JSON.stringify(bad)).toThrow(IpcInputError)
    }
    expect(core.setShellInsets).not.toHaveBeenCalled()
  })

  it('passes checked input through', async () => {
    await invoke['launcher:status-bar:set'](false)
    expect(core.setStatusBarVisible).toHaveBeenCalledWith(false)
    await invoke['launcher:install:run']({ stopFirst: true })
    expect(core.installServer).toHaveBeenCalledWith({ stopFirst: true })
    await invoke['launcher:logs:query']()
    expect(core.queryLogs).toHaveBeenCalledWith({ limit: 1000 })
    await invoke['launcher:sandbox:plan']()
    expect(core.sandboxPlan).toHaveBeenCalledWith('setup')
    await invoke['launcher:webui:set-screen'](null)
    expect(core.setShellScreen).toHaveBeenCalledWith(null)
    await invoke['launcher:webui:set-screen']('logs')
    expect(core.setShellScreen).toHaveBeenCalledWith('logs')
    await invoke['launcher:webui:connect']()
    expect(core.connectRemote).toHaveBeenCalledWith(undefined)
    await invoke['launcher:webui:connect']('srv_00000001')
    expect(core.connectRemote).toHaveBeenCalledWith('srv_00000001')
    await invoke['launcher:servers:test']({ address: ' https://a.example.com/x ' })
    expect(core.testServer).toHaveBeenCalledWith({ address: ' https://a.example.com/x ' })
    await invoke['launcher:servers:test']({ id: 'srv_00000001' })
    expect(core.testServer).toHaveBeenCalledWith({ id: 'srv_00000001' })
    await invoke['launcher:servers:add']({ name: 'Lab', address: 'https://lab.example.com', makeDefault: true })
    expect(core.addServer).toHaveBeenCalledWith({ name: 'Lab', address: 'https://lab.example.com', makeDefault: true })
    await invoke['launcher:servers:update']({ id: 'srv_00000001', name: 'New' })
    expect(core.updateServer).toHaveBeenCalledWith({ id: 'srv_00000001', name: 'New' })
    await invoke['launcher:servers:remove']('srv_00000001')
    expect(core.removeServer).toHaveBeenCalledWith('srv_00000001')
    await invoke['launcher:servers:set-default']('local')
    expect(core.setDefaultServer).toHaveBeenCalledWith('local')
    await invoke['launcher:servers:set-keep-local'](true)
    expect(core.setKeepLocalRunning).toHaveBeenCalledWith(true)
    await invoke['launcher:updates:set-channel']('dev', true)
    expect(core.setUpdateChannel).toHaveBeenCalledWith('dev', true)
    await invoke['launcher:updates:set-channel']('rc')
    expect(core.setUpdateChannel).toHaveBeenLastCalledWith('rc', false)
    send['launcher:webui:set-insets']({ top: 52.4, bottom: 0 })
    expect(core.setShellInsets).toHaveBeenCalledWith({ top: 53, bottom: 0 })
    await invoke['launcher:settings:save']({ openWebuiInBrowser: true })
    expect(core.saveSettings).toHaveBeenCalledWith({ openWebuiInBrowser: true })
  })

  it('passes checked secrets input through; the copy goes to the clipboard, never back to the renderer', async () => {
    await invoke['launcher:network:set-exposed'](false)
    expect(core.setNetworkExposed).toHaveBeenCalledWith(false, undefined)
    await invoke['launcher:network:set-exposed'](true, '0123456789abcdef0123456789abcdef')
    expect(core.setNetworkExposed).toHaveBeenCalledWith(true, '0123456789abcdef0123456789abcdef')
    await invoke['launcher:env:set']('HF_TOKEN', 'multi\nline value')
    expect(core.setEnvVar).toHaveBeenCalledWith('HF_TOKEN', 'multi\nline value')
    // A controlled name passes the shape check; the core answers with a reason.
    await invoke['launcher:env:set']('PATH', '/x')
    expect(core.setEnvVar).toHaveBeenCalledWith('PATH', '/x')
    await invoke['launcher:env:delete']('HF_TOKEN')
    expect(core.deleteEnvVar).toHaveBeenCalledWith('HF_TOKEN')
    expect(await invoke['launcher:api-key:copy']()).toEqual({ ok: true })
    expect(deps.writeSecretClipboard).toHaveBeenLastCalledWith('lai-test-key')
    const r = await invoke['launcher:api-key:copy']('curl')
    expect(JSON.stringify(r)).not.toContain('lai-test-key')
    expect(deps.writeSecretClipboard).toHaveBeenLastCalledWith('example with lai-test-key (curl)')
    expect(deps.writeClipboard).not.toHaveBeenCalled()
    core.apiKeyCopyText.mockReturnValueOnce(undefined)
    expect(await invoke['launcher:api-key:copy']('key')).toEqual({ ok: false, reason: 'no-key' })
  })

  it('opens the release page only for a version on offer', async () => {
    expect(() => invoke['launcher:updates:release-notes']('9.9.9')).toThrow(IpcInputError)
    expect(deps.openExternal).not.toHaveBeenCalled()
    await invoke['launcher:updates:release-notes']('4.2.1')
    expect(deps.openExternal).toHaveBeenCalledWith('https://github.com/mudler/LocalAI/releases/tag/v4.2.1')
  })

  it('passes the port and folder kind through', async () => {
    expect(await invoke['launcher:port:find-free'](8081)).toBe(8081)
    expect(deps.findFreePort).toHaveBeenCalledWith(8081)
    await invoke['launcher:port:process'](8080)
    expect(deps.portProcess).toHaveBeenCalledWith(8080)
    await invoke['launcher:settings:pick-folder']('models')
    expect(deps.pickFolder).toHaveBeenCalledWith('models')
  })

  it('export lets the main process choose the path', async () => {
    await invoke['launcher:logs:export']()
    expect(core.exportLogs).toHaveBeenCalledTimes(1)
    const chooser = core.exportLogs.mock.calls[0][0] as () => Promise<string | undefined>
    await chooser()
    expect(deps.chooseLogExportPath).toHaveBeenCalledTimes(1)
  })
})
