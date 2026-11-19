// A fake bridge for the component tests and the screenshot harness. It answers
// every call from a fixed state, records the calls, and can push events after a
// listener subscribes. No server and no main process.
import type {
  EnvVarsView,
  FolderRefusal,
  HardwareSummaryView,
  InstallStateView,
  LauncherInfo,
  LauncherSettings,
  LogLineView,
  LoginItemView,
  SandboxPlanView,
  SandboxView,
  ServerSnapshot,
  PortProcessView,
  SecretStorageView,
  ServerChangeResult,
  ServerRowView,
  ServersView,
  SwitchResult,
  TestOutcome,
  TestResultView,
  UpdateChannelName,
  UpdaterStatus,
  WebuiViewState,
} from '../../shared/types'
import type {
  Bridge,
  BridgeExtras,
  InstallDetailsView,
  NetworkView,
  OptionalApi,
  UpdateInfoView,
  WindowSettingsView,
  WorkerView,
} from '../bridge'

export interface FakeState {
  server: ServerSnapshot
  info: LauncherInfo
  updater: UpdaterStatus
  install: InstallStateView
  sandbox: SandboxView
  settings: LauncherSettings
  statusBar: boolean
  logs: LogLineView[]
  plan: SandboxPlanView
  removePlan: SandboxPlanView
  webui?: WebuiViewState
  worker?: WorkerView
  windowSettings?: WindowSettingsView
  updateInfo?: UpdateInfoView
  installDetails?: InstallDetailsView
  hardware?: HardwareSummaryView
  portProcess?: PortProcessView
  network?: NetworkView
  /** The key the fake main process holds. Only revealApiKey hands it out, as the real one does. */
  apiKey?: string
  envVars?: EnvVarsView
  /** Where secrets go. Default: the keychain. */
  storage?: SecretStorageView
  freePort?: number | null
  /** Install events pushed, in order, right after the renderer subscribes. */
  installEvents?: InstallStateView[]
  /** What runSandboxSetup answers. Default: the state, unchanged (setup refused). */
  setupResult?: SandboxView
  /** What turning "Launch at login" on or off gives. Default: ok. */
  loginItem?: LoginItemView
  /** The saved servers as the main process reports them. Default: This computer only. */
  servers?: ServersView
  /** What Test answers per origin; `testResult` is the answer for every other address. */
  testResults?: Record<string, TestResultView>
  testResult?: TestResultView
  /** What the folder picker returns: a folder (saved as the main process would), a refusal, or cancel (default). */
  pickedFolder?: string | { refused: FolderRefusal }
}

export const ENDPOINT = 'http://127.0.0.1:8080'

export const DEFAULT_SETTINGS: LauncherSettings = {
  modelsPath: '~/.localai/models',
  backendsPath: '~/.localai/backends',
  address: '127.0.0.1:8080',
  autoStartServer: true,
  startOnBoot: false,
  logLevel: 'info',
  showWelcome: false,
  showStatusBar: true,
  openWebuiInBrowser: false,
  remoteServerUrl: '',
  secretsPlaintextFile: false,
}

export const KEYCHAIN: SecretStorageView = { backend: 'keychain', encryptionAvailable: true, plaintextOptIn: false, unreadable: 0 }
export const NO_KEYRING: SecretStorageView = { backend: 'session', encryptionAvailable: false, plaintextOptIn: false, unreadable: 0 }
/** The one plan id the fake main process accepts. */
export const FAKE_PLAN_ID = '0123456789abcdef0123456789abcdef'
export const FAKE_LAN = 'http://192.0.2.24:8080'

/** The network view the main process sends: by default this computer only, no key. */
export function networkView(over: Partial<NetworkView> = {}): NetworkView {
  return {
    exposed: false,
    address: '127.0.0.1:8080',
    port: 8080,
    lanAddresses: [],
    apiKeySet: false,
    restartRequired: false,
    storage: KEYCHAIN,
    ...over,
  }
}

const hintOf = (key: string) => ({ prefix: key.startsWith('lai-') ? 'lai-' : '', last4: key.slice(-4) })

/** A WebUI view state as the main process sends it: by default the local page is loaded and on show. */
export function webuiView(over: Partial<WebuiViewState> = {}): WebuiViewState {
  return {
    placement: 'embedded',
    target: { kind: 'local', origin: ENDPOINT },
    layout: 'webui',
    home: 'webui',
    screen: null,
    barVisible: true,
    barForced: false,
    shellAbove: false,
    overlay: false,
    dot: null,
    banner: null,
    insets: { top: 0, bottom: 0 },
    problem: null,
    loaded: true,
    remote: null,
    ...over,
  }
}

