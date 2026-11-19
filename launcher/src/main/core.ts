// The launcher's main-process state and actions, without Electron. index.ts
// creates one LauncherCore, passes it the Electron pieces it needs (an event
// sink, the release provider, the sandbox runner) and maps IPC to it.
//
// Start order (init):
//   1. config: load launcher.json; a recovered file is reported in `configNotice`
//   2. data folders: models, backends and logs, created with mode 0700 when missing
//   3. log hub: the files under <data>/logs; earlier lines are replayed into it
//   4. sandbox probe, once, and the notice state
//   5. supervisor from the config, fed into the log hub
//   6. server installer, with the supervisor as its handle
// `autoStart()` then starts the server when the config says so.
//
// Secrets (secretStore.ts) open right after the config: the API key and the
// user's environment variables go to the server through its environment
// only, never on its command line, and never into launcher.json or the logs.
// Old plain-text values of launcher.json move into the store at the first
// start (migrateSecrets). Server output is scrubbed of secret values before
// it reaches the log or the renderer.
//
// The WebUI view's lifecycle is a WebuiModel (webuiState.ts) fed from here:
// server, install, updater, sandbox and settings changes go in as inputs; the
// effects go out through `webuiEffects` to the Electron side (webuiView.ts),
// and the state goes to the shell as 'launcher:webui:event'.
import crypto from 'node:crypto'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import type {
  AddServerInput,
  CopyKeyFormat,
  EnvVarResult,
  EnvVarsView,
  NetworkChangeResult,
  NetworkPlanView,
  NetworkView,
  RevealKeyResult,
  SecretStorageView,
  ExportLogsResult,
  FolderKind,
  FolderPickResult,
  InstallDetailsView,
  InstallOptions,
  InstallStateView,
  LauncherSettings,
  LogLineView,
  LogQueryInput,
  SandboxPlanKind,
  SandboxPlanView,
  SandboxView,
  SaveSettingsResult,
  ServerChangeResult,
  ServerRowView,
  ServersView,
  ServerStatusName,
  SetChannelResult,
  SwitchingView,
  SwitchResult,
  TestInput,
  TestOutcome,
  TestResultView,
  UpdateServerInput,
  ServerSnapshot,
  ShellInsets,
  ShellScreen,
  UpdateInfoView,
  UpdaterStatus,
  WebuiViewState,
  WindowSettingsView,
} from '../shared/types'
import type { EventContract } from '../shared/ipc'
import { dataPath, defaultConfigPath, type LauncherConfig } from './config'
import { API_KEY_VARS, envNameProblem, MAX_ENV_VALUE, MAX_ENV_VARS } from '../shared/envVars'
import { snippet } from '../shared/connectSnippets'
import { apiKeyHint, generateApiKey, RevealLimiter } from './apiKey'
import { exposedAddress, isExposedAddress, lanAddresses, loopbackAddress, splitAddress } from './network'
import {
  API_KEY_SECRET,
  ENV_PREFIX,
  envSecret,
  isSecretName,
  SECRETS_FILE,
  SecretStore,
  type SafeStorageLike,
  type SetResult,
} from './secretStore'
import {
  autoCheckUpdates,
  ConfigStore,
  lastUpdateCheck,
  desktopNotifications,
  openWebuiInBrowser,
  withDesktopNotifications,
  sandboxNoticeDismissedVersion,
  secretsPlaintextFile,
  showStatusBar,
  webuiPermissions,
  withAutoCheckUpdates,
  withLastUpdateCheck,
  withSandboxNoticeDismissed,
  withShowStatusBar,
  withWebuiPermissions,
} from './configStore'
import { serverBinaryName } from './platform'
import { homeLayout, layoutNote, type LauncherLayout } from './paths'
import { defaultLogsDir, LogHub, type LogLine } from './logs'
import { initialNotice, reduceNotice, type NoticeEvent, type NoticeState, type SetupOutcome } from './sandboxNotice'
import type { SandboxProbe } from './sandboxProbe'
import { InvalidAttachPattern, validateAttachPattern, type SandboxPlan } from './sandboxProfile'
import { prepareRemove, prepareSetup, runPlan, type Runner, type SetupResult } from './sandboxSetup'
import { buildServerLaunch, needsRestart, type ServerLaunch } from './serverLaunch'
import {
  execServerVersion,
  isActive,
  nodeInstallFs,
  ServerInstaller,
  type ReleaseSource,
  type SupervisorHandle,
} from './serverInstall'
import { applySettings, checkDataFolder, toSettings, type FolderProbe } from './settings'
import { ServerSupervisor, type SupervisorOptions } from './supervisor'
import { releasePageUrl, type PlatformTarget } from './updateProvider'
import { chatUrl } from './models'
import { resolveLink, type NotifyTarget } from './notifications'
import type { LoginItemResult } from './loginItem'
import { WebuiModel, type WebuiEffect, type WebuiEvent, type WebuiInputs } from './webuiState'
import { RemoteMonitor, REMOTE_RETRY_MS } from './remoteMonitor'
import {
  addRemote,
  defaultTarget,
  devChannelConfirmed,
  keepLocalRunning,
  LOCAL_TARGET,
  migrateRemoteUrl,
  newRemoteId,
  removeRemote,
  remotes,
  updateChannel,
  updateRemote,
  withDefaultTarget,
  withDevChannelConfirmed,
  withKeepLocalRunning,
  withUpdateChannel,
  type RemoteServer,
  type UpdateChannel,
} from './serversConfig'
import { isInsecureRemote, testServer as runTest, type TestResult } from './testConnection'
import {
  httpOrigin,
  recall,
  remember,
  resetPermissions,
  validateRemoteOrigin,
  type PermissionChoice,
  type PermissionStore,
} from './webuiPolicy'

export type Emit = <C extends keyof EventContract>(channel: C, payload: EventContract[C]) => void

export interface CoreDeps {
  /** Home directory; launcher.json, the binary and the default data folder live under <home>/.localai. */
  home: string
  /** Folders of the launcher and the default data folder (paths.ts). Default: <home>/.localai for all. */
  layout?: LauncherLayout
  /** launcher.json; default <launcher folder>/launcher.json. */
  configPath?: string
  launcherVersion: string
  /** Environment the server's environment is built from. */
  env: NodeJS.ProcessEnv
  /** undefined on a platform with no server build. */
  target: PlatformTarget | undefined
  /** Test hook: run this file as the server instead of <home>/.localai/bin/local-ai (local-ai.exe on Windows). */
  serverBinOverride?: string
  /** Test hook: the server listens here instead of the configured address. */
  endpointOverride?: string
  provider: ReleaseSource
  fetch: typeof fetch
  probeSandbox(): SandboxProbe
  sandboxRunner: Runner
  /**
   * The AppArmor attach pattern. Deliberately unset by default: the rule is
   * decided only after a hands-on test on Ubuntu 24.04 (see sandboxProfile.ts).
   * While unset, setup is refused and the notice points to the docs.
   */
  sandboxAttachPattern?: string
  emit: Emit
  /** Overrides for the supervisor timings, for tests. */
  supervisorTuning?: Partial<Pick<SupervisorOptions, 'readyTimeoutMs' | 'pollIntervalMs' | 'stopGraceMs' | 'killWaitMs'>>
  runVersion?: (binary: string) => Promise<string>
  /** Delay before buffered log lines are sent to the renderer. */
  logFlushMs?: number
  /** Applies WebUI view effects (create, load, close, reload the view; open the browser). */
  webuiEffects?: (effects: WebuiEffect[]) => void
  /** The URL the WebUI view shows now, for "Open in browser". */
  webuiCurrentUrl?: () => string | undefined
  /** Where release pages live. Default: the LocalAI repository. */
  releaseRepo?: { owner: string; repo: string }
  /** The update channel changed: true for release candidates. The launcher's own updater follows release candidates only. */
  onIncludePrereleases?: (on: boolean) => void
  /** Test hook: replaces the real server check. */
  testServer?: (origin: string, o: { signal?: AbortSignal }) => Promise<TestResult>
  /** Test hook: the wait between tries while the remote default does not answer. */
  remoteRetryMs?: number
  /** Turns "Launch at login" on or off (loginItem.ts). Without it the setting is only saved. */
  loginItem?: (enabled: boolean) => LoginItemResult
  /** Electron's safeStorage. Missing means no secure storage: secrets stay in memory unless the user opts in. */
  safeStorage?: SafeStorageLike
  /** Milestones of init(), for the packaged-build smoke test. Not for users. */
  trace?: (milestone: string) => void
  /** secrets.json; default: next to launcher.json. */
  secretsPath?: string
  /** Default: process.platform. Decides whether the Linux fallback of safeStorage counts. */
  platform?: NodeJS.Platform
  /** os.networkInterfaces, for the addresses other devices use. */
  networkInterfaces?: () => NodeJS.Dict<os.NetworkInterfaceInfo[]>
  /**
   * Gives the WebUI view of the local server the API key the server gets
   * (undefined: none), so the page signs in by itself. Called whenever the
   * server's launch values are rebuilt.
   */
  webuiCredential?: (origin: string, key: string | undefined) => Promise<void>
  /** Called when the servers to watch for notifications may have changed (see notificationTargets). */
  notificationTargetsChanged?: () => void
  /** A `fetch` that signs in to a remote server with the cookies of its WebUI session. */
  remoteFetch?: (origin: string) => typeof fetch
  /** Random bytes for keys and plan ids. Default: the OS CSPRNG. */
  randomBytes?: (n: number) => Buffer
  /** Milliseconds since the epoch, for the reveal limit and plan expiry. */
  now?: () => number
}

