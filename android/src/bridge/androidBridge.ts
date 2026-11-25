// SPDX-License-Identifier: MIT
// The bridge the launcher shell talks to on Android: the core and servers
// groups over the client core, and no local-server method, so the shell runs
// in its client-only mode. Everything is in the same page: a call is a method
// call on the core, a listener is a core event.
import type { CoreApi, ServersApi } from '@launcher/shared/apiGroups'
import type { Capabilities, LauncherInfo, UpdateInfoView, UpdaterStatus } from '@launcher/shared/types'
import type { ClientCore } from '../core/clientCore'
import type { ClientEvent } from '../core/events'

export const ANDROID_CAPABILITIES: Capabilities = { localServer: false, p2p: true, selfUpdate: 'apk', qrScan: true }

/**
 * The APK updater (its own lane). Until it lands, `idleUpdater` stands in:
 * nothing to check, nothing offered.
 */
export interface AndroidUpdater {
  status(): UpdaterStatus
  on(cb: (s: UpdaterStatus) => void): () => void
  info(): UpdateInfoView
  check(now: number): Promise<void>
  install(): Promise<void>
  setAutoCheck(on: boolean): Promise<UpdateInfoView>
}

export function idleUpdater(): AndroidUpdater {
  const info: UpdateInfoView = { autoCheck: false, channel: 'stable', devConfirmed: true, remoteDefault: false }
  return {
    status: () => ({ phase: 'idle' }),
    on: () => () => undefined,
    info: () => ({ ...info }),
    check: async () => undefined,
    install: async () => undefined,
    setAutoCheck: async () => ({ ...info }),
  }
}

/** The Android bridge: every core and servers method except the desktop notifications (later work). */
export type AndroidBridge = CoreApi & Omit<ServersApi, 'setDesktopNotifications'>

export interface AndroidBridgeDeps {
  clipboard(text: string): Promise<void>
  updater: AndroidUpdater
  info: { version: string }
}

const RELEASES = 'https://github.com/mudler/LocalAI/releases'

/** The release page of a version; anything that is not a plain version opens the list. */
export function releaseNotesUrl(version: string): string {
  const v = version.trim().replace(/^v/, '')
  return /^\d{1,4}\.\d{1,4}\.\d{1,6}$/.test(v) ? `${RELEASES}/tag/v${v}` : RELEASES
}

export function createAndroidBridge(core: ClientCore, deps: AndroidBridgeDeps): AndroidBridge {
  const listen = <K extends ClientEvent['kind']>(kind: K, cb: (e: Extract<ClientEvent, { kind: K }>) => void) =>
    core.on((e) => {
      if (e.kind === kind) cb(e as Extract<ClientEvent, { kind: K }>)
    })
  const u = deps.updater
  u.on((s) => core.setUpdaterStatus(s))

  return {
    // ---- core
    info: async (): Promise<LauncherInfo> => {
      const notice = core.configNotice()
      return {
        version: deps.info.version,
        packaged: true,
        trayActive: false,
        sandboxed: true,
        capabilities: ANDROID_CAPABILITIES,
        ...(notice ? { configNotice: notice } : {}),
      }
    },
    copyText: (text) => deps.clipboard(text),
    firstRender: (shown) => console.info(`shell: first render ${shown}`),
    getUpdater: async () => u.status(),
    checkForLauncherUpdate: () => u.check(Date.now()),
    onUpdater: (cb) => listen('updater', (e) => cb(e.status)),
    // The bar is the only way to the switcher and Settings on a phone: it stays.
    getStatusBarVisible: async () => true,
    setStatusBarVisible: async () => true,
    onStatusBar: (cb) => listen('statusBar', (e) => cb(e.visible)),

    // ---- servers
    getServers: async () => core.servers.serversView(),
    onServers: (cb) => listen('servers', (e) => cb(e.view)),
    onOpenSwitcher: (cb) => listen('openSwitcher', () => cb()),
    testServer: (input) => core.servers.testServer(input),
    addServer: (input) => core.servers.addServer(input),
    addServerByInvite: (input) => core.servers.addServerByInvite(input),
    updateServer: (input) => core.servers.updateServer(input),
    removeServer: (id) => core.servers.removeServer(id),
    setDefaultServer: (target) => core.servers.setDefaultServer(target),
    cancelSwitch: () => core.servers.cancelSwitch(),
    retryRemote: () => core.servers.retryRemote(),
    getWebui: async () => core.webuiState(),
    onWebui: (cb) => listen('webui', (e) => cb(e.state)),
    setShellScreen: async (screen) => core.webuiDispatch({ type: 'screen', screen }),
    setOverlay: async (open) => core.webuiDispatch({ type: 'overlay', open }),
    setInsets: (insets) => void core.webuiDispatch({ type: 'insets', insets }),
    openWebuiInBrowser: () => core.openInBrowser(),
    connectRemote: (id) => core.servers.setDefaultServer(id ?? core.servers.serversView().defaultId),
    reloadWebui: async () => core.webuiDispatch({ type: 'reload' }),
    resetWebuiPermissions: () => core.resetWebuiPermissions(),
    getWindowSettings: async () => core.windowSettings(),
    setOpenInBrowser: (on) => core.setOpenInBrowser(on),
    getUpdateInfo: async () => u.info(),
    setAutoCheck: (on) => u.setAutoCheck(on),
    // One channel per app (the development app has its own application ID).
    setUpdateChannel: async () => ({ ok: true, info: u.info() }),
    openReleaseNotes: (version) => core.openExternal(releaseNotesUrl(version)),
    installLauncherUpdate: () => u.install(),
  }
}
