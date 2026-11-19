import { contextBridge, ipcRenderer } from 'electron'
import type { EventContract, InvokeContract, SendContract } from '../shared/ipc'
import type { LauncherApi } from '../shared/types'

// Sandboxed preload: it may only require 'electron', so the channel names are
// written out here. test/ipcContract.test.ts compares them with src/shared/ipc.ts.
// The helpers tie each literal channel to its payload types in the contract.

const invoke = <C extends keyof InvokeContract>(channel: C, ...args: InvokeContract[C]['args']) =>
  ipcRenderer.invoke(channel, ...args) as Promise<InvokeContract[C]['result']>

const listen = <C extends keyof EventContract>(channel: C, cb: (payload: EventContract[C]) => void): (() => void) => {
  const h = (_e: unknown, payload: EventContract[C]) => cb(payload)
  ipcRenderer.on(channel, h)
  return () => {
    ipcRenderer.removeListener(channel, h)
  }
}

const send = <C extends keyof SendContract>(channel: C, ...args: SendContract[C]) => ipcRenderer.send(channel, ...args)

const api: LauncherApi = {
  info: () => invoke('launcher:info'),
  // server
  getServer: () => invoke('launcher:server:get'),
  start: () => invoke('launcher:server:start'),
  stop: () => invoke('launcher:server:stop'),
  restart: () => invoke('launcher:server:restart'),
  onServer: (cb) => listen('launcher:server:event', cb),
  listModels: () => invoke('launcher:models'),
  openChat: (model) => invoke('launcher:open-chat', model),
  copyText: (text) => invoke('launcher:copy', text),
  // launcher self-update
  getUpdater: () => invoke('launcher:updater:get'),
  checkForLauncherUpdate: () => invoke('launcher:updater:check'),
  onUpdater: (cb) => listen('launcher:updater:event', cb),
  // server install
  getInstallState: () => invoke('launcher:install:get'),
  checkServerUpdate: (options) => invoke('launcher:install:check', options),
  installServer: (options) => invoke('launcher:install:run', options),
  cancelInstall: () => invoke('launcher:install:cancel'),
  rollbackServer: (options) => invoke('launcher:install:rollback', options),
  onInstall: (cb) => listen('launcher:install:event', cb),
  // settings
  getSettings: () => invoke('launcher:settings:get'),
  saveSettings: (patch) => invoke('launcher:settings:save', patch),
  // logs
  queryLogs: (query) => invoke('launcher:logs:query', query),
  exportLogs: () => invoke('launcher:logs:export'),
  onLogs: (cb) => listen('launcher:logs:event', cb),
  // sandbox
  getSandboxState: () => invoke('launcher:sandbox:get'),
  getSandboxSetupPlan: (kind) => invoke('launcher:sandbox:plan', kind),
  runSandboxSetup: () => invoke('launcher:sandbox:setup'),
  runSandboxRemove: () => invoke('launcher:sandbox:remove'),
  dismissSandboxNotice: () => invoke('launcher:sandbox:dismiss'),
  onSandbox: (cb) => listen('launcher:sandbox:event', cb),
  // status bar
  getStatusBarVisible: () => invoke('launcher:status-bar:get'),
  setStatusBarVisible: (visible) => invoke('launcher:status-bar:set', visible),
  onStatusBar: (cb) => listen('launcher:status-bar:event', cb),
  // WebUI view and layout
  getWebui: () => invoke('launcher:webui:get'),
  onWebui: (cb) => listen('launcher:webui:event', cb),
  setShellScreen: (screen) => invoke('launcher:webui:set-screen', screen),
  setOverlay: (open) => invoke('launcher:webui:set-overlay', open),
  setInsets: (insets) => send('launcher:webui:set-insets', insets),
  openWebuiInBrowser: () => invoke('launcher:webui:open-in-browser'),
  connectRemote: (id) => invoke('launcher:webui:connect', id),
  backToLocal: () => invoke('launcher:webui:back-to-local'),
  reloadWebui: () => invoke('launcher:webui:reload'),
  resetWebuiPermissions: () => invoke('launcher:webui:reset-permissions'),
  // window settings
  getWindowSettings: () => invoke('launcher:window:get'),
  setOpenInBrowser: (on) => invoke('launcher:window:set-open-in-browser', on),
  setDesktopNotifications: (on) => invoke('launcher:window:set-desktop-notifications', on),
  // updates
  getUpdateInfo: () => invoke('launcher:updates:get'),
  setAutoCheck: (on) => invoke('launcher:updates:set-auto-check', on),
  setUpdateChannel: (channel, confirmed) => invoke('launcher:updates:set-channel', channel, ...(confirmed === undefined ? [] : [confirmed])),
  backToStable: () => invoke('launcher:updates:back-to-stable'),
  // servers
  getServers: () => invoke('launcher:servers:get'),
  onServers: (cb) => listen('launcher:servers:event', cb),
  onOpenSwitcher: (cb) => listen('launcher:servers:open-switcher', () => cb()),
  testServer: (input) => invoke('launcher:servers:test', input),
  addServer: (input) => invoke('launcher:servers:add', input),
  updateServer: (input) => invoke('launcher:servers:update', input),
  removeServer: (id) => invoke('launcher:servers:remove', id),
  setDefaultServer: (target) => invoke('launcher:servers:set-default', target),
  cancelSwitch: () => invoke('launcher:servers:cancel-switch'),
  retryRemote: () => invoke('launcher:servers:retry'),
  setKeepLocalRunning: (on) => invoke('launcher:servers:set-keep-local', on),
  openReleaseNotes: (version) => invoke('launcher:updates:release-notes', version),
  getInstallDetails: () => invoke('launcher:install:details'),
  installLauncherUpdate: () => invoke('launcher:updater:install'),
  relaunch: () => invoke('launcher:relaunch'),
  // start failure and first run
  findFreePort: (from) => invoke('launcher:port:find-free', from),
  showPortProcess: (port) => invoke('launcher:port:process', port),
  pickFolder: (kind) => invoke('launcher:settings:pick-folder', kind),
  getHardwareSummary: () => invoke('launcher:hardware'),
  // network exposure, API key, environment variables
  getNetwork: () => invoke('launcher:network:get'),
  getNetworkPlan: () => invoke('launcher:network:plan'),
  setNetworkExposed: (on, planId) => invoke('launcher:network:set-exposed', on, planId),
  regenerateApiKey: () => invoke('launcher:api-key:regenerate'),
  dismissKeyNotice: () => invoke('launcher:api-key:dismiss-notice'),
  revealApiKey: () => invoke('launcher:api-key:reveal'),
  copyApiKey: (format) => invoke('launcher:api-key:copy', format),
  getEnvVars: () => invoke('launcher:env:get'),
  setEnvVar: (name, value) => invoke('launcher:env:set', name, value),
  deleteEnvVar: (name) => invoke('launcher:env:delete', name),
  getSecretStorageState: () => invoke('launcher:secrets:state'),
  firstRender: (shownState) => send('launcher:first-render', shownState),
}

contextBridge.exposeInMainWorld('launcher', api)
