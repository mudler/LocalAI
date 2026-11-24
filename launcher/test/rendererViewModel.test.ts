import { describe, expect, it } from 'vitest'
import type { InstallStateView, SandboxNoticeView, SandboxView, ServerSnapshot, ServerState } from '../src/shared/types'
import { DEFAULT_SETTINGS, webuiView } from '../src/renderer/dev/fakeBridge'
import {
  addressOf,
  barActions,
  barNeeds,
  barVisibility,
  baseScreen,
  chipView,
  downloadNote,
  dotTone,
  draftOf,
  draftPatch,
  maskedKey,
  firstRunView,
  guideView,
  INITIAL_TRACK,
  installBusy,
  progressView,
  sandboxOff,
  sandboxRow,
  serverVersion,
  startFailure,
  timeAgo,
  trackInstall,
  updateOffer,
  withPort,
  type ShellInput,
} from '../src/renderer/viewModel'

const EP = 'http://127.0.0.1:8080'
const srv = (state: ServerState, extra: Partial<ServerSnapshot> = {}): ServerSnapshot => ({ state, endpoint: EP, logTail: [], ...extra })
const input = (state: ServerState, extra: Partial<ShellInput> = {}): ShellInput => ({ server: srv(state), ...extra })
const web = webuiView()
const loading = webuiView({ layout: 'shell-only', home: 'shell-only', loaded: false })
const remote = webuiView({ target: { kind: 'remote', origin: 'https://gpu-box.example.test', host: 'gpu-box.example.test' } })
const sbx = (state: SandboxView['state'], notice: SandboxNoticeView = { kind: 'hidden' }, extra: Partial<SandboxView> = {}): SandboxView => ({
  state,
  notice,
  profile: 'unknown',
  setupAvailable: false,
  ...extra,
})
const ALL_STATES: ServerState[] = ['not-installed', 'stopped', 'starting', 'ready', 'failed', 'stopping']

describe('formatters', () => {
  it('reads and changes addresses', () => {
    expect(addressOf('http://127.0.0.1:8080/')).toBe('127.0.0.1:8080')
    expect(withPort('127.0.0.1:8080', 8081)).toBe('127.0.0.1:8081')
    expect(withPort('localhost', 9000)).toBe('localhost:9000')
  })
  it('says how long ago', () => {
    const now = 10 * 86400000
    expect(timeAgo(now - 10_000, now)).toBe('just now')
    expect(timeAgo(now - 10 * 60000, now)).toBe('10 minutes ago')
    expect(timeAgo(now - 60000, now)).toBe('1 minute ago')
    expect(timeAgo(now - 3 * 3600000, now)).toBe('3 hours ago')
    expect(timeAgo(now - 2 * 86400000, now)).toBe('2 days ago')
  })
  it('writes the download note with rate and time left once the rate is known', () => {
    expect(downloadNote(640e6, 1.1e9)).toBe('640 MB of 1.1 GB')
    expect(downloadNote(640e6, 1.1e9, 38e6)).toBe('640 MB of 1.1 GB · 38 MB/s · about 12 s left')
    expect(downloadNote(0, 1.1e9, 1e6)).toContain('min left')
  })
})