/** A network plan from getNetworkPlan can be applied for this long. */
export const NETWORK_PLAN_TTL_MS = 5 * 60 * 1000
/** Secret values shorter than this are not scrubbed from log lines: a "4" or a common word would garble every line. A key or token is longer. */
export const REDACT_MIN_LENGTH = 12
export const REDACTED = '[REDACTED]'

/** The automatic server update check runs at most this often. */
export const AUTO_CHECK_INTERVAL_MS = 24 * 60 * 60 * 1000

/** "/home/user/.localai/bin" -> "~/.localai/bin": the folder as a person reads it, never sent anywhere. */
export function displayPath(p: string, home: string): string {
  const h = home.replace(/[\\/]+$/, '')
  if (h && (p === h || p.startsWith(h + path.sep))) return `~${p.slice(h.length)}`
  return p
}

const LOG_BATCH_MAX = 500
const FOLDER_MODE = 0o700

/** Cuts a stream of chunks into whole lines. */
export class LineSplitter {
  private rest = ''
  push(chunk: string, out: (line: string) => void): void {
    const parts = (this.rest + chunk).split(/\r?\n/)
    this.rest = parts.pop() ?? ''
    for (const p of parts) if (p !== '') out(p)
    // A very long line with no newline yet is passed on in pieces.
    if (this.rest.length > 64 * 1024) {
      out(this.rest)
      this.rest = ''
    }
  }
  flush(out: (line: string) => void): void {
    if (this.rest !== '') out(this.rest)
    this.rest = ''
  }
}

function setupOutcome(r: SetupResult): SetupOutcome {
  if (r.status === 'ok') return { ok: true }
  if (r.status === 'dismissed') return { ok: false, dismissed: true }
  return { ok: false, reason: r.detail ? `${r.status}: ${r.detail}` : r.status }
}

function planView(plan: SandboxPlan, available: boolean): SandboxPlanView {
  return {
    kind: plan.kind,
    available,
    profilePath: plan.profilePath,
    profileText: plan.profileText,
    steps: plan.steps.map((s) => ({ description: s.description, command: s.argv.join(' ') })),
    terminalText: plan.terminalText,
  }
}

export class LauncherCore {
  /** A key was created at start because the saved address exposed the server without one; Connect says so once. */
  private keyCreatedNotice = false
  private store!: ConfigStore
  private hub!: LogHub
  private supervisor!: ServerSupervisor
  private launch!: ServerLaunch
  private installer!: ServerInstaller
  private probe!: SandboxProbe
  private notice: NoticeState = initialNotice
  private attachPattern: string | undefined
  /** The config changed while the server ran; the next start uses the new values. */
  private launchStale = false
  private installRun: Promise<InstallStateView> | undefined
  private pendingLogs: LogLineView[] = []
  private logTimer: NodeJS.Timeout | undefined
  private early: string[] = []
  private serverLines = new LineSplitter()
  configNotice: { status: 'recovered'; reason: string } | undefined
  private webui: WebuiModel | undefined
  private lastWebuiJson = ''
  private secrets!: SecretStore
  /** Values to scrub from server output: every secret given to a server in this session. */
  private redactValues = new Set<string>()
  private plans = new Map<string, number>()
  private revealLimiter = new RevealLimiter()
  private monitor!: RemoteMonitor
  private checks = new Map<string, { status: ServerStatusName; at: number; version?: string }>()
  private switching: SwitchingView | null = null
  private switchAbort: AbortController | undefined
  private switchPrevious: string | undefined
  private localVersion: string | undefined
  private startAfterInstall = false
  private openTests = new Set<AbortController>()

  constructor(private readonly d: CoreDeps) {}

  // ---- start ------------------------------------------------------------------

  async init(): Promise<void> {
    const launcherLog = (m: string) => (this.hub ? this.hub.write('launcher', m) : this.early.push(m))

    this.d.trace?.('init-start')
    // 1. config
    const layoutLine = layoutNote(this.layout())
    if (layoutLine) launcherLog(`folders: ${layoutLine}`)
    this.store = await ConfigStore.open({ home: this.d.home, layout: this.layout(), path: this.d.configPath, log: launcherLog })
    if (this.store.loaded.status === 'recovered') {
      this.configNotice = { status: 'recovered', reason: this.store.loaded.reason ?? 'config file could not be used' }
    }
    this.d.trace?.('config-opened')
    // 1a. the old "last server chosen in Connect" moves into the server list, once
    const idGen = () => newRemoteId((n) => this.random(n))
    if (migrateRemoteUrl(this.store.get(), idGen).changed) {
      await this.store.update((c) => migrateRemoteUrl(c, idGen).config).catch(() => launcherLog('servers: could not save the migrated server list'))
    }
    let config = this.store.get()

    // 1b. secrets, then the move of old plain-text values out of launcher.json
    this.secrets = await SecretStore.open({
      file: this.d.secretsPath ?? path.join(path.dirname(this.d.configPath ?? defaultConfigPath(this.d.home, this.layout())), SECRETS_FILE),
      safeStorage: this.d.safeStorage,
      platform: this.d.platform,
      plaintextOptIn: secretsPlaintextFile(config),
      log: launcherLog,
    })
    launcherLog(`secrets: ${this.secrets.backend()}`)
    this.d.trace?.('secrets-opened')
    await this.migrateSecrets(launcherLog)
    this.d.trace?.('secrets-migrated')
    config = this.store.get()
    // The server never listens on the network without a key. Without a keyring
    // the key lives only for a session while the address is saved, and an old
    // config may expose the server with no key at all: make one now.
    if (isExposedAddress(config.address) && !this.secrets.has(API_KEY_SECRET)) {
      // Where secrets are kept across a restart, a missing key means the config was exposed
      // without one (an older launcher): tell the user once in Connect. Without a keyring a
      // missing key is expected, and Connect already says the key lasts for one session.
      const keptAcrossRestart = this.secrets.backend() !== 'session'
      await this.secrets.set(API_KEY_SECRET, generateApiKey((n) => this.random(n)))
      this.keyCreatedNotice = keptAcrossRestart
      launcherLog(
        keptAcrossRestart
          ? 'api key created: LocalAI was open to the network without one'
          : 'api key created: the server listens on the network and had no key it could read',
      )
    }

    this.d.trace?.('step-folders')
    // 2. data folders, as the Go launcher created them at start
    const data = dataPath(config, this.d.home, this.layout())
    this.ensureFolders([config.models_path, config.backends_path, defaultLogsDir(data)], launcherLog)

    this.d.trace?.('step-logs')
    // 3. logs
    this.hub = new LogHub({ dir: defaultLogsDir(data) })
    for (const m of this.early.splice(0)) this.hub.write('launcher', m)
    this.hub.subscribe((l) => this.queueLog(l))
    this.hub.write('launcher', `launcher ${this.d.launcherVersion} starting`)

    this.d.trace?.('step-sandbox')
    // 4. sandbox
    try {
      this.attachPattern = this.d.sandboxAttachPattern ? validateAttachPattern(this.d.sandboxAttachPattern) : undefined
    } catch (e) {
      this.attachPattern = undefined
      this.hub.write('launcher', `sandbox: ignoring the attach pattern (${e instanceof InvalidAttachPattern ? e.message : 'invalid'})`, 'warn')
    }
    this.probe = this.d.probeSandbox()
    this.hub.write('launcher', `sandbox: ${this.probe.state}`)
    this.dispatchNotice({
      type: 'probed',
      state: this.probe.state,
      launcherVersion: this.d.launcherVersion,
      dismissedVersion: sandboxNoticeDismissedVersion(config),
    })

    this.d.trace?.('step-supervisor')
    // 5. supervisor
    this.buildSupervisor(config)

    this.d.trace?.('step-installer')
    // 6. installer
    const paths = this.installPaths()
    const core = this // the getter below must read the current supervisor, which a settings change replaces
    const handle: SupervisorHandle = {
      get state() {
        return core.supervisor.state
      },
      start: () => this.supervisor.start(),
      stop: () => this.supervisor.stop(),
    }
    this.installer = new ServerInstaller({
      provider: this.d.provider,
      fetch: this.d.fetch,
      fs: nodeInstallFs,
      sleep: (ms) => new Promise((r) => setTimeout(r, ms)),
      now: Date.now,
      supervisor: handle,
      // Never used without a target: install and check refuse first (see unsupported()).
      target: this.d.target ?? { os: 'linux', arch: 'amd64' },
      paths,
      runVersion: this.d.runVersion ?? execServerVersion,
    })
    this.installer.subscribe((s) => {
      this.d.emit('launcher:install:event', s)
      if (s.kind === 'done') {
        void this.installer.installedVersion().then((v) => {
          this.localVersion = v
          this.emitServers()
        }).catch(() => undefined)
      }
      // Going back to Stable restarts a server that was running.
      if ((s.kind === 'done' || s.kind === 'failed') && this.startAfterInstall) {
        this.startAfterInstall = false
        void this.startServer()
      }
      if (s.kind === 'downloading') return
      this.hub.write('launcher', `server install: ${s.kind}${s.kind === 'failed' ? ` (${s.reason}): ${s.message}` : ''}`, s.kind === 'failed' ? 'error' : 'info')
      // A finished install or rollback changes "not installed" into "stopped".
      if (s.kind === 'done' || s.kind === 'failed') this.d.emit('launcher:server:event', this.snapshot())
      this.webuiInputs({ installActive: this.installActive(), serverUpdate: this.serverUpdateOffered() })
    })

    this.d.trace?.('step-webui-model')
    // 7. WebUI view model
    this.webui = new WebuiModel(this.currentWebuiInputs())
    void this.installer.installedVersion().then((v) => (this.localVersion = v)).catch(() => undefined)

    this.d.trace?.('step-default-server')
    // 8. the default server: a remote opens at once, and the local server is not touched
    this.monitor = new RemoteMonitor({
      probe: (origin) => this.checkOrigin(origin),
      onStatus: (st) => {
        const id = defaultTarget(this.store.get())
        const r = remotes(this.store.get()).find((x) => x.id === id)
        if (r) this.checks.set(r.id, st ? { status: 'unreachable', at: st.at } : { status: this.checks.get(r.id)?.status === 'needs-signin' ? 'needs-signin' : 'connected', at: this.nowMs(), version: this.checks.get(r.id)?.version })
        this.webuiDispatch({ type: 'remote-status', status: st })
        this.emitServers()
      },
      now: () => this.nowMs(),
      retryMs: this.d.remoteRetryMs ?? REMOTE_RETRY_MS,
    })
    if (remotes(config).some((r) => r.id === defaultTarget(config))) launcherLog('servers: a remote server is the default; the local server is not used')
  }