/** What the main process would say for a server state when a fixture names none. */
function defaultWebui(s: FakeState): WebuiViewState {
  const up = s.server.state === 'ready' && !s.windowSettings?.openInBrowser
  return webuiView({ layout: up ? 'webui' : 'shell-only', home: up ? 'webui' : 'shell-only', loaded: up, barVisible: s.statusBar })
}

export function baseState(over: Partial<FakeState> = {}): FakeState {
  return {
    server: { state: 'ready', endpoint: ENDPOINT, logTail: [] },
    info: { version: '4.1.3', packaged: true, trayActive: true, sandboxed: true },
    updater: { phase: 'not-available' },
    install: { kind: 'up-to-date', version: '4.1.3' },
    sandbox: { state: 'sandboxed', notice: { kind: 'hidden' }, profile: 'unknown', setupAvailable: false },
    settings: DEFAULT_SETTINGS,
    statusBar: true,
    logs: [],
    plan: { kind: 'setup', available: false, profilePath: '/etc/apparmor.d/localai-launcher', steps: [], terminalText: '' },
    removePlan: {
      kind: 'remove',
      available: true,
      profilePath: '/etc/apparmor.d/localai-launcher',
      steps: [{ description: 'Unload and delete the profile', command: 'pkexec sh -c "apparmor_parser -R /etc/apparmor.d/localai-launcher && rm /etc/apparmor.d/localai-launcher"' }],
      terminalText: '',
    },
    ...over,
  }
}

/** This computer as a row of the server list. */
export function localRow(over: Partial<ServerRowView> = {}): ServerRowView {
  return { id: 'local', kind: 'local', name: 'This computer', host: '127.0.0.1:8080', version: '4.1.3', isDefault: true, isCurrent: true, insecure: false, status: 'running', ...over }
}

/** A saved server as a row. */
export function remoteRow(id: string, name: string, origin: string, over: Partial<ServerRowView> = {}): ServerRowView {
  return { id, kind: 'remote', name, host: new URL(origin).host, origin, isDefault: false, isCurrent: false, insecure: origin.startsWith('http:'), status: 'not-tested', ...over }
}

export function serversView(over: Partial<ServersView> = {}): ServersView {
  return { rows: [localRow()], defaultId: 'local', keepLocal: false, firstRun: false, switching: null, ...over }
}

export interface FakeBridge extends Bridge {
  calls: { name: string; args: unknown[] }[]
  state: FakeState
  /** Push a server snapshot to the listeners. */
  emitServer(s: ServerSnapshot): void
  emitInstall(s: InstallStateView): void
  emitSandbox(s: SandboxView): void
  emitStatusBar(v: boolean): void
  emitLogs(l: LogLineView[]): void
  emitServers(v: ServersView): void
  /** The menu accelerator was pressed. */
  emitOpenSwitcher(): void
}

type Listener<T> = Set<(v: T) => void>

export type OptionalName = keyof BridgeExtras | OptionalApi

/**
 * `extras` picks the optional methods the bridge has: true for all, false for
 * none, or a list. `extras: false` is the bridge as the current preload has it
 * minus the optional LauncherApi methods, so the tests see that every control
 * that needs one hides.
 */