describe('the screen the state asks for', () => {
  it('has a screen for every server state without a WebUI view state', () => {
    const want: Record<ServerState, string> = {
      'not-installed': 'install',
      stopped: 'stopped',
      starting: 'starting',
      ready: 'web',
      failed: 'start-failed',
      stopping: 'stopping',
    }
    for (const s of ALL_STATES) expect(baseScreen(input(s)), s).toBe(want[s])
  })
  it('keeps the WebUI view when the server stops under it', () => {
    expect(baseScreen(input('stopped', { webui: webuiView({ banner: 'stopped' }) }))).toBe('web')
    expect(baseScreen(input('stopping', { webui: web }))).toBe('web')
    expect(baseScreen(input('starting', { webui: webuiView({ banner: 'starting' }) }))).toBe('web')
    expect(baseScreen(input('stopped', { webui: loading }))).toBe('stopped')
    // A failed server shows its fixes; the main process keeps the page underneath.
    expect(baseScreen(input('failed', { webui: webuiView({ home: 'shell-only', layout: 'shell-only' }) }))).toBe('start-failed')
  })
  it('follows the placement the main process reports', () => {
    expect(baseScreen(input('ready', { webui: webuiView({ placement: 'browser', home: 'shell-only' }) }))).toBe('browser')
    expect(baseScreen(input('ready', { webui: webuiView({ placement: 'browser-fallback', home: 'shell-only' }) }))).toBe('browser')
    // A bar screen open over the page does not change the base screen.
    expect(baseScreen(input('ready', { webui: webuiView({ screen: 'logs', layout: 'shell-only' }) }))).toBe('web')
  })
  it('shows first run while starting and while the view loads, when the welcome flag is on', () => {
    const settings = { ...DEFAULT_SETTINGS, showWelcome: true }
    expect(baseScreen(input('starting', { settings }))).toBe('first-run')
    expect(baseScreen(input('ready', { settings, webui: loading }))).toBe('first-run')
    expect(baseScreen(input('ready', { settings, webui: web }))).toBe('web')
    expect(baseScreen(input('ready', { webui: loading }))).toBe('starting')
  })
  it('gives the whole area to a running install, update or rollback', () => {
    const busy: InstallStateView[] = [
      { kind: 'downloading', done: 1, total: 2 },
      { kind: 'verifying' },
      { kind: 'installing' },
      { kind: 'rolling-back' },
    ]
    for (const install of busy) for (const s of ALL_STATES) expect(baseScreen(input(s, { install, webui: web }))).toBe('installing')
    expect(installBusy({ kind: 'checking' })).toBe(false)
    expect(baseScreen(input('ready', { install: { kind: 'checking' } }))).toBe('web')
  })
  it('shows a failed install until the person closes it, but not a cancel', () => {
    const failed: InstallStateView = { kind: 'failed', reason: 'network', message: 'x', rolledBack: false }
    expect(baseScreen(input('ready', { install: failed }))).toBe('installing')
    expect(baseScreen(input('ready', { install: failed, installFailureSeen: true }))).toBe('web')
    expect(baseScreen(input('ready', { install: { ...failed, reason: 'cancelled' } }))).toBe('web')
  })
  it('shows a remote server even when the local one failed, and the worker over everything', () => {
    expect(baseScreen(input('failed', { webui: remote }))).toBe('web')
    const worker = { mode: 'worker' as const, running: true, status: 'connected' as const, config: { serverUrl: 'https://a.example.test', natsUrl: '', advertiseAddress: '', nodeName: '' }, tokenStored: true }
    expect(baseScreen(input('stopped', { worker }))).toBe('worker')
  })
  it('shows the browser screen when the WebUI opens in the browser', () => {
    expect(baseScreen(input('ready', { windowSettings: { openInBrowser: true, desktopNotifications: true, anyRemembered: false } }))).toBe('browser')
  })
})

describe('the status chip and the bar buttons', () => {
  it('has a chip for every server state', () => {
    const tones = ALL_STATES.map((s) => chipView(input(s)).tone)
    expect(tones).toEqual(['warn', 'idle', 'accent', 'ok', 'error', 'accent'])
    expect(chipView(input('ready')).address).toBe('127.0.0.1:8080')
    expect(chipView(input('failed')).address).toBeUndefined()
  })
  it('says Starting while the view loads, Updating during an update, Installing on a first install', () => {
    expect(chipView(input('ready', { webui: loading })).label).toBe('Starting')
    expect(chipView(input('stopped', { install: { kind: 'verifying' }, installedVersion: '4.1.3' })).label).toBe('Updating')
    expect(chipView(input('not-installed', { install: { kind: 'downloading', done: 0, total: 1 } })).label).toBe('Installing')
  })
  it('names the remote host and the worker server', () => {
    expect(chipView(input('ready', { webui: remote }))).toMatchObject({ kind: 'remote', label: 'Remote', address: 'gpu-box.example.test', icon: 'globe' })
    const worker = { mode: 'worker' as const, running: true, status: 'connected' as const, config: { serverUrl: 'https://coord.example.test', natsUrl: '', advertiseAddress: '', nodeName: '' }, tokenStored: true }
    expect(chipView(input('stopped', { worker }))).toMatchObject({ kind: 'worker', address: 'coord.example.test' })
  })
  it('offers Stop and Restart only for a ready local server on show', () => {
    for (const s of ALL_STATES) expect(barActions(input(s)).stopRestart, s).toBe(s === 'ready')
    expect(barActions(input('ready', { webui: remote }))).toEqual({ stopRestart: false, backToComputer: false, logs: false })
    expect(barActions(input('ready', { webui: loading })).stopRestart).toBe(false)
    expect(barActions(input('ready', { install: { kind: 'installing' } })).stopRestart).toBe(false)
  })
  it('hides Logs on the first install screen only', () => {
    expect(barActions(input('not-installed')).logs).toBe(false)
    expect(barActions(input('not-installed', { install: { kind: 'downloading', done: 0, total: 1 } })).logs).toBe(true)
    expect(barActions(input('not-installed', { install: { kind: 'failed', reason: 'network', message: '', rolledBack: false } })).logs).toBe(true)
  })
})

