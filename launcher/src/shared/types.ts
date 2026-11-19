// Types shared by the main process, the preload script and the renderer.
// Types only: nothing here may import Electron or Node modules.

export type ServerState = 'not-installed' | 'stopped' | 'starting' | 'ready' | 'failed' | 'stopping'

export interface ServerSnapshot {
  state: ServerState
  /** Base URL of the API, for example http://127.0.0.1:8080 */
  endpoint: string
  /** Why the server is in the failed state, or a note for the user. */
  detail?: string
  /** Last lines of the server log, newest last. */
  logTail: string[]
}

export type UpdaterPhase =
  | 'unsupported'
  | 'idle'
  | 'checking'
  | 'available'
  | 'not-available'
  | 'downloading'
  | 'downloaded'
  | 'error'

export interface UpdaterStatus {
  phase: UpdaterPhase
  version?: string
  percent?: number
  message?: string
}

export interface LauncherInfo {
  version: string
  packaged: boolean
  trayActive: boolean
  /** False when Chromium runs with --no-sandbox (the AppImage start script adds it when user namespaces are blocked). */
  sandboxed: boolean
  /** Set when launcher.json could not be used at start and defaults were loaded. Never holds file content. */
  configNotice?: { status: 'recovered'; reason: string }
}

// ---- server install -------------------------------------------------------

/** Mirrors FailReason in main/serverInstall.ts. A UI maps each to a message and a fix action. */
export type InstallFailReason =
  | 'network'
  | 'no-checksum'
  | 'checksum-mismatch'
  | 'disk-full'
  | 'permission'
  | 'supervisor-busy'
  | 'start-failed'
  | 'cancelled'
  | 'unknown'

/** Mirrors InstallState in main/serverInstall.ts. */
export type InstallStateView =
  | { kind: 'idle' }
  | { kind: 'checking' }
  | { kind: 'up-to-date'; version: string }
  | { kind: 'update-available'; version: string; tag: string; installed?: string }
  | { kind: 'downloading'; done: number; total: number }
  | { kind: 'verifying' }
  | { kind: 'installing' }
  | { kind: 'rolling-back' }
  | { kind: 'done'; version: string; adopted: boolean }
  | { kind: 'failed'; reason: InstallFailReason; message: string; rolledBack: boolean }

export interface InstallOptions {
  /** The user opted into pre-releases for this run. */
  allowPrerelease?: boolean
  /** Stop a running server first and start it again on the new binary. */
  stopFirst?: boolean
}

// ---- logs -----------------------------------------------------------------

export type LogSourceName = 'server' | 'launcher' | 'updater'
export type LogLevelName = 'debug' | 'info' | 'warn' | 'error'

export interface LogLineView {
  seq: number
  /** Milliseconds since the epoch. */
  ts: number
  source: LogSourceName
  level: LogLevelName
  text: string
}

export interface LogQueryInput {
  source?: LogSourceName
  minLevel?: LogLevelName
  afterSeq?: number
  /** 1 to 5000. */
  limit?: number
}

export type ExportLogsResult = { status: 'saved'; fileName: string; bytes: number } | { status: 'cancelled' }

// ---- settings -------------------------------------------------------------

/** The server log levels `local-ai run --log-level` accepts. */
export type ServerLogLevel = 'error' | 'warn' | 'info' | 'debug' | 'trace'

/** The typed view of launcher.json that the renderer reads and writes. */
export interface LauncherSettings {
  modelsPath: string
  backendsPath: string
  /** host:port the server listens on. */
  address: string
  autoStartServer: boolean
  startOnBoot: boolean
  logLevel: ServerLogLevel
  showWelcome: boolean
  showStatusBar: boolean
  /** Open the WebUI in the system browser instead of the window. */
  openWebuiInBrowser: boolean
  /** The old "last server chosen in Connect" ('' for none). Read only: servers live in `ServersView` now. */
  remoteServerUrl: string
  /**
   * Without secure storage, keep secrets in a plain file that only this user
   * can read (mode 0600) instead of only for this session. Off by default.
   * Environment variables are not in this object: their values never leave
   * the main process (see EnvVarsView).
   */
  secretsPlaintextFile: boolean
}

