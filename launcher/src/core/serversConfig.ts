// The servers of launcher.json: saved remote servers, the default target, the
// opt-in to keep the local server running, and the update channel. Pure, no
// Electron import and no Node. Keys that only this launcher writes live in
// `extra`.
import type { LauncherConfig } from './configTypes'
import { validateRemoteOrigin } from './webuiPolicy'
import type { P2PRoutes, P2PTarget } from './p2pInvite'

export interface RemoteServer {
  id: string
  name: string
  /** An origin: scheme, host and port. No path, no credentials. */
  url: string
  /**
   * A server over P2P. `url` is then http://<host>:<port>, the local end of
   * the forwarder (host 127.0.0.1 unless the target names one), so the origin
   * of the server never changes.
   */
  p2p?: P2PTarget
}

const P2P_PEER_RE = /^[1-9A-HJ-NP-Za-km-z]{40,128}$/

function strings(v: unknown, max: number): string[] | null {
  if (v === undefined) return []
  if (!Array.isArray(v) || v.length > max || !v.every((x) => typeof x === 'string' && x.length > 0 && x.length <= 300 && x.startsWith('/'))) return null
  return v as string[]
}

/** The P2P part of a stored server, checked again: a hand-edited file is not trusted. */
export function parseP2PTarget(v: unknown): P2PTarget | null {
  if (typeof v !== 'object' || v === null) return null
  const o = v as Record<string, unknown>
  const addrs = strings(o.addrs, 2)
  const relays = strings(o.relays, 3)
  const hints = strings(o.hints, 2)
  if (typeof o.peer !== 'string' || !P2P_PEER_RE.test(o.peer)) return null
  if (typeof o.port !== 'number' || !Number.isInteger(o.port) || o.port < 1024 || o.port > 65535) return null
  if (!addrs || !relays || !hints) return null
  // An empty hint list is not saved: a target from before hints reads back the same.
  const t: P2PTarget = { peer: o.peer, port: o.port, addrs, relays, ...(hints.length > 0 ? { hints } : {}) }
  if (o.host === undefined) return t
  if (!isP2PHost(o.host)) return null
  return { ...t, host: o.host }
}

// No leading zeros: the URL parser reads them otherwise, and the origin would differ.
const P2P_HOST_RE = /^127\.(0|[1-9]\d{0,2})\.(0|[1-9]\d{0,2})\.(0|[1-9]\d{0,2})$/

/**
 * A loopback IPv4 address a forwarder may listen on: 127.x.y.z, each part at
 * most 255, not the network (127.0.0.0) or broadcast (127.255.255.255)
 * address. The Android plugin applies the same rule.
 */
export function isP2PHost(v: unknown): v is string {
  if (typeof v !== 'string') return false
  const m = P2P_HOST_RE.exec(v)
  if (!m) return false
  const parts = m.slice(1).map(Number)
  if (parts.some((x) => x > 255)) return false
  return !parts.every((x) => x === 0) && !parts.every((x) => x === 255)
}

/** The origin of a server over P2P: the local end of its forwarder. */
export function p2pOrigin(port: number, host = '127.0.0.1'): string {
  return `http://${host}:${port}`
}

export const REMOTES_KEY = 'remotes'
export const DEFAULT_TARGET_KEY = 'default_target'
export const KEEP_LOCAL_KEY = 'keep_local_running'
export const UPDATE_CHANNEL_KEY = 'update_channel'
/** Old key of the Go-era settings; kept as a read-only alias of the `rc` channel. */
export const INCLUDE_PRERELEASES_ALIAS = 'include_prereleases'
export const DEV_CONFIRMED_KEY = 'dev_channel_confirmed'
/** The old "last server chosen in Connect". Still read, never written. */
export const LEGACY_REMOTE_URL_KEY = 'remote_server_url'

export const MAX_REMOTES = 20
export const MAX_NAME = 60
export const LOCAL_TARGET = 'local'
const ID_RE = /^[A-Za-z0-9_-]{1,40}$/