describe('update offer', () => {
  const offer: InstallStateView = { kind: 'update-available', version: '4.2.1', tag: 'v4.2.1', installed: '4.1.3' }
  it('offers a server update, then a downloaded launcher update', () => {
    expect(updateOffer(input('ready', { install: offer }))).toEqual({ kind: 'server', version: '4.2.1', installed: '4.1.3' })
    expect(updateOffer(input('ready', { updater: { phase: 'downloaded', version: '0.2.0' } }))).toEqual({ kind: 'launcher', version: '0.2.0' })
    expect(updateOffer(input('ready', { install: offer, updater: { phase: 'downloaded' } }))?.kind).toBe('server')
  })
  it('is not an update on a computer with no server, nor while an install runs', () => {
    expect(updateOffer(input('not-installed', { install: { ...offer, installed: undefined } }))).toBeNull()
    expect(updateOffer(input('ready', { install: { kind: 'installing' } }))).toBeNull()
    for (const phase of ['idle', 'checking', 'available', 'downloading', 'error', 'unsupported', 'not-available'] as const) {
      expect(updateOffer(input('ready', { updater: { phase } }))).toBeNull()
    }
  })
})

describe('sandbox', () => {
  it('shows the chip for both unsandboxed states, or from the start flag when there is no probe', () => {
    expect(sandboxOff(input('ready', { sandbox: sbx('unsandboxed-restricted') }))).toBe(true)
    expect(sandboxOff(input('ready', { sandbox: sbx('unsandboxed-other') }))).toBe(true)
    expect(sandboxOff(input('ready', { sandbox: sbx('sandboxed') }))).toBe(false)
    expect(sandboxOff(input('ready', { sandbox: sbx('not-applicable') }))).toBe(false)
    expect(sandboxOff(input('ready', { info: { version: '1', packaged: true, trayActive: true, sandboxed: false } }))).toBe(true)
  })
  it('maps every notice state to a guide', () => {
    const r = (n: SandboxNoticeView, extra: Partial<SandboxView> = {}) => guideView(sbx('unsandboxed-restricted', n, extra), undefined)
    expect(r({ kind: 'shown' })).toMatchObject({ mode: 'offer', canSetUp: true, canDismiss: true, planShown: false })
    expect(r({ kind: 'dismissed' })).toMatchObject({ mode: 'offer', canSetUp: true, canDismiss: false })
    expect(r({ kind: 'hidden' })).toMatchObject({ mode: 'offer', canDismiss: false })
    expect(r({ kind: 'installing' })).toMatchObject({ mode: 'busy', busyLabel: 'Setting up the profile…', canDismiss: false })
    expect(r({ kind: 'removing' })).toMatchObject({ mode: 'busy', busyLabel: 'Removing the profile…' })
    expect(r({ kind: 'installed-needs-restart' })).toMatchObject({ mode: 'restart', canSetUp: false, canRemove: true })
    expect(r({ kind: 'failed', reason: 'cancelled' })).toMatchObject({ mode: 'failed', failure: 'You closed the password dialog. Nothing was changed.' })
    expect(r({ kind: 'failed', reason: 'pkexec exited with 127' }).failure).toBe('pkexec exited with 127')
    expect(r({ kind: 'removed' })).toMatchObject({ mode: 'removed', canRemove: false })
    expect(r({ kind: 'shown' }, { profile: 'loaded' }).canRemove).toBe(true)
    expect(guideView(sbx('unsandboxed-other'), undefined)).toMatchObject({ mode: 'other', canSetUp: false })
  })
  it('shows the plan only when the main process has one for setup', () => {
    const plan = { kind: 'setup' as const, available: true, profilePath: '/p', steps: [{ description: 'd', command: 'c' }], terminalText: 'c' }
    // The AppImage today: no attach rule, so setup is not available and no command is shown, even with a plan.
    expect(guideView(sbx('unsandboxed-restricted', { kind: 'shown' }), plan)).toMatchObject({ setupAvailable: false, planShown: false, canSetUp: true })
    expect(guideView(sbx('unsandboxed-restricted', { kind: 'shown' }, { setupAvailable: true }), plan)).toMatchObject({ setupAvailable: true, planShown: true })
    expect(guideView(sbx('unsandboxed-restricted', { kind: 'shown' }, { setupAvailable: true }), { ...plan, available: false }).planShown).toBe(false)
  })
  it('gives Settings a status row for every sandbox state', () => {
    expect(sandboxRow(input('ready', { sandbox: sbx('sandboxed') }))).toMatchObject({ tone: 'ok', label: 'On', action: false })
    expect(sandboxRow(input('ready', { sandbox: sbx('unsandboxed-restricted', { kind: 'dismissed' }) }))).toMatchObject({ tone: 'warn', label: 'Off', action: true })
    expect(sandboxRow(input('ready', { sandbox: sbx('unsandboxed-other') }))).toMatchObject({ tone: 'warn', action: true })
    expect(sandboxRow(input('ready', { sandbox: sbx('not-applicable') }))).toMatchObject({ tone: 'idle', label: 'Not needed' })
    expect(sandboxRow(input('ready', { sandbox: sbx('unsandboxed-restricted', { kind: 'installed-needs-restart' }) })).note).toBe('Restart the launcher to turn it on')
  })
})

