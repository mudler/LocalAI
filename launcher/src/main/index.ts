import { app, BaseWindow, clipboard, dialog, ipcMain, safeStorage, session, shell, WebContentsView, type IpcMainEvent, type IpcMainInvokeEvent } from 'electron'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { pathToFileURL } from 'node:url'
import { CHANNEL_KIND, type EventChannel, type EventContract } from '../shared/ipc'
import type { FolderPickResult } from '../shared/types'
import { isStrippedVar } from './childEnv'
import { LauncherCore } from './core'
import { guard, type FrameInfo, type TrustedShell } from './ipcGuard'
import { createIpcHandlers } from './ipcHandlers'
import { listModels } from './models'
import { applyLoginItem } from './loginItem'
import { probeSandbox } from './sandboxProbe'
import { spawnRunner } from './sandboxSetup'
import { setAppMenu } from './menu'
import { menuBarVisible } from './menuModel'
import { createTray, type LauncherTray } from './tray'
import { decideClose, shouldStartVisible } from './trayPolicy'
import { checkForLauncherUpdate, getUpdaterStatus, initSelfUpdate, installLauncherUpdate, setLauncherPrereleases } from './selfUpdate'
import { GithubReleasesProvider, RedirectFirstProvider } from './updateProvider'
import { platformTarget } from './platform'
import { resolveLayout } from './paths'
import { findFreePort, hardwareSummary, portProcess } from './hostTools'
import { externalUrl, partitionForOrigin } from './webuiPolicy'
import { WebuiHost } from './webuiView'
import { DesktopNotifier } from './notifyGlue'
import { WebuiCredentials } from './webuiCredential'
import { computeLayout, dimWanted, MIN_SIZE, orderChanged, type Layout, type ViewName } from './windowLayout'
import { DimLayer } from './dimView'
import { DEFAULT_WINDOW_HEIGHT, DEFAULT_WINDOW_WIDTH } from '../shared/layout'

// Smoke-test hook: when set, the main process appends one line per milestone.
// Used by the packaged-build check; harmless otherwise.
function signal(line: string) {
  const f = process.env.LOCALAI_LAUNCHER_SIGNAL_FILE
  if (!f) return
  try {
    fs.appendFileSync(f, `${Date.now()} ${line}\n`)
  } catch {
    /* ignore */
  }
}

const RENDERER_PAGE = path.join(__dirname, '..', '..', 'renderer', 'index.html')

// The window is a BaseWindow with up to three WebContentsViews (see
// windowLayout.ts): the shell (the only view with the preload bridge), the
// WebUI view and the corner dot. A BrowserWindow's own page cannot be raised
// above a child view, so the shell is a child view too.
let win: BaseWindow | undefined
let shellView: WebContentsView | undefined
let webui: WebuiHost | undefined
let dimLayer: DimLayer | undefined
let lastLayout: Layout | undefined
let lastLayoutMode: string | undefined
let tray: LauncherTray | undefined
let trayCreated = false
let quitRequested = false
let confirming = false
let core: LauncherCore | undefined
let notifier: DesktopNotifier | undefined
let unreadCount = 0

/** Sends an event to the shell view only. The status bar choice also updates both menus; WebUI state changes the layout. */
function emit<C extends EventChannel>(channel: C, payload: EventContract[C]): void {
  if (channel === 'launcher:status-bar:event') refreshMenus(payload as boolean)
  if (shellView && !shellView.webContents.isDestroyed()) shellView.webContents.send(channel, payload)
  if (channel === 'launcher:webui:event') relayout()
}

/** Opens a URL in the system browser, after the scheme check (http, https, mailto). */
function openExternal(url: string): void {
  const u = externalUrl(url)
  if (!u) return
  signal(`external ${u}`)
  // Test hook for the packaged security test: record the hand-off, start no browser.
  if (process.env.LOCALAI_LAUNCHER_SIGNAL_FILE && process.env.LOCALAI_LAUNCHER_EXTERNAL_DRY === '1') return
  void shell.openExternal(u).catch(() => core?.log('could not open the system browser', 'warn'))
}

