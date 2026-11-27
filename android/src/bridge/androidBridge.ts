// SPDX-License-Identifier: MIT
// The bridge the launcher shell talks to on Android: the core and servers
// groups over the client core, and no local-server method, so the shell runs
// in its client-only mode. Everything is in the same page: a call is a method
// call on the core, a listener is a core event.
import type { BridgeExtras } from '@launcher/renderer/bridge'
import type { CoreApi, ServersApi } from '@launcher/shared/apiGroups'
import type { Capabilities, LauncherInfo } from '@launcher/shared/types'
import type { AndroidUpdater } from '../core/androidUpdater'
import type { ClientCore } from '../core/clientCore'
import type { ClientEvent } from '../core/events'
import type { AppLinksPlugin, ScannerPlugin } from '../plugins/definitions'

export { idleUpdater, type AndroidUpdater } from '../core/androidUpdater'

export const ANDROID_CAPABILITIES: Capabilities = { localServer: false, p2p: true, selfUpdate: 'apk', qrScan: true }

/**
 * The Android bridge: every core and servers method except the desktop
 * notifications (later work), plus the invite scan and incoming links when
 * the plugins are given.
 */
export type AndroidBridge = CoreApi & Omit<ServersApi, 'setDesktopNotifications'> & Partial<Pick<BridgeExtras, 'scanInvite' | 'onInviteLink'>> &
  Pick<BridgeExtras, 'getApkUpdate' | 'openInstallSettings' | 'getDevApp'> &
  Partial<Pick<BridgeExtras, 'getP2PKeepInBackground' | 'setP2PKeepInBackground' | 'onP2PKeepInBackground'>> &
  Pick<BridgeExtras, 'openCertificateHelp'>

/** The docs section on private CAs, which the certificate test card links to. */
export const CERTIFICATE_HELP_URL = 'https://localai.io/docs/start/install/android/#certificates-and-private-cas'

/** Where invites come from outside the form: links and shared text, and the QR scanner. */
export interface InviteSources {
  appLinks: AppLinksPlugin
  scanner: ScannerPlugin
}

export interface AndroidBridgeDeps {
  clipboard(text: string): Promise<void>
  updater: AndroidUpdater
  /** The app version and, when the Updater plugin answered, its application ID (shown in About). */
  info: { version: string; applicationId?: string }
  invites?: InviteSources
}

/**
 * Texts from the AppLinks plugin as one feed: the link the app was opened
 * with (taken once), then every new link or shared text. It listens from the
 * start, so a text that comes before the shell subscribes is kept (the latest
 * one) and given to the next subscriber. The shell decides what the text is.
 */
export function inviteLinkFeed(appLinks: AppLinksPlugin): (cb: (text: string) => void) => () => void {
  const subs = new Set<(text: string) => void>()
  let pending: string | undefined
  const deliver = (text: string) => {
    if (subs.size === 0) pending = text
    else for (const cb of [...subs]) cb(text)
  }
  // Without the native plugin (a web preview) there is nothing to listen to.
  void appLinks.addListener('link', (x) => deliver(x.text)).catch(() => undefined)
  void appLinks
    .takeInitial()
    .then((r) => {
      if (r.text) deliver(r.text)
    })
    .catch(() => undefined)
  return (cb) => {
    subs.add(cb)
    if (pending !== undefined) {
      const text = pending
      pending = undefined
      cb(text)
    }
    return () => void subs.delete(cb)
  }
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
  const inv = deps.invites
  const invites: Partial<Pick<BridgeExtras, 'scanInvite' | 'onInviteLink'>> = inv
    ? {
        scanInvite: async () => {
          const r = await inv.scanner.scan()
          return r.cancelled ? undefined : r.text
        },
        onInviteLink: inviteLinkFeed(inv.appLinks),
      }
    : {}

  return {
    ...invites,
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
        ...(deps.info.applicationId ? { applicationId: deps.info.applicationId } : {}),
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
    // The server list: an explicit open, which turns the automatic sign-in on again.
    setDefaultServer: (target) => core.openServer(target),
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
    // From the update sheet's "Update now" only; the bar's pill opens the sheet.
    installLauncherUpdate: () => u.install(),
    getApkUpdate: () => u.apkUpdate(),
    openInstallSettings: () => u.openInstallSettings(),
    getDevApp: () => u.getDevApp(),

    // ---- Android settings
    // Without the P2P library the switch would do nothing: Settings shows it
    // only when these methods exist. The core knows this before the bridge
    // is made (ClientCore.open waits for the P2P check).
    ...(core.p2pAvailable()
      ? {
          getP2PKeepInBackground: async () => core.p2pKeepInBackground(),
          setP2PKeepInBackground: (on: boolean) => core.setP2PKeepInBackground(on),
          onP2PKeepInBackground: (cb: (on: boolean) => void) => listen('p2pKeep', (e) => cb(e.on)),
        }
      : {}),
    openCertificateHelp: () => core.openExternal(CERTIFICATE_HELP_URL),
  }
}