describe('status bar hide and show', () => {
  it('follows the saved choice, and a missing choice means shown', () => {
    expect(barVisibility(undefined, [])).toEqual({ visible: true, auto: false, needs: [] })
    expect(barVisibility(true, [])).toMatchObject({ visible: true, auto: false })
    expect(barVisibility(false, [])).toMatchObject({ visible: false, auto: false })
  })
  it('comes back by itself for the states that need the person', () => {
    const offer: InstallStateView = { kind: 'update-available', version: '4.2.1', tag: 'v4.2.1', installed: '4.1.3' }
    expect(barNeeds(input('failed'))).toEqual(['failed'])
    expect(barNeeds(input('ready', { install: offer }))).toEqual(['update'])
    expect(barNeeds(input('ready', { updater: { phase: 'downloaded' } }))).toEqual(['update'])
    expect(barNeeds(input('ready', { sandbox: sbx('unsandboxed-restricted', { kind: 'shown' }) }))).toEqual(['sandbox-notice'])
    expect(barNeeds(input('ready', { sandbox: sbx('unsandboxed-restricted', { kind: 'dismissed' }) }))).toEqual([])
    expect(barNeeds(input('ready', { webui: remote }))).toEqual(['remote'])
    expect(barNeeds(input('ready'))).toEqual([])
    expect(barNeeds(input('stopped'))).toEqual([])
    expect(barVisibility(false, ['failed'])).toEqual({ visible: true, auto: true, needs: ['failed'] })
  })
  it('colours the corner dot by state', () => {
    expect(dotTone(input('ready'))).toBe('ok')
    expect(dotTone(input('starting'))).toBe('warn')
    expect(dotTone(input('stopping'))).toBe('warn')
    expect(dotTone(input('ready', { webui: loading }))).toBe('warn')
    expect(dotTone(input('ready', { install: { kind: 'downloading', done: 0, total: 1 } }))).toBe('warn')
    expect(dotTone(input('ready', { sandbox: sbx('unsandboxed-restricted', { kind: 'dismissed' }) }))).toBe('warn')
    expect(dotTone(input('failed'))).toBe('error')
    expect(dotTone(input('ready', { install: { kind: 'failed', reason: 'network', message: '', rolledBack: false } }))).toBe('error')
    expect(dotTone(input('stopped'))).toBe('idle')
    expect(dotTone(input('not-installed'))).toBe('idle')
    expect(dotTone(input('stopped', { sandbox: sbx('unsandboxed-restricted') }))).toBe('idle')
    expect(dotTone(input('ready', { webui: remote }))).toBe('ok')
  })
})

