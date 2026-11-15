// The servers of launcher.json: saved remote servers, the default target, the
// opt-in to keep the local server running, and the update channel. Pure, no
// Electron import. Keys that only this launcher writes live in `extra`.
import crypto from 'node:crypto'
import type { LauncherConfig } from './config'
import { validateRemoteOrigin } from './webuiPolicy'

export interface RemoteServer {
  id: string
  name: string
  /** An origin: scheme, host and port. No path, no credentials. */
  url: string
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

export function newRemoteId(randomBytes: (n: number) => Buffer = crypto.randomBytes): string {
  return `srv_${randomBytes(4).toString('hex')}`
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
    const { id, name, url } = e as Record<string, unknown>
    if (typeof id !== 'string' || !ID_RE.test(id) || ids.has(id) || !validName(name) || typeof url !== 'string') continue
    const o = validateRemoteOrigin(url)
    if (!o.ok || urls.has(o.origin)) continue
    ids.add(id)
    urls.add(o.origin)
    out.push({ id, name: name.trim(), url: o.origin })
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
  if (patch.address !== undefined) {
    const o = validateRemoteOrigin(patch.address)
    if (!o.ok) return { ok: false, reason: o.reason as RemoteFail }
    const dup = list.find((r) => r.url === o.origin && r.id !== id)
    if (dup) return { ok: false, reason: 'duplicate', existingId: dup.id }
    url = o.origin
  }
  const remote = { id, name, url }
  return { ok: true, config: withRemotes(c, list.map((r) => (r.id === id ? remote : r))), remote }
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
