// SPDX-License-Identifier: MIT
import { afterEach, describe, expect, it } from 'vitest'
import { capabilitiesOf, CORE_METHODS, LOCAL_SERVER_METHODS, SERVERS_METHODS } from '@launcher/shared/apiGroups'
import { hasLocal } from '@launcher/renderer/bridge'
import type { UpdaterStatus, WebuiViewState } from '@launcher/shared/types'
import { ANDROID_CAPABILITIES, createAndroidBridge, idleUpdater } from '../src/bridge/androidBridge'
import { ClientCore } from '../src/core/clientCore'
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

  it('opens release notes for a version on GitHub', async () => {
    const { bridge, p } = await rig()
    await bridge.openReleaseNotes('5.1.0')
    await bridge.openReleaseNotes('../evil')
    expect(p.opened).toEqual(['https://github.com/mudler/LocalAI/releases/tag/v5.1.0', 'https://github.com/mudler/LocalAI/releases'])
  })
})