  /**
   * Opens the default server when it is a remote one. The shell calls this once
   * its window exists, like autoStart(): the 'show' effect is dropped when there
   * is no window yet, and the model would then think the view is up.
   */
  openDefaultRemote(): void {
    const c = this.store.get()
    const r = remotes(c).find((x) => x.id === defaultTarget(c))
    if (r) this.openRemote(r)
  }

  // ---- servers -------------------------------------------------------------------

  /** The local server follows its rules (install, start, update, check) only when it is the default or the user keeps it on. */
  localManaged(c: LauncherConfig = this.store.get()): boolean {
    return defaultTarget(c) === LOCAL_TARGET || keepLocalRunning(c)
  }

  private hostOf(r: RemoteServer): string {
    try {
      return new URL(r.url).host
    } catch {
      return r.url
    }
  }

  private checkOrigin(origin: string, signal?: AbortSignal): Promise<TestResult> {
    return (this.d.testServer ?? ((o, x) => runTest(o, x)))(origin, { signal })
  }

  private statusOfResult(r: TestResultView): ServerStatusName {
    return r.status === 'ok' ? 'connected' : r.status === 'needs-signin' ? 'needs-signin' : r.status === 'unreachable' ? 'unreachable' : r.status === 'cancelled' ? 'not-tested' : 'problem'
  }

  private record(id: string, r: TestResultView): void {
    if (r.status === 'cancelled') return
    const prev = this.checks.get(id)
    this.checks.set(id, { status: this.statusOfResult(r), at: this.nowMs(), version: r.status === 'ok' ? r.version : r.status === 'needs-signin' ? prev?.version : undefined })
  }

  serversView(): ServersView {
    const c = this.store.get()
    const def = defaultTarget(c)
    const sup = this.supervisor.state
    const rows: ServerRowView[] = [
      {
        id: LOCAL_TARGET,
        kind: 'local',
        name: 'This computer',
        host: c.address,
        version: this.localVersion,
        isDefault: def === LOCAL_TARGET,
        isCurrent: def === LOCAL_TARGET,
        insecure: false,
        status: sup === 'ready' ? 'running' : sup === 'starting' ? 'checking' : 'not-running',
      },
      ...remotes(c).map((r): ServerRowView => {
        const k = this.checks.get(r.id)
        const current = def === r.id
        return {
          id: r.id,
          kind: 'remote',
          name: r.name,
          host: this.hostOf(r),
          origin: r.url,
          version: k?.version,
          isDefault: current,
          isCurrent: current,
          insecure: isInsecureRemote(r.url),
          status: k?.status ?? (current ? 'connected' : 'not-tested'),
          checkedAt: k?.at,
        }
      }),
    ]
    return {
      rows,
      defaultId: def,
      keepLocal: keepLocalRunning(c),
      firstRun: def === LOCAL_TARGET && !this.binaryInstalled() && remotes(c).length === 0 && c.show_welcome,
      switching: this.switching,
    }
  }

  private emitServers(): void {
    this.d.emit('launcher:servers:event', this.serversView())
    this.d.notificationTargetsChanged?.()
  }

  /** Shows a saved server in the window and watches it. */
  private openRemote(r: RemoteServer): void {
    this.webuiDispatch({ type: 'connect', origin: r.url, host: this.hostOf(r) })
    void this.monitor.start(r.url)
  }

  async testServer(input: TestInput): Promise<TestOutcome> {
    let origin: string
    let host: string
    let id: string | undefined
    if ('id' in input) {
      const r = remotes(this.store.get()).find((x) => x.id === input.id)
      if (!r) return { ok: false, reason: 'not-found' }
      origin = r.url
      host = this.hostOf(r)
      id = r.id
    } else {
      const v = validateRemoteOrigin(input.address)
      if (!v.ok) return { ok: false, reason: v.reason }
      origin = v.origin
      host = v.host
      id = remotes(this.store.get()).find((x) => x.url === origin)?.id
    }
    if (id) {
      this.checks.set(id, { status: 'checking', at: this.nowMs(), version: this.checks.get(id)?.version })
      this.emitServers()
    }
    const ac = new AbortController()
    this.openTests.add(ac)
    let result: TestResultView
    try {
      result = (await this.checkOrigin(origin, ac.signal)) as TestResultView
    } finally {
      this.openTests.delete(ac)
    }
    if (id) {
      if (result.status === 'cancelled') this.checks.delete(id)
      else this.record(id, result)
      this.emitServers()
    }
    this.hub.write('launcher', `servers: test ${result.status}`)
    return { ok: true, origin, host, insecure: isInsecureRemote(origin), result }
  }

  async addServer(input: AddServerInput): Promise<ServerChangeResult> {
    const idGen = () => newRemoteId((n) => this.random(n))
    const pre = addRemote(this.store.get(), input, idGen)
    if (!pre.ok) return { ok: false, reason: pre.reason, existingId: pre.existingId }
    let test: TestResultView | undefined
    if (!input.skipTest) {
      const t = await this.testServer({ address: input.address })
      if (!t.ok) return { ok: false, reason: t.reason }
      test = t.result
      if (!(t.result.status === 'ok' || t.result.status === 'needs-signin')) return { ok: false, reason: 'test-failed', test }
    }
    let added: RemoteServer | undefined
    let failure: ServerChangeResult | undefined
    await this.store.update((c) => {
      const r = addRemote(c, input, idGen)
      if (!r.ok) {
        failure = { ok: false, reason: r.reason, existingId: r.existingId }
        return c
      }
      added = r.remote
      return r.config
    })
    if (failure || !added) return failure ?? { ok: false, reason: 'limit' }
    if (test) this.record(added.id, test)
    this.hub.write('launcher', 'servers: server saved')
    if (input.makeDefault) {
      const r = await this.switchTo(added.id, true)
      if (!r.ok) return { ok: false, reason: r.reason === 'busy' ? 'busy' : 'test-failed', test: r.test }
      return { ok: true, view: r.view, id: added.id, test }
    }
    this.emitServers()
    return { ok: true, view: this.serversView(), id: added.id, test }
  }

