// State machine of the sandbox notice. Pure reducer: no Electron, no storage.
// The caller keeps the dismissed version and passes it in on `probed`; when the
// reducer returns `persist`, the caller stores that value.

import type { SandboxState } from './sandboxProbe'

export type NoticeState =
  /** Nothing to say: sandboxed, not applicable, or another cause. */
  | { kind: 'hidden' }
  | { kind: 'shown' }
  /** "Not now" was chosen for this launcher version. Settings shows a status line. */
  | { kind: 'dismissed' }
  | { kind: 'installing' }
  | { kind: 'removing' }
  | { kind: 'installed-needs-restart' }
  | { kind: 'failed'; reason: string }
  | { kind: 'removed' }

export type SetupOutcome = { ok: true } | { ok: false; dismissed: true } | { ok: false; dismissed?: false; reason: string }

export type NoticeEvent =
  | { type: 'probed'; state: SandboxState; launcherVersion: string; dismissedVersion: string | null }
  | { type: 'dismiss'; launcherVersion: string }
  | { type: 'setup-requested' }
  | { type: 'setup-finished'; outcome: SetupOutcome }
  | { type: 'remove-requested' }
  | { type: 'remove-finished'; outcome: SetupOutcome }

export interface NoticeResult {
  state: NoticeState
  /** When set, the caller stores this as the dismissed launcher version. */
  persist?: { dismissedVersion: string }
}

export const initialNotice: NoticeState = { kind: 'hidden' }

const fail = (o: SetupOutcome): NoticeState =>
  o.ok ? { kind: 'hidden' } : { kind: 'failed', reason: o.dismissed ? 'cancelled' : o.reason }

export function reduceNotice(s: NoticeState, e: NoticeEvent): NoticeResult {
  switch (e.type) {
    case 'probed': {
      // A running action or a pending restart is not undone by a re-check,
      // unless the sandbox is now on.
      const busy = s.kind === 'installing' || s.kind === 'removing'
      if (busy) return { state: s }
      if (e.state !== 'unsandboxed-restricted') return { state: { kind: 'hidden' } }
      if (s.kind === 'installed-needs-restart') return { state: s }
      if (s.kind === 'removed' || s.kind === 'failed') return { state: s }
      return { state: { kind: e.dismissedVersion === e.launcherVersion ? 'dismissed' : 'shown' } }
    }
    case 'dismiss':
      if (s.kind !== 'shown') return { state: s }
      return { state: { kind: 'dismissed' }, persist: { dismissedVersion: e.launcherVersion } }
    case 'setup-requested':
      if (s.kind === 'shown' || s.kind === 'dismissed' || s.kind === 'failed' || s.kind === 'removed') {
        return { state: { kind: 'installing' } }
      }
      return { state: s }
    case 'setup-finished':
      if (s.kind !== 'installing') return { state: s }
      return { state: e.outcome.ok ? { kind: 'installed-needs-restart' } : fail(e.outcome) }
    case 'remove-requested':
      if (s.kind === 'installed-needs-restart' || s.kind === 'failed' || s.kind === 'dismissed' || s.kind === 'shown') {
        return { state: { kind: 'removing' } }
      }
      return { state: s }
    case 'remove-finished':
      if (s.kind !== 'removing') return { state: s }
      return { state: e.outcome.ok ? { kind: 'removed' } : fail(e.outcome) }
  }
}