describe('progress screen', () => {
  const ctx = { updating: true, target: '4.2.1', previous: '4.1.3' }
  const statuses = (i: InstallStateView, c = ctx) => progressView(i, c).steps.map((s) => s.status)
  it('walks the steps of an update', () => {
    expect(statuses({ kind: 'checking' })).toEqual(['done', 'active', 'todo', 'todo', 'todo'])
    expect(statuses({ kind: 'downloading', done: 640e6, total: 1.1e9 })).toEqual(['done', 'done', 'active', 'todo', 'todo'])
    expect(statuses({ kind: 'verifying' })).toEqual(['done', 'done', 'done', 'active', 'todo'])
    expect(statuses({ kind: 'installing' })).toEqual(['done', 'done', 'done', 'active', 'todo'])
    expect(statuses({ kind: 'done', version: '4.2.1', adopted: false })).toEqual(['done', 'done', 'done', 'done', 'done'])
  })
  it('says the version, the rollback copy and the percent', () => {
    const v = progressView({ kind: 'downloading', done: 640e6, total: 1.1e9 }, ctx)
    expect(v.title).toBe('Updating to LocalAI 4.2.1')
    expect(v.steps[2]).toMatchObject({ percent: 58, note: '640 MB of 1.1 GB' })
    expect(v.steps[3].label).toBe('Verify the checksum, then install and keep 4.1.3 as a rollback copy')
    expect(v.keepLine).toBe('If a step fails, 4.1.3 stays installed and starts as before.')
    expect(v.canCancel).toBe(true)
  })
  it('has no "stopped the server" step and no rollback copy on a first install', () => {
    const v = progressView({ kind: 'checking' }, { updating: false, target: '4.2.1' })
    expect(v.title).toBe('Installing LocalAI 4.2.1')
    expect(v.steps).toHaveLength(4)
    expect(v.keepLine).toBe('If a step fails, nothing is installed and nothing is changed.')
  })
  it('shows rollback, failure and done', () => {
    expect(progressView({ kind: 'rolling-back' }, ctx)).toMatchObject({ title: 'Going back to the previous version', canCancel: false })
    const f = progressView({ kind: 'failed', reason: 'checksum-mismatch', message: 'm', rolledBack: true }, ctx)
    expect(f).toMatchObject({ title: 'The last install did not finish', sub: '', canCancel: false, steps: [], failure: { reason: 'checksum-mismatch', rolledBack: true } })
    expect(progressView({ kind: 'done', version: '4.2.1', adopted: false }, ctx)).toMatchObject({ done: 'LocalAI 4.2.1 is installed.', canCancel: false })
  })
})

describe('install tracking', () => {
  const offer: InstallStateView = { kind: 'update-available', version: '4.2.1', tag: 'v4.2.1', installed: '4.1.3' }
  it('remembers the offer and the installed version across a batch of values', () => {
    let t = trackInstall(INITIAL_TRACK, offer, 'ready', 0)
    t = trackInstall(t, { kind: 'downloading', done: 0, total: 100 }, 'stopped', 0)
    expect(t).toMatchObject({ target: '4.2.1', known: '4.1.3', updating: true })
  })
  it('tells a first install from an update', () => {
    const t = trackInstall(INITIAL_TRACK, { kind: 'checking' }, 'not-installed', 0)
    expect(t.updating).toBe(false)
    expect(trackInstall(INITIAL_TRACK, { kind: 'checking' }, 'stopped', 0).updating).toBe(true)
  })
  it('measures the download rate after a second', () => {
    let t = trackInstall(INITIAL_TRACK, { kind: 'downloading', done: 0, total: 100e6 }, 'stopped', 0)
    t = trackInstall(t, { kind: 'downloading', done: 10e6, total: 100e6 }, 'stopped', 500)
    expect(t.rate).toBeUndefined()
    t = trackInstall(t, { kind: 'downloading', done: 40e6, total: 100e6 }, 'stopped', 2000)
    expect(t.rate).toBe(20e6)
    t = trackInstall(t, { kind: 'verifying' }, 'stopped', 3000)
    expect(t.rate).toBeUndefined()
  })
  it('reads the server version from each install state', () => {
    expect(serverVersion({ kind: 'up-to-date', version: '4.1.3' })).toBe('4.1.3')
    expect(serverVersion({ kind: 'done', version: '4.2.1', adopted: false }, '4.1.3')).toBe('4.2.1')
    expect(serverVersion({ kind: 'idle' }, '4.1.3')).toBe('4.1.3')
    expect(serverVersion(undefined)).toBeUndefined()
  })
})

