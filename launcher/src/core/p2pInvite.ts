// Invite links, pairing answers and forwarder status lines of servers reached
// over P2P. Pure, no Electron import and no Node: the desktop launcher reads
// them from the `local-ai p2p` commands, a client-only shell from its own P2P
// library, and both parse them here.
export const INVITE_PREFIX = 'localai://pair?'
const PEER_RE = /^[1-9A-HJ-NP-Za-km-z]{40,128}$/

/** What the launcher reads from an invite link before it uses it. */
export interface InviteInfo {
  kind: string
  peer: string
  label: string
  /** Milliseconds since the epoch. */
  expires: number
  /** The link names an open relay: anyone can use it. */
  openRelay: boolean
}

export type InviteProblem = 'invite-invalid' | 'invite-kind' | 'invite-expired'

/**
 * Reads an invite link. It checks only what the launcher must know before it
 * runs the pairing: the full check is the server's.
 */
export function readInvite(text: string, now: number): { ok: true; info: InviteInfo } | { ok: false; reason: InviteProblem } {
  const link = text.trim()
  if (!link.startsWith(INVITE_PREFIX) || link.length > 2048) return { ok: false, reason: 'invite-invalid' }
  const q = new URLSearchParams(link.slice(INVITE_PREFIX.length))
  const kind = q.get('k') ?? ''
  const peer = q.get('p') ?? ''
  const expires = Number(q.get('e') ?? '') * 1000
  if (q.get('v') !== '1' || !PEER_RE.test(peer) || !q.get('c') || !Number.isFinite(expires) || expires <= 0) {
    return { ok: false, reason: 'invite-invalid' }
  }
  // A device invite admits this computer, and the person signs in as on any
  // browser. A remote invite works too: its key is simply not used here.
  if (kind !== 'device' && kind !== 'remote') return { ok: false, reason: 'invite-kind' }
  if (expires <= now) return { ok: false, reason: 'invite-expired' }
  return { ok: true, info: { kind, peer, label: q.get('n') ?? '', expires, openRelay: (q.get('o') ?? '') !== '' } }
}

/** A saved server over P2P: how the forwarder reaches it. */
export interface P2PTarget {
  peer: string
  /** Loopback port of the forwarder; the origin of the server. */
  port: number
  addrs: string[]
  relays: string[]
  /**
   * The loopback address the forwarder listens on, `127.x.y.z`. Missing
   * means 127.0.0.1 (the desktop). The Android app gives each server its own
   * host, so two servers never share an origin, and so never a sign-in cookie.
   */
  host?: string
}

/** The result of `local-ai p2p pair --json`. */
export interface PairResult {
  kind: string
  phrase: string
  serverName: string
  peer: string
}

export type PairFail = 'pair-failed' | 'no-binary'

/** What a pairing gives: the result, or why it failed in the words of the pairing tool. */
export type PairOutcome = { ok: true; result: PairResult } | { ok: false; reason: PairFail; detail: string }

/**
 * Reads the JSON answer of a pairing: the last non-empty line of the output.
 * Undefined when the line is not JSON, the phrase is missing or the peer ID
 * of the server is not one.
 */
export function parsePairJson(text: string): PairResult | undefined {
  const lines = text.split('\n').filter((l) => l.trim() !== '')
  try {
    const j = JSON.parse(lines[lines.length - 1] ?? '') as {
      kind?: unknown
      phrase?: unknown
      server?: { name?: unknown; peer_id?: unknown }
    }
    const peer = typeof j.server?.peer_id === 'string' ? j.server.peer_id : ''
    if (typeof j.phrase !== 'string' || !PEER_RE.test(peer)) return undefined
    return { kind: typeof j.kind === 'string' ? j.kind : '', phrase: j.phrase, serverName: typeof j.server?.name === 'string' ? j.server.name : '', peer }
  } catch {
    return undefined
  }
}

/** Reads the relays and addresses of a link, as the pair command saved them. */
export function linkRoutes(link: string): { addrs: string[]; relays: string[] } {
  const q = new URLSearchParams(link.trim().slice(INVITE_PREFIX.length))
  return { addrs: q.getAll('a').slice(0, 2), relays: q.getAll('r').slice(0, 3) }
}

export type TunnelState = 'starting' | 'ready' | 'up' | 'down' | 'stopped' | 'failed'

export interface TunnelStatus {
  state: TunnelState
  /** direct, hole_punched or relayed, after the first forwarded connection. */
  path?: string
  error?: string
}

/** Delay before the n-th restart (n from 0): 1 s doubling up to 60 s. */
export function backoffMs(n: number): number {
  return Math.min(60_000, 1000 * 2 ** Math.min(n, 6))
}

/**
 * Reads one JSON status line of the forwarder. `listening` is the same as
 * `ready`; a path of `none` means no connection went through yet. Undefined
 * for a line that is not JSON or names no known state.
 */
export function parseForwardState(
  line: string,
): { state: 'ready' } | { state: 'up'; path?: string } | { state: 'down'; error?: string } | undefined {
  let j: { state?: unknown; path?: unknown; error?: unknown }
  try {
    j = JSON.parse(line) as typeof j
  } catch {
    return undefined
  }
  if (typeof j !== 'object' || j === null) return undefined
  switch (j.state) {
    case 'ready':
    case 'listening':
      return { state: 'ready' }
    case 'up':
      return typeof j.path === 'string' && j.path !== '' && j.path !== 'none' ? { state: 'up', path: j.path } : { state: 'up' }
    case 'down':
      return typeof j.error === 'string' && j.error !== '' ? { state: 'down', error: j.error } : { state: 'down' }
  }
  return undefined
}
