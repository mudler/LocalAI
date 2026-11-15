import { describe, expect, it } from 'vitest'
import { addressHint, addressUsable, suggestName, switcherLine, testCard } from '../src/renderer/serversUi'
import { remoteRow, localRow } from '../src/renderer/dev/fakeBridge'

const saved = [remoteRow('srv_00000001', 'Lab server', 'https://lab.example.com')]

describe('the live message under the address field (mock 19)', () => {
  it.each([
    ['', undefined],
    ['office-gpu.example.com', 'fix'],
    ['https://office gpu', 'error'],
    ['https://office-gpu.example.com/app/chat', 'note'],
    ['https://lab.example.com', 'note'],
    ['http://192.0.2.40:8080', 'warn'],
    ['http://localhost:8080', 'note'],
    ['https://office-gpu.example.com', 'ok'],
    ['ftp://office.example.com', 'fix'],
    ['https://user:pw@office.example.com', 'error'],
  ] as const)('%s is %s', (text, kind) => expect(addressHint(text, saved)?.kind).toBe(kind))

  it('offers a one-click fix for a missing scheme, a path and plain http', () => {
    expect(addressHint('office-gpu.example.com', saved)?.action).toEqual({ label: 'Use https://office-gpu.example.com', apply: 'https://office-gpu.example.com' })
    expect(addressHint('https://office-gpu.example.com/app/chat', saved)?.action?.apply).toBe('https://office-gpu.example.com')
    const h = addressHint('http://192.0.2.40:8080', saved)
    expect(h).toMatchObject({ title: 'This address is not encrypted.', action: { label: 'Use https instead', apply: 'https://192.0.2.40:8080' } })
  })

  it('points to the saved server and does not call the same server a duplicate when editing it', () => {
    expect(addressHint('https://lab.example.com', saved)).toMatchObject({ kind: 'note', editId: 'srv_00000001' })
    expect(addressHint('https://lab.example.com', saved, 'srv_00000001')?.kind).toBe('ok')
  })

  it('lets Test and Connect run on ok, warn and note, but not on a fix, an error or a duplicate', () => {
    const ok = (t: string) => addressUsable(addressHint(t, saved), t)
    expect(ok('https://office-gpu.example.com')).toBe(true)
    expect(ok('http://192.0.2.40:8080')).toBe(true)
    expect(ok('office-gpu.example.com')).toBe(false)
    expect(ok('https://lab.example.com')).toBe(false)
    expect(ok('')).toBe(false)
  })
})

describe('suggested name', () => {
  it('comes from the host', () => {
    expect(suggestName('http://office-gpu.example.com:8080')).toBe('Office gpu')
    expect(suggestName('https://lab_box.example.com')).toBe('Lab box')
    expect(suggestName('http://192.0.2.40:8080')).toBe('192.0.2.40')
    expect(suggestName('not a url')).toBe('')
  })
})

describe('test result cards (mock 19)', () => {
  it('says what happened, why and one next step', () => {
    expect(testCard({ status: 'ok', version: '4.2.1', secure: true }, 'office-gpu.example.com')).toMatchObject({ tone: 'ok', title: 'Connected to LocalAI 4.2.1', body: 'office-gpu.example.com answered and it is a LocalAI server. The certificate is valid.' })
    expect(testCard({ status: 'needs-signin' }, 'x')).toMatchObject({ tone: 'warn', title: 'Reachable. This server needs a sign-in' })
    expect(testCard({ status: 'not-localai', reason: 'answer' }, 'x')).toMatchObject({ tone: 'error', title: 'This is not a LocalAI server', actions: ['edit'] })
    const tls = testCard({ status: 'tls', reason: 'expired' }, 'x')!
    expect(tls.body).toContain('The launcher does not skip this check.')
    expect(tls.title).toBe('The certificate is not trusted')
    expect(testCard({ status: 'unreachable', reason: 'timeout' }, 'x')).toMatchObject({ title: 'Cannot reach the server', actions: ['again'] })
    expect(testCard({ status: 'cancelled' }, 'x')).toBeUndefined()
  })
  it('gives every result an icon and a title, so colour is never the only cue', () => {
    for (const r of [{ status: 'ok', secure: true }, { status: 'needs-signin' }, { status: 'not-localai', reason: 'redirect' }, { status: 'tls', reason: 'self-signed' }, { status: 'unreachable', reason: 'dns' }] as const) {
      const c = testCard(r, 'h')!
      expect(c.icon).not.toBe('')
      expect(c.title).not.toBe('')
    }
  })
})

describe('switcher lines', () => {
  it('names the state of each server in words', () => {
    expect(switcherLine(localRow({ status: 'not-running' }))).toBe('Not running. Starts when you pick it.')
    expect(switcherLine(localRow({ status: 'running', host: '127.0.0.1:8080' }))).toBe('127.0.0.1:8080 · running')
    expect(switcherLine(remoteRow('a', 'A', 'https://a.example.com', { status: 'connected' }))).toBe('a.example.com · connected')
    expect(switcherLine(remoteRow('a', 'A', 'http://192.0.2.40:8080', { status: 'connected' }))).toBe('192.0.2.40:8080 · not encrypted')
    expect(switcherLine(remoteRow('a', 'A', 'https://a.example.com', { status: 'unreachable', checkedAt: 1 }), () => '14:21')).toBe('Not reachable at 14:21')
  })
})
