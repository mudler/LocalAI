// The IPC contract: every channel, its direction and its payload types.
//
// - invoke: the renderer calls, the main process answers (ipcRenderer.invoke / ipcMain.handle)
// - event:  the main process pushes to the shell view (webContents.send / ipcRenderer.on)
// - send:   the renderer notifies, no answer (ipcRenderer.send / ipcMain.on)
//
// The preload script runs sandboxed and cannot require this file, so it
// repeats the channel strings. test/ipcContract.test.ts checks that the
// preload, the main handlers and this file list the same channels with the
// same direction; the TypeScript types below make the payloads agree.
import type {
  CopyKeyFormat,
  CopyKeyResult,
  EnvVarResult,
  EnvVarsView,
  ExportLogsResult,
  FolderKind,
  FolderPickResult,
  HardwareSummaryView,
  InstallDetailsView,
  InstallOptions,
  InstallStateView,
  LauncherInfo,
  LauncherSettings,
  LogLineView,
  LogQueryInput,
  NetworkChangeResult,
  NetworkPlanView,
  NetworkView,
  PortProcessView,
  RevealKeyResult,
  SandboxPlanKind,
  SandboxPlanView,
  SandboxView,
  SaveSettingsResult,
  SecretStorageView,
  ServerSnapshot,
  ShellInsets,
  ShellScreen,
  AddServerInput,
  ServerChangeResult,
  ServersView,
  SetChannelResult,
  SwitchResult,
  TestInput,
  TestOutcome,
  UpdateChannelName,
  UpdateServerInput,
  UpdateInfoView,
  UpdaterStatus,
  WebuiViewState,
  WindowSettingsView,
} from './types'

export const IPC = {
  info: 'launcher:info',
  // server
  getServer: 'launcher:server:get',
  startServer: 'launcher:server:start',
  stopServer: 'launcher:server:stop',
  restartServer: 'launcher:server:restart',
  serverEvent: 'launcher:server:event',
  listModels: 'launcher:models',
  openChat: 'launcher:open-chat',
  copyText: 'launcher:copy',
  // launcher self-update
  getUpdater: 'launcher:updater:get',
  checkUpdate: 'launcher:updater:check',
  updaterEvent: 'launcher:updater:event',
  // server install
  getInstall: 'launcher:install:get',
  checkServerUpdate: 'launcher:install:check',
  installServer: 'launcher:install:run',
  cancelInstall: 'launcher:install:cancel',
  rollbackServer: 'launcher:install:rollback',
  installEvent: 'launcher:install:event',
  // settings
  getSettings: 'launcher:settings:get',
  saveSettings: 'launcher:settings:save',
  // logs
  queryLogs: 'launcher:logs:query',
  exportLogs: 'launcher:logs:export',
  logsEvent: 'launcher:logs:event',
  // sandbox
  getSandbox: 'launcher:sandbox:get',
  getSandboxPlan: 'launcher:sandbox:plan',
  runSandboxSetup: 'launcher:sandbox:setup',
  runSandboxRemove: 'launcher:sandbox:remove',
  dismissSandboxNotice: 'launcher:sandbox:dismiss',
  sandboxEvent: 'launcher:sandbox:event',
  // status bar
  getStatusBar: 'launcher:status-bar:get',
  setStatusBar: 'launcher:status-bar:set',
  statusBarEvent: 'launcher:status-bar:event',
  // WebUI view and window layout
  getWebui: 'launcher:webui:get',
  webuiEvent: 'launcher:webui:event',
  setShellScreen: 'launcher:webui:set-screen',
  setOverlay: 'launcher:webui:set-overlay',
  setInsets: 'launcher:webui:set-insets',
  openWebuiInBrowser: 'launcher:webui:open-in-browser',
  connectRemote: 'launcher:webui:connect',
  backToLocal: 'launcher:webui:back-to-local',
  reloadWebui: 'launcher:webui:reload',
  resetWebuiPermissions: 'launcher:webui:reset-permissions',
  // window settings
  getWindowSettings: 'launcher:window:get',
  setOpenInBrowser: 'launcher:window:set-open-in-browser',
  setDesktopNotifications: 'launcher:window:set-desktop-notifications',
  // updates
  getUpdateInfo: 'launcher:updates:get',
  setAutoCheck: 'launcher:updates:set-auto-check',
  setUpdateChannel: 'launcher:updates:set-channel',
  backToStable: 'launcher:updates:back-to-stable',
  // servers
  getServers: 'launcher:servers:get',
  serversEvent: 'launcher:servers:event',
  openSwitcher: 'launcher:servers:open-switcher',
  testServer: 'launcher:servers:test',
  addServer: 'launcher:servers:add',
  updateServer: 'launcher:servers:update',
  removeServer: 'launcher:servers:remove',
  setDefaultServer: 'launcher:servers:set-default',
  cancelSwitch: 'launcher:servers:cancel-switch',
  retryRemote: 'launcher:servers:retry',
  setKeepLocalRunning: 'launcher:servers:set-keep-local',
  openReleaseNotes: 'launcher:updates:release-notes',
  getInstallDetails: 'launcher:install:details',
  installLauncherUpdate: 'launcher:updater:install',
  relaunch: 'launcher:relaunch',
  // start failure and first run
  findFreePort: 'launcher:port:find-free',
  showPortProcess: 'launcher:port:process',
  pickFolder: 'launcher:settings:pick-folder',
  getHardwareSummary: 'launcher:hardware',
  // network exposure, API key, environment variables, secret storage
  getNetwork: 'launcher:network:get',
  getNetworkPlan: 'launcher:network:plan',
  setNetworkExposed: 'launcher:network:set-exposed',
  regenerateApiKey: 'launcher:api-key:regenerate',
  dismissKeyNotice: 'launcher:api-key:dismiss-notice',
  revealApiKey: 'launcher:api-key:reveal',
  copyApiKey: 'launcher:api-key:copy',
  getEnvVars: 'launcher:env:get',
  setEnvVar: 'launcher:env:set',
  deleteEnvVar: 'launcher:env:delete',
  getSecretStorageState: 'launcher:secrets:state',
  // smoke test hook
  firstRender: 'launcher:first-render',
} as const