describe('start failure causes', () => {
  it('finds a port in use, with the port', () => {
    const f = startFailure(srv('failed', { detail: 'listen tcp 127.0.0.1:8080: bind: address already in use', logTail: ['a', 'ERR listen tcp 127.0.0.1:8080: bind: address already in use'] }))
    expect(f).toMatchObject({ cause: 'port-in-use', port: 8080, message: 'Port 8080 is already in use.' })
    expect(f.lines[1].error).toBe(true)
    expect(f.lines[0].error).toBe(false)
  })
  it('takes the port from the settings when the log does not name it', () => {
    const f = startFailure(srv('failed', { detail: 'EADDRINUSE' }), { ...DEFAULT_SETTINGS, address: '0.0.0.0:9090' })
    expect(f).toMatchObject({ cause: 'port-in-use', port: 9090 })
  })
  it('finds a missing or damaged server file', () => {
    expect(startFailure(srv('failed', { detail: 'spawn local-ai ENOENT' })).cause).toBe('binary-missing')
  })
  it('falls back to a crash with the detail, and keeps the last eight lines', () => {
    const f = startFailure(srv('failed', { detail: 'exited with code 2', logTail: Array.from({ length: 12 }, (_, i) => `l${i}`) }))
    expect(f).toMatchObject({ cause: 'crash', message: 'exited with code 2' })
    expect(f.lines.map((l) => l.text)).toEqual(['l4', 'l5', 'l6', 'l7', 'l8', 'l9', 'l10', 'l11'])
    expect(startFailure(srv('failed')).message).toBe('The server stopped with an error.')
  })
})

describe('first run', () => {
  it('lists install, hardware, start and the WebUI loading', () => {
    const settings = { ...DEFAULT_SETTINGS, showWelcome: true }
    const r = firstRunView(input('ready', { settings, installedVersion: '4.2.1' }), 'Found a GPU.')
    expect(r.steps.map((s) => [s.label, s.status])).toEqual([
      ['Installed LocalAI 4.2.1', 'done'],
      ['Found a GPU.', 'done'],
      ['Started the server on 127.0.0.1:8080', 'done'],
      ['Loading LocalAI in this window', 'active'],
    ])
    const s = firstRunView(input('starting', { settings }))
    expect(s.steps.map((x) => x.status)).toEqual(['done', 'active', 'todo'])
  })
})

describe('settings drafts', () => {
  it('makes a patch of the changed text fields only', () => {
    const d = draftOf(DEFAULT_SETTINGS)
    expect(draftPatch(DEFAULT_SETTINGS, d)).toEqual({})
    expect(draftPatch(DEFAULT_SETTINGS, { ...d, address: '0.0.0.0:8080', logLevel: 'debug' })).toEqual({ address: '0.0.0.0:8080', logLevel: 'debug' })
    // Environment variables are not part of the settings draft: their values never reach the renderer.
    expect(Object.keys(d)).not.toContain('environmentVars')
  })

  it('masks the API key from the hint only', () => {
    expect(maskedKey({ prefix: 'lai-', last4: '3f9a' }, true)).toBe('lai-••••••••••••••••3f9a')
    expect(maskedKey({ prefix: 'lai-', last4: '3f9a' })).toBe('lai-••••3f9a')
    expect(maskedKey({ prefix: '', last4: '' })).toBe('••••')
    expect(maskedKey(undefined)).toBe('••••')
  })
})
