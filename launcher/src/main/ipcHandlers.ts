// One handler per channel of the IPC contract. No Electron import: the
// Electron pieces come in through HandlerDeps, so the table is tested with fakes.
//
// Each handler gets the raw arguments as `unknown[]` and checks them with
// ipcValidate.ts before it does anything. index.ts wraps every handler with
// the sender check of ipcGuard.ts before it registers it.
import type { InvokeContract, SendContract } from '../shared/ipc'
import type { FolderKind, FolderPickResult, HardwareSummaryView, LauncherInfo, PortProcessView, UpdaterStatus } from '../shared/types'
import type { LauncherCore } from './core'
import * as v from './ipcValidate'

type Awaitable<T> = T | Promise<T>

export type InvokeHandlers = { [C in keyof InvokeContract]: (...args: unknown[]) => Awaitable<InvokeContract[C]['result']> }
export type SendHandlers = { [C in keyof SendContract]: (...args: unknown[]) => void }

/** The part of LauncherCore the handlers use. */
export type CoreApi = Pick<
  LauncherCore,
  | 'snapshot'
  | 'startServer'
  | 'stopServer'
  | 'restartServer'
  | 'endpoint'
  | 'installState'
  | 'checkServerUpdate'
  | 'installServer'
  | 'cancelInstall'
  | 'rollbackServer'
  | 'getSettings'
  | 'saveSettings'
  | 'queryLogs'
  | 'exportLogs'
  | 'sandboxView'
  | 'sandboxPlan'
  | 'runSandboxSetup'
  | 'runSandboxRemove'
  | 'dismissSandboxNotice'
  | 'statusBarVisible'
  | 'setStatusBarVisible'
  | 'webuiState'
  | 'setShellScreen'
  | 'setWebuiOverlay'
  | 'setShellInsets'
  | 'openWebuiInBrowser'
  | 'connectRemote'
  | 'backToLocal'
  | 'reloadWebui'
  | 'resetWebuiPermissions'
  | 'windowSettings'
  | 'setOpenInBrowser'
  | 'setDesktopNotifications'
  | 'updateInfo'
  | 'setAutoCheck'
  | 'setUpdateChannel'
  | 'backToStable'
  | 'serversView'
  | 'testServer'
  | 'addServer'
  | 'updateServer'
  | 'removeServer'
  | 'setDefaultServer'
  | 'cancelSwitch'
  | 'retryRemote'
  | 'setKeepLocalRunning'
  | 'releaseNotesUrl'
  | 'installDetails'
  | 'networkView'
  | 'networkPlan'
  | 'setNetworkExposed'
  | 'regenerateApiKey'
  | 'dismissKeyNotice'
  | 'revealApiKey'
  | 'apiKeyCopyText'
  | 'envVarsView'
  | 'setEnvVar'
  | 'deleteEnvVar'
  | 'secretStorageView'
>

export interface HandlerDeps {
  core: CoreApi
  info(): LauncherInfo
  listModels(endpoint: string): Promise<string[]>
  /** Opens the chat page of the server in the system browser. */
  openChat(endpoint: string, model?: string): Promise<void>
  writeClipboard(text: string): void
  /** Puts a secret on the clipboard and clears it again after a while if it is still there. */
  writeSecretClipboard(text: string): void
  /** Shows the native save dialog; resolves with the chosen path or undefined. */
  chooseLogExportPath(): Promise<string | undefined>
  getUpdater(): UpdaterStatus
  checkForLauncherUpdate(): Promise<void>
  /** Installs a downloaded launcher update, or downloads an available one. */
  installLauncherUpdate(): Promise<void>
  /** Restarts the launcher. */
  relaunch(): Promise<void>
  /** Opens a URL in the system browser after the scheme check. */
  openExternal(url: string): void
  findFreePort(from: number): Promise<number | null>
  portProcess(port: number): Promise<PortProcessView>
  /** Shows the native folder picker for a data folder, then checks and saves it (core.setDataFolder); the path never comes from the renderer. */
  pickFolder(kind: FolderKind): Promise<FolderPickResult>
  hardwareSummary(): HardwareSummaryView
  firstRender(shownState: string): void
}