  async updateServer(input: UpdateServerInput): Promise<ServerChangeResult> {
    const before = remotes(this.store.get()).find((r) => r.id === input.id)
    if (!before) return { ok: false, reason: 'not-found' }
    const pre = updateRemote(this.store.get(), input.id, { name: input.name, address: input.address })
    if (!pre.ok) return { ok: false, reason: pre.reason, existingId: pre.existingId }
    let test: TestResultView | undefined
    const moved = pre.remote.url !== before.url
    if (moved && !input.skipTest) {
      const t = await this.testServer({ address: input.address ?? pre.remote.url })
      if (!t.ok) return { ok: false, reason: t.reason }
      test = t.result
      if (!(t.result.status === 'ok' || t.result.status === 'needs-signin')) return { ok: false, reason: 'test-failed', test }
    }
    let failure: ServerChangeResult | undefined
    let after: RemoteServer | undefined
    await this.store.update((c) => {
      const r = updateRemote(c, input.id, { name: input.name, address: input.address })
      if (!r.ok) {
        failure = { ok: false, reason: r.reason, existingId: r.existingId }
        return c
      }
      after = r.remote
      return r.config
    })
    if (failure || !after) return failure ?? { ok: false, reason: 'not-found' }
    if (moved) {
      this.checks.delete(after.id)
      if (test) this.record(after.id, test)
      // The window shows this server: open it again at the new address.
      if (defaultTarget(this.store.get()) === after.id) this.openRemote(after)
    }
    this.hub.write('launcher', 'servers: server changed')
    this.emitServers()
    return { ok: true, view: this.serversView(), id: after.id, test }
  }

  async removeServer(id: string): Promise<ServerChangeResult> {
    const c = this.store.get()
    if (!remotes(c).some((r) => r.id === id)) return { ok: false, reason: 'not-found' }
    const wasDefault = defaultTarget(c) === id
    await this.store.update((x) => removeRemote(x, id))
    this.checks.delete(id)
    this.hub.write('launcher', 'servers: server removed')
    if (wasDefault) await this.openLocal()
    this.emitServers()
    return { ok: true, view: this.serversView() }
  }

  /** This computer is the default now: show it, and start it when it is installed. */
  private async openLocal(): Promise<void> {
    this.monitor.stop()
    this.webuiDispatch({ type: 'back-to-local' })
    if (this.launchStale) this.buildSupervisor(this.store.get())
    await this.startServer()
  }

  async setDefaultServer(target: string): Promise<SwitchResult> {
    const r = await this.switchTo(target, false)
    return r
  }

  /**
   * Makes `target` the default and opens it. A remote must answer first;
   * until the page has loaded the old page stays and the user can cancel.
   * The local server is stopped after the switch, unless it is kept on.
   */
  private async switchTo(target: string, alreadyTested: boolean): Promise<SwitchResult> {
    if (this.switching) return { ok: false, reason: 'busy' }
    const c = this.store.get()
    if (target === defaultTarget(c)) {
      if (target !== LOCAL_TARGET) this.openRemoteById(target)
      else await this.openLocal()
      return { ok: true, view: this.serversView() }
    }
    if (target === LOCAL_TARGET) {
      const wasRemote = defaultTarget(c)
      await this.store.update((x) => withDefaultTarget(x, LOCAL_TARGET))
      this.hub.write('launcher', 'servers: This computer is the default')
      await this.openLocal()
      void wasRemote
      this.emitServers()
      return { ok: true, view: this.serversView() }
    }
    const r = remotes(c).find((x) => x.id === target)
    if (!r) return { ok: false, reason: 'not-found' }
    this.switchPrevious = defaultTarget(c)
    this.switching = { targetId: r.id, name: r.name, host: this.hostOf(r), step: 'reaching', startedAt: this.nowMs() }
    const ac = new AbortController()
    this.switchAbort = ac
    this.emitServers()
    if (!alreadyTested) {
      const result = (await this.checkOrigin(r.url, ac.signal)) as TestResultView
      if (ac.signal.aborted || result.status === 'cancelled') {
        this.endSwitch()
        return { ok: false, reason: 'cancelled' }
      }
      this.record(r.id, result)
      if (!(result.status === 'ok' || result.status === 'needs-signin')) {
        this.endSwitch()
        return { ok: false, reason: 'test-failed', test: result }
      }
    }
    this.switching = { ...this.switching, step: 'loading' }
    await this.store.update((x) => withDefaultTarget(x, r.id))
    this.hub.write('launcher', 'servers: a remote server is the default')
    this.openRemote(r)
    this.emitServers()
    // No page will load (the browser setting): the switch is complete now.
    if (this.webuiViewOrigin() !== r.url) await this.finishSwitch()
    return { ok: true, view: this.serversView() }
  }

  private openRemoteById(id: string): void {
    const r = remotes(this.store.get()).find((x) => x.id === id)
    if (r) this.openRemote(r)
  }

  private endSwitch(): void {
    this.switching = null
    this.switchAbort = undefined
    this.switchPrevious = undefined
    this.emitServers()
  }

  /** The new page is on show: now the local server can stop, unless the user keeps it. */
  private async finishSwitch(): Promise<void> {
    if (!this.switching) return
    this.endSwitch()
    const c = this.store.get()
    if (!this.localManaged(c) && this.serverRunning()) {
      this.hub.write('launcher', 'servers: stopping the local server, a remote server is the default')
      await this.stopServer()
    }
  }

  async cancelSwitch(): Promise<void> {
    const sw = this.switching
    if (!sw) return
    if (sw.step === 'reaching') {
      this.switchAbort?.abort()
      return
    }
    // The page is loading: go back to the server that was the default.
    const prev = this.switchPrevious ?? LOCAL_TARGET
    this.endSwitch()
    await this.store.update((x) => withDefaultTarget(x, prev))
    if (prev === LOCAL_TARGET) await this.openLocal()
    else this.openRemoteById(prev)
    this.emitServers()
  }

  async retryRemote(): Promise<void> {
    await this.monitor.retryNow()
  }

  async setKeepLocalRunning(on: boolean): Promise<ServersView> {
    await this.store.update((c) => withKeepLocalRunning(c, on))
    this.hub.write('launcher', `servers: keep the local server running ${on ? 'on' : 'off'}`)
    if (defaultTarget(this.store.get()) !== LOCAL_TARGET) {
      if (on) await this.autoStart()
      else if (this.serverRunning()) await this.stopServer()
    }
    this.emitServers()
    return this.serversView()
  }

  /** Starts the server when the config says so and a binary is installed. */
  async autoStart(): Promise<void> {
    const c = this.store.get()
    if (!this.localManaged(c)) {
      this.hub.write('launcher', 'auto-start skipped: a remote server is the default')
      return
    }
    if (!c.auto_start_server) {
      this.hub.write('launcher', 'auto-start is off')
      return
    }
    if (!this.binaryInstalled()) {
      this.hub.write('launcher', 'auto-start skipped: the server is not installed')
      return
    }
    await this.startServer()
  }

  private ensureFolders(dirs: string[], log: (m: string) => void): void {
    for (const dir of dirs) {
      try {
        // The mode applies only to folders created now; existing ones keep theirs.
        fs.mkdirSync(dir, { recursive: true, mode: FOLDER_MODE })
      } catch (e) {
        log(`could not create a data folder (${(e as NodeJS.ErrnoException).code ?? 'error'})`)
      }
    }
  }

  private layout(): LauncherLayout {
    return this.d.layout ?? homeLayout(this.d.home)
  }

  private installPaths() {
    const base = this.layout().launcherDir
    return {
      binDir: this.d.serverBinOverride ? path.dirname(this.d.serverBinOverride) : path.join(base, 'bin'),
      metaDir: path.join(base, 'metadata'),
      downloadDir: path.join(base, 'downloads'),
    }
  }

  serverBinary(): string {
    return this.d.serverBinOverride ?? path.join(this.installPaths().binDir, serverBinaryName(this.d.target))
  }

  private binaryInstalled(): boolean {
    return fs.existsSync(this.serverBinary())
  }

  private buildSupervisor(config: LauncherConfig): void {
    this.supervisor?.removeAllListeners()
    const apiKey = this.secrets.get(API_KEY_SECRET)
    const env = this.userEnv()
    for (const v of [apiKey, ...Object.values(env)]) if (v && v.length >= REDACT_MIN_LENGTH) this.redactValues.add(v)
    this.launch = buildServerLaunch(config, dataPath(config, this.d.home, this.layout()), this.d.env, this.d.endpointOverride, { env, apiKey }, updateChannel(config))
    this.supervisor = new ServerSupervisor({
      command: this.serverBinary(),
      args: this.launch.args,
      env: this.launch.env,
      cwd: dataPath(config, this.d.home, this.layout()),
      healthUrl: this.launch.healthUrl,
      ...this.d.supervisorTuning,
    })
    this.supervisor.on('state', () => {
      this.webuiInputs({ server: this.supervisor.state })
      const s = this.supervisor.snapshot()
      if (s.state !== 'starting') this.serverLines.flush((l) => this.hub.write('server', this.redact(l)))
      this.hub.write('launcher', `server ${s.state}${s.detail ? `: ${this.redact(s.detail)}` : ''}`, s.state === 'failed' ? 'error' : 'info')
      this.d.emit('launcher:server:event', this.snapshot())
      this.d.notificationTargetsChanged?.()
    })
    this.supervisor.on('log', (chunk: string) => this.serverLines.push(chunk, (l) => this.hub.write('server', this.redact(l))))
    this.launchStale = false
    this.webuiInputs({ server: this.supervisor.state, localOrigin: this.localOrigin() })
    const origin = this.localOrigin()
    void this.d.webuiCredential?.(origin, apiKey).catch(() => this.hub?.write('launcher', 'webui: could not give the API key to the view', 'warn'))
  }

