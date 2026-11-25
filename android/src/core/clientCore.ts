// SPDX-License-Identifier: MIT
// The Android app's "main process": it owns what LauncherCore owns on the
// desktop, minus the local server. A ConfigStore over the config file, the
// saved servers (RemoteServers, with its monitor) and the WebUI view model.
// It runs in the same page as the shell; the bridge calls it directly and
// listens to its events. Platform work goes to the native plugins given in
// ClientPlatform, so the tests run it with fakes.
import type { PluginListenerHandle } from '@capacitor/core'
import { ConfigStore, openWebuiInBrowser, webuiPermissions, withOpenWebuiInBrowser, withWebuiPermissions } from '@launcher/core/configStore'
import { RemoteServers, type P2PTunnels } from '@launcher/core/remoteServers'
import { remotes } from '@launcher/core/serversConfig'
import { runServerTest, type HttpGet, type TestResult } from '@launcher/core/testClassify'
import { remember, resetPermissions, type PermissionChoice } from '@launcher/core/webuiPolicy'
import { WebuiModel, type WebuiEffect, type WebuiEvent } from '@launcher/core/webuiState'
import { STATUS_BAR_HEIGHT } from '@launcher/shared/layout'
import type { ServersView, UpdaterStatus, WebuiViewState, WindowSettingsView } from '@launcher/shared/types'
import type { WebuiViewPlugin } from '../plugins/definitions'
import { clientConfigIO, type FileApi } from './configIO'
import type { ClientEvent } from './events'

export type { ClientEvent } from './events'

export interface ClientPlatform {
  files: FileApi
  /** The transport of the server checks (the NativeHttp plugin). */
  http: HttpGet
  classifyError(e: unknown): TestResult
  webui: WebuiViewPlugin
  /** The forwarders of servers over P2P. A build without P2P answers `available()` false. */
  p2p: P2PTunnels
  openExternal(url: string): Promise<void>
  now(): number
  randomBytes(n: number): Uint8Array
  appVersion: string
  /** The name this device gives a server when it pairs. Read once at open. */
  deviceName?(): Promise<string>
  log?(message: string, level?: 'info' | 'warn'): void
}

/** A build without the P2P library: pairing answers `no-binary`. */
export const NO_P2P: P2PTunnels = {
  available: () => false,
  pair: async () => ({ ok: false, reason: 'no-binary', detail: '' }),
  allocatePort: async () => {
    throw new Error('P2P is not available')
  },
  start: () => undefined,
  stop: () => undefined,
  status: () => undefined,
  whenReady: async () => false,
}

const FALLBACK_DEVICE_NAME = 'Android device'

/**
 * The WebView profile of an origin: `webui-` and the first 24 hex digits of
 * the SHA-256 of the origin. One profile per origin keeps the cookies (the
 * sign-in) of each server apart, also for servers over P2P on loopback.
 */
export async function profileFor(origin: string): Promise<string> {
  const digest = await globalThis.crypto.subtle.digest('SHA-256', new TextEncoder().encode(origin))
  let hex = ''
  for (const b of new Uint8Array(digest)) hex += b.toString(16).padStart(2, '0')
  return `webui-${hex.slice(0, 24)}`
}

export class ClientCore {
  readonly servers: RemoteServers
  private readonly model: WebuiModel
  private readonly listeners = new Set<(e: ClientEvent) => void>()
  private readonly profiles = new Map<string, string>()
  private readonly handles: Promise<PluginListenerHandle>[] = []
  /** Plugin calls run one after the other, in the order the model asked for them. */
  private queue: Promise<void> = Promise.resolve()
  private lastState = ''
  private paused = false

  private constructor(
    private readonly p: ClientPlatform,
    private readonly store: ConfigStore,
    deviceName: string,
  ) {
    const c = store.get()
    this.model = new WebuiModel({
      server: 'not-installed',
      localOrigin: '',
      installActive: false,
      // The bar is the only way to the switcher and Settings on a phone: it stays.
      statusBar: true,
      updateReady: false,
      sandboxOff: false,
      sandboxNoticeShown: false,
      openInBrowser: openWebuiInBrowser(c),
      workerMode: false,
      serverUpdate: false,
      startedHidden: false,
    })
    this.servers = new RemoteServers({
      store,
      now: () => p.now(),
      randomBytes: (n) => p.randomBytes(n),
      checkOrigin: (origin, signal) => runServerTest(origin, p.http, (e) => p.classifyError(e), { signal }),
      p2p: p.p2p,
      deviceName: () => deviceName,
      webuiDispatch: (e) => this.webuiDispatch(e),
      webuiViewOrigin: () => this.model.viewOrigin,
      emitServers: (view) => this.publishServers(view),
      log: (m, level) => this.log(m, level),
      localRow: () => undefined,
      firstRun: (cfg) => remotes(cfg).length === 0,
      // RemoteServers has already stopped the monitor and sent back-to-local;
      // there is no local server to start.
      onLocalDefault: async () => undefined,
      onSwitchFinished: async () => undefined,
    })
  }