export type UpdateChannel = 'stable' | 'rc' | 'dev'
export const UPDATE_CHANNELS: readonly UpdateChannel[] = ['stable', 'rc', 'dev']

/** Random bytes from Web Crypto, present in browsers and in Node. */
export function webRandomBytes(n: number): Uint8Array {
  return globalThis.crypto.getRandomValues(new Uint8Array(n))
}

/** `srv_` and 8 hex digits. The same bytes give the same id on every platform. */
export function newRemoteId(randomBytes: (n: number) => Uint8Array = webRandomBytes): string {
  let hex = ''
  for (const b of randomBytes(4)) hex += b.toString(16).padStart(2, '0')
  return `srv_${hex}`
}

export function validName(name: unknown): name is string {
  return typeof name === 'string' && name.trim().length >= 1 && name.trim().length <= MAX_NAME && !/[\u0000-\u001f\u007f]/.test(name)
}

/** The list as stored, checked again on read: a hand-edited file is not trusted. */
export function parseRemotes(raw: unknown): RemoteServer[] {
  if (!Array.isArray(raw)) return []
  const out: RemoteServer[] = []
  const urls = new Set<string>()
  const ids = new Set<string>()
  for (const e of raw) {
    if (typeof e !== 'object' || e === null) continue
    const { id, name, url, p2p } = e as Record<string, unknown>
    if (typeof id !== 'string' || !ID_RE.test(id) || ids.has(id) || !validName(name) || typeof url !== 'string') continue
    const o = validateRemoteOrigin(url)
    if (!o.ok || urls.has(o.origin)) continue
    let target: P2PTarget | undefined
    if (p2p !== undefined) {
      const t = parseP2PTarget(p2p)
      // The origin of a server over P2P is its forwarder: anything else is a
      // file someone edited by hand.
      if (!t || o.origin !== p2pOrigin(t.port, t.host)) continue
      target = t
    }
    ids.add(id)
    urls.add(o.origin)
    out.push(target ? { id, name: name.trim(), url: o.origin, p2p: target } : { id, name: name.trim(), url: o.origin })
    if (out.length >= MAX_REMOTES) break
  }
  return out
}

export function remotes(c: LauncherConfig): RemoteServer[] {
  return parseRemotes(c.extra[REMOTES_KEY])
}

function withRemotes(c: LauncherConfig, list: RemoteServer[]): LauncherConfig {
  return { ...c, extra: { ...c.extra, [REMOTES_KEY]: list } }
}

/**
 * Desktop only. The desktop forwarder always listens on 127.0.0.1, so a
 * server over P2P whose stored host is another loopback address could never
 * connect there. Such an entry comes only from a hand-edited file; the
 * desktop drops it on read, like any other entry it cannot use.
 */
export function withoutP2PHosts(c: LauncherConfig): { config: LauncherConfig; dropped: number } {
  const raw = c.extra[REMOTES_KEY]
  if (!Array.isArray(raw)) return { config: c, dropped: 0 }
  const kept = raw.filter((e: unknown) => {
    if (typeof e !== 'object' || e === null) return true
    const p2p = (e as Record<string, unknown>).p2p
    if (typeof p2p !== 'object' || p2p === null) return true
    const host = (p2p as Record<string, unknown>).host
    return host === undefined || host === '127.0.0.1'
  })
  if (kept.length === raw.length) return { config: c, dropped: 0 }
  return { config: withRemotes(c, kept), dropped: raw.length - kept.length }
}

/** The default target; a missing value or an id that names no saved server is `local`. */
export function defaultTarget(c: LauncherConfig): string {
  const v = c.extra[DEFAULT_TARGET_KEY]
  if (typeof v === 'string' && v !== LOCAL_TARGET && remotes(c).some((r) => r.id === v)) return v
  return LOCAL_TARGET
}