export interface SaveSettingsResult {
  settings: LauncherSettings
  /** The server is running with the old values; a restart applies the new ones. */
  restartRequired: boolean
  /** Set when the patch turned "Launch at login" on or off: what happened. `settings.startOnBoot` holds what took effect. */
  loginItem?: LoginItemView
}

/** The outcome of turning "Launch at login" on or off. */
export type LoginItemView =
  | { status: 'ok' }
  | { status: 'unavailable'; reason: 'not-appimage' | 'platform' }
  | { status: 'failed'; reason: string }

// ---- secrets, network exposure, environment variables -----------------------

/**
 * Where secrets go now. `keychain`: the OS secure storage. `plaintext-file`:
 * a plain file with mode 0600, by the user's choice. `session`: only in
 * memory, lost when the launcher quits.
 */
export type SecretBackend = 'keychain' | 'plaintext-file' | 'session'

export interface SecretStorageView {
  backend: SecretBackend
  /** The OS secure storage works (on Linux: a keyring, not the built-in fallback password). */
  encryptionAvailable: boolean
  /** The user chose the plain file for a system with no secure storage. */
  plaintextOptIn: boolean
  /** Stored values that cannot be read now (a locked keyring); kept unchanged on disk. */
  unreadable: number
}

/** Network exposure and the API key. The key itself is never in this object. */
export interface NetworkView {
  /** The server listens on more than this computer. */
  exposed: boolean
  /** host:port the server listens on (the saved setting). */
  address: string
  port: number
  /** How other devices reach the server: one origin per non-internal IPv4 address of this computer. Empty when not exposed. */
  lanAddresses: string[]
  /** An API key is stored and given to the server. */
  apiKeySet: boolean
  /** The start and the last 4 characters of the key ("lai-…3f9a"), for the masked display. */
  apiKeyHint?: { prefix: string; last4: string }
  /** The running server uses older values; a restart applies the new ones. */
  restartRequired: boolean
  storage: SecretStorageView
  /** Set until dismissed: the key was created at this start because the saved settings exposed the server without one. */
  keyCreatedAtStart?: boolean
}

/** What "Allow other devices" will do, shown before it is applied. */
export interface NetworkPlanView {
  /** Pass back to setNetworkExposed(true, planId) to apply this plan. Valid for a few minutes, once. */
  planId: string
  /** What the server will listen on, for example 0.0.0.0:8080. */
  bindAddress: string
  /** The addresses other devices will use. */
  lanAddresses: string[]
  /** No API key exists yet: one is created in the same step. */
  createsKey: boolean
  /** The server is running and restarts to apply the change. */
  restartRequired: boolean
  storage: SecretStorageView
}

export type NetworkChangeResult =
  | { ok: true; network: NetworkView; createdKey: boolean }
  /** Exposing needs a plan from getNetworkPlan first; this one is missing, used or expired. */
  | { ok: false; reason: 'plan-required' }

export type RevealKeyResult = { ok: true; key: string } | { ok: false; reason: 'no-key' | 'rate-limited' }

/** What copyApiKey puts on the clipboard: the key, or a ready-made example with the key in it. */
export type CopyKeyFormat = 'key' | 'openai' | 'anthropic' | 'curl'

export type CopyKeyResult = { ok: true } | { ok: false; reason: 'no-key' }

/** One environment variable of the server. Its value never leaves the main process. */
export interface EnvVarView {
  name: string
  /** The launcher strips or sets this name itself, so the server does not get this value. */
  ignored: boolean
  /** The value is kept only for this session. */
  sessionOnly: boolean
}

export interface EnvVarsView {
  vars: EnvVarView[]
  restartRequired: boolean
  storage: SecretStorageView
}

