import { describe, expect, it } from 'vitest'
import { idleState, reduceInstall, type InstallEvent, type InstallState } from '../src/main/serverInstall'

const run = (events: InstallEvent[], from: InstallState = idleState): InstallState => events.reduce(reduceInstall, from)

describe('reduceInstall', () => {
  it('walks the happy install path', () => {
    let s = reduceInstall(idleState, { type: 'check-start' })
    expect(s.kind).toBe('checking')
    s = reduceInstall(s, { type: 'update-available', version: '3.1.0', tag: 'v3.1.0', installed: undefined })
    expect(s).toMatchObject({ kind: 'update-available', version: '3.1.0' })
    s = reduceInstall(s, { type: 'download-start', total: 100 })
    expect(s).toEqual({ kind: 'downloading', done: 0, total: 100 })
    s = reduceInstall(s, { type: 'progress', done: 40, total: 100 })
    expect(s).toEqual({ kind: 'downloading', done: 40, total: 100 })
    s = reduceInstall(s, { type: 'verify-start' })
    expect(s.kind).toBe('verifying')
    s = reduceInstall(s, { type: 'install-start' })
    expect(s.kind).toBe('installing')
    s = reduceInstall(s, { type: 'installed', version: '3.1.0' })
    expect(s).toEqual({ kind: 'done', version: '3.1.0', adopted: false })
  })

  it('goes from checking to up-to-date', () => {
    expect(run([{ type: 'check-start' }, { type: 'up-to-date', version: '3.1.0' }])).toEqual({ kind: 'up-to-date', version: '3.1.0' })
  })

  it('adopts an existing binary from verifying', () => {
    const s = run([
      { type: 'check-start' },
      { type: 'update-available', version: '3.1.0', tag: 'v3.1.0', installed: '3.0.0' },
      { type: 'verify-start' },
      { type: 'installed', version: '3.1.0', adopted: true },
    ])
    expect(s).toEqual({ kind: 'done', version: '3.1.0', adopted: true })
  })

  it('goes on to a download after a failed adoption check', () => {
    const s = run([
      { type: 'check-start' },
      { type: 'update-available', version: '3.1.0', tag: 'v3.1.0', installed: undefined },
      { type: 'verify-start' },
      { type: 'download-start', total: 5 },
    ])
    expect(s).toEqual({ kind: 'downloading', done: 0, total: 5 })
  })

  it('goes through rolling-back to failed with the rolled-back flag', () => {
    const s = run([
      { type: 'check-start' },
      { type: 'update-available', version: '3.1.0', tag: 'v3.1.0', installed: '3.0.0' },
      { type: 'download-start', total: 1 },
      { type: 'verify-start' },
      { type: 'install-start' },
      { type: 'rollback-start' },
    ])
    expect(s.kind).toBe('rolling-back')
    expect(reduceInstall(s, { type: 'fail', reason: 'unknown', message: 'x', rolledBack: true })).toEqual({
      kind: 'failed',
      reason: 'unknown',
      message: 'x',
      rolledBack: true,
    })
  })

  it('finishes a manual rollback as done', () => {
    const s = run([{ type: 'rollback-start' }, { type: 'installed', version: '3.0.0' }])
    expect(s).toEqual({ kind: 'done', version: '3.0.0', adopted: false })
  })

  it('can fail from every active state and keeps the reason', () => {
    for (const ev of [
      [{ type: 'check-start' }],
      [{ type: 'check-start' }, { type: 'update-available', version: '1.0.0', tag: 'v1.0.0', installed: undefined }, { type: 'download-start', total: 0 }],
      [{ type: 'check-start' }, { type: 'update-available', version: '1.0.0', tag: 'v1.0.0', installed: undefined }, { type: 'verify-start' }],
    ] as InstallEvent[][]) {
      const s = reduceInstall(run(ev), { type: 'fail', reason: 'network', message: 'down' })
      expect(s).toEqual({ kind: 'failed', reason: 'network', message: 'down', rolledBack: false })
    }
  })

  it('ignores events that are not valid in the current state (same reference)', () => {
    expect(reduceInstall(idleState, { type: 'progress', done: 1, total: 2 })).toBe(idleState)
    expect(reduceInstall(idleState, { type: 'install-start' })).toBe(idleState)
    const checking = reduceInstall(idleState, { type: 'check-start' })
    expect(reduceInstall(checking, { type: 'check-start' })).toBe(checking)
    expect(reduceInstall(checking, { type: 'installed', version: '1.0.0' })).toBe(checking)
    const downloading = run([
      { type: 'check-start' },
      { type: 'update-available', version: '1.0.0', tag: 'v1.0.0', installed: undefined },
      { type: 'download-start', total: 10 },
    ])
    expect(reduceInstall(downloading, { type: 'install-start' })).toBe(downloading)
    expect(reduceInstall(downloading, { type: 'reset' })).toBe(downloading)
  })

  it('allows a new check after a terminal state, and reset returns to idle', () => {
    const failed = run([{ type: 'check-start' }, { type: 'fail', reason: 'network', message: 'x' }])
    expect(reduceInstall(failed, { type: 'check-start' }).kind).toBe('checking')
    expect(reduceInstall(failed, { type: 'reset' })).toBe(idleState)
    const done = run([{ type: 'rollback-start' }, { type: 'installed', version: '1' }])
    expect(reduceInstall(done, { type: 'reset' })).toBe(idleState)
  })
})