export type Channel = (typeof IPC)[keyof typeof IPC]

/** Request and answer of each invoke channel. `args` is what the preload passes. */
export interface InvokeContract {
  'launcher:info': { args: []; result: LauncherInfo }
  'launcher:server:get': { args: []; result: ServerSnapshot }
  'launcher:server:start': { args: []; result: void }
  'launcher:server:stop': { args: []; result: void }
  'launcher:server:restart': { args: []; result: void }
  'launcher:models': { args: []; result: string[] }
  'launcher:open-chat': { args: [model?: string]; result: void }
  'launcher:copy': { args: [text: string]; result: void }
  'launcher:updater:get': { args: []; result: UpdaterStatus }
  'launcher:updater:check': { args: []; result: void }
  'launcher:install:get': { args: []; result: InstallStateView }
  'launcher:install:check': { args: [options?: InstallOptions]; result: InstallStateView }
  'launcher:install:run': { args: [options?: InstallOptions]; result: InstallStateView }
  'launcher:install:cancel': { args: []; result: boolean }
  'launcher:install:rollback': { args: [options?: InstallOptions]; result: InstallStateView }
  'launcher:settings:get': { args: []; result: LauncherSettings }
  'launcher:settings:save': { args: [patch: Partial<LauncherSettings>]; result: SaveSettingsResult }
  'launcher:logs:query': { args: [query?: LogQueryInput]; result: LogLineView[] }
  'launcher:logs:export': { args: []; result: ExportLogsResult }
  'launcher:sandbox:get': { args: []; result: SandboxView }
  'launcher:sandbox:plan': { args: [kind?: SandboxPlanKind]; result: SandboxPlanView }
  'launcher:sandbox:setup': { args: []; result: SandboxView }
  'launcher:sandbox:remove': { args: []; result: SandboxView }
  'launcher:sandbox:dismiss': { args: []; result: SandboxView }
  'launcher:status-bar:get': { args: []; result: boolean }
  'launcher:status-bar:set': { args: [visible: boolean]; result: boolean }
  'launcher:webui:get': { args: []; result: WebuiViewState }
  'launcher:webui:set-screen': { args: [screen: ShellScreen | null]; result: WebuiViewState }
  'launcher:webui:set-overlay': { args: [open: boolean]; result: WebuiViewState }
  'launcher:webui:open-in-browser': { args: []; result: void }
  'launcher:webui:connect': { args: [id?: string]; result: SwitchResult }
  'launcher:webui:back-to-local': { args: []; result: WebuiViewState }
  'launcher:webui:reload': { args: []; result: WebuiViewState }
  'launcher:webui:reset-permissions': { args: []; result: void }
  'launcher:window:get': { args: []; result: WindowSettingsView }
  'launcher:window:set-open-in-browser': { args: [on: boolean]; result: WindowSettingsView }
  'launcher:window:set-desktop-notifications': { args: [on: boolean]; result: WindowSettingsView }
  'launcher:updates:get': { args: []; result: UpdateInfoView }
  'launcher:updates:set-auto-check': { args: [on: boolean]; result: UpdateInfoView }
  'launcher:updates:set-channel': { args: [channel: UpdateChannelName, confirmed?: boolean]; result: SetChannelResult }
  'launcher:updates:back-to-stable': { args: []; result: UpdateInfoView }
  'launcher:servers:get': { args: []; result: ServersView }
  'launcher:servers:test': { args: [input: TestInput]; result: TestOutcome }
  'launcher:servers:add': { args: [input: AddServerInput]; result: ServerChangeResult }
  'launcher:servers:update': { args: [input: UpdateServerInput]; result: ServerChangeResult }
  'launcher:servers:remove': { args: [id: string]; result: ServerChangeResult }
  'launcher:servers:set-default': { args: [target: string]; result: SwitchResult }
  'launcher:servers:cancel-switch': { args: []; result: void }
  'launcher:servers:retry': { args: []; result: void }
  'launcher:servers:set-keep-local': { args: [on: boolean]; result: ServersView }
  'launcher:updates:release-notes': { args: [version: string]; result: void }
  'launcher:install:details': { args: []; result: InstallDetailsView }
  'launcher:updater:install': { args: []; result: void }
  'launcher:relaunch': { args: []; result: void }
  'launcher:port:find-free': { args: [from: number]; result: number | null }
  'launcher:port:process': { args: [port: number]; result: PortProcessView }
  'launcher:settings:pick-folder': { args: [kind: FolderKind]; result: FolderPickResult }
  'launcher:hardware': { args: []; result: HardwareSummaryView }
  'launcher:network:get': { args: []; result: NetworkView }
  'launcher:network:plan': { args: []; result: NetworkPlanView }
  'launcher:network:set-exposed': { args: [on: boolean, planId?: string]; result: NetworkChangeResult }
  'launcher:api-key:regenerate': { args: []; result: NetworkView }
  'launcher:api-key:dismiss-notice': { args: []; result: NetworkView }
  'launcher:api-key:reveal': { args: []; result: RevealKeyResult }
  'launcher:api-key:copy': { args: [format?: CopyKeyFormat]; result: CopyKeyResult }
  'launcher:env:get': { args: []; result: EnvVarsView }
  'launcher:env:set': { args: [name: string, value: string]; result: EnvVarResult }
  'launcher:env:delete': { args: [name: string]; result: EnvVarsView }
  'launcher:secrets:state': { args: []; result: SecretStorageView }
}

