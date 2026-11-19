import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { RemoteMonitor, REMOTE_RETRY_MS, reasonText } from '../src/main/remoteMonitor'
import type { TestResult } from '../src/main/testConnection'
import type { RemoteStatusView } from '../src/shared/types'

beforeEach(() => vi.useFakeTimers({ now: 1_000_000 }))
afterEach(() => vi.useRealTimers())

function rig(results: TestResult[]) {
  const seen: (RemoteStatusView | null)[] = []
  const probes: string[] = []
  const m = new RemoteMonitor({
    probe: async (origin) => {
      probes.push(origin)
      return results.shift() ?? { status: 'ok', secure: true }
    },
    onStatus: (s) => seen.push(s),
    now: () => Date.now(),
  })
  return { m, seen, probes }
}

describe('RemoteMonitor', () => {
  it('retries every 30 seconds while the server does not answer, and stops when it does', async () => {
    expect(REMOTE_RETRY_MS).toBe(30_000)
    const { m, seen, probes } = rig([{ status: 'unreachable', reason: 'timeout' }, { status: 'unreachable', reason: 'refused' }, { status: 'ok', secure: true }])
    await m.start('https://a.example.com')
    expect(seen).toEqual([{ kind: 'unreachable', reason: 'timed out', at: 1_000_000, nextAt: 1_030_000 }])
    await vi.advanceTimersByTimeAsync(30_000)
    expect(probes).toHaveLength(2)
    expect(seen[1]).toMatchObject({ reason: 'the connection was refused' })
    await vi.advanceTimersByTimeAsync(30_000)
    expect(seen[2]).toBeNull()
    await vi.advanceTimersByTimeAsync(120_000)
    expect(probes).toHaveLength(3) // no polling while it answers
  })

  it('counts a server that needs a sign-in as reachable', async () => {
    const { m, seen } = rig([{ status: 'needs-signin' }])
    await m.start('https://a.example.com')
    expect(seen).toEqual([null])
  })

  it('retryNow tries at once and a new start drops the old timer', async () => {
    const { m, seen, probes } = rig([{ status: 'unreachable', reason: 'dns' }, { status: 'ok', secure: true }])
    await m.start('https://a.example.com')
    await m.retryNow()
    expect(seen.at(-1)).toBeNull()
    m.stop()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(probes).toHaveLength(2)
  })

  it('ignores an answer that arrives after stop', async () => {
    let release!: (r: TestResult) => void
    const seen: unknown[] = []
    const m = new RemoteMonitor({ probe: () => new Promise((r) => (release = r)), onStatus: (s) => seen.push(s), now: () => Date.now() })
    const p = m.start('https://a.example.com')
    m.stop()
    release({ status: 'unreachable', reason: 'timeout' })
    await p
    expect(seen).toEqual([])
  })

  it('says why in plain words', () => {
    expect(reasonText({ status: 'tls', reason: 'expired' })).toBe('the certificate has expired')
    expect(reasonText({ status: 'not-localai', reason: 'answer' })).toBe('it is not a LocalAI server')
    expect(reasonText({ status: 'unreachable', reason: 'dns' })).toBe('the name was not found')
  })
})