  /** Replaces every secret value in a line of server output. */
  redact(line: string): string {
    let out = line
    for (const v of this.redactValues) if (out.includes(v)) out = out.split(v).join(REDACTED)
    return out
  }

  /** The user's environment variables, from the secret store. */
  private userEnv(): Record<string, string> {
    const env: Record<string, string> = {}
    for (const n of this.secrets.list(ENV_PREFIX)) {
      const v = this.secrets.get(n)
      if (v !== undefined) env[n.slice(ENV_PREFIX.length)] = v
    }
    return env
  }

  /**
   * Moves the values of `environment_vars` (plain text, as the Go launcher
   * wrote them) into the secret store, then removes them from launcher.json.
   * An API key among them becomes the launcher's API key. Without secure
   * storage and without the plain-file opt-in, the values are used for this
   * session and stay where they are, so nothing is lost; they move when
   * storage becomes available.
   */
  private async migrateSecrets(log: (m: string) => void): Promise<void> {
    const old = this.store.get().environment_vars
    const entries = Object.entries(old)
    if (entries.length === 0) return
    let allStored = true
    let moved = 0
    let dropped = 0
    for (const [k, v] of entries) {
      let r: SetResult
      try {
        if (API_KEY_VARS.includes(k)) {
          if (this.secrets.has(API_KEY_SECRET) || v.trim() === '' || v.length > MAX_ENV_VALUE) {
            dropped++
            continue
          }
          r = await this.secrets.set(API_KEY_SECRET, v.trim())
        } else if (isSecretName(envSecret(k)) && v.length <= MAX_ENV_VALUE && !v.includes('\u0000')) {
          // A value the store has already (moved by an earlier start) wins.
          if (this.secrets.has(envSecret(k))) continue
          r = await this.secrets.set(envSecret(k), v)
        } else {
          dropped++
          continue
        }
      } catch {
        allStored = false
        continue
      }
      moved++
      if (r.status !== 'stored') allStored = false
    }
    if (!allStored) {
      log(`secrets: no secure storage; ${moved} environment variables are used for this session and stay in launcher.json`)
      return
    }
    await this.store.update((c) => ({ ...c, environment_vars: {} }))
    log(`secrets: moved ${moved} environment variables out of launcher.json${dropped ? `, dropped ${dropped} that cannot be used` : ''}`)
  }

  /**
   * The server's launch values changed (API key, address, environment).
   * Stopped: rebuild now. Running: the next start uses them. Returns true
   * when a restart is needed.
   */
  private launchChanged(): boolean {
    if (this.serverRunning()) {
      this.launchStale = true
      return true
    }
    this.buildSupervisor(this.store.get())
    this.d.emit('launcher:server:event', this.snapshot())
    return false
  }

  private restartRequired(): boolean {
    return this.launchStale && this.serverRunning()
  }

  private random(n: number): Buffer {
    return (this.d.randomBytes ?? crypto.randomBytes)(n)
  }

  private nowMs(): number {
    return (this.d.now ?? Date.now)()
  }

  // ---- server -----------------------------------------------------------------

  get endpoint(): string {
    return this.launch.endpoint
  }

  serverRunning(): boolean {
    const s = this.supervisor?.state
    return s === 'starting' || s === 'ready' || s === 'stopping'
  }

  /** An install, update or rollback is under way (including the short resting steps inside a run). */
  installActive(): boolean {
    return !!this.installer && (isActive(this.installer.state) || this.installRunning)
  }
  private installRunning = false

  snapshot(): ServerSnapshot {
    const s = this.supervisor.snapshot()
    if (s.state === 'stopped' && !this.binaryInstalled()) return { state: 'not-installed', endpoint: this.endpoint, logTail: [] }
    return {
      state: s.state,
      detail: s.detail === undefined ? undefined : this.redact(s.detail),
      logTail: s.logTail.slice(-200).map((l) => this.redact(l)),
      endpoint: this.endpoint,
    }
  }

  /** Starts the server and returns once it is starting; readiness comes as events. */
  async startServer(): Promise<void> {
    if (!this.localManaged()) return
    if (!this.binaryInstalled() || this.installActive()) return
    if (this.serverRunning()) return
    if (this.launchStale) this.buildSupervisor(this.store.get())
    void this.supervisor.start()
  }

  async stopServer(): Promise<void> {
    await this.supervisor.stop()
  }

  async restartServer(): Promise<void> {
    if (this.installActive()) return
    await this.supervisor.stop()
    await this.startServer()
  }

  // ---- install ----------------------------------------------------------------

  installState(): InstallStateView {
    return this.installer.state
  }

  private unsupported(): InstallStateView | undefined {
    if (this.d.target) return undefined
    return { kind: 'failed', reason: 'unknown', message: 'there is no LocalAI server build for this platform', rolledBack: false }
  }

  async checkServerUpdate(o: InstallOptions): Promise<InstallStateView> {
    const no = this.unsupported()
    if (no) return no
    if (!this.localManaged()) return this.installer.state
    const r = await this.installer.check(this.runOptions(o))
    if (r.kind !== 'failed') {
      const at = Date.now()
      await this.store.update((c) => withLastUpdateCheck(c, at)).catch(() => this.hub.write('launcher', 'could not save the update check time', 'warn'))
    }
    return r
  }

  // ---- updates ------------------------------------------------------------------

  async updateInfo(): Promise<UpdateInfoView> {
    const c = this.store.get()
    const channel = updateChannel(c)
    return {
      checkedAt: lastUpdateCheck(c),
      autoCheck: autoCheckUpdates(c),
      previousVersion: await this.installer.rollbackVersion(),
      channel,
      devConfirmed: devChannelConfirmed(c),
      ...(channel === 'dev' ? { devBuild: await this.installer.installedVersion().catch(() => undefined) } : {}),
      remoteDefault: defaultTarget(c) !== LOCAL_TARGET,
    }
  }

  /**
   * Picks the channel of This computer. Development needs the user's
   * confirmation the first time, and again when it follows Stable.
   */
  async setUpdateChannel(channel: UpdateChannel, confirmed = false): Promise<SetChannelResult> {
    const c = this.store.get()
    const cur = updateChannel(c)
    if (channel === cur) return { ok: true, info: await this.updateInfo() }
    if (channel === 'dev' && !confirmed && (!devChannelConfirmed(c) || cur === 'stable')) return { ok: false, reason: 'confirm-required' }
    await this.store.update((x) => (channel === 'dev' ? withDevChannelConfirmed(withUpdateChannel(x, channel)) : withUpdateChannel(x, channel)))
    this.hub.write('launcher', `update channel: ${channel}`)
    // The launcher's own updater follows release candidates only; Development does not change it.
    this.d.onIncludePrereleases?.(channel === 'rc')
    // The server environment differs on Development: the next start uses it.
    if ((cur === 'dev') !== (channel === 'dev')) this.launchChanged()
    return { ok: true, info: await this.updateInfo() }
  }

  /** Leaves Development: Stable, then the stable release replaces the development build. The development build stays as the rollback copy. */
  async backToStable(): Promise<UpdateInfoView> {
    if (updateChannel(this.store.get()) !== 'dev') return this.updateInfo()
    const wasRunning = this.serverRunning()
    if (wasRunning) await this.stopServer()
    await this.setUpdateChannel('stable')
    // The server is stopped now, so its launch values are rebuilt without the development setting.
    this.startAfterInstall = wasRunning
    if (this.localManaged() && this.d.target) this.track(this.installer.install(this.runOptions({})))
    else this.startAfterInstall = false
    return this.updateInfo()
  }

  /** The options of an install or check, from the channel and the caller's one-off choice. */
  private runOptions(o: InstallOptions): { allowPrerelease: boolean; stopFirst?: boolean; dev: boolean } {
    const ch = updateChannel(this.store.get())
    return { allowPrerelease: ch === 'rc' || !!o.allowPrerelease, stopFirst: o.stopFirst, dev: ch === 'dev' }
  }

  channelIsRc(): boolean {
    return updateChannel(this.store.get()) === 'rc'
  }

  async setAutoCheck(on: boolean): Promise<UpdateInfoView> {
    await this.store.update((c) => withAutoCheckUpdates(c, on))
    this.hub.write('launcher', `automatic update check ${on ? 'on' : 'off'}`)
    return this.updateInfo()
  }

  autoCheckOn(): boolean {
    return autoCheckUpdates(this.store.get())
  }

  /** The automatic check is on, a server is installed and the last check is a day old. */
  autoCheckDue(now: number): boolean {
    const c = this.store.get()
    if (!this.localManaged(c) || !autoCheckUpdates(c) || !this.d.target || !this.binaryInstalled() || this.installActive()) return false
    const last = lastUpdateCheck(c)
    return last === undefined || now - last >= AUTO_CHECK_INTERVAL_MS || last > now
  }