/** Payload of each event channel (main to renderer). */
export interface EventContract {
  'launcher:server:event': ServerSnapshot
  'launcher:updater:event': UpdaterStatus
  'launcher:install:event': InstallStateView
  'launcher:logs:event': LogLineView[]
  'launcher:sandbox:event': SandboxView
  'launcher:status-bar:event': boolean
  'launcher:webui:event': WebuiViewState
  'launcher:servers:event': ServersView
  /** The menu accelerator for the server switcher was pressed. */
  'launcher:servers:open-switcher': true
}

/** Arguments of each send channel (renderer to main, no answer). */
export interface SendContract {
  'launcher:first-render': [shownState: string]
  'launcher:webui:set-insets': [insets: ShellInsets]
}

export type InvokeChannel = keyof InvokeContract
export type EventChannel = keyof EventContract
export type SendChannel = keyof SendContract
export type ChannelKind = 'invoke' | 'event' | 'send'

/** The direction of every channel. Typed so a channel cannot be missing or listed twice. */
export const CHANNEL_KIND: { [C in Channel]: C extends InvokeChannel ? 'invoke' : C extends EventChannel ? 'event' : 'send' } = {
  'launcher:info': 'invoke',
  'launcher:server:get': 'invoke',
  'launcher:server:start': 'invoke',
  'launcher:server:stop': 'invoke',
  'launcher:server:restart': 'invoke',
  'launcher:server:event': 'event',
  'launcher:models': 'invoke',
  'launcher:open-chat': 'invoke',
  'launcher:copy': 'invoke',
  'launcher:updater:get': 'invoke',
  'launcher:updater:check': 'invoke',
  'launcher:updater:event': 'event',
  'launcher:install:get': 'invoke',
  'launcher:install:check': 'invoke',
  'launcher:install:run': 'invoke',
  'launcher:install:cancel': 'invoke',
  'launcher:install:rollback': 'invoke',
  'launcher:install:event': 'event',
  'launcher:settings:get': 'invoke',
  'launcher:settings:save': 'invoke',
  'launcher:logs:query': 'invoke',
  'launcher:logs:export': 'invoke',
  'launcher:logs:event': 'event',
  'launcher:sandbox:get': 'invoke',
  'launcher:sandbox:plan': 'invoke',
  'launcher:sandbox:setup': 'invoke',
  'launcher:sandbox:remove': 'invoke',
  'launcher:sandbox:dismiss': 'invoke',
  'launcher:sandbox:event': 'event',
  'launcher:status-bar:get': 'invoke',
  'launcher:status-bar:set': 'invoke',
  'launcher:status-bar:event': 'event',
  'launcher:webui:get': 'invoke',
  'launcher:webui:event': 'event',
  'launcher:webui:set-screen': 'invoke',
  'launcher:webui:set-overlay': 'invoke',
  'launcher:webui:set-insets': 'send',
  'launcher:webui:open-in-browser': 'invoke',
  'launcher:webui:connect': 'invoke',
  'launcher:webui:back-to-local': 'invoke',
  'launcher:webui:reload': 'invoke',
  'launcher:webui:reset-permissions': 'invoke',
  'launcher:window:get': 'invoke',
  'launcher:window:set-open-in-browser': 'invoke',
  'launcher:window:set-desktop-notifications': 'invoke',
  'launcher:updates:get': 'invoke',
  'launcher:updates:set-auto-check': 'invoke',
  'launcher:updates:set-channel': 'invoke',
  'launcher:updates:back-to-stable': 'invoke',
  'launcher:servers:get': 'invoke',
  'launcher:servers:event': 'event',
  'launcher:servers:open-switcher': 'event',
  'launcher:servers:test': 'invoke',
  'launcher:servers:add': 'invoke',
  'launcher:servers:update': 'invoke',
  'launcher:servers:remove': 'invoke',
  'launcher:servers:set-default': 'invoke',
  'launcher:servers:cancel-switch': 'invoke',
  'launcher:servers:retry': 'invoke',
  'launcher:servers:set-keep-local': 'invoke',
  'launcher:updates:release-notes': 'invoke',
  'launcher:install:details': 'invoke',
  'launcher:updater:install': 'invoke',
  'launcher:relaunch': 'invoke',
  'launcher:port:find-free': 'invoke',
  'launcher:port:process': 'invoke',
  'launcher:settings:pick-folder': 'invoke',
  'launcher:hardware': 'invoke',
  'launcher:network:get': 'invoke',
  'launcher:network:plan': 'invoke',
  'launcher:network:set-exposed': 'invoke',
  'launcher:api-key:regenerate': 'invoke',
  'launcher:api-key:dismiss-notice': 'invoke',
  'launcher:api-key:reveal': 'invoke',
  'launcher:api-key:copy': 'invoke',
  'launcher:env:get': 'invoke',
  'launcher:env:set': 'invoke',
  'launcher:env:delete': 'invoke',
  'launcher:secrets:state': 'invoke',
  'launcher:first-render': 'send',
}

// Compile-time checks: every channel has a contract entry, and every contract
// entry is a channel of IPC.
type Missing = Exclude<Channel, InvokeChannel | EventChannel | SendChannel>
type Extra = Exclude<InvokeChannel | EventChannel | SendChannel, Channel>
export const CONTRACT_COMPLETE: [Missing, Extra] extends [never, never] ? true : never = true
