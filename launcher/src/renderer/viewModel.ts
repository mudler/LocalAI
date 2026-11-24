// Pure mapping from the bridge state to what the shell shows. No React, no
// bridge calls: every rule here is a function of its inputs, so the tests can
// walk every server, install, updater and sandbox state.
import type {
  InstallFailReason,
  InstallStateView,
  LauncherInfo,
  LauncherSettings,
  SandboxPlanView,
  SandboxView,
  ServersView,
  ServerSnapshot,
  ServerState,
  HardwareSummaryView,
  PortProcessView,
  UpdateInfoView,
  UpdaterStatus,
  WebuiViewState,
} from '../shared/types'
import type { WindowSettingsView, WorkerView } from './bridge'
import { S } from './strings'

export type Tone = 'ok' | 'warn' | 'error' | 'idle' | 'accent'

export interface ShellInput {
  server: ServerSnapshot
  info?: LauncherInfo
  updater?: UpdaterStatus
  install?: InstallStateView
  sandbox?: SandboxView
  settings?: LauncherSettings
  webui?: WebuiViewState
  worker?: WorkerView
  windowSettings?: WindowSettingsView
  /** The saved status bar choice; undefined until the main process answers. */
  statusBarSaved?: boolean
  /** The server version on disk, when the launcher knows it. */
  installedVersion?: string
  /** The person saw the result of a failed install or update and closed it. */
  installFailureSeen?: boolean
  /** The saved servers, the default and a switch under way. */
  servers?: ServersView
  updateInfo?: UpdateInfoView
}

// ---- the WebUI view as the main process reports it -------------------------------

/** The window shows a server on another computer. */
export function isRemote(i: ShellInput): boolean {
  return i.webui?.target.kind === 'remote'
}

/** Host of the remote server, or undefined. */
export function remoteHost(i: ShellInput): string | undefined {
  const t = i.webui?.target
  return t?.kind === 'remote' ? t.host : undefined
}

/** The WebUI is embedded and is what the window shows when no bar screen is open. */
export function viewUp(i: ShellInput): boolean {
  return i.webui?.home === 'webui'
}

/** The WebUI opens in the system browser (the setting, or the crash fallback). */
function inBrowser(i: ShellInput): boolean {
  return !!i.windowSettings?.openInBrowser || (!!i.webui && i.webui.placement !== 'embedded')
}

// ---- small formatters ---------------------------------------------------------

