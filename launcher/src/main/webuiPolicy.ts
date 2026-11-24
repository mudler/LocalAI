// The security policy of the WebUI view, as pure decisions. No Electron import:
// webuiView.ts calls these from the Electron handlers, and the tests call them
// with hostile input under plain Node.
//
// The WebUI view renders server pages, model output and MCP app frames, so
// everything it asks for is untrusted:
//   - navigation stays on the one server origin the user chose; any other
//     http, https or mailto link opens in the system browser; every other
//     scheme is refused;
//   - no page ever creates a window;
//   - permissions are refused unless the server origin asks for microphone,
//     camera or clipboard write in response to a user action;
//   - downloads always go through the native save dialog with a cleaned name.
import { createHash } from 'node:crypto'

// ---- origins ------------------------------------------------------------------

/**
 * The origin of an http or https URL as `scheme://host[:port]`, normalised the
 * way Chromium does it (lower-case host, punycode, default port dropped, IPv6
 * and numeric IPv4 forms canonicalised). undefined for any other scheme, for
 * text that is not a URL, and for a URL with user info: `http://a@b` is a
 * classic look-alike, and the view never needs credentials in a URL.
 */
export function httpOrigin(url: string): string | undefined {
  let u: URL
  try {
    u = new URL(url)
  } catch {
    return undefined
  }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return undefined
  if (u.username !== '' || u.password !== '') return undefined
  if (u.hostname === '') return undefined
  return u.origin
}

/** True when `url` is an http or https URL on exactly `serverOrigin` (scheme, host and port). */
export function isServerOrigin(url: string, serverOrigin: string): boolean {
  const o = httpOrigin(url)
  return o !== undefined && o === serverOrigin
}

/** Schemes the system browser may be asked to open. */
const EXTERNAL_SCHEMES = new Set(['http:', 'https:', 'mailto:'])

/** The URL to give the system browser, or undefined when it must not leave the app. */
export function externalUrl(url: string): string | undefined {
  let u: URL
  try {
    u = new URL(url)
  } catch {
    return undefined
  }
  if (!EXTERNAL_SCHEMES.has(u.protocol)) return undefined
  if (u.protocol === 'mailto:') return u.href
  if (u.username !== '' || u.password !== '' || u.hostname === '') return undefined
  return u.href
}

// ---- navigation -----------------------------------------------------------------

export type NavigationDecision =
  | { action: 'allow' }
  /** Do not navigate the view; open this URL in the system browser instead. */
  | { action: 'external'; url: string }
  | { action: 'deny'; reason: string }

export interface NavigationContext {
  /** The main frame of the view, or a subframe (iframe) inside it. */
  frame: 'main' | 'sub'
}

/**
 * Decides a navigation the page started (link, form, `location`, redirect).
 * Navigations the launcher starts itself (loadURL) do not come through here.
 */
export function decideNavigation(url: string, serverOrigin: string, ctx: NavigationContext = { frame: 'main' }): NavigationDecision {
  if (isServerOrigin(url, serverOrigin)) return { action: 'allow' }
  let u: URL
  try {
    u = new URL(url)
  } catch {
    return { action: 'deny', reason: 'not a URL' }
  }
  if (ctx.frame === 'sub') {
    // The WebUI draws MCP apps, previews and documents in srcdoc frames, and
    // object URLs it made itself. Nothing else may load inside the view.
    if (u.href === 'about:blank' || u.href === 'about:srcdoc') return { action: 'allow' }
    if (u.protocol === 'blob:' && isServerOrigin(u.pathname, serverOrigin)) return { action: 'allow' }
    // A frame never opens the browser by itself: that would let any embedded
    // page pop browser tabs without a click.
    return { action: 'deny', reason: `subframe to ${u.protocol}` }
  }
  if (u.protocol === 'http:' || u.protocol === 'https:') {
    if (u.username !== '' || u.password !== '') return { action: 'deny', reason: 'credentials in URL' }
    const ext = externalUrl(u.href)
    return ext ? { action: 'external', url: ext } : { action: 'deny', reason: 'bad http URL' }
  }
  if (u.protocol === 'mailto:') return { action: 'external', url: u.href }
  // javascript:, file:, data:, blob:, about:, chrome:, devtools:, custom protocols.
  return { action: 'deny', reason: `scheme ${u.protocol}` }
}

// ---- new windows ----------------------------------------------------------------

export type WindowOpenDecision =
  /** Load this URL in the WebUI view itself (same origin, WebUI page). */
  | { route: 'same-view'; url: string }
  | { route: 'external'; url: string }
  | { route: 'none'; reason: string }

/**
 * The same-origin paths that `window.open` or `target=_blank` may load in the
 * view itself: the single-page WebUI. Anything else on the server origin (the
 * API, the swagger page, sign-in routes that hand over to an identity
 * provider) opens in the system browser, where a new tab is what the page meant.
 */
export function sameViewPath(pathname: string): boolean {
  return pathname === '/app' || pathname.startsWith('/app/')
}