export function createIpcHandlers(d: HandlerDeps): { invoke: InvokeHandlers; send: SendHandlers } {
  const c = d.core
  const invoke: InvokeHandlers = {
    'launcher:info': (...a) => (v.noArgs(a), d.info()),
    // server
    'launcher:server:get': (...a) => (v.noArgs(a), c.snapshot()),
    'launcher:server:start': (...a) => (v.noArgs(a), c.startServer()),
    'launcher:server:stop': (...a) => (v.noArgs(a), c.stopServer()),
    'launcher:server:restart': (...a) => (v.noArgs(a), c.restartServer()),
    'launcher:models': (...a) => (v.noArgs(a), d.listModels(c.endpoint)),
    'launcher:open-chat': (...a) => d.openChat(c.endpoint, v.modelId(v.oneOptional(a))),
    'launcher:copy': (...a) => d.writeClipboard(v.clipboardText(v.one(a))),
    // launcher self-update
    'launcher:updater:get': (...a) => (v.noArgs(a), d.getUpdater()),
    'launcher:updater:check': (...a) => (v.noArgs(a), d.checkForLauncherUpdate()),
    // server install
    'launcher:install:get': (...a) => (v.noArgs(a), c.installState()),
    'launcher:install:check': (...a) => c.checkServerUpdate(v.installOptions(v.oneOptional(a))),
    'launcher:install:run': (...a) => c.installServer(v.installOptions(v.oneOptional(a))),
    'launcher:install:cancel': (...a) => (v.noArgs(a), c.cancelInstall()),
    'launcher:install:rollback': (...a) => c.rollbackServer(v.installOptions(v.oneOptional(a))),
    // settings
    'launcher:settings:get': (...a) => (v.noArgs(a), c.getSettings()),
    'launcher:settings:save': (...a) => c.saveSettings(v.settingsPatch(v.one(a))),
    // logs
    'launcher:logs:query': (...a) => c.queryLogs(v.logQuery(v.oneOptional(a))),
    'launcher:logs:export': (...a) => (v.noArgs(a), c.exportLogs(() => d.chooseLogExportPath())),
    // sandbox
    'launcher:sandbox:get': (...a) => (v.noArgs(a), c.sandboxView()),
    'launcher:sandbox:plan': (...a) => c.sandboxPlan(v.planKind(v.oneOptional(a))),
    'launcher:sandbox:setup': (...a) => (v.noArgs(a), c.runSandboxSetup()),
    'launcher:sandbox:remove': (...a) => (v.noArgs(a), c.runSandboxRemove()),
    'launcher:sandbox:dismiss': (...a) => (v.noArgs(a), c.dismissSandboxNotice()),
    // status bar
    'launcher:status-bar:get': (...a) => (v.noArgs(a), c.statusBarVisible()),
    'launcher:status-bar:set': (...a) => c.setStatusBarVisible(v.bool(v.one(a), 'visible')),
    // WebUI view and layout
    'launcher:webui:get': (...a) => (v.noArgs(a), c.webuiState()),
    'launcher:webui:set-screen': (...a) => c.setShellScreen(v.shellScreen(v.one(a))),
    'launcher:webui:set-overlay': (...a) => c.setWebuiOverlay(v.bool(v.one(a), 'open')),
    'launcher:webui:open-in-browser': (...a) => (v.noArgs(a), c.openWebuiInBrowser()),
    'launcher:webui:connect': (...a) => c.connectRemote(v.connectId(v.oneOptional(a))),
    'launcher:webui:back-to-local': (...a) => (v.noArgs(a), c.backToLocal()),
    'launcher:webui:reload': (...a) => (v.noArgs(a), c.reloadWebui()),
    'launcher:webui:reset-permissions': (...a) => (v.noArgs(a), c.resetWebuiPermissions()),
    // window settings
    'launcher:window:get': (...a) => (v.noArgs(a), c.windowSettings()),
    'launcher:window:set-open-in-browser': (...a) => c.setOpenInBrowser(v.bool(v.one(a), 'on')),
    'launcher:window:set-desktop-notifications': (...a) => c.setDesktopNotifications(v.bool(v.one(a), 'on')),
    // updates
    'launcher:updates:get': (...a) => (v.noArgs(a), c.updateInfo()),
    'launcher:updates:set-auto-check': (...a) => c.setAutoCheck(v.bool(v.one(a), 'on')),
    'launcher:updates:set-channel': (...a) => {
      const [channel, confirmed] = v.oneOrTwo(a)
      return c.setUpdateChannel(v.updateChannel(channel), confirmed === undefined ? false : v.bool(confirmed, 'confirmed'))
    },
    'launcher:updates:back-to-stable': (...a) => (v.noArgs(a), c.backToStable()),
    // servers
    'launcher:servers:get': (...a) => (v.noArgs(a), c.serversView()),
    'launcher:servers:test': (...a) => c.testServer(v.testInput(v.one(a))),
    'launcher:servers:add': (...a) => c.addServer(v.addServerInput(v.one(a))),
    'launcher:servers:update': (...a) => c.updateServer(v.updateServerInput(v.one(a))),
    'launcher:servers:remove': (...a) => c.removeServer(v.serverId(v.one(a))),
    'launcher:servers:set-default': (...a) => c.setDefaultServer(v.serverTarget(v.one(a))),
    'launcher:servers:cancel-switch': (...a) => (v.noArgs(a), c.cancelSwitch()),
    'launcher:servers:retry': (...a) => (v.noArgs(a), c.retryRemote()),
    'launcher:servers:set-keep-local': (...a) => c.setKeepLocalRunning(v.bool(v.one(a), 'on')),
    'launcher:updates:release-notes': (...a) => {
      const url = c.releaseNotesUrl(v.text(v.one(a), 80, 'version'), d.getUpdater().version)
      if (!url) throw new v.IpcInputError('version is not on offer')
      d.openExternal(url)
    },
    'launcher:install:details': (...a) => (v.noArgs(a), c.installDetails()),
    'launcher:updater:install': (...a) => (v.noArgs(a), d.installLauncherUpdate()),
    'launcher:relaunch': (...a) => (v.noArgs(a), d.relaunch()),
    // start failure and first run
    'launcher:port:find-free': (...a) => d.findFreePort(v.port(v.one(a), 'from')),
    'launcher:port:process': (...a) => d.portProcess(v.port(v.one(a))),
    'launcher:settings:pick-folder': (...a) => d.pickFolder(v.folderKind(v.one(a))),
    'launcher:hardware': (...a) => (v.noArgs(a), d.hardwareSummary()),
    // network exposure, API key, environment variables
    'launcher:network:get': (...a) => (v.noArgs(a), c.networkView()),
    'launcher:network:plan': (...a) => (v.noArgs(a), c.networkPlan()),
    'launcher:network:set-exposed': (...a) => {
      const [on, plan] = v.oneOrTwo(a)
      return c.setNetworkExposed(v.bool(on, 'on'), v.planId(plan))
    },
    'launcher:api-key:regenerate': (...a) => (v.noArgs(a), c.regenerateApiKey()),
    'launcher:api-key:dismiss-notice': (...a) => (v.noArgs(a), c.dismissKeyNotice()),
    'launcher:api-key:reveal': (...a) => (v.noArgs(a), c.revealApiKey()),
    'launcher:api-key:copy': (...a) => {
      const text = c.apiKeyCopyText(v.copyFormat(v.oneOptional(a)))
      if (text === undefined) return { ok: false as const, reason: 'no-key' as const }
      d.writeSecretClipboard(text)
      return { ok: true as const }
    },
    'launcher:env:get': (...a) => (v.noArgs(a), c.envVarsView()),
    'launcher:env:set': (...a) => {
      const [name, value] = v.two(a)
      return c.setEnvVar(v.envName(name), v.envValue(value))
    },
    'launcher:env:delete': (...a) => c.deleteEnvVar(v.envName(v.one(a))),
    'launcher:secrets:state': (...a) => (v.noArgs(a), c.secretStorageView()),
  }
  const send: SendHandlers = {
    // Only a label for the smoke test log: keep it short and on one line.
    'launcher:first-render': (...a) => d.firstRender(v.text(v.one(a), 64, 'shownState')),
    'launcher:webui:set-insets': (...a) => c.setShellInsets(v.shellInsets(v.one(a))),
  }
  return { invoke, send }
}
