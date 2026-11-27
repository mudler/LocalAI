// The saved servers: the list, the connection checks, switching the default,
// the watch on a remote default, and the forwarders of servers over P2P.
// No Electron import and no Node: the desktop launcher and a client-only
// shell inject the config store, the connection test, the P2P tunnels and the
// WebUI model.
//
// A switch to a remote checks the server first; until its page has loaded the
// old page stays and the user can cancel. What happens to a local server
// (start it, stop it) is the caller's: onLocalDefault and onSwitchFinished.
import type {
  AddServerByInviteInput,
  AddServerByInviteResult,
  AddServerInput,
  ServerChangeResult,
  ServerRowView,
  ServersView,
  ServerStatusName,
  SwitchingView,
  SwitchResult,
  TestInput,
  TestOutcome,
  TestResultView,
  TunnelStatusView,
  UpdateServerInput,
  WebuiViewState,
} from '../shared/types'
import type { ConfigStore } from './configStore'
import type { LauncherConfig } from './configTypes'
import { linkRoutes, readInvite, type P2PRoutes, type P2PTarget, type PairOutcome } from './p2pInvite'
import { RemoteMonitor, REMOTE_RETRY_MS } from './remoteMonitor'
import {
  addP2PRemote,
  addRemote,
  defaultTarget,
  keepLocalRunning,
  LOCAL_TARGET,
  newRemoteId,
  removeRemote,
  remotes,
  updateRemote,
  withDefaultTarget,
  withP2PRoutes,
  type RemoteServer,
} from './serversConfig'
import { isInsecureRemote, type TestResult } from './testClassify'
import { validateRemoteOrigin } from './webuiPolicy'
import type { WebuiEvent } from './webuiState'

export type { PairOutcome }

/** The forwarders of servers over P2P, one per saved server. */
export interface P2PTunnels {
  /** False when pairing cannot run now (desktop: no server binary). */
  available(): boolean
  pair(input: { link: string; name: string; allowOpenRelay: boolean }): Promise<PairOutcome>
  /** A loopback port for a new server. */
  allocatePort(): Promise<number>
  /**
   * A loopback port and host (127.x.y.z) for a new server. Preferred over
   * allocatePort when present (the Android app gives each server its own
   * host, so each has its own origin). No host means 127.0.0.1.
   */
  allocate?(): Promise<{ port: number; host?: string }>
  start(id: string, name: string, target: P2PTarget): void
  stop(id: string): void
  status(id: string): TunnelStatusView | undefined
  /** True once the forwarder listens (ready, up or down); false after timeoutMs, failed or stopped. */
  whenReady(id: string, timeoutMs: number): Promise<boolean>
}

export interface RemoteServersDeps {
  store: ConfigStore
  now(): number
  randomBytes(n: number): Uint8Array
  checkOrigin(origin: string, signal?: AbortSignal): Promise<TestResult>
  p2p: P2PTunnels
  /** The name this device gives a server when it pairs (desktop: the host name). */
  deviceName(): string
  webuiDispatch(e: WebuiEvent): WebuiViewState
  /** The origin the WebUI view shows, or undefined (the browser setting). */
  webuiViewOrigin(): string | undefined
  emitServers(view: ServersView): void
  log(message: string, level?: 'info' | 'warn'): void
  /** Desktop: the "This computer" row. A client-only app: undefined. */
  localRow(c: LauncherConfig): ServerRowView | undefined
  /** Desktop: today's first-run rule. */
  firstRun(c: LauncherConfig): boolean
  /**
   * The default became LOCAL_TARGET. Before this call, RemoteServers has
   * already stopped the remote monitor and dispatched `back-to-local`. The
   * callback only does the caller's own part (desktop: prepare and start the
   * local server). It runs when the default server is removed, on a switch to
   * LOCAL_TARGET, and when a cancelled switch goes back to local. A
   * client-only app must cope with `back-to-local` and no local origin.
   */
  onLocalDefault(): Promise<void>
  /** A switch to a remote finished (page loaded, or no page). Desktop: stop the local server when it is not managed. */
  onSwitchFinished(): Promise<void>
  remoteRetryMs?: number
}

/** How long a new server over P2P may take to listen before it is opened. */
const TUNNEL_READY_MS = 20_000

function hostOf(r: RemoteServer): string {
  try {
    return new URL(r.url).host
  } catch {
    return r.url
  }
}

