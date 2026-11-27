// SPDX-License-Identifier: MIT
// The forwarders of servers over P2P on Android: the P2PTunnels of the shared
// core over the P2P plugin. The Kotlin foreground service owns the
// forwarders, restarts them with backoff and on network changes; this side
// sends the full list of saved servers and maps the state lines it gets back.
//
// Each server listens on its own loopback host (127.0.0.2, 127.0.0.3, ...),
// so each has its own origin: the WebUI view keeps one profile, and so one
// sign-in cookie, per server.
import type { PluginListenerHandle } from '@capacitor/core'
import type { LauncherConfig } from '@launcher/core/configTypes'
import type { P2PTarget } from '@launcher/core/p2pInvite'
import { parseForwardState, parsePairJson, parseRoutesLine, type P2PRoutes, type PairOutcome } from '@launcher/core/p2pInvite'
import { withTrialRoutes, type P2PTunnels } from '@launcher/core/remoteServers'
import { isP2PHost, remotes } from '@launcher/core/serversConfig'
import type { TunnelStatusView } from '@launcher/shared/types'
import type { P2PForwardInput, P2PPlugin } from '../plugins/definitions'

/** What `P2P.version()` answers when the native library loaded. */
export const P2P_LIBRARY_VERSION = 'p2pmobile/4'

/**
 * The library versions this app works with. p2pmobile/4 returns the device
 * credential of a sign-in invite; with p2pmobile/3 such an invite pairs
 * without sign-in. p2pmobile/3 adds the routes state line; with p2pmobile/2
 * the saved routes only change by an invite.
 */
const KNOWN_LIBRARY_VERSIONS = new Set([P2P_LIBRARY_VERSION, 'p2pmobile/3', 'p2pmobile/2'])

/** The host of a server over P2P saved without one (the desktop's). */
const DEFAULT_HOST = '127.0.0.1'

/** The next free loopback host for a new server: 127.0.0.2, 127.0.0.3, ... Never 127.0.0.1. */
export function nextLoopbackHost(used: string[]): string {
  const taken = new Set(used)
  for (let b = 0; b < 256; b++) {
    for (let c = 0; c < 256; c++) {
      for (let d = 1; d < 255; d++) {
        if (b === 0 && c === 0 && d === 1) continue
        const h = `127.${b}.${c}.${d}`
        if (!taken.has(h)) return h
      }
    }
  }
  throw new Error('no free loopback address')
}

/**
 * Maps one state line of the plugin. The facade sends `ready`, `up`,
 * `down` and `closed`; the Kotlin supervisor adds `starting` (each start
 * attempt) and `failed` (a start that threw, retried with backoff). `closed`
 * comes when the service stops: the forwarder is stopped until the app is on
 * screen again.
 */
export function mapForwardLine(line: string): TunnelStatusView | undefined {
  let j: { state?: unknown; error?: unknown }
  try {
    j = JSON.parse(line) as typeof j
  } catch {
    return undefined
  }
  if (typeof j !== 'object' || j === null) return undefined
  switch (j.state) {
    case 'starting':
      return { state: 'starting' }
    case 'closed':
      return { state: 'stopped' }
    case 'failed':
      return typeof j.error === 'string' && j.error !== '' ? { state: 'failed', error: j.error } : { state: 'failed' }
  }
  return parseForwardState(line)
}

export interface AndroidTunnelsOptions {
  plugin: P2PPlugin
  /** The current config: the forward list is always rebuilt from the saved servers. */
  store(): LauncherConfig
  /** A state changed: the caller publishes a fresh servers view (RemoteServers has no hook for it). */
  onChange(): void
  /** The "keep P2P in the background" setting changed natively (the notification's Disconnect). */
  onKeepChange?(on: boolean): void
  /** The forwarder of a saved server sent the routes the server gives now: the caller saves them. */
  onRoutes?(id: string, routes: P2PRoutes): void
  log?(message: string, level?: 'info' | 'warn'): void
  /** How long `init` waits for the plugin to say whether the library loaded. */
  initTimeoutMs?: number
}