/** Places the views from the current WebUI state and window size. */
function relayout(): void {
  if (!win || win.isDestroyed() || !shellView || !core) return
  const state = core.webuiState()
  const [width, height] = win.getContentSize()
  const layout = computeLayout({
    width,
    height,
    mode: state.layout,
    barVisible: state.barVisible,
    shellAbove: state.shellAbove,
    insetTop: state.insets.top,
    insetBottom: state.insets.bottom,
    dim: dimWanted(state),
  })
  shellView.setBounds(layout.shell)
  // The shell page must be see-through where it does not draw while it is above the WebUI.
  shellView.setBackgroundColor(state.shellAbove ? '#00000000' : '#FFFFFFFF')
  const view = webui?.webuiView
  if (view) {
    if (layout.webui) {
      view.setBounds(layout.webui)
      view.setVisible(true)
    } else {
      // Hidden, never reloaded: an open chat or a draft survives a shell screen.
      view.setVisible(false)
    }
  }
  let dot: WebContentsView | undefined
  if (layout.dot && state.dot) dot = webui?.showDot(state.dot, layout.dot)
  else webui?.hideDot()
  let dim: WebContentsView | undefined
  if (layout.dim && view) dim = dimLayer?.show(layout.dim)
  else dimLayer?.hide()
  if (orderChanged(lastLayout, layout) || state.layout !== lastLayoutMode) {
    const views: Record<ViewName, WebContentsView | undefined> = { shell: shellView, webui: view, dim, dot }
    // Adding a child view that is already there moves it to the top.
    for (const name of layout.order) {
      const v = views[name]
      if (v) win.contentView.addChildView(v)
    }
    // Under the dim layer the shell gets the keyboard: its banner holds Start.
    const top = layout.order.filter((n) => n !== 'dot').at(-1)
    const focus = top === 'webui' ? view : shellView
    if (win.isFocused() && focus && !focus.webContents.isDestroyed()) focus.webContents.focus()
  }
  lastLayout = layout
  lastLayoutMode = state.layout
}

function toggleStatusBar(visible: boolean): void {
  // The config store saves and then emits the status bar event, which refreshes the menus.
  void core?.setStatusBarVisible(visible).catch(() => refreshMenus(core?.statusBarVisible() ?? true))
}

/** The menu accelerator works while the WebUI view has the focus: it asks the shell to open the switcher. */
function openSwitcher(): void {
  win?.show()
  emit('launcher:servers:open-switcher', true)
}

function refreshMenus(visible: boolean): void {
  setAppMenu({ platform: process.platform, statusBarVisible: visible, onToggleStatusBar: toggleStatusBar, onSwitchServer: openSwitcher })
  applyMenuBar()
  tray?.setStatusBarVisible(visible)
}

/** No menu bar in the window on Linux and Windows; the menu and its accelerators stay set. Again after each new menu. */
function applyMenuBar(): void {
  if (win && !win.isDestroyed()) win.setMenuBarVisibility(menuBarVisible(process.platform))
}

function trustedShell(): TrustedShell | undefined {
  if (!win || win.isDestroyed() || !shellView || shellView.webContents.isDestroyed()) return undefined
  const wc = shellView.webContents
  return {
    webContentsId: wc.id,
    processId: wc.mainFrame.processId,
    routingId: wc.mainFrame.routingId,
    pageUrl: pathToFileURL(RENDERER_PAGE).href,
  }
}

function frameOf(e: IpcMainInvokeEvent | IpcMainEvent): FrameInfo | null {
  const f = e.senderFrame
  if (!f) return null
  return { webContentsId: e.sender.id, processId: f.processId, routingId: f.routingId, url: f.url, isMainFrame: f.parent === null }
}

/** The launcher was asked to start without its window: `--hidden`, which a login item passes (macOS passes none; it says so instead). */
function startHiddenRequested(): boolean {
  if (process.argv.includes('--hidden')) return true
  return process.platform === 'darwin' && app.getLoginItemSettings().wasOpenedAtLogin
}

/** "Launch at login" for this platform (loginItem.ts). */
function setLoginItem(on: boolean) {
  return applyLoginItem(on, {
    platform: process.platform,
    env: process.env,
    home: os.homedir(),
    setElectronLoginItem: (openAtLogin, args) => {
      app.setLoginItemSettings({ openAtLogin, args })
      return process.platform === 'darwin' ? { status: app.getLoginItemSettings().status } : undefined
    },
  })
}

function busy(): boolean {
  return !!core && (core.serverRunning() || core.installActive())
}