function statusOfResult(r: TestResultView): ServerStatusName {
  return r.status === 'ok' ? 'connected' : r.status === 'needs-signin' ? 'needs-signin' : r.status === 'unreachable' ? 'unreachable' : r.status === 'cancelled' ? 'not-tested' : 'problem'
}

export class RemoteServers {
  private readonly monitor: RemoteMonitor
  private checks = new Map<string, { status: ServerStatusName; at: number; version?: string }>()
  private switching: SwitchingView | null = null
  private switchAbort: AbortController | undefined
  private switchPrevious: string | undefined
  private openTests = new Set<AbortController>()
  /** The ids whose forwarder was started and not stopped since. */
  private tunnelIds = new Set<string>()
  /** The routes each started forwarder dials, which may be routes of an invite that are not saved. */
  private tunnelTargets = new Map<string, P2PTarget>()
  /** Why the last start of a forwarder threw, until it starts or stops. */
  private tunnelErrors = new Map<string, string>()

  constructor(private readonly d: RemoteServersDeps) {
    this.monitor = new RemoteMonitor({
      probe: (origin) => this.d.checkOrigin(origin),
      onStatus: (st) => {
        const c = this.d.store.get()
        const id = defaultTarget(c)
        const r = remotes(c).find((x) => x.id === id)
        if (r) this.checks.set(r.id, st ? { status: 'unreachable', at: st.at } : { status: this.checks.get(r.id)?.status === 'needs-signin' ? 'needs-signin' : 'connected', at: this.d.now(), version: this.checks.get(r.id)?.version })
        this.d.webuiDispatch({ type: 'remote-status', status: st })
        this.emit()
      },
      now: () => this.d.now(),
      retryMs: this.d.remoteRetryMs ?? REMOTE_RETRY_MS,
    })
  }

  private newId(): string {
    return newRemoteId((n) => this.d.randomBytes(n))
  }

  private record(id: string, r: TestResultView): void {
    if (r.status === 'cancelled') return
    const prev = this.checks.get(id)
    this.checks.set(id, { status: statusOfResult(r), at: this.d.now(), version: r.status === 'ok' ? r.version : r.status === 'needs-signin' ? prev?.version : undefined })
  }

  serversView(): ServersView {
    const c = this.d.store.get()
    const def = defaultTarget(c)
    const local = this.d.localRow(c)
    const rows: ServerRowView[] = [
      ...(local ? [local] : []),
      ...remotes(c).map((r): ServerRowView => {
        const k = this.checks.get(r.id)
        const current = def === r.id
        return {
          id: r.id,
          kind: 'remote',
          name: r.name,
          host: hostOf(r),
          origin: r.url,
          version: k?.version,
          isDefault: current,
          isCurrent: current,
          insecure: isInsecureRemote(r.url),
          status: k?.status ?? (current ? 'connected' : 'not-tested'),
          checkedAt: k?.at,
          p2p: r.p2p ? this.tunnelStatus(r.id) : undefined,
          ...(r.p2p ? { peer: r.p2p.peer } : {}),
        }
      }),
    ]
    return {
      rows,
      defaultId: def,
      keepLocal: keepLocalRunning(c),
      firstRun: this.d.firstRun(c),
      switching: this.switching,
    }
  }

  private tunnelStatus(id: string): TunnelStatusView {
    const err = this.tunnelErrors.get(id)
    if (err !== undefined) return { state: 'failed', error: err }
    return this.d.p2p.status(id) ?? { state: 'stopped' }
  }

  private emit(): void {
    this.d.emitServers(this.serversView())
  }

  /** Opens the default server when it is a remote one. */
  openDefaultRemote(): void {
    const c = this.d.store.get()
    const r = remotes(c).find((x) => x.id === defaultTarget(c))
    if (r) this.openRemote(r)
  }

  /** Shows a saved server in the window and watches it. */
  private openRemote(r: RemoteServer): void {
    this.d.webuiDispatch({ type: 'connect', origin: r.url, host: hostOf(r) })
    void this.monitor.start(r.url)
  }

  private openRemoteById(id: string): void {
    const r = remotes(this.d.store.get()).find((x) => x.id === id)
    if (r) this.openRemote(r)
  }

  /** This computer is the default now: stop watching the remote, show the local target, then the caller's part. */
  private async openLocal(): Promise<void> {
    this.monitor.stop()
    this.d.webuiDispatch({ type: 'back-to-local' })
    await this.d.onLocalDefault()
  }

