// The bridge method groups: CORE, SERVERS and LOCAL_SERVER together are
// LauncherApi, with no name in two groups, and a missing `capabilities` in
// info() means the desktop set. The compile-time version of the coverage check
// lives in src/shared/apiGroups.ts (test files are not type-checked).
import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import {
  CORE_METHODS,
  DESKTOP_CAPABILITIES,
  LOCAL_REQUIRED_METHODS,
  LOCAL_SERVER_METHODS,
  SERVERS_METHODS,
  capabilitiesOf,
} from '../src/shared/apiGroups'
import { OPTIONAL_API, hasLocal } from '../src/renderer/bridge'
import { fakeBridge } from '../src/renderer/dev/fakeBridge'
import { fixture } from '../src/renderer/dev/fixtures'

/** The method names of `interface LauncherApi`, read from the source. */
function launcherApiNames(): string[] {
  const src = fs.readFileSync(path.resolve(__dirname, '../src/shared/types.ts'), 'utf8')
  const block = src.match(/export interface LauncherApi \{\n([\s\S]*?)\n\}/)
  if (!block) throw new Error('LauncherApi not found in src/shared/types.ts')
  return [...block[1].matchAll(/^ {2}([a-zA-Z]+)\(/gm)].map((m) => m[1])
}

describe('bridge method groups', () => {
  it('are disjoint and have 9, 28 and 48 names, 85 in all', () => {
    expect(CORE_METHODS).toHaveLength(9)
    expect(SERVERS_METHODS).toHaveLength(28)
    expect(LOCAL_SERVER_METHODS).toHaveLength(48)
    const all = [...CORE_METHODS, ...SERVERS_METHODS, ...LOCAL_SERVER_METHODS]
    expect(all).toHaveLength(85)
    expect(new Set(all).size).toBe(85)
  })

  it('keep the required local methods inside the local-server group', () => {
    expect(LOCAL_REQUIRED_METHODS).toHaveLength(24)
    for (const m of LOCAL_REQUIRED_METHODS) expect(LOCAL_SERVER_METHODS).toContain(m)
  })

  it('together are exactly the methods of LauncherApi', () => {
    const declared = launcherApiNames()
    expect(declared).toHaveLength(85)
    expect([...CORE_METHODS, ...SERVERS_METHODS, ...LOCAL_SERVER_METHODS].sort()).toEqual([...declared].sort())
  })

  it('keep OPTIONAL_API the same set as before the split', () => {
    const before = [
      'getWebui', 'onWebui', 'setShellScreen', 'setOverlay', 'setInsets', 'openWebuiInBrowser', 'connectRemote',
      'backToLocal', 'reloadWebui', 'resetWebuiPermissions',
      'getWindowSettings', 'setOpenInBrowser', 'setDesktopNotifications',
      'getUpdateInfo', 'setAutoCheck', 'setUpdateChannel', 'backToStable',
      'getServers', 'onServers', 'onOpenSwitcher', 'testServer', 'addServer', 'addServerByInvite', 'updateServer', 'removeServer', 'setDefaultServer', 'cancelSwitch', 'retryRemote', 'setKeepLocalRunning',
      'openReleaseNotes', 'getInstallDetails', 'installLauncherUpdate', 'relaunch',
      'findFreePort', 'showPortProcess', 'pickFolder', 'getHardwareSummary',
      'getNetwork', 'getNetworkPlan', 'setNetworkExposed', 'regenerateApiKey', 'dismissKeyNotice', 'revealApiKey', 'copyApiKey',
      'getEnvVars', 'setEnvVar', 'deleteEnvVar', 'getSecretStorageState',
      'getRegistryCredentials', 'saveRegistryCredential', 'removeRegistryCredential', 'testRegistryCredential',
    ]
    expect(OPTIONAL_API).toHaveLength(52)
    expect([...OPTIONAL_API].sort()).toEqual(before.sort())
  })
})

describe('capabilitiesOf', () => {
  const info = fixture('ready').info

  it('treats an info() with no capabilities (an older main process) as the desktop set', () => {
    expect(capabilitiesOf({ ...info, capabilities: undefined })).toEqual(DESKTOP_CAPABILITIES)
    expect(capabilitiesOf({ ...info, capabilities: undefined }).localServer).toBe(true)
  })

  it('treats a missing info as the desktop set', () => {
    expect(capabilitiesOf(undefined)).toEqual(DESKTOP_CAPABILITIES)
  })

  it('returns the capabilities info() sends', () => {
    const client = { localServer: false, p2p: false, selfUpdate: 'apk' as const, qrScan: true }
    expect(capabilitiesOf({ ...info, capabilities: client })).toEqual(client)
  })

  it('is the desktop set for desktop', () => {
    expect(DESKTOP_CAPABILITIES).toEqual({ localServer: true, p2p: true, selfUpdate: 'electron', qrScan: false })
  })
})

describe('hasLocal', () => {
  it('is true for the desktop fake bridge', () => {
    expect(hasLocal(fakeBridge(fixture('ready')))).toBe(true)
    expect(hasLocal(fakeBridge(fixture('ready'), { extras: false }))).toBe(true)
  })

  it('is false when a required local method is missing', () => {
    const b = fakeBridge(fixture('ready'))
    const { start: _start, ...rest } = b
    expect(hasLocal(rest as typeof b)).toBe(false)
  })
})