/**
 * Decides `window.open`, `target=_blank` and similar. The answer never creates
 * a window: Electron is always told to deny, and the caller routes the URL.
 */
export function decideWindowOpen(url: string, serverOrigin: string): WindowOpenDecision {
  if (isServerOrigin(url, serverOrigin)) {
    const u = new URL(url)
    if (sameViewPath(u.pathname)) return { route: 'same-view', url: u.href }
    return { route: 'external', url: u.href }
  }
  const nav = decideNavigation(url, serverOrigin, { frame: 'main' })
  if (nav.action === 'external') return { route: 'external', url: nav.url }
  return { route: 'none', reason: nav.action === 'deny' ? nav.reason : 'refused' }
}

// ---- permissions ----------------------------------------------------------------

/** Permissions the server origin may get at all. Everything else is refused. */
export type GrantablePermission = 'media' | 'clipboard-sanitized-write'

/** Permissions whose choice the user makes once per origin and the launcher remembers. */
export type RememberedPermission = 'media'

export type PermissionChoice = 'allow' | 'deny'

/** Remembered choices, per origin. Plain data so it can live in launcher.json. */
export interface PermissionStore {
  [origin: string]: Partial<Record<RememberedPermission, PermissionChoice>>
}

export const emptyPermissionStore = (): PermissionStore => ({})

export function recall(store: PermissionStore, origin: string, permission: RememberedPermission): PermissionChoice | undefined {
  return Object.prototype.hasOwnProperty.call(store, origin) ? store[origin][permission] : undefined
}

export function remember(store: PermissionStore, origin: string, permission: RememberedPermission, choice: PermissionChoice): PermissionStore {
  const prev = Object.prototype.hasOwnProperty.call(store, origin) ? store[origin] : {}
  return { ...store, [origin]: { ...prev, [permission]: choice } }
}

/** Forgets every choice, or the choices of one origin. */
export function resetPermissions(store: PermissionStore, origin?: string): PermissionStore {
  if (origin === undefined) return {}
  const out: PermissionStore = {}
  for (const [o, v] of Object.entries(store)) if (o !== origin) out[o] = v
  return out
}

/** Reads a store from the config file. Anything malformed is dropped, never trusted. */
export function parsePermissionStore(v: unknown): PermissionStore {
  const out: PermissionStore = {}
  if (typeof v !== 'object' || v === null || Array.isArray(v)) return out
  for (const [origin, choices] of Object.entries(v as Record<string, unknown>)) {
    if (httpOrigin(origin) !== origin) continue
    if (typeof choices !== 'object' || choices === null || Array.isArray(choices)) continue
    const media = (choices as Record<string, unknown>).media
    if (media === 'allow' || media === 'deny') out[origin] = { media }
  }
  return out
}

export interface PermissionRequestInfo {
  permission: string
  /** The URL of the frame that asks (Electron's `requestingUrl`, or the check's origin). */
  requestingUrl: string
  /** The URL of the top-level page of the view. */
  topUrl: string
  /** The kinds of media for a `media` request; empty means unknown. */
  mediaTypes?: readonly string[]
  /** The page had a user action shortly before (see hasRecentGesture). */
  userGesture: boolean
}

/**
 * `ask` means: show the user a prompt, then remember the answer if they want.
 * The caller treats any value it does not know as `deny`.
 */
export type PermissionDecision = 'allow' | 'deny' | 'ask'

/** Decides a permission request (the page called getUserMedia, clipboard.writeText, ...). */
export function decidePermission(r: PermissionRequestInfo, serverOrigin: string, store: PermissionStore): PermissionDecision {
  // Both the asking frame and the page around it must be the server: an
  // iframe of another origin inside a server page gets nothing.
  if (!isServerOrigin(r.requestingUrl, serverOrigin) || !isServerOrigin(r.topUrl, serverOrigin)) return 'deny'
  if (r.permission === 'clipboard-sanitized-write') return r.userGesture ? 'allow' : 'deny'
  if (r.permission === 'media') {
    const types = r.mediaTypes ?? []
    if (types.some((t) => t !== 'audio' && t !== 'video')) return 'deny'
    if (!r.userGesture) return 'deny'
    const remembered = recall(store, serverOrigin, 'media')
    return remembered ?? 'ask'
  }
  return 'deny'
}

/**
 * Decides a synchronous permission check (navigator.permissions.query, the
 * checks Chromium makes before a request). It never asks: only a remembered
 * allow or the clipboard write of the server origin pass.
 */
export function decidePermissionCheck(
  permission: string,
  requestingOrigin: string,
  topUrl: string,
  serverOrigin: string,
  store: PermissionStore,
): boolean {
  if (httpOrigin(requestingOrigin) !== serverOrigin || !isServerOrigin(topUrl, serverOrigin)) return false
  if (permission === 'clipboard-sanitized-write') return true
  if (permission === 'media') return recall(store, serverOrigin, 'media') === 'allow'
  return false
}

/**
 * Chromium's transient user activation lasts about five seconds after a
 * click or a key press. The main process cannot read it, so it keeps the
 * time of the last input event it saw for the view and uses the same window.
 */