export type EnvVarFailReason = 'name-format' | 'name-length' | 'name-controlled' | 'value-length' | 'too-many'

export type EnvVarResult = { ok: true; view: EnvVarsView } | { ok: false; reason: EnvVarFailReason }

// ---- sandbox --------------------------------------------------------------

/** Mirrors SandboxState in main/sandboxProbe.ts. */
export type SandboxStateName = 'sandboxed' | 'unsandboxed-restricted' | 'unsandboxed-other' | 'not-applicable'

/** Mirrors NoticeState in main/sandboxNotice.ts. */
export type SandboxNoticeView =
  | { kind: 'hidden' }
  | { kind: 'shown' }
  | { kind: 'dismissed' }
  | { kind: 'installing' }
  | { kind: 'removing' }
  | { kind: 'installed-needs-restart' }
  | { kind: 'failed'; reason: string }
  | { kind: 'removed' }

export interface SandboxView {
  state: SandboxStateName
  notice: SandboxNoticeView
  /** Whether the launcher's AppArmor profile is loaded; usually `unknown` (the list is root-only). */
  profile: 'loaded' | 'not-loaded' | 'unknown'
  /** False until the profile attach rule is decided; then setup is refused and the notice links the docs. */
  setupAvailable: boolean
}

export type SandboxPlanKind = 'setup' | 'remove'

export interface SandboxPlanView {
  kind: SandboxPlanKind
  /** False when there is no plan to show (setup before the attach rule is decided). */
  available: boolean
  profilePath: string
  /** The exact profile file text, for setup. */
  profileText?: string
  /** Each step as shown to the user: what it does and the command line. */
  steps: { description: string; command: string }[]
  /** The same plan as commands to paste into a terminal. */
  terminalText: string
}

// ---- updates, install details, window settings -----------------------------

export interface UpdateInfoView {
  /** Milliseconds since the epoch of the last server update check. */
  checkedAt?: number
  /** Check for LocalAI updates by itself, once a day (config `auto_check_updates`). */
  autoCheck: boolean
  /** The version kept as the rollback copy. For a development build: its build id. */
  previousVersion?: string
  /** The update channel of this computer (config `update_channel`; the old `include_prereleases: true` reads as `rc`). */
  channel: UpdateChannelName
  /** The user has confirmed the Development channel before. */
  devConfirmed: boolean
  /** The installed server is a development build: its build id (the short commit sha). */
  devBuild?: string
  /** The default server is a remote: the channel applies to This computer only and nothing is checked. */
  remoteDefault: boolean
}

export type UpdateChannelName = 'stable' | 'rc' | 'dev'

export type SetChannelResult =
  | { ok: true; info: UpdateInfoView }
  /** Development needs the confirmation first (`confirmed: true`). */
  | { ok: false; reason: 'confirm-required' }

export interface InstallDetailsView {
  /** "linux · amd64" */
  build?: string
  /** Size of the installed server binary, in bytes. */
  size?: number
  /** Where the server binary goes, as a display string ("~/.localai/bin"). */
  installDir?: string
  /** Version of the installed server binary, when one is installed and its version is known. */
  version?: string
}

export interface WindowSettingsView {
  /** Open the WebUI in the system browser instead of this window. */
  openInBrowser: boolean
  /** Show a desktop notification for each new item of a server's notification inbox. */
  desktopNotifications: boolean
  /** The remembered microphone and camera choice for the local server, if any. */
  media?: 'allow' | 'deny'
  /** Some server has a remembered choice; reset forgets all of them. */
  anyRemembered: boolean
}

// ---- start failure and first run helpers -----------------------------------

/** What listens on a port, best effort. Only names and pids, never raw tool output. */
export type PortProcessView =
  | { status: 'found'; processes: { name: string; pid: number }[] }
  /** Nothing listens on it now. */
  | { status: 'none' }
  /** Something listens, owned by another user, so its name is not visible. */
  | { status: 'hidden' }
  /** No tool to ask on this platform, or it failed. */
  | { status: 'unavailable' }