/** P2PTunnels plus what the Android client core drives on its own. */
export interface AndroidTunnels extends P2PTunnels {
  allocate(): Promise<{ port: number; host: string }>
  /** Asks the plugin whether the library loaded, listens to its lines and reads the last states. Call before startTunnels. */
  init(): Promise<void>
  /**
   * The app is on screen again: reads every state and the keep setting. The
   * plugin answers only once restarting forwarders listen again (or after
   * 3 s), so a WebUI reload after this finds the port open.
   */
  refresh(): Promise<void>
  keepInBackground(): boolean
  setKeepInBackground(on: boolean): Promise<void>
  /**
   * The person opens the saved server `id` from the server list: turns its
   * automatic sign-in on again after a sign-out in its WebUI. True when it
   * was off. Never throws; a server that is not over P2P answers false.
   */
  allowAutoSignIn(id: string): Promise<boolean>
  close(): Promise<void>
}

const INIT_TIMEOUT_MS = 10_000
/** How long opening a server waits for the plugin to turn its automatic sign-in on again. */
const ALLOW_SIGN_IN_TIMEOUT_MS = 2_000

function message(e: unknown): string {
  return e instanceof Error ? e.message : typeof e === 'object' && e !== null && 'message' in e ? String((e as { message: unknown }).message) : String(e)
}

function withTimeout<T>(p: Promise<T>, ms: number): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const t = setTimeout(() => reject(new Error('timed out')), ms)
    p.then(
      (v) => {
        clearTimeout(t)
        resolve(v)
      },
      (e: unknown) => {
        clearTimeout(t)
        reject(e instanceof Error ? e : new Error(message(e)))
      },
    )
  })
}

