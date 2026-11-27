// SPDX-License-Identifier: MIT
// The Android app's "main process": it owns what LauncherCore owns on the
// desktop, minus the local server. A ConfigStore over the config file, the
// saved servers (RemoteServers, with its monitor) and the WebUI view model.
// It runs in the same page as the shell; the bridge calls it directly and
// listens to its events. Platform work goes to the native plugins given in
// ClientPlatform, so the tests run it with fakes.
import type { PluginListenerHandle } from '@capacitor/core'
import { ConfigStore, openWebuiInBrowser, webuiPermissions, withOpenWebuiInBrowser, withWebuiPermissions } from '@launcher/core/configStore'
import { REMOTE_RETRY_MS } from '@launcher/core/remoteMonitor'
import { RemoteServers } from '@launcher/core/remoteServers'
import { remotes } from '@launcher/core/serversConfig'
import { runServerTest, type HttpGet, type TestResult } from '@launcher/core/testClassify'
import { remember, resetPermissions, type PermissionChoice } from '@launcher/core/webuiPolicy'
import { WebuiModel, type WebuiEffect, type WebuiEvent } from '@launcher/core/webuiState'
import type { RemoteStatusView, ServersView, SwitchResult, UpdaterStatus, WebuiViewState, WindowSettingsView } from '@launcher/shared/types'
import { MOBILE_BAR_HEIGHT } from '../mobileShell'
import type { P2PPlugin, WebuiViewPlugin } from '../plugins/definitions'
import { clientConfigIO, type FileApi } from './configIO'
import type { ClientEvent } from './events'
import { androidP2PTunnels, type AndroidTunnels } from './p2pTunnels'

export type { ClientEvent } from './events'

export interface ClientPlatform {
  files: FileApi
  /** The transport of the server checks (the NativeHttp plugin). */
  http: HttpGet
  classifyError(e: unknown): TestResult
  webui: WebuiViewPlugin
  /** The P2P plugin. In a build without the library its `version()` rejects, and pairing answers `no-binary`. */
  p2p: P2PPlugin
  openExternal(url: string): Promise<void>
  now(): number
  randomBytes(n: number): Uint8Array
  appVersion: string
  /** The name this device gives a server when it pairs. Read once at open. */
  deviceName?(): Promise<string>
  log?(message: string, level?: 'info' | 'warn'): void
}

const FALLBACK_DEVICE_NAME = 'Android device'

/** The reason on the unreachable screen while this app's forwarder of the server does not listen. */
export const P2P_NOT_LISTENING = "this app's P2P connection to it is not open"