  /** Runs the server update check when it is due. Never installs. */
  async autoCheck(now = Date.now()): Promise<boolean> {
    if (!this.autoCheckDue(now)) return false
    this.hub.write('launcher', 'automatic update check')
    await this.checkServerUpdate({})
    return true
  }

  /**
   * The release page of `version`, only when that version is on offer now (a
   * server update, or the launcher update `launcherOffer`). Anything else is
   * refused, so the renderer cannot open an arbitrary page through this.
   */
  releaseNotesUrl(version: string, launcherOffer?: string): string | undefined {
    const st = this.installer.state
    const offered = [st.kind === 'update-available' ? st.version : undefined, launcherOffer].filter((v): v is string => !!v)
    const norm = (v: string) => v.replace(/^v/, '')
    if (!offered.some((o) => norm(o) === norm(version))) return undefined
    return releasePageUrl(this.d.releaseRepo ?? { owner: 'mudler', repo: 'LocalAI' }, version)
  }

  async installDetails(): Promise<InstallDetailsView> {
    const t = this.d.target
    let size: number | undefined
    try {
      size = (await fs.promises.stat(this.serverBinary())).size
    } catch {
      size = undefined
    }
    // Only ask the binary when there is one; it runs `--version` when the metadata is stale.
    const version = size === undefined ? undefined : await this.installer.installedVersion().catch(() => undefined)
    return {
      build: t ? `${t.os} · ${t.arch}` : undefined,
      size,
      installDir: displayPath(this.installPaths().binDir, this.d.home),
      version,
    }
  }

  /** Starts the install and returns the first state; progress comes as events. */
  installServer(o: InstallOptions): InstallStateView {
    const no = this.unsupported()
    if (no) return no
    if (!this.localManaged()) return this.installer.state
    this.track(this.installer.install(this.runOptions(o)))
    return this.installer.state
  }

  cancelInstall(): boolean {
    return this.installer.cancel()
  }

  rollbackServer(o: InstallOptions): InstallStateView {
    if (!this.localManaged()) return this.installer.state
    this.track(this.installer.rollback({ stopFirst: o.stopFirst }))
    return this.installer.state
  }

  private track(run: Promise<InstallStateView>): void {
    this.installRun = run
    this.installRunning = true
    this.webuiInputs({ installActive: true })
    void run.finally(() => {
      if (this.installRun === run) this.installRunning = false
      this.webuiInputs({ installActive: this.installActive() })
    })
  }

  /** Resolves when no install, update or rollback is running. For tests and shutdown. */
  async installIdle(): Promise<InstallStateView> {
    // A run passes through resting states (update-available) on its way, so wait for the run itself.
    while (this.installRun) {
      const run = this.installRun
      await run
      if (this.installRun === run) this.installRun = undefined
    }
    return this.installer.state
  }

  // ---- settings -----------------------------------------------------------------

  getSettings(): LauncherSettings {
    return toSettings(this.store.get())
  }

  async saveSettings(patch: Partial<LauncherSettings>): Promise<SaveSettingsResult> {
    let loginItem: LoginItemResult | undefined
    if (patch.startOnBoot !== undefined && patch.startOnBoot !== this.store.get().start_on_boot && this.d.loginItem) {
      const on = patch.startOnBoot
      loginItem = this.d.loginItem(on)
      this.hub.write('launcher', `launch at login: ${loginItem.status}${loginItem.status === 'ok' ? ` (${on ? 'on' : 'off'})` : ` (${loginItem.reason})`}`, loginItem.status === 'ok' ? 'info' : 'warn')
      // Save only what took effect: the switch never says on when nothing starts at login,
      // nor off while an entry is left. Turning off where there are no login items just saves.
      const tookEffect = loginItem.status === 'ok' || (!on && loginItem.status === 'unavailable')
      if (!tookEffect) {
        const { startOnBoot: _dropped, ...rest } = patch
        patch = rest
      }
    }
    const before = this.store.get()
    // An address that other devices can reach turns the API key on in the same
    // step, as "Allow other devices" in Connect does.
    if (patch.address !== undefined && isExposedAddress(patch.address) && !this.secrets.has(API_KEY_SECRET)) {
      await this.secrets.set(API_KEY_SECRET, generateApiKey((n) => this.random(n)))
      this.hub.write('launcher', 'api key created: the new address reaches other devices')
    }
    const after = await this.store.update((c) => applySettings(c, patch))
    this.hub.write('launcher', `settings saved: ${Object.keys(patch).join(', ') || 'nothing'}`)
    if (patch.secretsPlaintextFile !== undefined && patch.secretsPlaintextFile !== secretsPlaintextFile(before)) {
      await this.secrets.setPlaintextOptIn(patch.secretsPlaintextFile)
      if (patch.secretsPlaintextFile) await this.migrateSecrets((m) => this.hub.write('launcher', m))
    }
    if (after.models_path !== before.models_path || after.backends_path !== before.backends_path) {
      this.ensureFolders([after.models_path, after.backends_path], (m) => this.hub.write('launcher', m, 'warn'))
    }
    let restartRequired = false
    if (needsRestart(before, after)) {
      if (this.serverRunning()) {
        this.launchStale = true
        restartRequired = true
      } else {
        this.buildSupervisor(after)
        this.d.emit('launcher:server:event', this.snapshot())
      }
    }
    if (showStatusBar(before) !== showStatusBar(after)) this.d.emit('launcher:status-bar:event', showStatusBar(after))
    this.webuiInputs({ statusBar: showStatusBar(after), openInBrowser: openWebuiInBrowser(after) })
    return { settings: toSettings(after), restartRequired, ...(loginItem ? { loginItem } : {}) }
  }

  /**
   * Saves a data folder that the main-process folder picker returned, after
   * checkDataFolder. A running server keeps the old folder until a restart
   * (restartRequired).
   */
  async setDataFolder(kind: FolderKind, picked: string): Promise<FolderPickResult> {
    const r = checkDataFolder(picked, this.d.home, this.folderProbe)
    if (!r.ok) {
      this.hub.write('launcher', `${kind} folder not changed: ${r.reason}`, 'warn')
      return { status: 'refused', reason: r.reason }
    }
    const res = await this.saveSettings(kind === 'models' ? { modelsPath: r.path } : { backendsPath: r.path })
    return { status: 'saved', settings: res.settings, restartRequired: res.restartRequired }
  }

  private readonly folderProbe: FolderProbe = {
    isDir: (p) => {
      try {
        return fs.statSync(p).isDirectory()
      } catch (e) {
        return (e as NodeJS.ErrnoException).code === 'ENOENT' ? undefined : false
      }
    },
    writable: (p) => {
      try {
        fs.accessSync(p, fs.constants.W_OK | fs.constants.X_OK)
        return true
      } catch {
        return false
      }
    },
  }

  /**
   * At start: writes an enabled login item again, so it names the file this
   * launcher runs from now (an AppImage that was moved or renamed).
   */
  syncLoginItem(): void {
    if (!this.store.get().start_on_boot || !this.d.loginItem) return
    const r = this.d.loginItem(true)
    if (r.status !== 'ok') this.hub.write('launcher', `launch at login: could not refresh the login item (${r.reason})`, 'warn')
  }

  // ---- network exposure and the API key ----------------------------------------

  private ifaces(): NodeJS.Dict<os.NetworkInterfaceInfo[]> {
    try {
      return (this.d.networkInterfaces ?? os.networkInterfaces)()
    } catch {
      return {}
    }
  }

  /** Connect closed the notice about the key created at start. */
  dismissKeyNotice(): NetworkView {
    this.keyCreatedNotice = false
    return this.networkView()
  }

  secretStorageView(): SecretStorageView {
    return this.secrets.state()
  }

  networkView(): NetworkView {
    const c = this.store.get()
    const exposed = isExposedAddress(c.address)
    const key = this.secrets.get(API_KEY_SECRET)
    return {
      exposed,
      address: c.address,
      port: splitAddress(c.address).port,
      lanAddresses: exposed ? lanAddresses(c.address, this.ifaces()) : [],
      apiKeySet: key !== undefined,
      ...(key !== undefined ? { apiKeyHint: apiKeyHint(key) } : {}),
      restartRequired: this.restartRequired(),
      storage: this.secrets.state(),
      ...(this.keyCreatedNotice ? { keyCreatedAtStart: true } : {}),
    }
  }

  /** What exposing would do. The id must come back with setNetworkExposed(true, id). */
  networkPlan(): NetworkPlanView {
    const now = this.nowMs()
    for (const [id, exp] of this.plans) if (exp <= now) this.plans.delete(id)
    // A handful of open plans is plenty; drop the oldest beyond that.
    while (this.plans.size >= 8) this.plans.delete(this.plans.keys().next().value as string)
    const planId = this.random(16).toString('hex')
    this.plans.set(planId, now + NETWORK_PLAN_TTL_MS)
    const bind = exposedAddress(this.store.get().address)
    return {
      planId,
      bindAddress: bind,
      lanAddresses: lanAddresses(bind, this.ifaces()),
      createsKey: !this.secrets.has(API_KEY_SECRET),
      restartRequired: this.serverRunning(),
      storage: this.secrets.state(),
    }
  }