/** Written only when it changes, like show_status_bar. */
export function withDefaultTarget(c: LauncherConfig, target: string): LauncherConfig {
  if (defaultTarget(c) === target) return c
  return { ...c, extra: { ...c.extra, [DEFAULT_TARGET_KEY]: target } }
}

export function keepLocalRunning(c: LauncherConfig): boolean {
  return c.extra[KEEP_LOCAL_KEY] === true
}

export function withKeepLocalRunning(c: LauncherConfig, on: boolean): LauncherConfig {
  const extra = { ...c.extra }
  if (on) extra[KEEP_LOCAL_KEY] = true
  else delete extra[KEEP_LOCAL_KEY]
  return { ...c, extra }
}

export type RemoteFail = 'invalid-url' | 'scheme' | 'credentials' | 'needs-https' | 'name' | 'duplicate' | 'limit' | 'not-found'
export type RemoteChange = { ok: true; config: LauncherConfig; remote: RemoteServer } | { ok: false; reason: RemoteFail; existingId?: string }

export function addRemote(c: LauncherConfig, input: { name: string; address: string }, genId: () => string = newRemoteId): RemoteChange {
  const list = remotes(c)
  if (list.length >= MAX_REMOTES) return { ok: false, reason: 'limit' }
  if (!validName(input.name)) return { ok: false, reason: 'name' }
  const o = validateRemoteOrigin(input.address)
  if (!o.ok) return { ok: false, reason: o.reason as RemoteFail }
  const dup = list.find((r) => r.url === o.origin)
  if (dup) return { ok: false, reason: 'duplicate', existingId: dup.id }
  let id = genId()
  while (list.some((r) => r.id === id)) id = genId()
  const remote = { id, name: input.name.trim(), url: o.origin }
  return { ok: true, config: withRemotes(c, [...list, remote]), remote }
}

/** Adds a server over P2P that `local-ai p2p pair` has just paired with. */
export function addP2PRemote(c: LauncherConfig, input: { name: string; target: P2PTarget }, genId: () => string = newRemoteId): RemoteChange {
  const list = remotes(c)
  if (list.length >= MAX_REMOTES) return { ok: false, reason: 'limit' }
  if (!validName(input.name)) return { ok: false, reason: 'name' }
  const target = parseP2PTarget(input.target)
  if (!target) return { ok: false, reason: 'invalid-url' }
  const dup = list.find((r) => r.p2p?.peer === target.peer)
  if (dup) return { ok: false, reason: 'duplicate', existingId: dup.id }
  const url = p2pOrigin(target.port, target.host)
  if (list.some((r) => r.url === url)) return { ok: false, reason: 'duplicate' }
  let id = genId()
  while (list.some((r) => r.id === id)) id = genId()
  const remote = { id, name: input.name.trim(), url, p2p: target }
  return { ok: true, config: withRemotes(c, [...list, remote]), remote }
}

export function updateRemote(c: LauncherConfig, id: string, patch: { name?: string; address?: string }): RemoteChange {
  const list = remotes(c)
  const cur = list.find((r) => r.id === id)
  if (!cur) return { ok: false, reason: 'not-found' }
  let name = cur.name
  let url = cur.url
  if (patch.name !== undefined) {
    if (!validName(patch.name)) return { ok: false, reason: 'name' }
    name = patch.name.trim()
  }
  if (patch.address !== undefined && cur.p2p) {
    // The address of a server over P2P is its forwarder; pair again to move it.
    const o = validateRemoteOrigin(patch.address)
    if (!o.ok || o.origin !== cur.url) return { ok: false, reason: 'invalid-url' }
    patch = { name: patch.name }
  }
  if (patch.address !== undefined) {
    const o = validateRemoteOrigin(patch.address)
    if (!o.ok) return { ok: false, reason: o.reason as RemoteFail }
    const dup = list.find((r) => r.url === o.origin && r.id !== id)
    if (dup) return { ok: false, reason: 'duplicate', existingId: dup.id }
    url = o.origin
  }
  const remote: RemoteServer = cur.p2p ? { id, name, url, p2p: cur.p2p } : { id, name, url }
  return { ok: true, config: withRemotes(c, list.map((r) => (r.id === id ? remote : r))), remote }
}