/** "http://127.0.0.1:8080" -> "127.0.0.1:8080" */
export function addressOf(endpoint: string): string {
  return endpoint.replace(/^[a-z]+:\/\//i, '').replace(/\/+$/, '')
}

export function portOf(address: string): number | undefined {
  const m = /:(\d+)$/.exec(addressOf(address))
  return m ? Number(m[1]) : undefined
}

export function withPort(address: string, port: number): string {
  const a = addressOf(address)
  return /:\d+$/.test(a) ? a.replace(/:\d+$/, `:${port}`) : `${a}:${port}`
}

export function formatBytes(n: number): string {
  if (n >= 1e9) return `${(n / 1e9).toFixed(1)} GB`
  if (n >= 1e6) return `${Math.round(n / 1e6)} MB`
  if (n >= 1e3) return `${Math.round(n / 1e3)} KB`
  return `${n} B`
}

export function timeAgo(ts: number, now: number): string {
  const m = Math.floor((now - ts) / 60000)
  if (m < 1) return S.time.justNow
  if (m < 60) return S.time.minutes(m)
  const h = Math.floor(m / 60)
  if (h < 24) return S.time.hours(h)
  return S.time.days(Math.floor(h / 24))
}

const pad = (n: number) => String(n).padStart(2, '0')
export function clockTime(ts: number, seconds = false): string {
  const d = new Date(ts)
  return `${pad(d.getHours())}:${pad(d.getMinutes())}${seconds ? `:${pad(d.getSeconds())}` : ''}`
}

// ---- install state ------------------------------------------------------------

const BUSY_INSTALL = new Set<InstallStateView['kind']>(['downloading', 'verifying', 'installing', 'rolling-back'])

/** An install, update or rollback is running: the shell fills the area with the progress screen. */
export function installBusy(install?: InstallStateView): boolean {
  return !!install && BUSY_INSTALL.has(install.kind)
}

/** What the shell remembers across install values: versions, whether it is an update, the download rate. */
export interface InstallTrack {
  /** The version the last offer named; the run goes to it. */
  target?: string
  /** The installed server version, when a value said it. */
  known?: string
  /** A server was there when the run started. */
  updating: boolean
  prev?: InstallStateView['kind']
  download?: { t: number; done: number }
  /** Bytes per second since the download started. */
  rate?: number
}

export const INITIAL_TRACK: InstallTrack = { updating: false }

export function trackInstall(t: InstallTrack, v: InstallStateView, server: ServerState | undefined, now: number): InstallTrack {
  const n: InstallTrack = { ...t, prev: v.kind, known: serverVersion(v, t.known) }
  if (v.kind === 'update-available') n.target = v.version
  const starts = (v.kind === 'checking' || v.kind === 'downloading') && t.prev !== 'checking' && t.prev !== 'downloading'
  if (starts) n.updating = !!n.known || (server !== undefined && server !== 'not-installed')
  if (v.kind === 'downloading') {
    if (t.prev !== 'downloading' || !t.download) {
      n.download = { t: now, done: v.done }
      n.rate = undefined
    } else {
      const s = (now - t.download.t) / 1000
      if (s >= 1) n.rate = Math.max(0, (v.done - t.download.done) / s)
    }
  } else {
    n.download = undefined
    n.rate = undefined
  }
  return n
}

// ---- which screen fills the area ----------------------------------------------

export type BaseScreen =
  | 'web'
  | 'install'
  | 'installing'
  | 'start-failed'
  | 'first-run'
  | 'starting'
  | 'stopping'
  | 'stopped'
  | 'browser'
  | 'worker'
  /** "Where should LocalAI run?" (first run, nothing installed, no server saved). */
  | 'first-choose'
  /** The remote default does not answer. */
  | 'remote-unreachable'

/** The screen the state asks for, before the person opens one from the bar. */
export function baseScreen(i: ShellInput): BaseScreen {
  const state = i.server.state
  if (i.worker?.running) return 'worker'
  if (installBusy(i.install)) return 'installing'
  if (i.install?.kind === 'failed' && i.install.reason !== 'cancelled' && !i.installFailureSeen) return 'installing'
  if (i.install?.kind === 'done' && state === 'not-installed') return 'installing'
  // A remote default: its page, or the recovery screen. The local server is not part of this.
  if (isRemote(i)) return i.webui?.remote ? 'remote-unreachable' : 'web'
  if (state === 'not-installed') return i.servers?.firstRun ? 'first-choose' : 'install'
  if (state === 'failed') return 'start-failed'
  // The view is up and stays up when the server stops or starts again: a banner says so.
  if ((state === 'stopped' || state === 'stopping' || state === 'starting') && viewUp(i)) return 'web'
  if (state === 'stopping') return 'stopping'
  if (state === 'stopped') return 'stopped'
  const firstRun = !!i.settings?.showWelcome
  if (state === 'starting') return firstRun ? 'first-run' : 'starting'
  // ready
  if (inBrowser(i)) return 'browser'
  // Without a view state from the main process, ready means the view shows.
  if (i.webui && !viewUp(i)) return firstRun ? 'first-run' : 'starting'
  return 'web'
}

// ---- the status chip of the bar -----------------------------------------------

export interface ChipView {
  kind: 'local' | 'remote' | 'worker'
  /** A remote over plain http: the chip stays amber and visible. */
  insecure?: boolean
  tone: Tone
  label: string
  /** Shown beside the chip in the mono face. */
  address?: string
  icon?: string
  busy: boolean
}

export function chipView(i: ShellInput): ChipView {
  if (i.worker?.running) {
    return { kind: 'worker', tone: 'ok', label: S.chip.worker, address: hostOf(i.worker.config.serverUrl), busy: false }
  }
  if (i.servers?.switching) {
    return { kind: 'remote', tone: 'accent', label: S.chip.opening, address: i.servers.switching.host, busy: false }
  }
  if (isRemote(i)) {
    if (i.webui?.remote) return { kind: 'remote', tone: 'error', label: S.chip.unreachable, address: remoteHost(i), icon: 'alert-circle', busy: false }
    if (currentRow(i)?.insecure) return { kind: 'remote', tone: 'warn', label: S.chip.remoteInsecure, address: remoteHost(i), icon: 'warning', insecure: true, busy: false }
    return { kind: 'remote', tone: 'accent', label: S.chip.remote, address: remoteHost(i), icon: 'globe', busy: false }
  }
  if (installBusy(i.install)) {
    const label = i.server.state === 'not-installed' && !i.installedVersion ? S.chip.installing : S.chip.updating
    return { kind: 'local', tone: 'accent', label, busy: true }
  }
  const address = addressOf(i.server.endpoint)
  switch (i.server.state) {
    case 'ready':
      // Until LocalAI is on show the person sees "Starting", as on the first run screen.
      if (viewLoading(i)) return { kind: 'local', tone: 'accent', label: S.chip.starting, busy: true }
      return { kind: 'local', tone: 'ok', label: S.chip.ready, address, busy: false }
    case 'starting':
      return { kind: 'local', tone: 'accent', label: S.chip.starting, busy: true }
    case 'stopping':
      return { kind: 'local', tone: 'accent', label: S.chip.stopping, busy: true }
    case 'failed':
      return { kind: 'local', tone: 'error', label: S.chip.failed, busy: false }
    case 'not-installed':
      return { kind: 'local', tone: 'warn', label: S.chip.notInstalled, busy: false }
    case 'stopped':
    default:
      return { kind: 'local', tone: 'idle', label: S.chip.stopped, busy: false }
  }
}

/** The row of the server the window shows. */
export function currentRow(i: ShellInput) {
  return i.servers?.rows.find((r) => r.isCurrent)
}

/** The Development channel is on and the window shows This computer: the chip stays in the bar. */
export function devChipShown(i: ShellInput): boolean {
  return i.updateInfo?.channel === 'dev' && !isRemote(i) && !i.worker?.running
}

/** "This computer: running" next to a remote, when the user keeps the local server on. */
export function keepLocalNote(i: ShellInput): boolean {
  return isRemote(i) && !!i.servers?.keepLocal && i.servers.rows.find((r) => r.kind === 'local')?.status === 'running'
}

/** The server is ready and the WebUI view is still loading in this window. */
export function viewLoading(i: ShellInput): boolean {
  return i.server.state === 'ready' && !!i.webui && !isRemote(i) && !inBrowser(i) && (!viewUp(i) || !i.webui.loaded)
}

export function hostOf(url: string): string {
  try {
    return new URL(url).host
  } catch {
    return addressOf(url)
  }
}

/** The bar buttons for a state. Stop and Restart control only a local server that runs. */
export interface BarActions {
  stopRestart: boolean
  backToComputer: boolean
  logs: boolean
}

export function barActions(i: ShellInput): BarActions {
  const remote = isRemote(i)
  const local = !remote && !i.worker?.running
  const freshInstall = i.server.state === 'not-installed' && !installBusy(i.install) && i.install?.kind !== 'failed'
  return {
    stopRestart: local && i.server.state === 'ready' && !installBusy(i.install) && !viewLoading(i),
    backToComputer: false,
    logs: !remote && !freshInstall,
  }
}

// ---- update offer -------------------------------------------------------------

export type UpdateOffer =
  | { kind: 'server'; version: string; installed?: string }
  | { kind: 'launcher'; version?: string }
  | null

/** The update the bar pill offers. A server update comes first: it is what the person runs. */
export function updateOffer(i: ShellInput): UpdateOffer {
  if (installBusy(i.install)) return null
  if (i.install?.kind === 'update-available' && i.server.state !== 'not-installed') {
    return { kind: 'server', version: i.install.version, installed: i.install.installed ?? i.installedVersion }
  }
  if (i.updater?.phase === 'downloaded') return { kind: 'launcher', version: i.updater.version }
  return null
}

// ---- sandbox ------------------------------------------------------------------

/** The bar shows the "Sandbox off" chip. */
export function sandboxOff(i: ShellInput): boolean {
  if (i.sandbox) return i.sandbox.state === 'unsandboxed-restricted' || i.sandbox.state === 'unsandboxed-other'
  return i.info?.sandboxed === false
}

export type GuideMode = 'offer' | 'busy' | 'restart' | 'failed' | 'removed' | 'other'

export interface GuideView {
  mode: GuideMode
  /** Why the sandbox is off, in one sentence. */
  why: string
  /** Text and command known: the plan is shown before anything is written. */
  planShown: boolean
  /** "Set up sandbox profile" is offered. */
  canSetUp: boolean
  /**
   * The main process can set the profile up. False on the AppImage until the
   * attach rule is decided: the button is shown disabled with a sentence, and
   * no command is shown.
   */
  setupAvailable: boolean
  /** "Not now" is offered: the notice was not dismissed for this version. */
  canDismiss: boolean
  canRemove: boolean
  busyLabel?: string
  failure?: string
}

export function guideView(sandbox: SandboxView | undefined, plan: SandboxPlanView | undefined): GuideView {
  const restricted = sandbox?.state === 'unsandboxed-restricted'
  const n = sandbox?.notice ?? { kind: 'hidden' as const }
  const base: GuideView = {
    mode: 'offer',
    why: restricted ? S.sandbox.why : S.sandbox.whyOther,
    planShown: sandbox?.setupAvailable === true && !!plan?.available && plan.kind === 'setup',
    canSetUp: restricted,
    setupAvailable: sandbox?.setupAvailable === true,
    canDismiss: n.kind === 'shown',
    canRemove: sandbox?.profile === 'loaded',
  }
  if (!restricted) return { ...base, mode: 'other', canSetUp: false, canDismiss: false }
  switch (n.kind) {
    case 'installing':
      return { ...base, mode: 'busy', canSetUp: false, canDismiss: false, canRemove: false, busyLabel: S.sandbox.installing }
    case 'removing':
      return { ...base, mode: 'busy', canSetUp: false, canDismiss: false, canRemove: false, busyLabel: S.sandbox.removing }
    case 'installed-needs-restart':
      return { ...base, mode: 'restart', canSetUp: false, canDismiss: false, canRemove: true }
    case 'failed':
      return { ...base, mode: 'failed', failure: n.reason === 'cancelled' ? S.sandbox.cancelled : n.reason }
    case 'removed':
      return { ...base, mode: 'removed', canRemove: false }
    default:
      return base
  }
}

export interface SandboxRowView {
  tone: Tone
  label: string
  note: string
  /** Offer the guide from Settings. */
  action: boolean
}

export function sandboxRow(i: ShellInput): SandboxRowView {
  const s = i.sandbox
  if (s?.notice.kind === 'installed-needs-restart') {
    return { tone: 'accent', label: S.settings.sandboxOff, note: S.settings.sandboxRestart, action: true }
  }
  if (s?.state === 'unsandboxed-restricted') {
    return { tone: 'warn', label: S.settings.sandboxOff, note: S.settings.sandboxRestricted, action: true }
  }
  if (s?.state === 'unsandboxed-other' || (!s && i.info?.sandboxed === false)) {
    return { tone: 'warn', label: S.settings.sandboxOff, note: S.settings.sandboxOther, action: true }
  }
  if (s?.state === 'not-applicable') {
    return { tone: 'idle', label: S.settings.sandboxNotNeeded, note: S.settings.sandboxNotApplicable, action: false }
  }
  return { tone: 'ok', label: S.settings.sandboxOn, note: S.settings.sandboxNote, action: false }
}

// ---- status bar hide and show -------------------------------------------------

export type BarNeed = 'failed' | 'update' | 'sandbox-notice' | 'remote'

/** The states that bring a hidden bar back by itself, because they need the person. */
export function barNeeds(i: ShellInput): BarNeed[] {
  const needs: BarNeed[] = []
  if (i.server.state === 'failed') needs.push('failed')
  if (updateOffer(i)) needs.push('update')
  if (i.sandbox?.notice.kind === 'shown') needs.push('sandbox-notice')
  if (isRemote(i)) needs.push('remote')
  return needs
}

export interface BarVisibility {
  visible: boolean
  /** Visible although the saved choice is "hidden". */
  auto: boolean
  needs: BarNeed[]
}

/** The auto-return rule. A missing saved choice means shown (config default). */
export function barVisibility(saved: boolean | undefined, needs: BarNeed[]): BarVisibility {
  const chosen = saved ?? true
  if (chosen) return { visible: true, auto: false, needs }
  return { visible: needs.length > 0, auto: needs.length > 0, needs }
}

export type DotTone = 'ok' | 'warn' | 'error' | 'idle'

/** The corner dot of a hidden bar: green ready, amber working or sandbox off, red failed, grey stopped. */
export function dotTone(i: ShellInput): DotTone {
  const s = i.server.state
  if (s === 'failed' || (i.install?.kind === 'failed' && !i.installFailureSeen)) return 'error'
  if (s === 'starting' || s === 'stopping' || installBusy(i.install) || viewLoading(i)) return 'warn'
  if (isRemote(i) || i.worker?.running) return sandboxOff(i) ? 'warn' : 'ok'
  if (s === 'stopped' || s === 'not-installed') return 'idle'
  return sandboxOff(i) ? 'warn' : 'ok'
}

// ---- progress screen ----------------------------------------------------------

export type StepStatus = 'done' | 'active' | 'todo'

export interface StepView {
  label: string
  note?: string
  status: StepStatus
  /** 0 to 100 for the active download step. */
  percent?: number
}

export interface ProgressView {
  title: string
  sub: string
  steps: StepView[]
  canCancel: boolean
  keepLine: string
  failure?: { reason: InstallFailReason; message: string; rolledBack: boolean }
  done?: string
}

/**
 * The install and update progress screen. `updating` is true when a server was
 * installed before the run (the first step then says it was stopped).
 */
export function progressView(install: InstallStateView, ctx: { updating: boolean; target?: string; previous?: string; rate?: number }): ProgressView {
  const title = ctx.updating ? S.progress.updatingTitle(ctx.target) : S.progress.installingTitle(ctx.target)
  const sub = ctx.updating ? S.progress.updateSub : S.progress.installSub
  const keepLine = S.progress.keepLine(ctx.updating ? ctx.previous : undefined)
  const order: InstallStateView['kind'][] = ['checking', 'downloading', 'verifying', 'done']
  const at = install.kind === 'installing' ? order.indexOf('verifying') : order.indexOf(install.kind)
  const st = (idx: number): StepStatus => (at < 0 ? 'todo' : at > idx ? 'done' : at === idx ? 'active' : 'todo')

  if (install.kind === 'rolling-back') {
    return {
      title: S.progress.rollingBackTitle,
      sub,
      steps: [{ label: S.progress.rollingBack, status: 'active' }],
      canCancel: false,
      keepLine,
    }
  }

  const steps: StepView[] = []
  if (ctx.updating) steps.push({ label: S.progress.stepStopped, status: 'done' })
  steps.push({ label: st(0) === 'done' ? S.progress.stepFound : S.progress.stepFinding, status: st(0) })
  if (install.kind === 'downloading') {
    const percent = install.total > 0 ? Math.min(100, Math.round((install.done / install.total) * 100)) : undefined
    steps.push({
      label: S.progress.stepDownload,
      note: install.total > 0 ? downloadNote(install.done, install.total, ctx.rate) : undefined,
      status: 'active',
      percent,
    })
  } else {
    steps.push({ label: st(1) === 'done' ? S.progress.stepDownloaded : S.progress.stepDownloadTodo, status: st(1) })
  }
  const verifyStatus = st(2)
  steps.push({
    label: verifyStatus === 'todo' ? S.progress.stepVerify(ctx.updating ? ctx.previous : undefined) : verifyStatus === 'done' ? S.progress.stepInstalled : S.progress.stepVerifying,
    status: verifyStatus,
  })
  steps.push({ label: S.progress.stepStart, status: install.kind === 'done' ? 'done' : 'todo' })

  const view: ProgressView = { title, sub, steps, canCancel: BUSY_INSTALL.has(install.kind) || install.kind === 'checking', keepLine }
  if (install.kind === 'failed') {
    view.failure = { reason: install.reason, message: install.message, rolledBack: install.rolledBack }
    view.title = S.progress.failedHeading
    view.sub = ''
    view.steps = []
    view.canCancel = false
  }
  if (install.kind === 'done') {
    view.done = S.progress.doneTitle(install.version)
    view.canCancel = false
  }
  return view
}

/** "640 MB of 1.1 GB · 38 MB/s · about 12 s left" */
export function downloadNote(done: number, total: number, rate?: number): string {
  const parts = [S.progress.bytes(formatBytes(done), formatBytes(total))]
  if (rate && rate > 0) {
    parts.push(S.progress.rate(formatBytes(rate)))
    parts.push(S.progress.left(Math.max(1, Math.round((total - done) / rate))))
  }
  return parts.join(' · ')
}

// ---- start failure ------------------------------------------------------------

export type FailureCause = 'port-in-use' | 'binary-missing' | 'crash'

export interface StartFailureView {
  cause: FailureCause
  /** The port that is taken, for port-in-use. */
  port?: number
  /** The cause in one plain sentence. */
  message: string
  /** The last log lines, newest last, error lines marked. */
  lines: { text: string; error: boolean }[]
}

const PORT_RE = /:(\d+):?\s*(?:bind:\s*)?address already in use/i
const MISSING_RE = /ENOENT|no such file|not found|checksum|exec format error|permission denied/i
const ERROR_RE = /\b(ERR|ERROR|FATAL|panic)\b|error:/i

/** Names the cause of a failed start from the detail and the last log lines. */
export function startFailure(server: ServerSnapshot, settings?: LauncherSettings): StartFailureView {
  const text = [server.detail ?? '', ...server.logTail].join('\n')
  const lines = server.logTail.slice(-8).map((t) => ({ text: t, error: ERROR_RE.test(t) || /address already in use/i.test(t) }))
  const portMatch = PORT_RE.exec(text)
  if (portMatch || /address already in use|EADDRINUSE/i.test(text)) {
    const port = portMatch ? Number(portMatch[1]) : portOf(settings?.address ?? server.endpoint)
    return { cause: 'port-in-use', port, message: port ? S.failed.portInUse(port) : S.failed.crashed, lines }
  }
  if (MISSING_RE.test(server.detail ?? '')) return { cause: 'binary-missing', message: S.failed.binaryMissing, lines }
  return { cause: 'crash', message: server.detail?.trim() || S.failed.crashed, lines }
}

// ---- first run ----------------------------------------------------------------

export interface FirstRunView {
  title: string
  sub: string
  steps: StepView[]
}

export function firstRunView(i: ShellInput, hardware?: string): FirstRunView {
  const ready = i.server.state === 'ready'
  const address = addressOf(i.server.endpoint)
  const steps: StepView[] = [{ label: S.firstRun.installed(i.installedVersion), status: 'done' }]
  if (hardware) steps.push({ label: hardware, status: 'done' })
  steps.push(ready ? { label: S.firstRun.started(address), status: 'done' } : { label: S.firstRun.starting(address), status: 'active' })
  steps.push({ label: S.firstRun.loading, note: S.firstRun.loadingNote, status: ready ? 'active' : 'todo' })
  return { title: S.firstRun.title, sub: ready ? S.firstRun.sub(i.installedVersion) : S.firstRun.subStarting, steps }
}

// ---- server version -----------------------------------------------------------

/** The version of the installed server, from the install state when it says. */
export function serverVersion(install: InstallStateView | undefined, known?: string): string | undefined {
  if (!install) return known
  switch (install.kind) {
    case 'up-to-date':
      return install.version
    case 'update-available':
      return install.installed ?? known
    case 'done':
      return install.version
    default:
      return known
  }
}

// ---- live announcement --------------------------------------------------------

export function announcement(chip: ChipView): string {
  return S.bar.announce(chip.label, chip.address)
}

// ---- settings drafts ----------------------------------------------------------

/** The keys a text field edits; a switch saves at once and is not a draft. The data folders are not here: the main process picks and saves them. */
export const DRAFT_KEYS = ['address', 'logLevel'] as const
export type DraftKey = (typeof DRAFT_KEYS)[number]
export type SettingsDraft = Pick<LauncherSettings, DraftKey>

export function draftOf(s: LauncherSettings): SettingsDraft {
  return { address: s.address, logLevel: s.logLevel }
}

/** The changed fields of a draft, as a patch for saveSettings. */
export function draftPatch(saved: LauncherSettings, draft: SettingsDraft): Partial<LauncherSettings> {
  const patch: Partial<LauncherSettings> = {}
  for (const k of DRAFT_KEYS) if (saved[k] !== draft[k]) (patch as Record<string, unknown>)[k] = draft[k]
  return patch
}

/** "lai-••••3f9a" from the hint the main process sends; the key itself is not in the renderer. */
export function maskedKey(hint: { prefix: string; last4: string } | undefined, long = false): string {
  const dots = '•'.repeat(long ? 16 : 4)
  if (!hint) return dots
  return `${hint.prefix}${dots}${hint.last4}`
}

// ---- start failure and first run helpers --------------------------------------

/** One line on the hardware found, for first run. */
export function hardwareLine(h: HardwareSummaryView): string {
  return S.firstRun.hardware({ gpu: h.gpu, cpuCount: h.cpuCount, memory: formatBytes(h.memoryBytes) })
}

/** What listens on the port, in one sentence. */
export function portProcessLine(p: PortProcessView): string {
  switch (p.status) {
    case 'found':
      return S.failed.process.found(p.processes.map((x) => `${x.name || '?'} (pid ${x.pid})`).join(', '))
    case 'none':
      return S.failed.process.none
    case 'hidden':
      return S.failed.process.hidden
    default:
      return S.failed.process.unavailable
  }
}