/** The forwarder states in which this app's forwarder holds the loopback address. */
const LISTENING = new Set(['ready', 'up', 'down'])

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
  private readonly tunnels: AndroidTunnels
  private readonly model: WebuiModel
  private readonly listeners = new Set<(e: ClientEvent) => void>()
  private readonly profiles = new Map<string, string>()
  private readonly handles: Promise<PluginListenerHandle>[] = []
  /** Plugin calls run one after the other, in the order the model asked for them. */
  private queue: Promise<void> = Promise.resolve()
  private lastState = ''
  private paused = false
  private keyboardOpen = false
  /** The last listening verdict of the P2P server on show, to act on its changes only. */
  private gate: { origin: string; listening: boolean } | undefined

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
    this.tunnels = androidP2PTunnels({
      plugin: p.p2p,
      store: () => store.get(),
      // RemoteServers has no hook for a tunnel state: publish the view here.
      // Lines arrive only from plugin events, after the constructor.
      onChange: () => {
        this.publishServers(this.servers.serversView())
        this.regate()
      },
      onKeepChange: (on) => this.emit({ kind: 'p2pKeep', on }),
      onRoutes: (id, routes) => {
        void this.servers.updateP2PRoutes(id, routes).catch((e: unknown) => this.log(`servers: could not save the routes of a server: ${String(e)}`, 'warn'))
      },
      log: (m, level) => this.log(m, level),
    })
    this.servers = new RemoteServers({
      store,
      now: () => p.now(),
      randomBytes: (n) => p.randomBytes(n),
      // Nothing is sent to the address of a P2P server while this app's
      // forwarder does not hold it: another app may listen there instead.
      checkOrigin: (origin, signal) =>
        this.p2pListening(origin) === false
          ? Promise.resolve({ status: 'unreachable', reason: 'refused' })
          : runServerTest(origin, p.http, (e) => p.classifyError(e), { signal }),
      p2p: this.tunnels,
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
    // The forwarders listen before the default server opens.
    await this.tunnels.init()
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

  /**
   * Feeds one event to the model, applies its effects and places the view when the state changed.
   *
   * Any app on the phone can bind a loopback address, so a P2P server's
   * origin is loaded (and gets its sign-in cookie) only while this app's own
   * forwarder listens on it. Otherwise a show, reload or navigate is dropped
   * and the model learns that the server is unreachable, whatever the
   * monitor's probe said.
   */
  webuiDispatch(e: WebuiEvent): WebuiViewState {
    const fx = this.model.dispatch(this.gated(e))
    let blocked = false
    for (const f of fx) {
      if (this.blocks(f)) blocked = true
      else this.apply(f)
    }
    this.servers?.afterWebuiEvent(e)
    if (blocked) {
      for (const f of this.model.dispatch({ type: 'remote-status', status: this.notListening() })) if (!this.blocks(f)) this.apply(f)
    }
    return this.syncState(false)
  }

  /**
   * True when `origin` is a saved P2P server whose forwarder (this app's)
   * listens: ready, up or down. False when it is a P2P server whose
   * forwarder is starting, failed, stopped or unknown. undefined for any
   * other origin.
   */
  private p2pListening(origin: string): boolean | undefined {
    const r = remotes(this.store.get()).find((x) => x.url === origin && x.p2p)
    if (!r) return undefined
    const s = this.tunnels.status(r.id)
    return s !== undefined && LISTENING.has(s.state)
  }

  private notListening(): RemoteStatusView {
    const at = this.p.now()
    return { kind: 'unreachable', reason: P2P_NOT_LISTENING, at, nextAt: at + REMOTE_RETRY_MS }
  }

  /** A monitor result for a P2P server whose forwarder does not listen says so instead. */
  private gated(e: WebuiEvent): WebuiEvent {
    if (e.type !== 'remote-status') return e
    const t = this.model.target
    if (t.kind === 'remote' && this.p2pListening(t.origin) === false) return { type: 'remote-status', status: this.notListening() }
    return e
  }

  private blocks(f: WebuiEffect): boolean {
    if (f.kind === 'show') return this.p2pListening(f.origin) === false
    if (f.kind === 'reload' || f.kind === 'navigate') {
      const o = this.model.viewOrigin
      return o !== undefined && this.p2pListening(o) === false
    }
    return false
  }

  /**
   * A forwarder state changed. When the P2P server on show stops listening,
   * the view closes; when it listens again, the monitor checks it at once
   * and the view loads on its answer.
   */
  private regate(): void {
    const t = this.model.target
    const listening = t.kind === 'remote' ? this.p2pListening(t.origin) : undefined
    if (listening === undefined) {
      this.gate = undefined
      return
    }
    const before = this.gate?.origin === t.origin ? this.gate.listening : undefined
    this.gate = { origin: t.origin, listening }
    if (listening === before) return
    if (!listening) {
      this.webuiDispatch({ type: 'remote-status', status: this.notListening() })
      return
    }
    if (!this.paused) void this.servers.retryRemote()
  }

  private syncState(force: boolean): WebuiViewState {
    const s = this.model.state()
    const json = JSON.stringify(s)
    if (force || json !== this.lastState) {
      this.lastState = json
      this.sendLayout(s)
      this.emit({ kind: 'webui', state: s })
    }
    return s
  }

  /**
   * Places the native view. The contract with WebuiHost (Kotlin):
   * `top`, `bottom` and `barHeight` are the shell's own sizes in dp, without
   * the system insets, which the native side adds. The bar is the phone bar
   * (MOBILE_BAR_HEIGHT), and 0 while the keyboard hides it.
   */
  private sendLayout(s: WebuiViewState): void {
    const layout = {
      visible: s.layout === 'webui',
      shellAbove: s.shellAbove,
      top: s.insets.top,
      bottom: s.insets.bottom,
      barHeight: this.keyboardOpen ? 0 : MOBILE_BAR_HEIGHT,
    }
    this.call('setLayout', () => this.p.webui.setLayout(layout))
  }

  /** The soft keyboard opened or closed (html.lnr-ime): the bar steps aside while it is open. */
  setKeyboardOpen(open: boolean): void {
    if (open === this.keyboardOpen) return
    this.keyboardOpen = open
    this.sendLayout(this.model.state())
  }

  /**
   * The person picks a server in the server list. A P2P server they signed
   * out of in its WebUI gets its automatic sign-in back first. When its page
   * was already on show (on its sign-in page), it reloads, so the sign-in
   * runs without another tap; a server that was not on show loads anew and
   * needs no reload.
   */
  async openServer(target: string): Promise<SwitchResult> {
    const wasSignedOut = await this.tunnels.allowAutoSignIn(target)
    const origin = remotes(this.store.get()).find((x) => x.id === target)?.url
    const shown = origin !== undefined && this.model.viewOrigin === origin
    const r = await this.servers.setDefaultServer(target)
    if (wasSignedOut && shown && r.ok && this.model.viewOrigin === origin) this.webuiDispatch({ type: 'reload' })
    return r
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

  /** The config store, shared with the APK updater (its daily-check settings live in the same file). */
  configStore(): ConfigStore {
    return this.store
  }

  /** The updater's state: a downloaded update brings the bar's update pill. */
  setUpdaterStatus(status: UpdaterStatus): void {
    this.webuiDispatch({ type: 'inputs', patch: { updateReady: status.phase === 'downloaded' } })
    this.emit({ kind: 'updater', status })
  }

  /** One step back in the WebUI history. False when the page has no history (or the call failed). */
  async webuiGoBack(): Promise<boolean> {
    try {
      return (await this.p.webui.goBack()).handled
    } catch {
      return false
    }
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
    // The service restarts the forwarders on return; the states are read
    // (the plugin waits while they restart) before the default is checked
    // and its page reloads.
    void this.tunnels
      .refresh()
      .catch((e) => this.log(`servers: P2P refresh failed: ${e instanceof Error ? e.message : String(e)}`, 'warn'))
      .then(() => {
        if (!this.paused) this.servers.resumeMonitor()
      })
  }

  /** The P2P library loaded and has the expected version. Known once `open` returns. */
  p2pAvailable(): boolean {
    return this.tunnels.available()
  }

  /** "Keep P2P connection in the background", as the native side has it. */
  p2pKeepInBackground(): boolean {
    return this.tunnels.keepInBackground()
  }

  async setP2PKeepInBackground(on: boolean): Promise<boolean> {
    await this.tunnels.setKeepInBackground(on)
    return this.tunnels.keepInBackground()
  }

  async shutdown(): Promise<void> {
    this.servers.stopAll()
    await this.tunnels.close()
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