/**
 * Replaces the routes of a saved server over P2P with fresher ones: a routes
 * line of its forwarder, or an invite of the same server. The peer, port and
 * host stay, so the origin and the sign-in stay. `changed` is false when the
 * routes are the same.
 */
export function withP2PRoutes(
  c: LauncherConfig,
  id: string,
  routes: P2PRoutes,
): { ok: true; config: LauncherConfig; remote: RemoteServer; changed: boolean } | { ok: false; reason: 'not-found' | 'invalid-url' } {
  const list = remotes(c)
  const cur = list.find((r) => r.id === id)
  if (!cur?.p2p) return { ok: false, reason: 'not-found' }
  const { peer, port, host } = cur.p2p
  const target = parseP2PTarget({ peer, port, ...(host !== undefined ? { host } : {}), addrs: routes.addrs, relays: routes.relays, hints: routes.hints })
  if (!target) return { ok: false, reason: 'invalid-url' }
  const same = (a: string[] = [], b: string[] = []) => a.length === b.length && a.every((x, i) => x === b[i])
  const changed = !same(cur.p2p.addrs, target.addrs) || !same(cur.p2p.relays, target.relays) || !same(cur.p2p.hints, target.hints)
  if (!changed) return { ok: true, config: c, remote: cur, changed: false }
  const remote: RemoteServer = { ...cur, p2p: target }
  return { ok: true, config: withRemotes(c, list.map((r) => (r.id === id ? remote : r))), remote, changed: true }
}

/** Removes a server. When it was the default, the default falls back to This computer. */
export function removeRemote(c: LauncherConfig, id: string): LauncherConfig {
  const wasDefault = defaultTarget(c) === id
  const next = withRemotes(c, remotes(c).filter((r) => r.id !== id))
  if (!wasDefault) return next
  return { ...next, extra: { ...next.extra, [DEFAULT_TARGET_KEY]: LOCAL_TARGET } }
}

export function updateChannel(c: LauncherConfig): UpdateChannel {
  const v = c.extra[UPDATE_CHANNEL_KEY]
  if (typeof v === 'string' && (UPDATE_CHANNELS as readonly string[]).includes(v)) return v as UpdateChannel
  return c.extra[INCLUDE_PRERELEASES_ALIAS] === true ? 'rc' : 'stable'
}

export function withUpdateChannel(c: LauncherConfig, ch: UpdateChannel): LauncherConfig {
  return { ...c, extra: { ...c.extra, [UPDATE_CHANNEL_KEY]: ch } }
}

export function devChannelConfirmed(c: LauncherConfig): boolean {
  return c.extra[DEV_CONFIRMED_KEY] === true
}

export function withDevChannelConfirmed(c: LauncherConfig): LauncherConfig {
  return { ...c, extra: { ...c.extra, [DEV_CONFIRMED_KEY]: true } }
}

/**
 * Moves the old `remote_server_url` into the list, once (the `remotes` key
 * marks it done). It never becomes the default: the old key was only a spike
 * value, and an existing local user must stay on This computer. The old key
 * stays and is not written again.
 */
export function migrateRemoteUrl(c: LauncherConfig, genId: () => string = newRemoteId): { config: LauncherConfig; changed: boolean } {
  if (REMOTES_KEY in c.extra) return { config: c, changed: false }
  const old = c.extra[LEGACY_REMOTE_URL_KEY]
  if (typeof old !== 'string') return { config: c, changed: false }
  const o = validateRemoteOrigin(old)
  if (!o.ok) return { config: c, changed: false }
  const remote = { id: genId(), name: o.host, url: o.origin }
  return { config: withRemotes(c, [remote]), changed: true }
}