export function androidP2PTunnels(o: AndroidTunnelsOptions): AndroidTunnels {
  const { plugin } = o
  const log = (m: string, level: 'info' | 'warn' = 'info') => o.log?.(m, level)
  let available = false
  let keep = false
  /** The ids started and not stopped since. */
  const active = new Set<string>()
  const states = new Map<string, TunnelStatusView>()
  // Routes a start asked for that are not saved: those of an invite of a
  // saved server, dialled until the server gives its own (see the core).
  const trials = new Map<string, P2PTarget>()
  /** The peer ID of each started server, to delete its credential once the server is removed. */
  const peers = new Map<string, string>()
  /** Hosts handed out by allocate whose server is not saved yet. */
  const reserved = new Set<string>()
  const waiters = new Set<() => void>()
  const handles: PluginListenerHandle[] = []
  /** setForwards calls run one after the other, so the last list sent wins. */
  let queue: Promise<void> = Promise.resolve()

  const wake = () => {
    for (const w of [...waiters]) w()
  }

  const setState = (id: string, s: TunnelStatusView) => {
    states.set(id, s)
    wake()
  }

  const setKeep = (on: boolean) => {
    if (on === keep) return
    keep = on
    o.onKeepChange?.(on)
  }

  const onLine = (id: string, line: string, notify: boolean) => {
    // A routes line is no state: the forwarder still listens as before.
    const routes = parseRoutesLine(line)
    if (routes) {
      if (notify && active.has(id)) o.onRoutes?.(id, routes)
      return
    }
    const s = mapForwardLine(line)
    if (!s) return
    setState(id, s)
    if (notify) o.onChange()
  }

  /** The full list of started servers, from the saved config. Only loopback hosts reach the plugin. */
  const forwards = (): P2PForwardInput[] => {
    const out: P2PForwardInput[] = []
    for (const r of remotes(o.store())) {
      if (!r.p2p || !active.has(r.id)) continue
      const host = r.p2p.host ?? DEFAULT_HOST
      if (!isP2PHost(host)) {
        setState(r.id, { state: 'failed', error: 'listen address must be loopback' })
        continue
      }
      const trial = trials.get(r.id)
      const t = trial ? withTrialRoutes(r.p2p, trial) : r.p2p
      out.push({ id: r.id, name: r.name, peer: r.p2p.peer, host, port: r.p2p.port, addrs: [...t.addrs], relays: [...t.relays], hints: [...(t.hints ?? [])] })
    }
    return out
  }

  const sync = () => {
    const list = forwards()
    queue = queue
      .then(() => plugin.setForwards({ forwards: list }))
      .catch((e: unknown) => {
        const msg = message(e)
        log(`servers: the P2P forwarders could not be set: ${msg}`, 'warn')
        for (const f of list) if (active.has(f.id)) setState(f.id, { state: 'failed', error: msg })
        o.onChange()
      })
  }

  const readNative = async () => {
    try {
      const { states: lines } = await plugin.statuses()
      for (const x of lines) onLine(x.id, x.line, false)
    } catch (e) {
      log(`servers: could not read the P2P states: ${message(e)}`, 'warn')
    }
    try {
      setKeep((await plugin.keepInBackground()).on === true)
    } catch (e) {
      log(`servers: could not read the P2P background setting: ${message(e)}`, 'warn')
    }
  }

  /** The saved servers over P2P, by peer ID. */
  const savedPeers = (): string[] => remotes(o.store()).flatMap((r) => (r.p2p ? [r.p2p.peer] : []))

  /**
   * A stopped server that is no longer saved was removed: its device
   * credential (of a sign-in invite) is deleted too. Another saved server
   * with the same peer keeps it.
   */
  const forgetIfRemoved = (id: string) => {
    const peer = peers.get(id)
    peers.delete(id)
    if (!peer) return
    const c = remotes(o.store())
    if (c.some((r) => r.id === id) || c.some((r) => r.p2p?.peer === peer)) return
    plugin.forgetCredential({ peer }).catch((e: unknown) => log(`servers: could not delete the sign-in of a removed server: ${message(e)}`, 'warn'))
  }

  const status = (id: string): TunnelStatusView | undefined => (active.has(id) ? states.get(id) : undefined)

  const allocate = async (): Promise<{ port: number; host: string }> => {
    const saved = remotes(o.store())
      .filter((r) => r.p2p)
      .map((r) => r.p2p?.host ?? DEFAULT_HOST)
    for (const h of saved) reserved.delete(h)
    const host = nextLoopbackHost([...saved, ...reserved])
    reserved.add(host)
    try {
      const { port } = await plugin.freePort({ host })
      if (!Number.isInteger(port) || port < 1024 || port > 65535) throw new Error(`unexpected port ${String(port)}`)
      return { port, host }
    } catch (e) {
      reserved.delete(host)
      throw e
    }
  }

  return {
    available: () => available,

    async pair(input): Promise<PairOutcome> {
      let res: Awaited<ReturnType<P2PPlugin['pair']>>
      try {
        res = await plugin.pair(input)
      } catch (e) {
        return { ok: false, reason: 'pair-failed', detail: message(e) }
      }
      if (!res.ok) {
        // The app has its own words for a server that needs a relay; the
        // library's text goes to the log only.
        if (res.code === 'needs_relay') {
          log(`servers: pairing needs a relay: ${res.detail}`, 'warn')
          return { ok: false, reason: 'needs-relay', detail: '' }
        }
        return { ok: false, reason: 'pair-failed', detail: res.detail }
      }
      const result = parsePairJson(res.json)
      if (!result) return { ok: false, reason: 'pair-failed', detail: 'unexpected answer' }
      // A server over P2P is being added: the service notification (and its
      // Disconnect) needs the permission on Android 13+. The plugin shows
      // the system dialog at most once.
      void plugin.requestNotifications().catch((e: unknown) => log(`servers: notification permission: ${message(e)}`, 'warn'))
      return { ok: true, result }
    },

    allocate,
    allocatePort: async () => (await allocate()).port,

    start(id, _name, target) {
      active.add(id)
      const saved = remotes(o.store()).find((r) => r.id === id)?.p2p
      const peer = saved?.peer ?? target?.peer
      if (peer) peers.set(id, peer)
      if (target && saved && !sameRouteLists(target, saved)) trials.set(id, target)
      else trials.delete(id)
      const cur = states.get(id)
      if (!cur || cur.state === 'stopped' || cur.state === 'failed') states.set(id, { state: 'starting' })
      sync()
    },

    stop(id) {
      trials.delete(id)
      forgetIfRemoved(id)
      if (!active.delete(id)) return
      states.delete(id)
      sync()
      wake()
    },

    status,

    whenReady(id, timeoutMs) {
      // undefined: still waiting.
      const verdict = (): boolean | undefined => {
        const s = status(id)
        if (!active.has(id)) return false
        if (!s) return undefined
        if (s.state === 'ready' || s.state === 'up' || s.state === 'down') return true
        if (s.state === 'failed' || s.state === 'stopped') return false
        return undefined
      }
      const now = verdict()
      if (now !== undefined) return Promise.resolve(now)
      return new Promise<boolean>((resolve) => {
        const finish = (ok: boolean) => {
          clearTimeout(timer)
          waiters.delete(check)
          resolve(ok)
        }
        const check = () => {
          const v = verdict()
          if (v !== undefined) finish(v)
        }
        const timer = setTimeout(() => finish(false), timeoutMs)
        waiters.add(check)
      })
    },

    async init() {
      try {
        const v = await withTimeout(plugin.version(), o.initTimeoutMs ?? INIT_TIMEOUT_MS)
        available = KNOWN_LIBRARY_VERSIONS.has(v.version)
        if (!available) log(`servers: unknown P2P library ${v.version}`, 'warn')
      } catch (e) {
        available = false
        log(`servers: P2P is not available in this build: ${message(e)}`)
      }
      if (!available) return
      try {
        handles.push(await plugin.addListener('forwardState', (x) => onLine(x.id, x.line, true)))
        handles.push(await plugin.addListener('keepInBackground', (x) => setKeep(x.on === true)))
      } catch (e) {
        log(`servers: could not listen to the P2P plugin: ${message(e)}`, 'warn')
      }
      await readNative()
      // Credentials of servers removed while the app could not tell the plugin.
      try {
        await plugin.retainCredentials({ peers: savedPeers() })
      } catch (e) {
        log(`servers: could not clean up the stored sign-ins: ${message(e)}`, 'warn')
      }
    },

    async refresh() {
      if (!available) return
      await readNative()
      o.onChange()
    },

    keepInBackground: () => keep,

    async setKeepInBackground(on) {
      if (!available) return
      await plugin.setKeepInBackground({ on })
      setKeep(on)
    },

    async allowAutoSignIn(id) {
      if (!available) return false
      const peer = remotes(o.store()).find((r) => r.id === id)?.p2p?.peer
      if (!peer) return false
      try {
        return (await withTimeout(plugin.allowAutoSignIn({ peer }), ALLOW_SIGN_IN_TIMEOUT_MS)).wasSignedOut === true
      } catch (e) {
        log(`servers: could not turn the automatic sign-in on again: ${message(e)}`, 'warn')
        return false
      }
    },

    async close() {
      await queue
      for (const h of handles.splice(0)) {
        try {
          await h.remove()
        } catch {
          // already gone
        }
      }
    },
  }
}

function sameRouteLists(a: P2PTarget, b: P2PTarget): boolean {
  const same = (x: string[] = [], y: string[] = []) => x.length === y.length && x.every((v, i) => v === y[i])
  return same(a.addrs, b.addrs) && same(a.relays, b.relays) && same(a.hints, b.hints)
}
