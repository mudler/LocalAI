// The bridge methods in three groups. Every bridge has the core group; a
// bridge that talks to servers has the servers group; only a bridge that runs
// a LocalAI server on this device has the local-server group. A client-only
// bridge leaves the last group out. Types and constants only: no Electron or
// Node import.
import type { Capabilities, LauncherApi, LauncherInfo } from './types'

export const CORE_METHODS = [
  'info', 'copyText', 'firstRender', 'getUpdater', 'checkForLauncherUpdate', 'onUpdater',
  'getStatusBarVisible', 'setStatusBarVisible', 'onStatusBar',
] as const satisfies readonly (keyof LauncherApi)[]

export const SERVERS_METHODS = [
  'getServers', 'onServers', 'onOpenSwitcher', 'testServer', 'addServer', 'addServerByInvite', 'updateServer',
  'removeServer', 'setDefaultServer', 'cancelSwitch', 'retryRemote', 'getWebui', 'onWebui', 'setShellScreen',
  'setOverlay', 'setInsets', 'openWebuiInBrowser', 'connectRemote', 'reloadWebui', 'resetWebuiPermissions',
  'getWindowSettings', 'setOpenInBrowser', 'setDesktopNotifications', 'getUpdateInfo', 'setAutoCheck',
  'setUpdateChannel', 'openReleaseNotes', 'installLauncherUpdate',
] as const satisfies readonly (keyof LauncherApi)[]

/** The local-server methods that every desktop bridge has (required before this change). */
export const LOCAL_REQUIRED_METHODS = [
  'getServer', 'start', 'stop', 'restart', 'onServer', 'listModels', 'openChat', 'getInstallState',
  'checkServerUpdate', 'installServer', 'cancelInstall', 'rollbackServer', 'onInstall', 'getSettings',
  'saveSettings', 'queryLogs', 'exportLogs', 'onLogs', 'getSandboxState', 'getSandboxSetupPlan',
  'runSandboxSetup', 'runSandboxRemove', 'dismissSandboxNotice', 'onSandbox',
] as const satisfies readonly (keyof LauncherApi)[]

/** The local-server methods a desktop bridge may lack (a fake, or an older preload). */
export const LOCAL_OPTIONAL_METHODS = [
  'backToLocal', 'backToStable', 'setKeepLocalRunning', 'getInstallDetails', 'relaunch', 'findFreePort',
  'showPortProcess', 'pickFolder', 'getHardwareSummary', 'getNetwork', 'getNetworkPlan', 'setNetworkExposed',
  'regenerateApiKey', 'dismissKeyNotice', 'revealApiKey', 'copyApiKey', 'getEnvVars', 'setEnvVar',
  'deleteEnvVar', 'getSecretStorageState', 'getRegistryCredentials', 'saveRegistryCredential',
  'removeRegistryCredential', 'testRegistryCredential',
] as const satisfies readonly (keyof LauncherApi)[]

export const LOCAL_SERVER_METHODS = [...LOCAL_REQUIRED_METHODS, ...LOCAL_OPTIONAL_METHODS] as const

export type CoreApi = Pick<LauncherApi, (typeof CORE_METHODS)[number]>
export type ServersApi = Pick<LauncherApi, (typeof SERVERS_METHODS)[number]>
export type LocalServerApi = Pick<LauncherApi, (typeof LOCAL_SERVER_METHODS)[number]>

// Compile-time check: the three groups cover every LauncherApi method. A new
// method that is in no group makes this line fail to compile.
type Ungrouped = Exclude<
  keyof LauncherApi,
  (typeof CORE_METHODS)[number] | (typeof SERVERS_METHODS)[number] | (typeof LOCAL_SERVER_METHODS)[number]
>
const _exhaustive: [Ungrouped] extends [never] ? true : false = true
void _exhaustive

export const DESKTOP_CAPABILITIES: Capabilities = { localServer: true, p2p: true, selfUpdate: 'electron', qrScan: false }

/** A missing value (an older main process, or info not loaded yet) means the desktop set. */
export function capabilitiesOf(info: LauncherInfo | undefined): Capabilities {
  return info?.capabilities ?? DESKTOP_CAPABILITIES
}
