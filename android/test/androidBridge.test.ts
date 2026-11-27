// SPDX-License-Identifier: MIT
import { afterEach, describe, expect, it } from 'vitest'
import { capabilitiesOf, CORE_METHODS, LOCAL_SERVER_METHODS, SERVERS_METHODS } from '@launcher/shared/apiGroups'
import { hasLocal } from '@launcher/renderer/bridge'
import type { UpdaterStatus, WebuiViewState } from '@launcher/shared/types'
import { ANDROID_CAPABILITIES, CERTIFICATE_HELP_URL, createAndroidBridge, idleUpdater } from '../src/bridge/androidBridge'
import { ClientCore } from '../src/core/clientCore'
import { P2P_LIBRARY_VERSION } from '../src/core/p2pTunnels'
import { CONFIG_FILE } from '../src/core/configIO'
import { fakePlatform, flush, type FakePlatform } from './fakes/plugins'

const AI = 'https://ai.example.com'

let cores: ClientCore[] = []
afterEach(async () => {
  for (const c of cores) await c.shutdown()
  cores = []
})

async function rig(p: FakePlatform = fakePlatform()) {
  const core = await ClientCore.open(p)
  cores.push(core)
  const copied: string[] = []
  const updater = idleUpdater()
  const bridge = createAndroidBridge(core, { clipboard: async (t) => void copied.push(t), updater, info: { version: '5.0.2' } })
  await flush()
  return { core, bridge, copied, p, updater }
}

