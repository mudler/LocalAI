// SPDX-License-Identifier: MIT
// The contracts of the app's native plugins (Kotlin, android/native). The web
// side and the Kotlin side each implement one half of them; change a signature
// only together with the Kotlin plugin of the same name.
import type { PluginListenerHandle } from '@capacitor/core'
import type { TlsReason, UnreachableReason } from '@launcher/core/testClassify'

export type NativeGetResult =
  | { ok: true; status: number; location?: string; contentType: string; body: string }
  | { ok: false; error: 'tls'; reason: TlsReason }
  | { ok: false; error: 'unreachable'; reason: UnreachableReason }
  | { ok: false; error: 'cancelled' }

export interface NativeHttpPlugin {
  get(o: { id: string; url: string; timeoutMs: number }): Promise<NativeGetResult>
  cancel(o: { id: string }): Promise<void>
}

export interface WebuiShowOptions { origin: string; url: string; profile: string }
export interface WebuiLayout { visible: boolean; shellAbove: boolean; top: number; bottom: number; barHeight: number }

export interface WebuiViewPlugin {
  show(o: WebuiShowOptions): Promise<void>
  navigate(o: { url: string }): Promise<void>
  close(): Promise<void>
  reload(): Promise<void>
  setLayout(o: WebuiLayout): Promise<void>
  setPermissionStore(o: { json: string }): Promise<void>
  goBack(): Promise<{ handled: boolean }>
  currentUrl(): Promise<{ url?: string }>
  addListener(e: 'loaded', cb: () => void): Promise<PluginListenerHandle>
  addListener(e: 'loadFailed', cb: (x: { code: number; description: string }) => void): Promise<PluginListenerHandle>
  addListener(e: 'crashed', cb: (x: { reason: string }) => void): Promise<PluginListenerHandle>
  addListener(e: 'unresponsive' | 'responsive', cb: () => void): Promise<PluginListenerHandle>
  addListener(e: 'permissionChoice', cb: (x: { origin: string; choice: 'allow' | 'deny'; remember: boolean }) => void): Promise<PluginListenerHandle>
  addListener(e: 'openExternal', cb: (x: { url: string }) => void): Promise<PluginListenerHandle>
}

export interface P2PForwardInput {
  id: string
  name: string
  peer: string
  host: string
  port: number
  addrs: string[]
  relays: string[]
  /** Public relays of the invite that only help a hole punch: they never carry data. */
  hints: string[]
}

/**
 * A failed pairing. `code` is the stable code of the library (`needs_relay`,
 * `gone`, `expired`, ...); `detail` is its error text, for logs.
 */
export interface P2PPairFailure {
  ok: false
  code?: string
  detail: string
}

export interface P2PPlugin {
  version(): Promise<{ version: string }>
  deviceName(): Promise<{ name: string }>
  peerId(): Promise<{ peerId?: string }>
  /**
   * `json` is the result object of the library. A server that gave a device
   * credential (a sign-in invite) also sends `sign_in` and `owner_name`; the
   * credential itself stays in Kotlin and never reaches this side.
   */
  pair(o: { link: string; name: string; allowOpenRelay: boolean }): Promise<{ ok: true; json: string } | P2PPairFailure>
  /** Deletes the stored device credential of a server (by its peer ID): the server was removed. */
  forgetCredential(o: { peer: string }): Promise<void>
  /**
   * The person opened the server (by its peer ID) from the server list: a
   * sign-out in its WebUI no longer stops the automatic sign-in.
   * `wasSignedOut` is true when it did.
   */
  allowAutoSignIn(o: { peer: string }): Promise<{ wasSignedOut: boolean }>
  /** Deletes every stored device credential whose server is not in `peers` (the saved servers). */
  retainCredentials(o: { peers: string[] }): Promise<void>
  freePort(o: { host: string }): Promise<{ port: number }>
  setForwards(o: { forwards: P2PForwardInput[] }): Promise<void>
  statuses(): Promise<{ states: { id: string; line: string }[] }>
  setKeepInBackground(o: { on: boolean }): Promise<void>
  /** The saved "keep P2P in the background" setting. The notification's Disconnect turns it off. */
  keepInBackground(): Promise<{ on: boolean }>
  /**
   * Asks for POST_NOTIFICATIONS (Android 13+) so the service notification
   * and its Disconnect action show. The system dialog comes at most once per
   * install; later calls only answer the current state.
   */
  requestNotifications(): Promise<{ granted: boolean }>
  /**
   * `line` is the raw JSON line: the facade's `ready`, `up`, `down`,
   * `closed`, and the supervisor's `starting` and `failed` (with `error`).
   */
  addListener(e: 'forwardState', cb: (x: { id: string; line: string }) => void): Promise<PluginListenerHandle>
  /** The setting changed natively (the notification's Disconnect). */
  addListener(e: 'keepInBackground', cb: (x: { on: boolean }) => void): Promise<PluginListenerHandle>
}

export interface UpdaterPlugin {
  fetchText(o: { url: string; maxBytes: number }): Promise<{ status: number; text: string }>
  download(o: { url: string; sha256: string; size: number }): Promise<{ file: string }>
  verifySigner(o: { file: string }): Promise<{ sameSigner: boolean }>
  canInstall(): Promise<{ allowed: boolean }>
  openInstallSettings(): Promise<void>
  install(o: { file: string }): Promise<void>
  appInfo(): Promise<{ applicationId: string; versionName: string; versionCode: number; sdkInt: number; feedBase: string }>
  addListener(e: 'progress', cb: (x: { done: number; total: number }) => void): Promise<PluginListenerHandle>
  addListener(e: 'status', cb: (x: { status: 'pending-user-action' | 'success' | 'failure'; message?: string }) => void): Promise<PluginListenerHandle>
}

export interface ScannerPlugin { scan(): Promise<{ text?: string; cancelled: boolean }> }

export interface AppLinksPlugin {
  /** The link or shared text the app was opened with, once. */
  takeInitial(): Promise<{ text?: string }>
  addListener(e: 'link', cb: (x: { text: string }) => void): Promise<PluginListenerHandle>
}