  async testServer(input: TestInput): Promise<TestOutcome> {
    let origin: string
    let host: string
    let id: string | undefined
    if ('id' in input) {
      const r = remotes(this.d.store.get()).find((x) => x.id === input.id)
      if (!r) return { ok: false, reason: 'not-found' }
      origin = r.url
      host = hostOf(r)
      id = r.id
    } else {
      const v = validateRemoteOrigin(input.address)
      if (!v.ok) return { ok: false, reason: v.reason }
      origin = v.origin
      host = v.host
      id = remotes(this.d.store.get()).find((x) => x.url === origin)?.id
    }
    if (id) {
      this.checks.set(id, { status: 'checking', at: this.d.now(), version: this.checks.get(id)?.version })
      this.emit()
    }
    const ac = new AbortController()
    this.openTests.add(ac)
    let result: TestResultView
    try {
      result = (await this.d.checkOrigin(origin, ac.signal)) as TestResultView
    } finally {
      this.openTests.delete(ac)
    }
    if (id) {
      if (result.status === 'cancelled') this.checks.delete(id)
      else this.record(id, result)
      this.emit()
    }
    this.d.log(`servers: test ${result.status}`)
    return { ok: true, origin, host, insecure: isInsecureRemote(origin), result }
  }

  async addServer(input: AddServerInput): Promise<ServerChangeResult> {
    const idGen = () => this.newId()
    const pre = addRemote(this.d.store.get(), input, idGen)
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
    await this.d.store.update((c) => {
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
    this.d.log('servers: server saved')
    if (input.makeDefault) {
      const r = await this.switchTo(added.id, true)
      if (!r.ok) return { ok: false, reason: r.reason === 'busy' ? 'busy' : 'test-failed', test: r.test }
      return { ok: true, view: r.view, id: added.id, test }
    }
    this.emit()
    return { ok: true, view: this.serversView(), id: added.id, test }
  }

  async updateServer(input: UpdateServerInput): Promise<ServerChangeResult> {
    const before = remotes(this.d.store.get()).find((r) => r.id === input.id)
    if (!before) return { ok: false, reason: 'not-found' }
    const pre = updateRemote(this.d.store.get(), input.id, { name: input.name, address: input.address })
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
    await this.d.store.update((c) => {
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
      if (defaultTarget(this.d.store.get()) === after.id) this.openRemote(after)
    }
    this.d.log('servers: server changed')
    this.emit()
    return { ok: true, view: this.serversView(), id: after.id, test }
  }

  /**
   * Starts the forwarders of every saved server over P2P. A start that
   * throws marks only its own server as failed; the others still start.
   */
  startTunnels(): void {
    if (!this.d.p2p.available()) return
    let failed = false
    for (const r of remotes(this.d.store.get())) if (this.startTunnel(r) === 'failed') failed = true
    if (failed) this.emit()
  }

  private startTunnel(r: RemoteServer): 'started' | 'failed' | 'none' {
    if (!r.p2p) return 'none'
    // Record the id first: a start that throws may still have left a
    // forwarder behind, and stopAll must stop it.
    this.tunnelIds.add(r.id)
    this.tunnelTargets.set(r.id, r.p2p)
    try {
      this.d.p2p.start(r.id, r.name, r.p2p)
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      this.tunnelErrors.set(r.id, msg)
      this.d.log(`servers: the P2P forwarder of a server did not start: ${msg}`, 'warn')
      return 'failed'
    }
    this.tunnelErrors.delete(r.id)
    return 'started'
  }

  private stopTunnel(id: string): void {
    this.tunnelErrors.delete(id)
    this.tunnelTargets.delete(id)
    if (!this.tunnelIds.delete(id)) return
    this.d.p2p.stop(id)
  }

  /**
   * Adds a server from an invite link: pairs this device with it, picks a
   * loopback port, saves the server and starts its forwarder. The answer
   * carries the confirmation phrase, which the other server shows too.
   */
  async addServerByInvite(input: AddServerByInviteInput): Promise<AddServerByInviteResult> {
    const inv = readInvite(input.link, this.d.now())
    if (!inv.ok) return { ok: false, reason: inv.reason }
    if (inv.info.openRelay && !input.allowOpenRelay) return { ok: false, reason: 'open-relay' }
    const known = remotes(this.d.store.get()).find((r) => r.p2p?.peer === inv.info.peer)
    if (known) return this.updateRoutesByInvite(known, input.link)
    if (!this.d.p2p.available()) return { ok: false, reason: 'no-binary' }
    this.d.log('servers: pairing with a server over P2P')
    const paired = await this.d.p2p.pair({
      link: input.link.trim(),
      name: this.d.deviceName(),
      allowOpenRelay: input.allowOpenRelay === true,
    })
    if (!paired.ok) {
      this.d.log(`servers: pairing failed: ${paired.detail}`, 'warn')
      return { ok: false, reason: paired.reason, detail: paired.detail }
    }
    const at = this.d.p2p.allocate ? await this.d.p2p.allocate() : { port: await this.d.p2p.allocatePort() }
    const name = [input.name, paired.result.serverName, inv.info.label, 'LocalAI'].map((x) => (x ?? '').trim()).find((x) => x !== '') ?? 'LocalAI'
    const target: P2PTarget = { peer: paired.result.peer, port: at.port, ...linkRoutes(input.link), ...(at.host !== undefined ? { host: at.host } : {}) }
    const idGen = () => this.newId()
    let added: RemoteServer | undefined
    let failure: AddServerByInviteResult | undefined
    await this.d.store.update((c) => {
      const r = addP2PRemote(c, { name: name.slice(0, 60), target }, idGen)
      if (!r.ok) {
        failure = { ok: false, reason: r.reason }
        return c
      }
      added = r.remote
      return r.config
    })
    if (failure || !added) return failure ?? { ok: false, reason: 'limit' }
    this.d.log('servers: server over P2P saved')
    // A forwarder that does not start leaves the server saved, with the error on its row.
    const started = this.startTunnel(added) === 'started'
    const phrase = paired.result.phrase
    if (input.makeDefault && started && (await this.d.p2p.whenReady(added.id, TUNNEL_READY_MS))) {
      const r = await this.switchTo(added.id, false)
      if (!r.ok) return { ok: true, view: this.serversView(), id: added.id, phrase, opened: false }
      return { ok: true, view: r.view, id: added.id, phrase, opened: true }
    }
    this.emit()
    return { ok: true, view: this.serversView(), id: added.id, phrase, opened: false }
  }

  /**
   * An invite of a server that is already saved: this device is paired, so
   * the code is not redeemed. Anyone can write a link with the peer ID of a
   * saved server, so its routes are not saved: the forwarder starts again
   * and dials the routes of the link first, then the saved ones (the way
   * back when every saved route is out of date). Once it is connected to
   * that peer, it asks the server for its routes over the authenticated
   * connection, and those replace the saved ones (see updateP2PRoutes). The
   * invite stays pending on the server, so the person asks its owner to
   * cancel it.
   */
  private async updateRoutesByInvite(known: RemoteServer, link: string): Promise<AddServerByInviteResult> {
    if (!known.p2p) return { ok: false, reason: 'duplicate' }
    const target = withTrialRoutes(known.p2p, linkRoutes(link))
    const running = this.tunnelTargets.get(known.id)
    const changed = !running || !sameRoutes(running, target)
    this.d.log(`servers: ${changed ? 'trying the routes of an invite' : 'the routes of an invite are tried already'} for a server over P2P; the invite was not used`)
    if (changed) {
      this.stopTunnel(known.id)
      this.startTunnel({ ...known, p2p: target })
    }
    this.emit()
    return { ok: true, view: this.serversView(), id: known.id, phrase: '', opened: false, routesUpdated: true }
  }

  /**
   * The forwarder of a saved server reported the routes the server gives
   * now. They replace the saved ones, so the next start dials what the
   * server has today (a relay reservation moves now and then). The running
   * forwarder is left alone: it works. Resolves true when something changed.
   */
  async updateP2PRoutes(id: string, routes: P2PRoutes): Promise<boolean> {
    let changed = false
    await this.d.store.update((c) => {
      const r = withP2PRoutes(c, id, routes)
      if (!r.ok || !r.changed) return c
      changed = true
      return r.config
    })
    if (changed) this.d.log('servers: routes of a server over P2P updated')
    return changed
  }

  async removeServer(id: string): Promise<ServerChangeResult> {
    const c = this.d.store.get()
    if (!remotes(c).some((r) => r.id === id)) return { ok: false, reason: 'not-found' }
    const wasDefault = defaultTarget(c) === id
    await this.d.store.update((x) => removeRemote(x, id))
    this.checks.delete(id)
    this.stopTunnel(id)
    this.d.log('servers: server removed')
    if (wasDefault) await this.openLocal()
    this.emit()
    return { ok: true, view: this.serversView() }
  }

  async setDefaultServer(target: string): Promise<SwitchResult> {
    return this.switchTo(target, false)
  }

  /**
   * Makes `target` the default and opens it. A remote must answer first;
   * until the page has loaded the old page stays and the user can cancel.
   * onSwitchFinished runs after the switch (the desktop stops the local
   * server there, unless it is kept on).
   */
  private async switchTo(target: string, alreadyTested: boolean): Promise<SwitchResult> {
    if (this.switching) return { ok: false, reason: 'busy' }
    const c = this.d.store.get()
    if (target === defaultTarget(c)) {
      if (target !== LOCAL_TARGET) this.openRemoteById(target)
      else await this.openLocal()
      return { ok: true, view: this.serversView() }
    }
    if (target === LOCAL_TARGET) {
      await this.d.store.update((x) => withDefaultTarget(x, LOCAL_TARGET))
      this.d.log('servers: This computer is the default')
      await this.openLocal()
      this.emit()
      return { ok: true, view: this.serversView() }
    }
    const r = remotes(c).find((x) => x.id === target)
    if (!r) return { ok: false, reason: 'not-found' }
    this.switchPrevious = defaultTarget(c)
    this.switching = { targetId: r.id, name: r.name, host: hostOf(r), step: 'reaching', startedAt: this.d.now() }
    const ac = new AbortController()
    this.switchAbort = ac
    this.emit()
    if (!alreadyTested) {
      const result = (await this.d.checkOrigin(r.url, ac.signal)) as TestResultView
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
    await this.d.store.update((x) => withDefaultTarget(x, r.id))
    this.d.log('servers: a remote server is the default')
    this.openRemote(r)
    this.emit()
    // No page will load (the browser setting): the switch is complete now.
    if (this.d.webuiViewOrigin() !== r.url) await this.finishSwitch()
    return { ok: true, view: this.serversView() }
  }

  private endSwitch(): void {
    this.switching = null
    this.switchAbort = undefined
    this.switchPrevious = undefined
    this.emit()
  }

  /** The new page is on show: the switch is over. */
  private async finishSwitch(): Promise<void> {
    if (!this.switching) return
    this.endSwitch()
    await this.d.onSwitchFinished()
  }

  /** Finishes a switch once its page has loaded, or failed to load. */
  afterWebuiEvent(e: WebuiEvent): void {
    if (this.switching?.step === 'loading' && (e.type === 'view-loaded' || e.type === 'view-load-failed')) void this.finishSwitch()
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
    await this.d.store.update((x) => withDefaultTarget(x, prev))
    if (prev === LOCAL_TARGET) await this.openLocal()
    else this.openRemoteById(prev)
    this.emit()
  }

  async retryRemote(): Promise<void> {
    await this.monitor.retryNow()
  }

  /** The app left the screen (a phone): stop watching the remote default until resumeMonitor. */
  pauseMonitor(): void {
    this.monitor.stop()
  }

  /** The app is back on screen: check the remote default once, then watch it as before. */
  resumeMonitor(): void {
    if (this.switching) return
    const c = this.d.store.get()
    const r = remotes(c).find((x) => x.id === defaultTarget(c))
    if (r) void this.monitor.start(r.url)
  }

  /** Stops the monitor and every forwarder and aborts open tests. */
  stopAll(): void {
    this.monitor.stop()
    for (const id of [...this.tunnelIds]) this.stopTunnel(id)
    for (const ac of this.openTests) ac.abort()
  }
}

/**
 * The routes a forwarder dials after an invite of a saved server: those of
 * the link first, then the saved ones, each once. They are only dialled,
 * never saved, so they need not fit the caps of a saved server.
 */
export function withTrialRoutes(saved: P2PTarget, link: { addrs?: string[]; relays?: string[]; hints?: string[] }): P2PTarget {
  // The same check as a saved route: a multiaddr, never something a forwarder could read as a flag.
  const ok = (x: string) => x.length > 0 && x.length <= 300 && x.startsWith('/')
  const merge = (a: string[] = [], b: string[] = []) => [...new Set([...a.filter(ok), ...b])]
  const hints = merge(link.hints, saved.hints)
  return {
    ...saved,
    addrs: merge(link.addrs, saved.addrs),
    relays: merge(link.relays, saved.relays),
    ...(hints.length > 0 ? { hints } : {}),
  }
}

function sameRoutes(a: P2PTarget, b: P2PTarget): boolean {
  const same = (x: string[] = [], y: string[] = []) => x.length === y.length && x.every((v, i) => v === y[i])
  return same(a.addrs, b.addrs) && same(a.relays, b.relays) && same(a.hints, b.hints)
}