  async setNetworkExposed(on: boolean, planId?: string): Promise<NetworkChangeResult> {
    if (on) {
      const exp = planId === undefined ? undefined : this.plans.get(planId)
      if (planId !== undefined) this.plans.delete(planId)
      if (exp === undefined || exp <= this.nowMs()) return { ok: false, reason: 'plan-required' }
      // The key first: the server never listens on the network without one.
      let createdKey = false
      if (!this.secrets.has(API_KEY_SECRET)) {
        await this.secrets.set(API_KEY_SECRET, generateApiKey((n) => this.random(n)))
        createdKey = true
      }
      const c = this.store.get()
      const changed = !isExposedAddress(c.address)
      if (changed) await this.store.update((x) => ({ ...x, address: exposedAddress(x.address) }))
      this.hub.write('launcher', `network: other devices allowed${createdKey ? ', api key created' : ''}`)
      if (changed || createdKey) this.launchChanged()
      return { ok: true, network: this.networkView(), createdKey }
    }
    const c = this.store.get()
    if (isExposedAddress(c.address)) {
      // The key stays on: turning it off is not part of this step.
      await this.store.update((x) => ({ ...x, address: loopbackAddress(x.address) }))
      this.hub.write('launcher', 'network: only this computer')
      this.launchChanged()
    }
    return { ok: true, network: this.networkView(), createdKey: false }
  }

  async regenerateApiKey(): Promise<NetworkView> {
    await this.secrets.set(API_KEY_SECRET, generateApiKey((n) => this.random(n)))
    this.hub.write('launcher', 'api key replaced')
    this.launchChanged()
    return this.networkView()
  }

  /** The key, once per request, rate limited. */
  revealApiKey(): RevealKeyResult {
    const key = this.secrets.get(API_KEY_SECRET)
    if (key === undefined) return { ok: false, reason: 'no-key' }
    if (!this.revealLimiter.take(this.nowMs())) {
      this.hub.write('launcher', 'api key: show refused, too many requests', 'warn')
      return { ok: false, reason: 'rate-limited' }
    }
    this.hub.write('launcher', 'api key shown')
    return { ok: true, key }
  }

  /** The text copyApiKey puts on the clipboard, or undefined when there is no key. */
  apiKeyCopyText(format: CopyKeyFormat): string | undefined {
    const key = this.secrets.get(API_KEY_SECRET)
    if (key === undefined) return undefined
    this.hub.write('launcher', `api key copied${format === 'key' ? '' : ` (${format} example)`}`)
    return format === 'key' ? key : snippet(format, this.endpoint, key)
  }

  // ---- environment variables ------------------------------------------------------

  envVarsView(): EnvVarsView {
    return {
      vars: this.secrets.list(ENV_PREFIX).map((n) => {
        const name = n.slice(ENV_PREFIX.length)
        return { name, ignored: envNameProblem(name, this.envIgnoreCase()) !== undefined, sessionOnly: this.secrets.isSessionOnly(n) }
      }),
      restartRequired: this.restartRequired(),
      storage: this.secrets.state(),
    }
  }

  /** Windows: variable names have no case, so `Path` is the controlled PATH. */
  private envIgnoreCase(): boolean {
    return (this.d.platform ?? process.platform) === 'win32'
  }

  async setEnvVar(name: string, value: string): Promise<EnvVarResult> {
    const problem = envNameProblem(name, this.envIgnoreCase())
    if (problem) return { ok: false, reason: problem === 'format' ? 'name-format' : problem === 'length' ? 'name-length' : 'name-controlled' }
    if (value.length > MAX_ENV_VALUE) return { ok: false, reason: 'value-length' }
    if (!this.secrets.has(envSecret(name)) && this.secrets.list(ENV_PREFIX).length >= MAX_ENV_VARS) return { ok: false, reason: 'too-many' }
    await this.secrets.set(envSecret(name), value)
    // An old plain-text value of the same name must not come back at the next start.
    await this.dropConfigEnv(name)
    this.hub.write('launcher', `environment variable ${name} set`)
    this.launchChanged()
    return { ok: true, view: this.envVarsView() }
  }

  async deleteEnvVar(name: string): Promise<EnvVarsView> {
    const had = this.secrets.has(envSecret(name)) || name in this.store.get().environment_vars
    await this.secrets.delete(envSecret(name))
    await this.dropConfigEnv(name)
    if (had) {
      this.hub.write('launcher', `environment variable ${name} removed`)
      this.launchChanged()
    }
    return this.envVarsView()
  }

  private async dropConfigEnv(name: string): Promise<void> {
    if (!(name in this.store.get().environment_vars)) return
    await this.store.update((c) => {
      const env = { ...c.environment_vars }
      delete env[name]
      return { ...c, environment_vars: env }
    })
  }

  // ---- status bar ---------------------------------------------------------------

  statusBarVisible(): boolean {
    return showStatusBar(this.store.get())
  }

  async setStatusBarVisible(visible: boolean): Promise<boolean> {
    const after = await this.store.update((c) => withShowStatusBar(c, visible))
    const v = showStatusBar(after)
    this.d.emit('launcher:status-bar:event', v)
    this.webuiInputs({ statusBar: v })
    return v
  }

  // ---- logs ---------------------------------------------------------------------

  private queueLog(l: LogLine): void {
    this.pendingLogs.push(l)
    if (this.pendingLogs.length > LOG_BATCH_MAX) this.pendingLogs.splice(0, this.pendingLogs.length - LOG_BATCH_MAX)
    if (this.logTimer) return
    this.logTimer = setTimeout(() => this.flushLogs(), this.d.logFlushMs ?? 100)
    this.logTimer.unref?.()
  }

  private flushLogs(): void {
    this.logTimer = undefined
    if (this.pendingLogs.length === 0) return
    this.d.emit('launcher:logs:event', this.pendingLogs.splice(0))
  }

  queryLogs(q: LogQueryInput): LogLineView[] {
    return this.hub.query(q)
  }

  /** `choose` shows the save dialog and returns the path the user picked, or undefined. */
  async exportLogs(choose: () => Promise<string | undefined>): Promise<ExportLogsResult> {
    const dest = await choose()
    if (!dest) return { status: 'cancelled' }
    const r = await this.hub.exportLogs(dest)
    this.hub.write('launcher', `logs exported (${r.files} files)`)
    return { status: 'saved', fileName: path.basename(r.path), bytes: r.bytes }
  }

  /** Updater status changes, from selfUpdate.ts. */
  recordUpdater(s: UpdaterStatus): void {
    const level = s.phase === 'error' ? 'error' : 'info'
    const extra = s.phase === 'downloading' ? ` ${s.percent ?? 0}%` : s.version ? ` ${s.version}` : ''
    this.webuiInputs({ updateReady: s.phase === 'downloaded' })
    if (s.phase === 'downloading' && (s.percent ?? 0) % 10 !== 0) return // keep the file short
    this.hub.write('updater', `${s.phase}${extra}${s.message ? `: ${s.message}` : ''}`, level)
  }

  /** A launcher event for the log, from index.ts. */
  log(message: string, level: 'info' | 'warn' | 'error' = 'info'): void {
    this.hub.write('launcher', message, level)
  }

  // ---- sandbox ------------------------------------------------------------------

  private dispatchNotice(e: NoticeEvent): void {
    const r = reduceNotice(this.notice, e)
    this.notice = r.state
    if (r.persist) {
      const version = r.persist.dismissedVersion
      this.store.update((c) => withSandboxNoticeDismissed(c, version)).catch(() => this.hub.write('launcher', 'sandbox: could not save the dismissed notice', 'warn'))
    }
    this.d.emit('launcher:sandbox:event', this.sandboxView())
    this.webuiInputs({ sandboxNoticeShown: this.notice.kind === 'shown' })
  }

  sandboxView(): SandboxView {
    return { state: this.probe.state, notice: this.notice, profile: this.probe.inputs.profile, setupAvailable: this.attachPattern !== undefined }
  }

  sandboxPlan(kind: SandboxPlanKind): SandboxPlanView {
    if (kind === 'remove') return planView(prepareRemove(), true)
    if (!this.attachPattern) {
      return { kind: 'setup', available: false, profilePath: prepareRemove().profilePath, steps: [], terminalText: '' }
    }
    return planView(prepareSetup(this.attachPattern), true)
  }

