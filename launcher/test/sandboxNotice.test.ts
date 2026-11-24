import { describe, expect, it } from 'vitest'
import type { SandboxState } from '../src/main/sandboxProbe'
import { initialNotice, reduceNotice, type NoticeEvent, type NoticeState } from '../src/main/sandboxNotice'

const V = '4.1.0'
const probed = (state: SandboxState, dismissedVersion: string | null = null): NoticeEvent => ({
  type: 'probed',
  state,
  launcherVersion: V,
  dismissedVersion,
})
const run = (s: NoticeState, ...es: NoticeEvent[]) => es.reduce((acc, e) => reduceNotice(acc, e).state, s)

describe('sandbox notice', () => {
  it('starts hidden and stays hidden when there is nothing to say', () => {
    expect(initialNotice.kind).toBe('hidden')
    for (const s of ['sandboxed', 'unsandboxed-other', 'not-applicable'] as const) {
      expect(reduceNotice(initialNotice, probed(s)).state.kind).toBe('hidden')
    }
  })
  it('shows when restricted', () => {
    expect(reduceNotice(initialNotice, probed('unsandboxed-restricted')).state.kind).toBe('shown')
  })
  it('"Not now" is stored for this version and returned as persist', () => {
    const shown = run(initialNotice, probed('unsandboxed-restricted'))
    const r = reduceNotice(shown, { type: 'dismiss', launcherVersion: V })
    expect(r.state.kind).toBe('dismissed')
    expect(r.persist).toEqual({ dismissedVersion: V })
  })
  it('the stored version keeps it dismissed on the next start', () => {
    expect(reduceNotice(initialNotice, probed('unsandboxed-restricted', V)).state.kind).toBe('dismissed')
  })
  it('a new launcher version shows the notice again', () => {
    expect(reduceNotice(initialNotice, probed('unsandboxed-restricted', '4.0.9')).state.kind).toBe('shown')
  })
  it('dismiss does nothing outside shown, and never persists', () => {
    const r = reduceNotice({ kind: 'installing' }, { type: 'dismiss', launcherVersion: V })
    expect(r.state.kind).toBe('installing')
    expect(r.persist).toBeUndefined()
  })
  it('setup succeeds: installing, then needs restart', () => {
    const s = run(
      initialNotice,
      probed('unsandboxed-restricted'),
      { type: 'setup-requested' },
      { type: 'setup-finished', outcome: { ok: true } },
    )
    expect(s.kind).toBe('installed-needs-restart')
  })
  it('setup can start from the dismissed status line', () => {
    expect(run({ kind: 'dismissed' }, { type: 'setup-requested' }).kind).toBe('installing')
  })
  it('a dismissed password dialog is a calm failure that can be retried', () => {
    const s = run(
      { kind: 'installing' },
      { type: 'setup-finished', outcome: { ok: false, dismissed: true } },
    )
    expect(s).toEqual({ kind: 'failed', reason: 'cancelled' })
    expect(run(s, { type: 'setup-requested' }).kind).toBe('installing')
  })
  it('a failure keeps its reason', () => {
    const s = run({ kind: 'installing' }, { type: 'setup-finished', outcome: { ok: false, reason: 'boom' } })
    expect(s).toEqual({ kind: 'failed', reason: 'boom' })
  })
  it('ignores a finish event when nothing runs', () => {
    expect(run({ kind: 'shown' }, { type: 'setup-finished', outcome: { ok: true } }).kind).toBe('shown')
  })
  it('a re-check does not interrupt a running action', () => {
    expect(run({ kind: 'installing' }, probed('unsandboxed-restricted')).kind).toBe('installing')
    expect(run({ kind: 'removing' }, probed('sandboxed')).kind).toBe('removing')
  })
  it('after install, the restart note stays while still restricted, and clears once sandboxed', () => {
    const s: NoticeState = { kind: 'installed-needs-restart' }
    expect(run(s, probed('unsandboxed-restricted')).kind).toBe('installed-needs-restart')
    expect(run(s, probed('sandboxed')).kind).toBe('hidden')
  })
  it('remove: removing, then removed; a restart check brings the notice back', () => {
    let s = run({ kind: 'installed-needs-restart' }, { type: 'remove-requested' })
    expect(s.kind).toBe('removing')
    s = run(s, { type: 'remove-finished', outcome: { ok: true } })
    expect(s.kind).toBe('removed')
    expect(run(initialNotice, probed('unsandboxed-restricted')).kind).toBe('shown')
  })
  it('remove failure keeps its reason', () => {
    const s = run({ kind: 'removing' }, { type: 'remove-finished', outcome: { ok: false, reason: 'x' } })
    expect(s).toEqual({ kind: 'failed', reason: 'x' })
  })
  it('does not mutate the state it is given', () => {
    const s: NoticeState = Object.freeze({ kind: 'shown' })
    expect(() => reduceNotice(s, { type: 'setup-requested' })).not.toThrow()
  })
})