export interface HardwareSummaryView {
  cpuModel?: string
  cpuCount: number
  memoryBytes: number
  /** A hint from one cheap check; undefined when nothing was found. */
  gpu?: 'nvidia' | 'amd' | 'apple'
}

export type FolderKind = 'models' | 'backends'

/** Why the main process did not save a picked data folder. */
export type FolderRefusal = 'not-absolute' | 'root' | 'home' | 'not-a-folder' | 'not-creatable'

/** The outcome of "Change…" for a data folder: the main process picks, checks and saves it. */
export type FolderPickResult =
  | { status: 'cancelled' }
  | { status: 'saved'; settings: LauncherSettings; restartRequired: boolean }
  | { status: 'refused'; reason: FolderRefusal }

// ---- WebUI view and window layout -----------------------------------------

/** `shell-only`: the shell fills the window. `webui`: the WebUI view above the status bar. */
export type LayoutMode = 'shell-only' | 'webui'

/** Colour of the corner dot while the status bar is hidden. */
export type DotTone = 'ok' | 'busy' | 'failed' | 'stopped'

/** Shell screens that cover the WebUI view (which stays alive underneath). */
export type ShellScreen = 'logs' | 'settings' | 'connect'

/**
 * Where the WebUI is shown. `browser`: the user chose the system browser
 * (setting `openWebuiInBrowser`). `browser-fallback`: the embedded view
 * crashed twice in this session, so the launcher opened the browser instead.
 */
export type WebuiPlacement = 'embedded' | 'browser' | 'browser-fallback'

export type WebuiTarget = { kind: 'local'; origin: string } | { kind: 'remote'; origin: string; host: string }

export type WebuiProblem =
  | { kind: 'crashed'; reason: string; count: number }
  | { kind: 'unresponsive' }
  | { kind: 'load-failed'; code: number; description: string }

/**
 * Heights in CSS pixels of the strips the shell draws next to the WebUI view,
 * not counting the status bar: `top` above the view (the update banner),
 * `bottom` between the view and the bar (the "stopped" banner). The main
 * process shrinks the view by these amounts, so the strips never cover it.
 */
export interface ShellInsets {
  top: number
  bottom: number
}

/** The largest inset the main process accepts, in CSS pixels. */
export const MAX_SHELL_INSET = 400

/** The state of the WebUI view and the window layout, for the shell. */
export interface WebuiViewState {
  placement: WebuiPlacement
  /** The server the view shows (or would show). A remote target is shown as "Remote: host". */
  target: WebuiTarget
  /** What the window shows now. In `webui` the shell draws only the bar and its strips (and popovers while `shellAbove`). */
  layout: LayoutMode
  /** What the window shows when no shell screen is open. The shell uses it to tell "a screen covers the WebUI" from "no WebUI yet". */
  home: LayoutMode
  /** The shell screen open over the WebUI, as last set by the shell. */
  screen: ShellScreen | null
  /** The status bar is shown: the saved choice, or forced by a state that needs the user. */
  barVisible: boolean
  /** True when `barVisible` is true only because of such a state. */
  barForced: boolean
  /**
   * The shell view is above the WebUI view and covers the window. It must
   * keep a transparent page background outside what it draws, and it gets
   * every click. Set only while a popover of the bar is open (`overlay`).
   */
  shellAbove: boolean
  /** A popover of the bar is open (set by the shell). */
  overlay: boolean
  /** The corner dot while the bar is hidden; null otherwise. Drawn by the main process, not the shell. */
  dot: DotTone | null
  /**
   * The local server is not ready while the page is kept: the shell shows
   * this banner between the view and the bar. A failed server shows the
   * start-failed screen instead (`home` is `shell-only`), with the page kept.
   */
  banner: 'stopped' | 'starting' | null
  /** The strips the shell reported, as the main process applies them. */
  insets: ShellInsets
  /** The view crashed, hangs or could not load; the shell offers Reload. */
  problem: WebuiProblem | null
  /** A page is loaded in the view (it may be hidden behind a shell screen). */
  loaded: boolean
  /** Set while the remote default does not answer. The shell shows the recovery screen. */
  remote: RemoteStatusView | null
}