export function fakeBridge(state: FakeState, opts: { extras?: boolean | OptionalName[] } = {}): FakeBridge {
  const calls: FakeBridge['calls'] = []
  const ls = {
    server: new Set() as Listener<ServerSnapshot>,
    install: new Set() as Listener<InstallStateView>,
    sandbox: new Set() as Listener<SandboxView>,
    bar: new Set() as Listener<boolean>,
    logs: new Set() as Listener<LogLineView[]>,
    updater: new Set() as Listener<UpdaterStatus>,
    webui: new Set() as Listener<WebuiViewState>,
    servers: new Set() as Listener<ServersView>,
    switcher: new Set() as Listener<void>,
  }
  const rec = <T>(name: string, value: T, ...args: unknown[]): Promise<T> => {
    calls.push({ name, args })
    return Promise.resolve(value)
  }
  const sub = <T>(set: Listener<T>, cb: (v: T) => void) => {
    set.add(cb)
    return () => void set.delete(cb)
  }
  const s = state
  const webui = () => s.webui ?? defaultWebui(s)
  const storage = () => s.storage ?? KEYCHAIN
  const net = (over: Partial<NetworkView> = {}) => ({ ...networkView({ storage: storage() }), ...s.network, ...over })
  const env = (): EnvVarsView => s.envVars ?? { vars: [], restartRequired: false, storage: storage() }
  const servers = (): ServersView => s.servers ?? serversView()
  const pushServers = () => ls.servers.forEach((cb) => cb(servers()))
  const info = (): UpdateInfoView => ({ autoCheck: true, channel: 'stable', devConfirmed: false, remoteDefault: servers().defaultId !== 'local', ...s.updateInfo })
  // Without a fixed WebUI state, the fake main process follows the server and the bar choice.
  const pushWebui = () => {
    if (!s.webui) ls.webui.forEach((cb) => cb(webui()))
  }

  const core: Bridge = {
    info: () => rec('info', s.info),
    getServer: () => rec('getServer', s.server),
    start: () => rec('start', undefined),
    stop: () => rec('stop', undefined),
    restart: () => rec('restart', undefined),
    onServer: (cb) => sub(ls.server, cb),
    listModels: () => rec('listModels', [] as string[]),
    openChat: (m) => rec('openChat', undefined, m),
    copyText: (t) => rec('copyText', undefined, t),
    getUpdater: () => rec('getUpdater', s.updater),
    checkForLauncherUpdate: () => rec('checkForLauncherUpdate', undefined),
    onUpdater: (cb) => sub(ls.updater, cb),
    getInstallState: () => rec('getInstallState', s.install),
    checkServerUpdate: (o) => rec('checkServerUpdate', s.install, o),
    installServer: (o) => rec('installServer', s.install, o),
    cancelInstall: () => rec('cancelInstall', true),
    rollbackServer: (o) => rec('rollbackServer', s.install, o),
    onInstall: (cb) => {
      const off = sub(ls.install, cb)
      const evs = s.installEvents
      if (evs?.length) setTimeout(() => evs.forEach((e) => cb(e)), 0)
      return off
    },
    getSettings: () => rec('getSettings', s.settings),
    saveSettings: (patch) => {
      // As the main process does: "Launch at login" is saved only when it took effect.
      const login = patch.startOnBoot !== undefined && patch.startOnBoot !== s.settings.startOnBoot ? (s.loginItem ?? { status: 'ok' as const }) : undefined
      const kept = login && login.status !== 'ok' && !(patch.startOnBoot === false && login.status === 'unavailable')
      const applied = kept ? { ...patch, startOnBoot: s.settings.startOnBoot } : patch
      s.settings = { ...s.settings, ...applied }
      if (patch.secretsPlaintextFile !== undefined && !storage().encryptionAvailable) {
        s.storage = { ...storage(), backend: patch.secretsPlaintextFile ? 'plaintext-file' : 'session', plaintextOptIn: patch.secretsPlaintextFile }
        if (s.envVars) s.envVars = { ...s.envVars, storage: s.storage, vars: s.envVars.vars.map((v) => ({ ...v, sessionOnly: !patch.secretsPlaintextFile })) }
      }
      return rec('saveSettings', { settings: s.settings, restartRequired: 'address' in patch, ...(login ? { loginItem: login } : {}) }, patch)
    },
    queryLogs: (q) => rec('queryLogs', s.logs, q),
    exportLogs: () => rec('exportLogs', { status: 'saved' as const, fileName: 'localai-logs.txt', bytes: 1024 }),
    onLogs: (cb) => sub(ls.logs, cb),
    getSandboxState: () => rec('getSandboxState', s.sandbox),
    getSandboxSetupPlan: (k) => rec('getSandboxSetupPlan', k === 'remove' ? s.removePlan : s.plan, k),
    runSandboxSetup: () => rec('runSandboxSetup', s.setupResult ?? s.sandbox),
    runSandboxRemove: () => rec('runSandboxRemove', { ...s.sandbox, notice: { kind: 'removed' as const } }),
    dismissSandboxNotice: () => rec('dismissSandboxNotice', { ...s.sandbox, notice: { kind: 'dismissed' as const } }),
    onSandbox: (cb) => sub(ls.sandbox, cb),
    getStatusBarVisible: () => rec('getStatusBarVisible', s.statusBar),
    setStatusBarVisible: (v) => {
      s.statusBar = v
      setTimeout(pushWebui, 0)
      return rec('setStatusBarVisible', v, v)
    },
    onStatusBar: (cb) => sub(ls.bar, cb),
    firstRender: (label) => void calls.push({ name: 'firstRender', args: [label] }),
  }

  const extras: BridgeExtras & Pick<Required<Bridge>, OptionalApi> = {
    getWebui: () => rec('getWebui', webui()),
    onWebui: (cb) => sub(ls.webui, cb),
    backToLocal: () => rec('backToLocal', webui()),
    openWebuiInBrowser: () => rec('openWebuiInBrowser', undefined),
    setShellScreen: (screen) => rec('setShellScreen', webui(), screen),
    setOverlay: (open) => rec('setOverlay', webui(), open),
    setInsets: (insets) => void calls.push({ name: 'setInsets', args: [insets] }),
    connectRemote: (id) => rec('connectRemote', { ok: true as const, view: servers() } as SwitchResult, id),
    reloadWebui: () => rec('reloadWebui', webui()),
    resetWebuiPermissions: () => {
      s.windowSettings = { openInBrowser: s.windowSettings?.openInBrowser ?? false, desktopNotifications: s.windowSettings?.desktopNotifications ?? true, anyRemembered: false }
      return rec('resetWebuiPermissions', undefined)
    },
    getWindowSettings: () => rec('getWindowSettings', s.windowSettings ?? { openInBrowser: false, desktopNotifications: true, anyRemembered: false }),
    setOpenInBrowser: (on) => {
      s.windowSettings = { ...(s.windowSettings ?? { desktopNotifications: true, anyRemembered: false }), openInBrowser: on }
      return rec('setOpenInBrowser', s.windowSettings, on)
    },
    setDesktopNotifications: (on) => {
      s.windowSettings = { ...(s.windowSettings ?? { openInBrowser: false, anyRemembered: false }), desktopNotifications: on }
      return rec('setDesktopNotifications', s.windowSettings, on)
    },
    getUpdateInfo: () => rec('getUpdateInfo', info()),
    setAutoCheck: (on) => rec('setAutoCheck', { ...info(), autoCheck: on }, on),
    setUpdateChannel: (channel: UpdateChannelName, confirmed?: boolean) => {
      const cur = info()
      if (channel === 'dev' && cur.channel !== 'dev' && !confirmed && (!cur.devConfirmed || cur.channel === 'stable')) {
        return rec('setUpdateChannel', { ok: false as const, reason: 'confirm-required' as const }, ...(confirmed === undefined ? [channel] : [channel, confirmed]))
      }
      s.updateInfo = { ...cur, channel, devConfirmed: cur.devConfirmed || channel === 'dev', ...(channel === 'dev' ? { devBuild: cur.devBuild ?? '3f9a1c2' } : { devBuild: undefined }) }
      return rec('setUpdateChannel', { ok: true as const, info: s.updateInfo }, ...(confirmed === undefined ? [channel] : [channel, confirmed]))
    },
    backToStable: () => {
      s.updateInfo = { ...info(), channel: 'stable', devBuild: undefined, previousVersion: info().devBuild ?? '3f9a1c2' }
      return rec('backToStable', s.updateInfo)
    },
    getServers: () => rec('getServers', servers()),
    onServers: (cb) => sub(ls.servers, cb),
    onOpenSwitcher: (cb) => sub(ls.switcher as Listener<void>, cb as () => void),
    testServer: (input) => {
      const origin = 'id' in input ? servers().rows.find((r) => r.id === input.id)?.origin : fakeOrigin(input.address)
      if (!origin) return rec('testServer', { ok: false as const, reason: 'not-found' as const } as TestOutcome, input)
      const result = s.testResults?.[origin] ?? s.testResult ?? { status: 'ok' as const, version: '4.2.1', secure: origin.startsWith('https:') }
      return rec('testServer', { ok: true as const, origin, host: new URL(origin).host, insecure: origin.startsWith('http:') && !/^http:\/\/(127\.|localhost|\[::1\])/.test(origin), result } as TestOutcome, input)
    },
    addServer: async (input) => {
      const origin = fakeOrigin(input.address)
      if (!origin) return rec('addServer', { ok: false as const, reason: 'invalid-url' as const } as ServerChangeResult, input)
      const dup = servers().rows.find((r) => r.origin === origin)
      if (dup) return rec('addServer', { ok: false as const, reason: 'duplicate' as const, existingId: dup.id } as ServerChangeResult, input)
      const result = s.testResults?.[origin] ?? s.testResult ?? { status: 'ok' as const, version: '4.2.1', secure: origin.startsWith('https:') }
      const pass = result.status === 'ok' || result.status === 'needs-signin'
      if (!input.skipTest && !pass) return rec('addServer', { ok: false as const, reason: 'test-failed' as const, test: result } as ServerChangeResult, input)
      const id = `srv_${String(servers().rows.length).padStart(8, '0')}`
      const row = remoteRow(id, input.name, origin, { status: input.skipTest ? 'not-tested' : pass ? (result.status === 'ok' ? 'connected' : 'needs-signin') : 'not-tested' })
      s.servers = { ...servers(), rows: [...servers().rows, row] }
      if (input.makeDefault) s.servers = withDefault(servers(), id)
      pushServers()
      return rec('addServer', { ok: true as const, view: servers(), id, test: input.skipTest ? undefined : result } as ServerChangeResult, input)
    },
    updateServer: (input) => {
      const v = servers()
      if (!v.rows.some((r) => r.id === input.id)) return rec('updateServer', { ok: false as const, reason: 'not-found' as const } as ServerChangeResult, input)
      s.servers = { ...v, rows: v.rows.map((r) => (r.id === input.id ? { ...r, ...(input.name ? { name: input.name } : {}), ...(input.address ? remoteRow(r.id, input.name ?? r.name, fakeOrigin(input.address) ?? r.origin ?? '', { isDefault: r.isDefault, isCurrent: r.isCurrent }) : {}) } : r)) }
      pushServers()
      return rec('updateServer', { ok: true as const, view: servers(), id: input.id } as ServerChangeResult, input)
    },
    removeServer: (id) => {
      const v = servers()
      const gone = v.rows.find((r) => r.id === id)
      let next: ServersView = { ...v, rows: v.rows.filter((r) => r.id !== id) }
      if (gone?.isDefault) next = withDefault(next, 'local')
      s.servers = next
      pushServers()
      return rec('removeServer', { ok: true as const, view: next } as ServerChangeResult, id)
    },
    setDefaultServer: (target) => {
      s.servers = withDefault(servers(), target)
      pushServers()
      return rec('setDefaultServer', { ok: true as const, view: servers() } as SwitchResult, target)
    },
    cancelSwitch: () => rec('cancelSwitch', undefined),
    retryRemote: () => rec('retryRemote', undefined),
    setKeepLocalRunning: (on) => {
      s.servers = { ...servers(), keepLocal: on }
      pushServers()
      return rec('setKeepLocalRunning', servers(), on)
    },
    openReleaseNotes: (v) => rec('openReleaseNotes', undefined, v),
    getInstallDetails: () => rec('getInstallDetails', s.installDetails ?? {}),
    installLauncherUpdate: () => rec('installLauncherUpdate', undefined),
    relaunch: () => rec('relaunch', undefined),
    findFreePort: (from) => rec('findFreePort', s.freePort === undefined ? from : s.freePort, from),
    showPortProcess: (p) => rec('showPortProcess', s.portProcess ?? { status: 'unavailable' as const }, p),
    pickFolder: (k) => {
      const p = s.pickedFolder
      if (p === undefined) return rec('pickFolder', { status: 'cancelled' as const }, k)
      if (typeof p !== 'string') return rec('pickFolder', { status: 'refused' as const, reason: p.refused }, k)
      s.settings = { ...s.settings, ...(k === 'models' ? { modelsPath: p } : { backendsPath: p }) }
      return rec('pickFolder', { status: 'saved' as const, settings: s.settings, restartRequired: s.server.state === 'ready' }, k)
    },
    getHardwareSummary: () => (s.hardware ? rec('getHardwareSummary', s.hardware) : Promise.reject(new Error('none'))),
    getNetwork: () => rec('getNetwork', net()),
    getNetworkPlan: () =>
      rec('getNetworkPlan', {
        planId: FAKE_PLAN_ID,
        bindAddress: `0.0.0.0:${net().port}`,
        lanAddresses: [FAKE_LAN],
        createsKey: !net().apiKeySet,
        restartRequired: s.server.state === 'ready',
        storage: storage(),
      }),
    setNetworkExposed: (on, planId) => {
      if (on && planId !== FAKE_PLAN_ID) return rec('setNetworkExposed', { ok: false as const, reason: 'plan-required' as const }, on, planId)
      const createdKey = on && !s.apiKey
      if (createdKey) s.apiKey = 'lai-NEWexampleKEYexampleKEYexampleKEYexample0new'
      s.network = net({
        exposed: on,
        address: `${on ? '0.0.0.0' : '127.0.0.1'}:${net().port}`,
        lanAddresses: on ? [FAKE_LAN] : [],
        apiKeySet: !!s.apiKey,
        apiKeyHint: s.apiKey ? hintOf(s.apiKey) : undefined,
      })
      return rec('setNetworkExposed', { ok: true as const, network: s.network, createdKey }, ...(planId === undefined ? [on] : [on, planId]))
    },
    regenerateApiKey: () => {
      s.apiKey = 'lai-REGENexampleKEYexampleKEYexampleKEYexample9b1c'
      s.network = net({ apiKeySet: true, apiKeyHint: hintOf(s.apiKey), restartRequired: s.server.state === 'ready' })
      return rec('regenerateApiKey', s.network)
    },
    dismissKeyNotice: () => {
      const { keyCreatedAtStart: _gone, ...rest } = net()
      s.network = rest
      return rec('dismissKeyNotice', s.network)
    },
    revealApiKey: () => rec('revealApiKey', s.apiKey ? { ok: true as const, key: s.apiKey } : { ok: false as const, reason: 'no-key' as const }),
    copyApiKey: (format) => rec('copyApiKey', s.apiKey ? { ok: true as const } : { ok: false as const, reason: 'no-key' as const }, format),
    getEnvVars: () => rec('getEnvVars', env()),
    setEnvVar: (name, value) => {
      const vars = env().vars.filter((v) => v.name !== name)
      s.envVars = { ...env(), vars: [...vars, { name, ignored: false, sessionOnly: storage().backend === 'session' }].sort((a, b) => a.name.localeCompare(b.name)), restartRequired: s.server.state === 'ready' }
      return rec('setEnvVar', { ok: true as const, view: s.envVars }, name, value)
    },
    deleteEnvVar: (name) => {
      s.envVars = { ...env(), vars: env().vars.filter((v) => v.name !== name), restartRequired: s.server.state === 'ready' }
      return rec('deleteEnvVar', s.envVars, name)
    },
    getSecretStorageState: () => rec('getSecretStorageState', storage()),
    getWorker: () => (s.worker ? rec('getWorker', s.worker) : Promise.reject(new Error('none'))),
    onWorker: () => () => undefined,
    setRunMode: (m) => rec('setRunMode', { ...(s.worker as WorkerView), mode: m }, m),
    testWorker: (c) => rec('testWorker', { ...(s.worker as WorkerView), status: 'reachable' as const }, c),
    startWorker: (c) => rec('startWorker', { ...(s.worker as WorkerView), running: true, status: 'connected' as const }, c),
    stopWorker: () => rec('stopWorker', { ...(s.worker as WorkerView), running: false, status: 'idle' as const }),
  }

  const pick = opts.extras ?? true
  const chosen: Partial<typeof extras> =
    pick === true ? extras : pick === false ? {} : Object.fromEntries(pick.map((k) => [k, extras[k]]))

  return Object.assign(core, chosen, {
    calls,
    state,
    emitServer: (v: ServerSnapshot) => {
      s.server = v
      ls.server.forEach((cb) => cb(v))
      pushWebui()
    },
    emitInstall: (v: InstallStateView) => ls.install.forEach((cb) => cb(v)),
    emitSandbox: (v: SandboxView) => ls.sandbox.forEach((cb) => cb(v)),
    emitStatusBar: (v: boolean) => {
      s.statusBar = v
      ls.bar.forEach((cb) => cb(v))
      pushWebui()
    },
    emitLogs: (v: LogLineView[]) => ls.logs.forEach((cb) => cb(v)),
    emitServers: (v: ServersView) => {
      s.servers = v
      pushServers()
    },
    emitOpenSwitcher: () => ls.switcher.forEach((cb) => cb()),
  }) as FakeBridge
}

/** The origin of a typed address, or undefined. The fake accepts http and https. */
function fakeOrigin(address: string): string | undefined {
  try {
    const u = new URL(address.trim())
    return u.protocol === 'http:' || u.protocol === 'https:' ? u.origin : undefined
  } catch {
    return undefined
  }
}

/** The list after `id` became the default. */
export function withDefault(v: ServersView, id: string): ServersView {
  return { ...v, defaultId: id, switching: null, rows: v.rows.map((r) => ({ ...r, isDefault: r.id === id, isCurrent: r.id === id })) }
}