  static async open(p: ClientPlatform): Promise<ClientCore> {
    const store = await ConfigStore.open(clientConfigIO(p.files, () => new Date(p.now())))
    let name = FALLBACK_DEVICE_NAME
    try {
      name = ((await p.deviceName?.()) ?? '').trim() || FALLBACK_DEVICE_NAME
    } catch {
      // keep the fallback
    }
    const core = new ClientCore(p, store, name)
    await core.start()
    return core
  }

  private async start(): Promise<void> {
    const w = this.p.webui
    this.handles.push(
      w.addListener('loaded', () => void this.webuiDispatch({ type: 'view-loaded' })),
      w.addListener('loadFailed', (x) => void this.webuiDispatch({ type: 'view-load-failed', code: x.code, description: x.description })),
      w.addListener('crashed', (x) => void this.webuiDispatch({ type: 'view-crashed', reason: x.reason })),
      w.addListener('unresponsive', () => void this.webuiDispatch({ type: 'view-unresponsive' })),
      w.addListener('responsive', () => void this.webuiDispatch({ type: 'view-responsive' })),
      w.addListener('permissionChoice', (x) => void this.onPermissionChoice(x.origin, x.choice, x.remember)),
      w.addListener('openExternal', (x) => void this.openExternal(x.url)),
    )
    await Promise.all(this.handles)
    // Every saved origin has its profile before the first `show`.
    await Promise.all(remotes(this.store.get()).map((r) => this.profileOf(r.url)))
    this.call('setPermissionStore', () => w.setPermissionStore({ json: JSON.stringify(webuiPermissions(this.store.get())) }))
    this.servers.startTunnels()
    this.servers.openDefaultRemote()
    this.syncState(true)
  }

  on(cb: (e: ClientEvent) => void): () => void {
    this.listeners.add(cb)
    return () => void this.listeners.delete(cb)
  }

  private emit(e: ClientEvent): void {
    for (const cb of this.listeners) {
      try {
        cb(e)
      } catch (err) {
        this.log(`client: a listener failed: ${String(err)}`, 'warn')
      }
    }
  }

  private log(message: string, level: 'info' | 'warn' = 'info'): void {
    this.p.log?.(message, level)
  }

  private async profileOf(origin: string): Promise<string> {
    const known = this.profiles.get(origin)
    if (known) return known
    const p = await profileFor(origin)
    this.profiles.set(origin, p)
    return p
  }

  /** Queues one plugin call; a failure is logged and does not stop the next call. */
  private call(name: string, run: () => Promise<unknown>): void {
    this.queue = this.queue.then(
      () => run().then(() => undefined),
    ).catch((e) => this.log(`webui: ${name} failed: ${e instanceof Error ? e.message : String(e)}`, 'warn'))
  }

  private apply(f: WebuiEffect): void {
    const w = this.p.webui
    switch (f.kind) {
      case 'show':
        this.call('show', async () => w.show({ origin: f.origin, url: f.url, profile: await this.profileOf(f.origin) }))
        break
      case 'navigate':
        this.call('navigate', () => w.navigate({ url: f.url }))
        break
      case 'close':
        this.call('close', () => w.close())
        break
      case 'reload':
        this.call('reload', () => w.reload())
        break
      case 'open-browser':
        void this.openExternal(f.url)
        break
    }
  }

  private publishServers(view: ServersView): void {
    // Fill the profile map early, so a later `show` finds it ready.
    for (const r of view.rows) if (r.origin) void this.profileOf(r.origin)
    this.emit({ kind: 'servers', view })
  }

  /** Feeds one event to the model, applies its effects and places the view when the state changed. */
  webuiDispatch(e: WebuiEvent): WebuiViewState {
    const fx = this.model.dispatch(e)
    for (const f of fx) this.apply(f)
    this.servers?.afterWebuiEvent(e)
    return this.syncState(false)
  }