/** The remote server of the window does not answer (see main/remoteMonitor.ts). */
export interface RemoteStatusView {
  kind: 'unreachable'
  /** A short plain reason: "timed out", "refused", "name not found", "network", "not LocalAI", "certificate". */
  reason: string
  /** Milliseconds since the epoch of the last try. */
  at: number
  /** Milliseconds since the epoch of the next automatic try. */
  nextAt: number
}

// ---- servers --------------------------------------------------------------

export type TlsProblem = 'expired' | 'not-yet-valid' | 'self-signed' | 'name-mismatch' | 'untrusted'
export type UnreachableWhy = 'timeout' | 'refused' | 'dns' | 'network'

/** What Test found (mirrors main/testConnection.ts), plus whether the address is plain http off this computer. */
export type TestResultView =
  | { status: 'ok'; version?: string; secure: boolean }
  | { status: 'needs-signin' }
  | { status: 'not-localai'; reason: 'answer' | 'redirect' }
  | { status: 'tls'; reason: TlsProblem }
  | { status: 'unreachable'; reason: UnreachableWhy }
  | { status: 'cancelled' }

export type ServerStatusName = 'running' | 'not-running' | 'connected' | 'needs-signin' | 'unreachable' | 'problem' | 'not-tested' | 'checking'

export interface ServerRowView {
  /** `local` or the id of a saved server. */
  id: string
  kind: 'local' | 'remote'
  name: string
  /** What to show under the name: the address (local) or the host (remote). */
  host: string
  /** The origin of a remote. */
  origin?: string
  /** LocalAI version, when known (the installed server for This computer; the last test for a remote). */
  version?: string
  isDefault: boolean
  /** The server the window shows now. */
  isCurrent: boolean
  /** Plain http to an address that is not this computer. */
  insecure: boolean
  status: ServerStatusName
  /** Milliseconds since the epoch of the last check, when there was one. */
  checkedAt?: number
}

/** The switch to another server is under way (the calm "Opening X" card). */
export interface SwitchingView {
  targetId: string
  name: string
  host: string
  step: 'reaching' | 'loading'
  startedAt: number
}

export interface ServersView {
  /** This computer first, then the saved servers in the order they were added. */
  rows: ServerRowView[]
  defaultId: string
  /** Keep the local server running while another server is the default (opt-in). */
  keepLocal: boolean
  /** True when there is no local install, no saved server and the welcome is still on: the first screen "Where should LocalAI run?". */
  firstRun: boolean
  switching: SwitchingView | null
}

export type TestInput = { id: string } | { address: string }

export type TestOutcome =
  | { ok: true; origin: string; host: string; insecure: boolean; result: TestResultView }
  | { ok: false; reason: ServerFailReason }

/** Why a typed or saved address was refused. */
export type ConnectFailReason = ServerFailReason | 'no-url'
export type ServerFailReason = 'invalid-url' | 'scheme' | 'credentials' | 'needs-https' | 'name' | 'duplicate' | 'limit' | 'not-found'

export interface AddServerInput {
  name: string
  address: string
  /** Make it the default and open it. */
  makeDefault?: boolean
  /** "Save without testing" (Settings only). Otherwise the server is saved only after a passing test. */
  skipTest?: boolean
}

export interface UpdateServerInput {
  id: string
  name?: string
  address?: string
  /** Save a changed address without testing it. */
  skipTest?: boolean
}

export type ServerChangeResult =
  | { ok: true; view: ServersView; id?: string; test?: TestResultView }
  | { ok: false; reason: ServerFailReason | 'test-failed' | 'busy'; existingId?: string; test?: TestResultView }