  async runSandboxSetup(): Promise<SandboxView> {
    if (!this.attachPattern) return this.sandboxView()
    const before = this.notice
    this.dispatchNotice({ type: 'setup-requested' })
    if (this.notice === before || this.notice.kind !== 'installing') return this.sandboxView()
    this.hub.write('launcher', 'sandbox: profile setup started')
    const r = await runPlan(prepareSetup(this.attachPattern), this.d.sandboxRunner)
    this.hub.write('launcher', `sandbox: profile setup ${r.status}`, r.status === 'ok' ? 'info' : 'warn')
    this.dispatchNotice({ type: 'setup-finished', outcome: setupOutcome(r) })
    return this.sandboxView()
  }

  async runSandboxRemove(): Promise<SandboxView> {
    this.dispatchNotice({ type: 'remove-requested' })
    if (this.notice.kind !== 'removing') return this.sandboxView()
    this.hub.write('launcher', 'sandbox: profile removal started')
    const r = await runPlan(prepareRemove(), this.d.sandboxRunner)
    this.hub.write('launcher', `sandbox: profile removal ${r.status}`, r.status === 'ok' ? 'info' : 'warn')
    this.dispatchNotice({ type: 'remove-finished', outcome: setupOutcome(r) })
    return this.sandboxView()
  }

  async dismissSandboxNotice(): Promise<SandboxView> {
    this.dispatchNotice({ type: 'dismiss', launcherVersion: this.d.launcherVersion })
    await this.store.idle()
    return this.sandboxView()
  }

  // ---- WebUI view ---------------------------------------------------------------

  private localOrigin(): string {
    // The endpoint is always http on a host and port; fall back to the text if it is not parseable.
    return httpOrigin(this.launch.endpoint) ?? this.launch.endpoint
  }

  private currentWebuiInputs(): WebuiInputs {
    const c = this.store.get()
    return {
      server: this.supervisor.state,
      localOrigin: this.localOrigin(),
      installActive: this.installActive(),
      statusBar: showStatusBar(c),
      updateReady: false,
      sandboxOff: this.probe.state === 'unsandboxed-restricted' || this.probe.state === 'unsandboxed-other',
      sandboxNoticeShown: this.notice.kind === 'shown',
      openInBrowser: openWebuiInBrowser(c),
      workerMode: false,
      serverUpdate: this.serverUpdateOffered(),
      startedHidden: false,
    }
  }

  /** An update of an installed server is offered (it brings a hidden bar back). */
  private serverUpdateOffered(): boolean {
    return !!this.installer && this.installer.state.kind === 'update-available' && this.binaryInstalled()
  }

  /** The window did not show at start (tray start or login item). See WebuiInputs.startedHidden. */
  setStartedHidden(hidden: boolean): void {
    this.webuiInputs({ startedHidden: hidden })
  }

  private webuiInputs(patch: Partial<WebuiInputs>): void {
    if (!this.webui) return
    this.webuiDispatch({ type: 'inputs', patch })
  }

  /** Feeds one event to the model, applies its effects and tells the shell when the state changed. */
  webuiDispatch(e: WebuiEvent): WebuiViewState {
    if (!this.webui) throw new Error('core not initialised')
    const fx = this.webui.dispatch(e)
    if (fx.length) {
      for (const f of fx) this.hub.write('launcher', `webui: ${f.kind}${f.kind === 'show' ? ` ${f.origin === this.localOrigin() ? 'local' : 'remote'}` : ''}`)
      this.d.webuiEffects?.(fx)
    }
    if (this.switching?.step === 'loading' && (e.type === 'view-loaded' || e.type === 'view-load-failed')) void this.finishSwitch()
    const s = this.webui.state()
    const json = JSON.stringify(s)
    if (json !== this.lastWebuiJson) {
      this.lastWebuiJson = json
      this.d.emit('launcher:webui:event', s)
    }
    return s
  }

  webuiState(): WebuiViewState {
    if (!this.webui) throw new Error('core not initialised')
    return this.webui.state()
  }

  /** The origin the WebUI view shows, or undefined when there is none. Read by the view's policy handlers. */
  webuiViewOrigin(): string | undefined {
    return this.webui?.viewOrigin
  }

  setShellScreen(screen: ShellScreen | null): WebuiViewState {
    return this.webuiDispatch({ type: 'screen', screen })
  }

  setWebuiOverlay(open: boolean): WebuiViewState {
    return this.webuiDispatch({ type: 'overlay', open })
  }

  /** The heights of the shell's strips around the WebUI view (already validated). */
  setShellInsets(insets: ShellInsets): void {
    this.webuiDispatch({ type: 'insets', insets })
  }

  openWebuiInBrowser(): void {
    this.webuiDispatch({ type: 'open-in-browser', currentUrl: this.d.webuiCurrentUrl?.() })
  }

  /** Open Chat and deep links: `/app`, or `/app?model=<id>`, in the view or the browser. */
  openChat(model?: string): void {
    this.webuiDispatch({ type: 'deep-link', url: chatUrl(this.localOrigin(), model) })
  }

  /** Opens a saved server (it becomes the default), or the default again when there is no id. */
  async connectRemote(id?: string): Promise<SwitchResult> {
    return this.switchTo(id ?? defaultTarget(this.store.get()), false)
  }

  /** "Back to this computer": This computer becomes the default and starts. */
  async backToLocal(): Promise<WebuiViewState> {
    await this.switchTo(LOCAL_TARGET, false)
    return this.webuiState()
  }

  reloadWebui(): WebuiViewState {
    return this.webuiDispatch({ type: 'reload' })
  }

  // ---- window settings ----------------------------------------------------------

  windowSettings(): WindowSettingsView {
    const c = this.store.get()
    const store = webuiPermissions(c)
    return {
      openInBrowser: openWebuiInBrowser(c),
      desktopNotifications: desktopNotifications(c),
      media: recall(store, this.localOrigin(), 'media'),
      anyRemembered: Object.keys(store).length > 0,
    }
  }

  async setOpenInBrowser(on: boolean): Promise<WindowSettingsView> {
    await this.saveSettings({ openWebuiInBrowser: on })
    return this.windowSettings()
  }

  async setDesktopNotifications(on: boolean): Promise<WindowSettingsView> {
    await this.store.update((c) => withDesktopNotifications(c, on))
    this.hub.write('launcher', `desktop notifications ${on ? 'on' : 'off'}`)
    this.d.notificationTargetsChanged?.()
    return this.windowSettings()
  }

  /**
   * The servers whose notification inbox the launcher watches: the local
   * server while it runs, and every saved remote server. `all` is the number
   * of configured servers; the notifier puts the server name in a title when
   * it is more than one. The local server signs in with the API key, a remote
   * one with the cookies of its WebUI session.
   */
  notificationTargets(): { enabled: boolean; targets: NotifyTarget[]; all: number } {
    const c = this.store.get()
    const list = remotes(c)
    const targets: NotifyTarget[] = []
    if (this.supervisor?.state === 'ready') {
      targets.push({
        id: LOCAL_TARGET,
        name: 'This computer',
        origin: this.localOrigin(),
        headers: (): Record<string, string> => {
          const key = this.secrets.get(API_KEY_SECRET)
          return key ? { authorization: `Bearer ${key}` } : {}
        },
      })
    }
    for (const r of list) targets.push({ id: r.id, name: r.name, origin: r.url, ...(this.d.remoteFetch ? { fetch: this.d.remoteFetch(r.url) } : {}) })
    return { enabled: desktopNotifications(c), targets, all: 1 + list.length }
  }

  /**
   * A click on a notification: shows the notification's page. The link is an
   * in-app path of the server that sent it and is checked again here; the
   * view model decides where it opens (the window for the server on show, the
   * system browser for another one).
   */
  openNotificationLink(targetId: string, link: string): boolean {
    const t = this.notificationTargets().targets.find((x) => x.id === targetId)
    const url = t ? resolveLink(t.origin, link) : undefined
    if (!url) return false
    this.webuiDispatch({ type: 'deep-link', url })
    return true
  }

  permissionStore(): PermissionStore {
    return webuiPermissions(this.store.get())
  }

  async rememberPermission(origin: string, choice: PermissionChoice): Promise<void> {
    await this.store.update((c) => withWebuiPermissions(c, remember(webuiPermissions(c), origin, 'media', choice)))
    this.hub.write('launcher', `webui: ${choice === 'allow' ? 'allowed' : 'blocked'} microphone and camera for a server, remembered`)
  }

  async resetWebuiPermissions(): Promise<void> {
    await this.store.update((c) => withWebuiPermissions(c, resetPermissions(webuiPermissions(c))))
    this.hub.write('launcher', 'webui: remembered permissions reset')
  }

  // ---- stop ---------------------------------------------------------------------

  async shutdown(): Promise<void> {
    this.monitor?.stop()
    for (const ac of this.openTests) ac.abort()
    this.installer?.cancel()
    try {
      await this.supervisor?.stop()
    } finally {
      this.serverLines.flush((l) => this.hub?.write('server', l))
      this.hub?.write('launcher', 'launcher stopped')
      this.flushLogs()
      await this.store?.idle()
      await this.secrets?.idle()
      this.hub?.close()
    }
  }
}
