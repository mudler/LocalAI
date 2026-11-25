// The renderer's view of the preload bridge: LauncherApi, with some of its
// methods treated as optional, plus optional methods for channels the main
// process does not have yet (worker mode). A
// screen calls an optional method only when it is there and hides the control
// that needs it otherwise, so the shell also runs on a fake or older bridge.
import { createContext, useContext } from 'react'
import type { LauncherApi } from '../shared/types'
import {
  LOCAL_OPTIONAL_METHODS,
  LOCAL_REQUIRED_METHODS,
  SERVERS_METHODS,
  type CoreApi,
  type LocalServerApi,
  type ServersApi,
} from '../shared/apiGroups'

/** The shell screens a person opens from the bar. */
export type ShellScreenId = 'logs' | 'settings' | 'connect' | 'worker-settings'

export type { ServersView, EnvVarsView, InstallDetailsView, NetworkPlanView, NetworkView, SecretStorageView, UpdateInfoView, WindowSettingsView } from '../shared/types'

export type WorkerStatus = 'idle' | 'testing' | 'reachable' | 'unreachable' | 'waiting-approval' | 'connected' | 'failed'

export interface WorkerConfig {
  serverUrl: string
  natsUrl: string
  /** Empty means "keep the stored token". */
  token: string
  advertiseAddress: string
  nodeName: string
}

export interface WorkerView {
  mode: 'local' | 'worker'
  running: boolean
  status: WorkerStatus
  config: Omit<WorkerConfig, 'token'>
  /** A token is stored in the system keychain. */
  tokenStored: boolean
  message?: string
}

/** The app update of an APK (`selfUpdate: 'apk'`), as the update sheet shows it. */
export interface ApkUpdateView {
  /** The newer release, once a check found one. `downloaded`: the file is checked and ready to install. */
  offer?: { version: string; size: number; notesUrl: string; downloaded: boolean }
  /** Android lets this app install updates ("Allow from this source" is on). */
  canInstall: boolean
  /** Android has the update: it installs it (`installing`), or showed its own confirmation (`confirm`). */
  install?: 'installing' | 'confirm'
}

/** Channels the other lanes add. All optional; see the comment at the top. */
export interface BridgeExtras {
  // distributed worker
  getWorker(): Promise<WorkerView>
  onWorker(cb: (w: WorkerView) => void): () => void
  setRunMode(mode: 'local' | 'worker'): Promise<WorkerView>
  testWorker(config: WorkerConfig): Promise<WorkerView>
  startWorker(config: WorkerConfig): Promise<WorkerView>
  stopWorker(): Promise<WorkerView>
  // invites from outside the form (the Android app)
  /** Scans a QR code with the camera: its text, or undefined when the person cancels. */
  scanInvite(): Promise<string | undefined>
  /** Texts that reach the app from a `localai://pair` link or the share sheet, including the one it was opened with. */
  onInviteLink(cb: (text: string) => void): () => void
  // app updates of an APK (the Android app)
  getApkUpdate(): Promise<ApkUpdateView>
  /** Opens the Android screen where the person allows this app to install updates. */
  openInstallSettings(): Promise<void>
  /** Opens the download of the development app (a separate app) in the browser. */
  getDevApp(): Promise<void>
  // "Keep P2P connection in the background" (the Android app). The native side
  // owns the value: the notification's Disconnect turns it off.
  getP2PKeepInBackground(): Promise<boolean>
  /** Answers the value the native side has after the change. */
  setP2PKeepInBackground(on: boolean): Promise<boolean>
  onP2PKeepInBackground(cb: (on: boolean) => void): () => void
  /** Opens the help page about installing a private CA (the Android app). The URL is fixed on the other side. */
  openCertificateHelp(): Promise<void>
}

/**
 * LauncherApi methods a desktop bridge may lack (the shell hides the control):
 * the servers group and the optional local-server methods. Kept for the fake
 * bridge's `extras` option.
 */
export const OPTIONAL_API = [...SERVERS_METHODS, ...LOCAL_OPTIONAL_METHODS] as const satisfies readonly (keyof LauncherApi)[]
export type OptionalApi = (typeof OPTIONAL_API)[number]

/**
 * Every bridge has the core group. The servers group and the local-server
 * group are optional: a client-only bridge has no local-server methods, and a
 * fake or older desktop bridge may lack some of the others.
 */
export type Bridge = CoreApi & Partial<ServersApi> & Partial<LocalServerApi> & Partial<BridgeExtras>

/** The bridge runs a LocalAI server on this device: it has every required local-server method. */
export function hasLocal(b: Bridge): b is Bridge & Pick<LocalServerApi, (typeof LOCAL_REQUIRED_METHODS)[number]> {
  return LOCAL_REQUIRED_METHODS.every((k) => typeof b[k] === 'function')
}

export const BridgeContext = createContext<Bridge | null>(null)

export function useBridge(): Bridge {
  const b = useContext(BridgeContext)
  if (!b) throw new Error('useBridge needs a BridgeContext provider')
  return b
}

/** The names of the optional methods this bridge does not have. The report and a test use it. */
export function missingExtras(b: Bridge): (keyof BridgeExtras)[] {
  return EXTRA_NAMES.filter((k) => typeof b[k] !== 'function')
}

/** Optional methods that no main-process channel answers yet. */
export const EXTRA_NAMES: (keyof BridgeExtras)[] = [
  'getWorker', 'onWorker', 'setRunMode', 'testWorker', 'startWorker', 'stopWorker', 'scanInvite', 'onInviteLink',
  'getApkUpdate', 'openInstallSettings', 'getDevApp', 'getP2PKeepInBackground', 'setP2PKeepInBackground',
  'onP2PKeepInBackground', 'openCertificateHelp',
]