/** `needs-signin` counts as a pass: the server answers and asks for a login. */
export const TEST_PASSES = (r: TestResultView): boolean => r.status === 'ok' || r.status === 'needs-signin'

export type SwitchResult = { ok: true; view: ServersView } | { ok: false; reason: 'not-found' | 'test-failed' | 'cancelled' | 'busy'; test?: TestResultView }

/** The API the preload script exposes on window.launcher. Every method maps to one channel of shared/ipc.ts. */
export interface LauncherApi {
  info(): Promise<LauncherInfo>
  // server
  getServer(): Promise<ServerSnapshot>
  start(): Promise<void>
  stop(): Promise<void>
  restart(): Promise<void>
  onServer(cb: (s: ServerSnapshot) => void): () => void
  listModels(): Promise<string[]>
  openChat(model?: string): Promise<void>
  copyText(text: string): Promise<void>
  // launcher self-update
  getUpdater(): Promise<UpdaterStatus>
  checkForLauncherUpdate(): Promise<void>
  onUpdater(cb: (s: UpdaterStatus) => void): () => void
  // server install, update, rollback
  getInstallState(): Promise<InstallStateView>
  checkServerUpdate(options?: InstallOptions): Promise<InstallStateView>
  installServer(options?: InstallOptions): Promise<InstallStateView>
  cancelInstall(): Promise<boolean>
  rollbackServer(options?: InstallOptions): Promise<InstallStateView>
  onInstall(cb: (s: InstallStateView) => void): () => void
  // settings
  getSettings(): Promise<LauncherSettings>
  saveSettings(patch: Partial<LauncherSettings>): Promise<SaveSettingsResult>
  // logs
  queryLogs(query?: LogQueryInput): Promise<LogLineView[]>
  /** Opens a save dialog in the main process; the renderer never names a path. */
  exportLogs(): Promise<ExportLogsResult>
  onLogs(cb: (lines: LogLineView[]) => void): () => void
  // sandbox
  getSandboxState(): Promise<SandboxView>
  getSandboxSetupPlan(kind?: SandboxPlanKind): Promise<SandboxPlanView>
  runSandboxSetup(): Promise<SandboxView>
  runSandboxRemove(): Promise<SandboxView>
  dismissSandboxNotice(): Promise<SandboxView>
  onSandbox(cb: (s: SandboxView) => void): () => void
  // status bar
  getStatusBarVisible(): Promise<boolean>
  setStatusBarVisible(visible: boolean): Promise<boolean>
  onStatusBar(cb: (visible: boolean) => void): () => void
  // WebUI view and layout
  getWebui(): Promise<WebuiViewState>
  onWebui(cb: (s: WebuiViewState) => void): () => void
  /** Tells the main process which shell screen covers the WebUI (null: none). */
  setShellScreen(screen: ShellScreen | null): Promise<WebuiViewState>
  /** A popover of the bar opens (true) or closes (false); the shell is raised above the WebUI meanwhile. */
  setOverlay(open: boolean): Promise<WebuiViewState>
  /** The heights of the shell's strips around the WebUI view; the view gives way to them. */
  setInsets(insets: ShellInsets): void
  /** Opens the page the view shows (or the WebUI home) in the system browser. */
  openWebuiInBrowser(): Promise<void>
  /** Makes a saved server the default and opens it; without an id, opens the default again. */
  connectRemote(id?: string): Promise<SwitchResult>
  backToLocal(): Promise<WebuiViewState>
  /** Reloads the view after a crash, a hang or a failed load. */
  reloadWebui(): Promise<WebuiViewState>
  /** Forgets every remembered microphone and camera choice. */
  resetWebuiPermissions(): Promise<void>
  // window settings
  getWindowSettings(): Promise<WindowSettingsView>
  setOpenInBrowser(on: boolean): Promise<WindowSettingsView>
  setDesktopNotifications(on: boolean): Promise<WindowSettingsView>
  // updates
  getUpdateInfo(): Promise<UpdateInfoView>
  setAutoCheck(on: boolean): Promise<UpdateInfoView>
  /**
   * Picks the update channel of this computer. `dev` needs `confirmed: true`
   * the first time and whenever it follows Stable; the main process checks.
   */
  setUpdateChannel(channel: UpdateChannelName, confirmed?: boolean): Promise<SetChannelResult>
  /** Leaves Development: switches the channel to Stable and installs the stable release. */
  backToStable(): Promise<UpdateInfoView>
  // servers
  getServers(): Promise<ServersView>
  onServers(cb: (v: ServersView) => void): () => void
  /** The menu accelerator (Ctrl+Shift+S, Cmd+Shift+S on macOS) asks the shell to open the switcher. */
  onOpenSwitcher(cb: () => void): () => void
  /** Tests a saved server (by id) or an address the user typed. The main process makes the request. */
  testServer(input: TestInput): Promise<TestOutcome>
  /** Saves a server; unless `skipTest`, only after a passing test. */
  addServer(input: AddServerInput): Promise<ServerChangeResult>
  updateServer(input: UpdateServerInput): Promise<ServerChangeResult>
  /** Removes a saved server. Removing the default falls back to This computer and starts it. */
  removeServer(id: string): Promise<ServerChangeResult>
  /** `local` or a saved server id. Opens it and keeps it as the default. */
  setDefaultServer(target: string): Promise<SwitchResult>
  cancelSwitch(): Promise<void>
  /** Tests the unreachable remote default now. */
  retryRemote(): Promise<void>
  setKeepLocalRunning(on: boolean): Promise<ServersView>
  /** Opens the release page of an offered version in the system browser. */
  openReleaseNotes(version: string): Promise<void>
  getInstallDetails(): Promise<InstallDetailsView>
  /** Installs a downloaded launcher update (quits and restarts), or downloads an available one. */
  installLauncherUpdate(): Promise<void>
  /** Restarts the launcher (after the sandbox profile setup). */
  relaunch(): Promise<void>
  // start failure and first run
  /** The first free port from `from` upward, tested on loopback; null when none of the next 100 is free. */
  findFreePort(from: number): Promise<number | null>
  showPortProcess(port: number): Promise<PortProcessView>
  /** A native folder picker; the main process chooses where it opens. Null when cancelled. */
  /** Opens the native folder picker, checks the folder and saves it. The renderer never sends a path. */
  pickFolder(kind: FolderKind): Promise<FolderPickResult>
  getHardwareSummary(): Promise<HardwareSummaryView>
  // network exposure, API key, environment variables
  getNetwork(): Promise<NetworkView>
  /** What exposing would do; apply it with setNetworkExposed(true, planId). */
  getNetworkPlan(): Promise<NetworkPlanView>
  /** true needs the planId of a plan the user confirmed; false keeps the API key. */
  setNetworkExposed(on: boolean, planId?: string): Promise<NetworkChangeResult>
  /** Replaces the API key (the renderer asks the user first). */
  regenerateApiKey(): Promise<NetworkView>
  /** Closes the notice about the key created at start. */
  dismissKeyNotice(): Promise<NetworkView>
  /** Returns the key once, for Show. Rate limited. */
  revealApiKey(): Promise<RevealKeyResult>
  /** The main process puts the key (or an example with it) on the clipboard; the renderer never sees it. */
  copyApiKey(format?: CopyKeyFormat): Promise<CopyKeyResult>
  /** Names only; values never leave the main process. */
  getEnvVars(): Promise<EnvVarsView>
  setEnvVar(name: string, value: string): Promise<EnvVarResult>
  deleteEnvVar(name: string): Promise<EnvVarsView>
  getSecretStorageState(): Promise<SecretStorageView>
  /** Tells the main process the first React render happened (smoke test hook). */
  firstRender(shownState: string): void
}