function createWindow() {
  win = new BaseWindow({
    width: DEFAULT_WINDOW_WIDTH,
    height: DEFAULT_WINDOW_HEIGHT,
    show: false,
    title: 'LocalAI Launcher',
  })
  win.setMinimumSize(MIN_SIZE.width, MIN_SIZE.height)
  applyMenuBar()
  shellView = new WebContentsView({
    webPreferences: {
      preload: path.join(__dirname, '..', 'preload', 'index.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  })
  win.contentView.addChildView(shellView)
  const shellWc = shellView.webContents
  shellWc.setWindowOpenHandler(({ url }) => {
    if (/^https?:\/\//.test(url)) openExternal(url)
    return { action: 'deny' }
  })
  shellWc.on('will-navigate', (e) => e.preventDefault())
  shellWc.once('did-finish-load', () => {
    signal('window-ready')
    if (shouldStartVisible(trayCreated, startHiddenRequested())) win?.show()
  })
  webui = new WebuiHost({
    window: () => (win && !win.isDestroyed() ? win : undefined),
    dispatch: (e) => void core?.webuiDispatch(e),
    permissionStore: () => core?.permissionStore() ?? {},
    rememberPermission: (origin, choice) => core?.rememberPermission(origin, choice) ?? Promise.resolve(),
    openExternal,
    showStatusBar: () => toggleStatusBar(true),
    downloadsDir: () => app.getPath('downloads'),
    log: (m, level) => core?.log(m, level),
    signal,
  })
  dimLayer = new DimLayer(() => (win && !win.isDestroyed() ? win : undefined))
  for (const ev of ['resize', 'maximize', 'unmaximize', 'enter-full-screen', 'leave-full-screen'] as const) {
    win.on(ev as 'resize', () => relayout())
  }
  relayout()
  win.on('close', (e) => {
    const action = decideClose({ trayCreated, serverRunning: busy(), quitRequested })
    signal(`close action=${action}`)
    if (action === 'hide') {
      e.preventDefault()
      win?.hide()
    } else if (action === 'confirm-quit') {
      // Never block the main process with a synchronous dialog: signals and
      // server state changes must keep being handled while it is open.
      e.preventDefault()
      if (confirming) return
      confirming = true
      const installing = core?.installActive() ?? false
      void dialog
        .showMessageBox(win!, {
          type: 'question',
          buttons: [installing ? 'Cancel the update and quit' : 'Stop server and quit', 'Cancel'],
          defaultId: 1,
          cancelId: 1,
          message: installing ? 'A LocalAI update is running.' : 'The LocalAI server is running.',
          detail: installing ? 'Quitting cancels it and keeps the installed version.' : 'Quitting stops it.',
        })
        .then((r) => {
          confirming = false
          signal(`confirm answer=${r.response}`)
          if (r.response === 0) void shutdown()
        })
    }
  })
  win.on('closed', () => {
    // A BaseWindow does not destroy its views' web contents.
    webui?.destroy()
    dimLayer?.destroy()
    dimLayer = undefined
    if (shellView && !shellView.webContents.isDestroyed()) shellView.webContents.close()
    webui = undefined
    shellView = undefined
    win = undefined
    lastLayout = undefined
    lastLayoutMode = undefined
  })
  void shellWc.loadFile(RENDERER_PAGE)
}

async function shutdown(code = 0) {
  if (quitRequested) return
  quitRequested = true
  notifier?.stop()
  try {
    await core?.shutdown()
  } finally {
    const n = credentials.pending().length
    await credentials.clearAtQuit().catch(() => undefined)
    if (n) signal(`webui-cookies-cleared=${n}`)
    signal('exit')
    app.exit(code)
  }
}

/** Restarts the launcher with the same arguments. An AppImage restarts from its file, not from its mount. */
function relaunch(): void {
  signal('relaunch')
  // A restart the user asked for shows the window.
  const args = process.argv.slice(1).filter((a) => a !== '--hidden')
  const appImage = process.env.APPIMAGE
  app.relaunch(appImage ? { execPath: appImage, args } : { args })
  void shutdown()
}

/** The native folder picker for a data folder, opened at the current one; the core checks and saves the choice. */
async function pickFolder(c: LauncherCore, kind: 'models' | 'backends'): Promise<FolderPickResult> {
  const current = kind === 'models' ? c.getSettings().modelsPath : c.getSettings().backendsPath
  const opts: Electron.OpenDialogOptions = {
    title: kind === 'models' ? 'Choose the models folder' : 'Choose the backends folder',
    defaultPath: current || undefined,
    properties: ['openDirectory', 'createDirectory'],
  }
  const r = win ? await dialog.showOpenDialog(win, opts) : await dialog.showOpenDialog(opts)
  if (r.canceled || r.filePaths.length === 0) return { status: 'cancelled' }
  return c.setDataFolder(kind, r.filePaths[0])
}

/** A copied secret is cleared from the clipboard after this long, if it is still there. */
const SECRET_CLIPBOARD_MS = 60_000

function writeSecretClipboard(text: string): void {
  clipboard.writeText(text)
  setTimeout(() => {
    // readText is async in this Electron version's types; accept both.
    void Promise.resolve(clipboard.readText())
      .then((now) => {
        if (now === text) clipboard.clear()
      })
      .catch(() => undefined)
  }, SECRET_CLIPBOARD_MS).unref()
}

/**
 * With an API key on, the server's WebUI asks for it. The launcher gives the
 * WebUI view of the local server the key as the server's own sign-in cookie
 * (`token`, HttpOnly, the name the server sets after a key sign-in), in that
 * origin's session only, so the page works as before. No key: the cookie is
 * removed. Without secure storage the partition's cookies are cleared at
 * quit (webuiCredential.ts).
 */
const credentials = new WebuiCredentials({
  session: (origin) => session.fromPartition(partitionForOrigin(origin)),
  secureStorage: () => core?.secretStorageView().encryptionAvailable ?? false,
  log: (m) => core?.log(m),
})
const webuiCredential = (origin: string, key: string | undefined) => credentials.give(origin, key)

async function chooseLogExportPath(): Promise<string | undefined> {
  const stamp = new Date().toISOString().replace(/[:.]/g, '-')
  const opts = {
    title: 'Export logs',
    defaultPath: path.join(app.getPath('downloads'), `localai-launcher-logs-${stamp}.txt`),
    filters: [{ name: 'Text', extensions: ['txt'] }],
  }
  const r = win ? await dialog.showSaveDialog(win, opts) : await dialog.showSaveDialog(opts)
  return r.canceled ? undefined : r.filePath
}

function registerIpc(c: LauncherCore) {
  const { invoke, send } = createIpcHandlers({
    core: c,
    info: () => ({
      version: app.getVersion(),
      packaged: app.isPackaged,
      trayActive: trayCreated,
      sandboxed: !app.commandLine.hasSwitch('no-sandbox'),
      configNotice: c.configNotice,
    }),
    listModels,
    // Deep link: /app or /app?model=<id>, in the WebUI view (or the browser when it is not embedded).
    openChat: async (_endpoint, model) => c.openChat(model),
    writeClipboard: (text) => clipboard.writeText(text),
    writeSecretClipboard,
    chooseLogExportPath,
    getUpdater: getUpdaterStatus,
    checkForLauncherUpdate,
    installLauncherUpdate: () => installLauncherUpdate({ beforeQuit: () => c.shutdown(), relaunch, log: (m, level) => c.log(m, level) }),
    relaunch: async () => relaunch(),
    openExternal,
    findFreePort: (from) => findFreePort(from),
    portProcess: (port) => portProcess(port, process.platform),
    pickFolder: (kind) => pickFolder(c, kind),
    hardwareSummary: () =>
      hardwareSummary({ platform: process.platform, arch: process.arch, cpus: os.cpus(), totalMem: os.totalmem(), exists: (p) => fs.existsSync(p) }),
    firstRender: (shown) => signal(`first-render state=${shown}`),
  })
  const refused = (f: FrameInfo | null | undefined) => {
    signal('ipc-refused')
    c.log(`IPC refused from a frame that is not the launcher window (${f ? (f.isMainFrame ? 'other view' : 'subframe') : 'no frame'})`, 'warn')
  }
  for (const [channel, handler] of Object.entries(invoke) as [string, (...args: unknown[]) => unknown][]) {
    ipcMain.handle(channel, guard(handler, frameOf, trustedShell, refused))
  }
  for (const [channel, handler] of Object.entries(send)) {
    const g = guard(handler, frameOf, trustedShell, refused)
    ipcMain.on(channel, (e, ...args) => {
      try {
        g(e, ...args)
      } catch {
        /* refused or invalid: already logged, a send has no answer */
      }
    })
  }
  // Every invoke and send channel of the contract has a handler (the contract test checks this too).
  for (const [channel, kind] of Object.entries(CHANNEL_KIND)) {
    if (kind !== 'event' && !(channel in invoke) && !(channel in send)) throw new Error(`no IPC handler for ${channel}`)
  }
}

// Windows groups the taskbar button, the tray and notifications by this id.
// It must match the appId of the installer (electron-builder.yml), which puts
// the same id on the Start menu shortcut.
if (process.platform === 'win32') app.setAppUserModelId('io.localai.launcher')

const gotLock = app.requestSingleInstanceLock()
if (!gotLock) {
  app.quit()
} else {
  app.on('second-instance', () => {
    win?.show()
    win?.focus()
  })
  app.whenReady().then(async () => {
    signal('ready')
    const owner = 'mudler'
    const repo = 'LocalAI'
    core = new LauncherCore({
      home: os.homedir(),
      layout: resolveLayout({ platform: process.platform, env: process.env, home: os.homedir(), exists: (p) => fs.existsSync(p) }),
      launcherVersion: app.getVersion(),
      env: process.env,
      target: platformTarget(process.platform, process.arch),
      serverBinOverride: process.env.LOCALAI_LAUNCHER_SERVER_BIN,
      endpointOverride: process.env.LOCALAI_LAUNCHER_ENDPOINT,
      provider: new RedirectFirstProvider({ owner, repo }, new GithubReleasesProvider({ owner, repo }), (m) => core?.log(m, 'warn')),
      fetch,
      probeSandbox: () => probeSandbox(),
      sandboxRunner: spawnRunner,
      sandboxAttachPattern: process.env.LOCALAI_LAUNCHER_SANDBOX_ATTACH,
      emit,
      webuiEffects: (fx) => {
        webui?.apply(fx)
        relayout()
      },
      webuiCurrentUrl: () => webui?.currentUrl(),
      releaseRepo: { owner, repo },
      loginItem: setLoginItem,
      onIncludePrereleases: setLauncherPrereleases,
      safeStorage,
      trace: (m) => signal(`init ${m}`),
      networkInterfaces: () => os.networkInterfaces(),
      webuiCredential,
      notificationTargetsChanged: () => notifier?.sync(),
      remoteFetch: (origin) => {
        const ses = session.fromPartition(partitionForOrigin(origin))
        return ((input: string | URL | Request, init?: RequestInit) => ses.fetch(input as string, init)) as typeof fetch
      },
    })
    await core.init()
    core.syncLoginItem()
    signal(`config=${core.configNotice ? 'recovered' : 'ok'}`)
    signal(`sandbox-state=${core.sandboxView().state}`)
    registerIpc(core)
    setAppMenu({ platform: process.platform, statusBarVisible: core.statusBarVisible(), onToggleStatusBar: toggleStatusBar, onSwitchServer: openSwitcher })
    // The tray policy is unchanged: the tray is optional, and without one the
    // window stays and closing it quits (after the confirm when busy).
    tray = await createTray({
      iconPath: path.join(__dirname, '..', '..', '..', 'build', 'icon.png'),
      statusBarVisible: core.statusBarVisible(),
      onShow: () => {
        win?.show()
        win?.focus()
      },
      onToggleStatusBar: toggleStatusBar,
      onQuit: () => void shutdown(),
    })
    trayCreated = tray !== undefined
    tray?.setUnread(unreadCount)
    notifier = new DesktopNotifier({
      core: () => core,
      windowFocused: () => !!win && !win.isDestroyed() && win.isVisible() && win.isFocused(),
      currentUrl: () => webui?.currentUrl(),
      showWindow: () => {
        win?.show()
        win?.focus()
      },
      onUnread: (n) => {
        unreadCount = n
        tray?.setUnread(n)
      },
      log: (m, level) => core?.log(m, level),
      signal,
    })
    notifier.sync()
    // A hidden start never opens the browser by itself ("open in browser" waits for a user action).
    core.setStartedHidden(!shouldStartVisible(trayCreated, startHiddenRequested()))
    signal(`pid=${process.pid}`)
    signal(`tray=${trayCreated}`)
    signal(`launcher-env-stripped-for-child=${Object.keys(process.env).filter((k) => isStrippedVar(k)).join(',')}`)
    signal(`sandbox=${!app.commandLine.hasSwitch('no-sandbox')}`)
    initSelfUpdate(
      (s) => {
        core?.recordUpdater(s)
        emit('launcher:updater:event', s)
      },
      { includePrereleases: core.channelIsRc() },
    )
    createWindow()
    core.openDefaultRemote()
    void core.autoStart()
    // Packaged builds check by themselves (the server once a day), unless the user turned it off.
    if (app.isPackaged) {
      setTimeout(() => {
        if (!core?.autoCheckOn()) return
        void checkForLauncherUpdate()
        void core.autoCheck()
      }, 3000)
      setInterval(() => void core?.autoCheck(), 60 * 60 * 1000).unref()
    }
  })
  app.on('window-all-closed', () => {
    // With a tray the app lives on; without one a closed window means quit.
    if (!trayCreated) void shutdown()
  })
  app.on('before-quit', (e) => {
    if (!quitRequested) {
      e.preventDefault()
      void shutdown()
    }
  })
  for (const sig of ['SIGTERM', 'SIGINT'] as const) process.on(sig, () => void shutdown())
}
