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

/** Channels the other lanes add. All optional; see the comment at the top. */
export interface BridgeExtras {
  // distributed worker
  getWorker(): Promise<WorkerView>
  onWorker(cb: (w: WorkerView) => void): () => void
  setRunMode(mode: 'local' | 'worker'): Promise<WorkerView>
  testWorker(config: WorkerConfig): Promise<WorkerView>
  startWorker(config: WorkerConfig): Promise<WorkerView>
  stopWorker(): Promise<WorkerView>
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
  'getWorker', 'onWorker', 'setRunMode', 'testWorker', 'startWorker', 'stopWorker',
]
