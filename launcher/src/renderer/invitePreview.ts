// SPDX-License-Identifier: MIT
// What the invite form shows about a link before anyone pairs with it. Pure:
// the card, the countdown and the "not an invite" check all read from here.
import { linkRoutes, readInvite, type InviteProblem } from '../core/p2pInvite'
import type { Capabilities } from '../shared/types'
import type { Bridge } from './bridge'
import { S } from './strings'

export type { InviteProblem }

export interface InvitePreviewView {
  /** The name the server owner gave the invite; may be empty. */
  label: string
  kind: 'device' | 'remote'
  /** Milliseconds since the epoch. */
  expiresAt: number
  secondsLeft: number
  openRelay: boolean
  /** The server peer ID, to recognise a server that is already saved. */
  peer: string
  /** The server peer ID as `first10…last3`. */
  peerShort: string
  /** The routes the pairing uses (the same cut the pair step saves). */
  routes: { addrs: number; relays: number }
}

/** The preview of a link, the problem that stops it, or undefined for empty text. */
export function invitePreview(link: string, now: number): InvitePreviewView | { problem: InviteProblem } | undefined {
  if (link.trim() === '') return undefined
  const r = readInvite(link, now)
  if (!r.ok) return { problem: r.reason }
  const { info } = r
  const routes = linkRoutes(link)
  return {
    label: info.label,
    kind: info.kind === 'remote' ? 'remote' : 'device',
    expiresAt: info.expires,
    secondsLeft: Math.max(0, Math.ceil((info.expires - now) / 1000)),
    openRelay: info.openRelay,
    peer: info.peer,
    peerShort: info.peer.length > 13 ? `${info.peer.slice(0, 10)}…${info.peer.slice(-3)}` : info.peer,
    routes: { addrs: routes.addrs.length, relays: routes.relays.length },
  }
}

/**
 * A text from outside (a link, the share sheet, a QR code) is an invite when
 * it reads as one, even an expired one: the form then says what is wrong with
 * it. Any other text is not an invite and opens nothing.
 */
export function isInviteText(text: string, now: number): boolean {
  const v = invitePreview(text, now)
  return v !== undefined && !('problem' in v && v.problem === 'invite-invalid')
}

// Pairs a messenger or a QR code may wrap a link in: <...>, straight and
// curly double and single quotes.
const WRAPPERS: [string, string][] = [
  ['<', '>'],
  ['"', '"'],
  ["'", "'"],
  ['\u201c', '\u201d'],
  ['\u2018', '\u2019'],
]

/**
 * Text from the share sheet or a QR code without what is often around a
 * link in a message: spaces, surrounding <> or quotes, a trailing full stop.
 * Only the outside is touched; the link itself is not changed.
 */
export function cleanInviteText(text: string): string {
  let t = text.trim()
  for (;;) {
    const before = t
    if (t.endsWith('.')) t = t.slice(0, -1).trimEnd()
    for (const [open, close] of WRAPPERS) {
      if (t.length >= 2 && t.startsWith(open) && t.endsWith(close)) t = t.slice(open.length, -close.length).trim()
    }
    if (t === before) return t
  }
}

/**
 * The message for a scan that failed. The scanner's own reasons (no camera,
 * camera permission refused) get their own text: a failed scan is never
 * "not an invite".
 */
export function scanErrorMessage(e: unknown): string {
  const code = typeof e === 'object' && e !== null ? (e as { code?: unknown }).code : undefined
  if (code === 'no-camera') return S.invitePreview.scanNoCamera
  if (code === 'permission-denied') return S.invitePreview.scanPermissionDenied
  return S.invitePreview.scanFailed
}

/** The QR scan the invite screens offer: only where the platform says it can scan and the bridge has a scanner. */
export function inviteScanner(b: Pick<Bridge, 'scanInvite'>, caps: Pick<Capabilities, 'qrScan'>): (() => Promise<string | undefined>) | undefined {
  return caps.qrScan && b.scanInvite ? () => b.scanInvite!() : undefined
}