  private syncState(force: boolean): WebuiViewState {
    const s = this.model.state()
    const json = JSON.stringify(s)
    if (force || json !== this.lastState) {
      this.lastState = json
      const layout = { visible: s.layout === 'webui', shellAbove: s.shellAbove, top: s.insets.top, bottom: s.insets.bottom, barHeight: STATUS_BAR_HEIGHT }
      this.call('setLayout', () => this.p.webui.setLayout(layout))
      this.emit({ kind: 'webui', state: s })
    }
    return s
  }

  webuiState(): WebuiViewState {
    return this.model.state()
  }

  configNotice(): { status: 'recovered'; reason: string } | undefined {
    const l = this.store.loaded
    return l.status === 'recovered' ? { status: 'recovered', reason: l.reason ?? 'config file could not be used' } : undefined
  }

  windowSettings(): WindowSettingsView {
    const c = this.store.get()
    return {
      openInBrowser: openWebuiInBrowser(c),
      // Server notifications are later work on Android.
      desktopNotifications: false,
      anyRemembered: Object.keys(webuiPermissions(c)).length > 0,
    }
  }

  async setOpenInBrowser(on: boolean): Promise<WindowSettingsView> {
    await this.store.update((c) => withOpenWebuiInBrowser(c, on))
    this.webuiDispatch({ type: 'inputs', patch: { openInBrowser: on } })
    return this.windowSettings()
  }

  /** "Open in browser": the page on show (when it is still on the server), else the server's home. */
  async openInBrowser(): Promise<void> {
    let currentUrl: string | undefined
    try {
      currentUrl = (await this.p.webui.currentUrl()).url
    } catch {
      // the home then
    }
    this.webuiDispatch({ type: 'open-in-browser', currentUrl })
  }

  async openExternal(url: string): Promise<void> {
    try {
      await this.p.openExternal(url)
    } catch (e) {
      this.log(`client: could not open a link: ${e instanceof Error ? e.message : String(e)}`, 'warn')
    }
  }

  private pushPermissions(): void {
    const json = JSON.stringify(webuiPermissions(this.store.get()))
    this.call('setPermissionStore', () => this.p.webui.setPermissionStore({ json }))
  }

  async resetWebuiPermissions(): Promise<void> {
    await this.store.update((c) => withWebuiPermissions(c, resetPermissions(webuiPermissions(c))))
    this.log('webui: remembered permissions reset')
    this.pushPermissions()
  }

  async rememberPermission(origin: string, choice: PermissionChoice): Promise<void> {
    await this.store.update((c) => withWebuiPermissions(c, remember(webuiPermissions(c), origin, 'media', choice)))
    this.log(`webui: ${choice === 'allow' ? 'allowed' : 'blocked'} microphone and camera for a server, remembered`)
    this.pushPermissions()
  }

  private async onPermissionChoice(origin: string, choice: PermissionChoice, rememberIt: boolean): Promise<void> {
    if (!rememberIt) return
    // Only the server on show can have asked.
    if (origin !== this.model.viewOrigin || (choice !== 'allow' && choice !== 'deny')) return
    try {
      await this.rememberPermission(origin, choice)
    } catch (e) {
      this.log(`webui: could not save a permission choice: ${e instanceof Error ? e.message : String(e)}`, 'warn')
    }
  }

  /** The updater's state: a downloaded update brings the bar's update pill. */
  setUpdaterStatus(status: UpdaterStatus): void {
    this.webuiDispatch({ type: 'inputs', patch: { updateReady: status.phase === 'downloaded' } })
    this.emit({ kind: 'updater', status })
  }

  /** Opens the server switcher (a shortcut from outside the shell). */
  openSwitcher(): void {
    this.emit({ kind: 'openSwitcher' })
  }

  /** App to foreground: check the remote default once. To background: pause the monitor. */
  setForeground(on: boolean): void {
    if (!on) {
      if (this.paused) return
      this.paused = true
      this.servers.pauseMonitor()
      return
    }
    if (!this.paused) return
    this.paused = false
    this.servers.resumeMonitor()
  }

  async shutdown(): Promise<void> {
    this.servers.stopAll()
    for (const h of this.handles) {
      try {
        await (await h).remove()
      } catch {
        // already gone
      }
    }
    await this.store.idle()
    await this.queue
  }
}