export const GESTURE_WINDOW_MS = 5000

export function hasRecentGesture(lastInputMs: number | undefined, nowMs: number, windowMs = GESTURE_WINDOW_MS): boolean {
  return lastInputMs !== undefined && nowMs >= lastInputMs && nowMs - lastInputMs <= windowMs
}

/** Input event types (Electron's InputEvent.type) that count as a user action. */
export const GESTURE_INPUT_TYPES: ReadonlySet<string> = new Set(['mouseDown', 'mouseUp', 'keyDown', 'rawKeyDown', 'touchStart', 'touchEnd'])

// ---- downloads ------------------------------------------------------------------

const MAX_FILE_NAME = 120
const WINDOWS_RESERVED = /^(con|prn|aux|nul|com[0-9]|lpt[0-9])(\..*)?$/i

/**
 * The file name to suggest in the save dialog. The page chooses the suggested
 * name, so it is cleaned: no folders, no control or direction-override
 * characters (which can hide the real extension), no leading dots (hidden
 * files), no trailing dots or spaces, no reserved Windows names.
 */
export function sanitizeFileName(name: string, fallback = 'download'): string {
  let s = String(name)
  // Keep only the last path part, whatever separator the page used.
  s = s.split(/[/\\]/).pop() ?? ''
  // Control characters, bidirectional overrides and isolates, zero-width characters.
  s = s.replace(/[\u0000-\u001f\u007f-\u009f​-‏‪-‮⁦-⁩﻿]/g, '')
  // Characters that are not allowed in file names on some platform.
  s = s.replace(/[<>:"|?*]/g, '_')
  s = s.replace(/^[.\s]+/, '').replace(/[.\s]+$/, '')
  if (WINDOWS_RESERVED.test(s)) s = `_${s}`
  if (s.length > MAX_FILE_NAME) {
    const dot = s.lastIndexOf('.')
    const ext = dot > 0 && s.length - dot <= 16 ? s.slice(dot) : ''
    s = s.slice(0, MAX_FILE_NAME - ext.length) + ext
  }
  return s === '' ? fallback : s
}

// ---- remote servers -------------------------------------------------------------

export type RemoteOriginResult =
  | { ok: true; origin: string; host: string }
  | { ok: false; reason: 'invalid-url' | 'scheme' | 'credentials' | 'needs-https' }

function ipv4(host: string): number[] | undefined {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(host)
  if (!m) return undefined
  const parts = m.slice(1).map(Number)
  return parts.every((p) => p <= 255) ? parts : undefined
}

/**
 * True for a host that is this computer or a private network address, written
 * as an IP literal (or `localhost`). Host names other than `localhost` are not
 * trusted here, because a name can resolve to anything: they need https.
 */
export function isLocalOrPrivateHost(hostname: string): boolean {
  const h = hostname.toLowerCase()
  if (h === 'localhost') return true
  const v4 = ipv4(h)
  if (v4) {
    const [a, b] = v4
    return (
      a === 127 || // loopback
      a === 10 ||
      (a === 172 && b >= 16 && b <= 31) ||
      (a === 192 && b === 168) ||
      (a === 169 && b === 254) || // link-local
      (a === 100 && b >= 64 && b <= 127) // shared address space, used by mesh VPNs
    )
  }
  if (h.startsWith('[') && h.endsWith(']')) {
    const v6 = h.slice(1, -1)
    if (v6 === '::1') return true
    if (/^f[cd][0-9a-f]{0,2}:/.test(v6)) return true // fc00::/7 unique local
    if (/^fe[89ab][0-9a-f]?:/.test(v6)) return true // fe80::/10 link-local
    return false
  }
  return false
}

/**
 * Checks a server address the user typed. Only the origin is kept (the view
 * always opens `/app` on it). Plain http is accepted for any host (owner
 * decision 2026-11-15: it works out of the box); the shell shows a warning and
 * a chip for http off this computer (`isInsecureRemote` in testConnection.ts).
 */
export function validateRemoteOrigin(input: string): RemoteOriginResult {
  const text = input.trim()
  if (text === '' || text.length > 2048) return { ok: false, reason: 'invalid-url' }
  let u: URL
  try {
    u = new URL(text)
  } catch {
    return { ok: false, reason: 'invalid-url' }
  }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return { ok: false, reason: 'scheme' }
  if (u.username !== '' || u.password !== '') return { ok: false, reason: 'credentials' }
  if (u.hostname === '') return { ok: false, reason: 'invalid-url' }
  return { ok: true, origin: u.origin, host: u.host }
}

// ---- sessions -------------------------------------------------------------------

/**
 * The persistent session partition of the view for one origin. Cookies,
 * storage and logins of each server stay apart, and apart from the shell.
 * A hash, so the partition name never carries a host name to disk.
 */
export function partitionForOrigin(origin: string): string {
  return `persist:webui-${createHash('sha256').update(origin).digest('hex').slice(0, 24)}`
}