describe('createAndroidBridge', () => {
  it('has the core and servers groups and no local-server method', async () => {
    const { bridge } = await rig()
    const b = bridge as unknown as Record<string, unknown>
    for (const k of CORE_METHODS) expect(typeof b[k], k).toBe('function')
    for (const k of SERVERS_METHODS) {
      // Server notifications are later work on Android.
      if (k === 'setDesktopNotifications') expect(k in b, k).toBe(false)
      else expect(typeof b[k], k).toBe('function')
    }
    for (const k of LOCAL_SERVER_METHODS) expect(k in b, k).toBe(false)
    expect(hasLocal(bridge)).toBe(false)
  })

  it('reports the Android capabilities', async () => {
    const { bridge } = await rig()
    const info = await bridge.info()
    expect(capabilitiesOf(info)).toEqual(ANDROID_CAPABILITIES)
    expect(info).toMatchObject({ version: '5.0.2', packaged: true, trayActive: false, sandboxed: true })
    expect(info.configNotice).toBeUndefined()
  })

  it('passes the application ID on in info when the app knows it', async () => {
    const core = await ClientCore.open(fakePlatform())
    cores.push(core)
    const bridge = createAndroidBridge(core, { clipboard: async () => undefined, updater: idleUpdater(), info: { version: '5.0.2', applicationId: 'io.localai.client' } })
    expect(await bridge.info()).toMatchObject({ version: '5.0.2', applicationId: 'io.localai.client' })
    const { bridge: without } = await rig()
    expect('applicationId' in (await without.info())).toBe(false)
  })

  it('passes a recovered config on in info', async () => {
    const { bridge } = await rig(fakePlatform({ files: { [CONFIG_FILE]: '[]' } }))
    expect((await bridge.info()).configNotice).toEqual({ status: 'recovered', reason: 'config file is not a JSON object' })
  })

  it('keeps the status bar on', async () => {
    const { bridge } = await rig()
    expect(await bridge.getStatusBarVisible()).toBe(true)
    expect(await bridge.setStatusBarVisible(false)).toBe(true)
  })

  it('copies through the clipboard and ignores firstRender', async () => {
    const { bridge, copied } = await rig()
    await bridge.copyText('hello')
    expect(copied).toEqual(['hello'])
    expect(() => bridge.firstRender('first-connect')).not.toThrow()
  })

  it('sends server and WebUI changes to the listeners', async () => {
    const { bridge, p } = await rig(fakePlatform({ answers: { [AI]: 'ok' } }))
    const states: WebuiViewState[] = []
    let views = 0
    const off1 = bridge.onWebui((s) => states.push(s))
    const off2 = bridge.onServers(() => views++)
    const r = await bridge.addServer({ name: 'GPU box', address: AI, makeDefault: true })
    expect(r.ok).toBe(true)
    expect(views).toBeGreaterThan(0)
    expect(states.at(-1)?.target).toMatchObject({ kind: 'remote', origin: AI })
    expect((await bridge.getServers()).rows).toHaveLength(1)
    off1()
    off2()
    await bridge.setShellScreen('settings')
    expect(states.at(-1)?.screen).not.toBe('settings')
    expect((await bridge.getWebui()).screen).toBe('settings')
    await flush()
    expect(p.webui.calls.filter((c) => c.name === 'setLayout').at(-1)?.args[0]).toMatchObject({ visible: false })
  })

  it('connectRemote with no id opens the default again', async () => {
    const { bridge, p } = await rig(fakePlatform({ answers: { [AI]: 'ok' } }))
    await bridge.addServer({ name: 'GPU box', address: AI, makeDefault: true })
    p.webui.emit('loaded')
    const r = await bridge.connectRemote()
    expect(r.ok).toBe(true)
  })

  it('opens the WebUI page in the browser', async () => {
    const { bridge, p } = await rig(fakePlatform({ answers: { [AI]: 'ok' } }))
    await bridge.addServer({ name: 'GPU box', address: AI, makeDefault: true })
    await bridge.openWebuiInBrowser()
    expect(p.opened).toEqual([`${AI}/app`])
  })

  it('maps the updater calls to the APK updater', async () => {
    const { bridge, core } = await rig()
    expect(await bridge.getUpdater()).toEqual({ phase: 'idle' })
    const seen: UpdaterStatus[] = []
    bridge.onUpdater((s) => seen.push(s))
    core.setUpdaterStatus({ phase: 'available', version: '5.1.0' })
    expect(seen).toEqual([{ phase: 'available', version: '5.1.0' }])
    const info = await bridge.getUpdateInfo()
    expect(info).toMatchObject({ channel: 'stable', remoteDefault: false })
    expect(await bridge.setUpdateChannel('dev')).toEqual({ ok: true, info })
  })

  it('passes the update sheet calls to the APK updater', async () => {
    const { core, p } = await rig()
    const calls: string[] = []
    const updater = {
      ...idleUpdater(),
      apkUpdate: async () => ({ canInstall: true }),
      openInstallSettings: async () => void calls.push('openInstallSettings'),
      getDevApp: async () => void calls.push('getDevApp'),
      install: async () => void calls.push('install'),
    }
    const bridge = createAndroidBridge(core, { clipboard: async () => undefined, updater, info: { version: '5.0.2' } })
    expect(await bridge.getApkUpdate()).toEqual({ canInstall: true })
    await bridge.openInstallSettings()
    await bridge.getDevApp()
    await bridge.installLauncherUpdate()
    expect(calls).toEqual(['openInstallSettings', 'getDevApp', 'install'])
    expect(p.opened).toEqual([])
  })

  it('reads, sets and follows "Keep P2P connection in the background"', async () => {
    const p = fakePlatform({ p2pVersion: P2P_LIBRARY_VERSION })
    p.p2p.keep = true
    const { bridge: b } = await rig(p)
    // With the P2P library, the bridge has the three methods.
    const bridge = b as Required<typeof b>
    expect(typeof b.getP2PKeepInBackground).toBe('function')
    expect(typeof b.setP2PKeepInBackground).toBe('function')
    expect(typeof b.onP2PKeepInBackground).toBe('function')
    expect(await bridge.getP2PKeepInBackground()).toBe(true)
    const seen: boolean[] = []
    const off = bridge.onP2PKeepInBackground((on) => seen.push(on))
    // Disconnect in the notification.
    p.p2p.emit('keepInBackground', { on: false })
    expect(seen).toEqual([false])
    expect(await bridge.getP2PKeepInBackground()).toBe(false)
    expect(await bridge.setP2PKeepInBackground(true)).toBe(true)
    expect(p.p2p.calls.filter((c) => c.name === 'setKeepInBackground').at(-1)?.args[0]).toEqual({ on: true })
    expect(seen).toEqual([false, true])
    off()
    p.p2p.emit('keepInBackground', { on: false })
    expect(seen).toEqual([false, true])
  })

  it('has no "Keep P2P connection in the background" methods when the build has no P2P library', async () => {
    // fakePlatform() answers version() with an error: no P2P library.
    const { bridge } = await rig()
    const b = bridge as unknown as Record<string, unknown>
    for (const k of ['getP2PKeepInBackground', 'setP2PKeepInBackground', 'onP2PKeepInBackground']) expect(k in b, k).toBe(false)
    // The CA help does not need P2P.
    expect(typeof b.openCertificateHelp).toBe('function')
  })

  it('opens the docs about private CAs, and nothing else, for the certificate help', async () => {
    const { bridge, p } = await rig()
    await bridge.openCertificateHelp()
    expect(p.opened).toEqual([CERTIFICATE_HELP_URL])
    expect(CERTIFICATE_HELP_URL).toBe('https://localai.io/docs/start/install/android/#certificates-and-private-cas')
  })

  it('opens release notes for a version on GitHub', async () => {
    const { bridge, p } = await rig()
    await bridge.openReleaseNotes('5.1.0')
    await bridge.openReleaseNotes('../evil')
    expect(p.opened).toEqual(['https://github.com/mudler/LocalAI/releases/tag/v5.1.0', 'https://github.com/mudler/LocalAI/releases'])
  })
})

describe('the server list on Android', () => {
  it('opens a server through the client core, which turns its automatic sign-in on again', async () => {
    const files = {
      [CONFIG_FILE]: JSON.stringify({
        remotes: [{ id: 'srv_00000001', name: 'Studio', url: 'http://127.0.0.2:41001', p2p: { peer: '12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGm', port: 41001, addrs: [], relays: [], host: '127.0.0.2' } }],
        default_target: 'srv_00000001',
      }),
    }
    const { bridge, p } = await rig(fakePlatform({ p2pVersion: P2P_LIBRARY_VERSION, files }))
    await bridge.setDefaultServer('srv_00000001')
    expect(p.p2p.calls.filter((c) => c.name === 'allowAutoSignIn').map((c) => c.args[0])).toEqual([{ peer: '12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGm' }])
  })
})
