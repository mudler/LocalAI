import { describe, expect, it } from 'vitest'
import { cleanInviteText, invitePreview, isInviteText, scanErrorMessage } from '../src/renderer/invitePreview'

const PEER = '12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGm'
const NOW = new Date(2026, 10, 14, 14, 30).getTime()

function link(o: { kind?: string; secs?: number; name?: string; open?: string; addrs?: number; relays?: number } = {}): string {
  const q = new URLSearchParams({ v: '1', k: o.kind ?? 'device', p: PEER, c: 'ABCDEFGHJK', e: String(Math.floor(NOW / 1000) + (o.secs ?? 581)) })
  if (o.name !== undefined) q.set('n', o.name)
  for (let i = 0; i < (o.addrs ?? 0); i++) q.append('a', `/ip4/192.0.2.${i + 1}/tcp/4001`)
  for (let i = 0; i < (o.relays ?? 0); i++) q.append('r', `/dns4/relay${i}.example.com/tcp/4001`)
  if (o.open) q.set('o', o.open)
  return `localai://pair?${q.toString()}`
}

describe('invitePreview', () => {
  it('is undefined for empty text', () => {
    expect(invitePreview('', NOW)).toBeUndefined()
    expect(invitePreview('   \n', NOW)).toBeUndefined()
  })

  it('reads a device invite: label, kind, time left, short peer, routes', () => {
    const v = invitePreview(link({ name: 'Home GPU box', addrs: 2, relays: 1 }), NOW)
    expect(v).toEqual({
      label: 'Home GPU box',
      kind: 'device',
      expiresAt: (Math.floor(NOW / 1000) + 581) * 1000,
      secondsLeft: 581,
      openRelay: false,
      peer: PEER,
      peerShort: '12D3KooWQ6…JGm',
      routes: { addrs: 2, relays: 1 },
    })
  })

  it('counts only the routes the pairing uses (two addresses, three relays)', () => {
    const v = invitePreview(link({ kind: 'remote', addrs: 5, relays: 6, open: '/dns4/open.example.com/tcp/4001' }), NOW)
    expect(v).toMatchObject({ kind: 'remote', label: '', openRelay: true, routes: { addrs: 2, relays: 3 } })
  })

  it('names the problem of a link it refuses', () => {
    expect(invitePreview('hello', NOW)).toEqual({ problem: 'invite-invalid' })
    expect(invitePreview('localai://pair?v=1', NOW)).toEqual({ problem: 'invite-invalid' })
    expect(invitePreview(link({ kind: 'worker' }), NOW)).toEqual({ problem: 'invite-kind' })
    expect(invitePreview(link({ secs: -1 }), NOW)).toEqual({ problem: 'invite-expired' })
  })

  it('isInviteText: an invite of any state, but not other text', () => {
    expect(isInviteText(link(), NOW)).toBe(true)
    expect(isInviteText(link({ secs: -60 }), NOW)).toBe(true)
    expect(isInviteText(link({ kind: 'worker' }), NOW)).toBe(true)
    expect(isInviteText('hello', NOW)).toBe(false)
    expect(isInviteText('', NOW)).toBe(false)
    expect(isInviteText('https://example.com/?x=localai://pair', NOW)).toBe(false)
  })
})

describe('cleanInviteText', () => {
  it('drops spaces, surrounding <> and quotes, and a trailing full stop', () => {
    const l = link()
    expect(cleanInviteText(`  ${l}\n`)).toBe(l)
    expect(cleanInviteText(`<${l}>`)).toBe(l)
    expect(cleanInviteText(`"${l}"`)).toBe(l)
    expect(cleanInviteText(`'${l}'`)).toBe(l)
    expect(cleanInviteText(`\u201c${l}\u201d`)).toBe(l)
    expect(cleanInviteText(`${l}.`)).toBe(l)
    expect(cleanInviteText(`"<${l}>".`)).toBe(l)
    expect(isInviteText(cleanInviteText(`<${l}>.`), NOW)).toBe(true)
  })

  it('leaves other text and unmatched marks alone', () => {
    expect(cleanInviteText('hello')).toBe('hello')
    expect(cleanInviteText('<abc')).toBe('<abc')
    expect(cleanInviteText('')).toBe('')
  })
})

describe('scanErrorMessage', () => {
  it('gives the scanner reasons their own text, and a generic one otherwise', () => {
    expect(scanErrorMessage({ code: 'no-camera' })).toContain('no camera')
    expect(scanErrorMessage({ code: 'permission-denied' })).toContain('may not use the camera')
    expect(scanErrorMessage(new Error('boom'))).toContain('could not be scanned')
    expect(scanErrorMessage(undefined)).toContain('could not be scanned')
    for (const m of [scanErrorMessage({ code: 'no-camera' }), scanErrorMessage({ code: 'permission-denied' })]) expect(m).not.toContain('not a LocalAI invite')
  })
})
