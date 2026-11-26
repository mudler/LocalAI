// Servers over P2P: the invite reader, the pair command, the forwarder
// supervisor and the stored entry.
import { EventEmitter } from 'node:events'
import { PassThrough } from 'node:stream'
import type { ChildProcess } from 'node:child_process'
import { describe, expect, it, vi } from 'vitest'
import { migrate } from '../src/main/config'
import { backoffMs, linkRoutes, pair, readInvite, TunnelSupervisor, type P2PRoutes, type RunCommand, type TunnelStatus } from '../src/main/p2pRemote'
import { parseRoutesLine } from '../src/core/p2pInvite'
import { addP2PRemote, remotes, updateRemote, withP2PRoutes } from '../src/core/serversConfig'

const PEER = '12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGm'
const NOW = Date.UTC(2026, 10, 17)
const exp = Math.floor(NOW / 1000) + 600
const link = (extra = '', kind = 'device') =>
  `localai://pair?v=1&k=${kind}&p=${PEER}&c=ABCDEFGHJK&e=${exp}&n=studio${extra}`
const cfg = (raw: Record<string, unknown> = {}) => migrate(raw, { home: '/h' }).config

describe('readInvite', () => {
  it('reads a device or remote invite', () => {
    expect(readInvite(link(), NOW)).toEqual({ ok: true, info: { kind: 'device', peer: PEER, label: 'studio', expires: exp * 1000, openRelay: false } })
    expect(readInvite(`  ${link('', 'remote')}\n`, NOW)).toMatchObject({ ok: true, info: { kind: 'remote' } })
    expect(readInvite(link('&o=0'), NOW)).toMatchObject({ ok: true, info: { openRelay: true } })
  })

  it('refuses other links, other kinds and expired links', () => {
    expect(readInvite('https://studio.example.com', NOW)).toEqual({ ok: false, reason: 'invite-invalid' })
    expect(readInvite(link().replace('v=1', 'v=2'), NOW)).toEqual({ ok: false, reason: 'invite-invalid' })
    expect(readInvite(link().replace(PEER, 'not-a-peer'), NOW)).toEqual({ ok: false, reason: 'invite-invalid' })
    expect(readInvite(link('', 'worker'), NOW)).toEqual({ ok: false, reason: 'invite-kind' })
    expect(readInvite(link(), (exp + 1) * 1000)).toEqual({ ok: false, reason: 'invite-expired' })
  })

  it('reads the routes of a link', () => {
    const a = encodeURIComponent('/ip4/192.0.2.1/tcp/4001')
    const r = encodeURIComponent(`/ip4/192.0.2.9/tcp/4001/p2p/${PEER}`)
    expect(linkRoutes(link(`&a=${a}&r=${r}`))).toEqual({ addrs: ['/ip4/192.0.2.1/tcp/4001'], relays: [`/ip4/192.0.2.9/tcp/4001/p2p/${PEER}`], hints: [] })
  })

  it('reads the relay hints of a link apart from its relays, at most two', () => {
    const hint = (n: number) => `/ip4/198.51.100.${n}/tcp/4001/p2p/${PEER}`
    const r = encodeURIComponent(`/ip4/192.0.2.9/tcp/4001/p2p/${PEER}`)
    const h = [1, 2, 3].map((n) => `&h=${encodeURIComponent(hint(n))}`).join('')
    expect(linkRoutes(link(`&r=${r}${h}`))).toEqual({ addrs: [], relays: [`/ip4/192.0.2.9/tcp/4001/p2p/${PEER}`], hints: [hint(1), hint(2)] })
    expect(linkRoutes(link(`&h=${encodeURIComponent(hint(1))}`))).toEqual({ addrs: [], relays: [], hints: [hint(1)] })
  })

  it('does not take a relay hint for an open relay', () => {
    expect(readInvite(link(`&h=${encodeURIComponent(`/ip4/198.51.100.1/tcp/4001/p2p/${PEER}`)}`), NOW)).toMatchObject({ ok: true, info: { openRelay: false } })
  })
})

describe('pair', () => {
  const input = { link: link(), identity: '/id/identity.key', name: 'laptop', allowOpenRelay: false }

  it('runs local-ai p2p pair and reads its JSON', async () => {
    const run = vi.fn<RunCommand>(async () => ({
      code: 0,
      stdout: JSON.stringify({ kind: 'device', phrase: 'apple river stone cloud', server: { peer_id: PEER, name: 'Studio' } }) + '\n',
      stderr: '',
    }))
    const r = await pair(run, '/bin/local-ai', input)
    expect(r).toEqual({ ok: true, result: { kind: 'device', phrase: 'apple river stone cloud', serverName: 'Studio', peer: PEER } })
    expect(run.mock.calls[0][1]).toEqual(['p2p', 'pair', '--invite', link(), '--identity', '/id/identity.key', '--name', 'laptop', '--json'])
    await pair(run, '/bin/local-ai', { ...input, allowOpenRelay: true })
    expect(run.mock.calls[1][1]).toContain('--allow-open-relay')
  })

  it('returns the last line of a failure', async () => {
    const run: RunCommand = async () => ({ code: 1, stdout: '', stderr: 'starting\nthe invite is not valid any more: it was used, cancelled or expired\n' })
    expect(await pair(run, '/bin/local-ai', input)).toEqual({ ok: false, reason: 'pair-failed', detail: 'the invite is not valid any more: it was used, cancelled or expired' })
    const missing: RunCommand = async () => ({ code: null, stdout: '', stderr: 'spawn ENOENT' })
    expect(await pair(missing, '/bin/local-ai', input)).toMatchObject({ ok: false, reason: 'no-binary' })
    const odd: RunCommand = async () => ({ code: 0, stdout: 'not json', stderr: '' })
    expect(await pair(odd, '/bin/local-ai', input)).toMatchObject({ ok: false, reason: 'pair-failed' })
  })
})

class FakeChild extends EventEmitter {
  stderr = new PassThrough()
  killed: string[] = []
  kill(sig: string) {
    this.killed.push(sig)
    return true
  }
}

describe('TunnelSupervisor', () => {
  const target = { peer: PEER, port: 41231, addrs: ['/ip4/192.0.2.1/tcp/4001'], relays: [`/ip4/192.0.2.9/tcp/4001/p2p/${PEER}`] }

  it('runs local-ai p2p forward and follows its status lines', async () => {
    const children: FakeChild[] = []
    const calls: string[][] = []
    const t = new TunnelSupervisor('/bin/local-ai', '/id/identity.key', target, (_cmd, args) => {
      calls.push(args)
      const c = new FakeChild()
      children.push(c)
      return c as unknown as ChildProcess
    })
    const seen: TunnelStatus[] = []
    t.on('status', (s: TunnelStatus) => seen.push(s))
    t.start()
    expect(calls[0]).toEqual([
      'p2p', 'forward', '--peer', PEER, '--listen', '127.0.0.1:41231', '--identity', '/id/identity.key', '--json',
      '--relay', target.relays[0], '--addr', target.addrs[0],
    ])
    children[0].stderr.write('{"state":"ready","addr":"127.0.0.1:41231"}\n{"state":"up","path":"hole_punched"}\nnoise\n')
    await new Promise((r) => setImmediate(r))
    expect(t.status).toEqual({ state: 'up', path: 'hole_punched' })
    children[0].stderr.write('{"state":"down","error":"no route"}\n')
    await new Promise((r) => setImmediate(r))
    expect(t.status).toEqual({ state: 'down', error: 'no route' })
    expect(seen.map((s) => s.state)).toEqual(['starting', 'ready', 'up', 'down'])
    t.stop()
    expect(children[0].killed).toEqual(['SIGTERM'])
    expect(t.status.state).toBe('stopped')
  })

  it('passes the hints of the target and emits the routes the server gives', async () => {
    const children: FakeChild[] = []
    const calls: string[][] = []
    const hint = `/ip4/192.0.2.7/tcp/4001/p2p/${PEER}`
    const t = new TunnelSupervisor('/bin/local-ai', '/id', { ...target, hints: [hint] }, (_cmd, args) => {
      calls.push(args)
      const c = new FakeChild()
      children.push(c)
      return c as unknown as ChildProcess
    })
    const routes: P2PRoutes[] = []
    const seen: TunnelStatus[] = []
    t.on('routes', (r: P2PRoutes) => routes.push(r))
    t.on('status', (s: TunnelStatus) => seen.push(s))
    t.start()
    expect(calls[0].slice(-2)).toEqual(['--hint', hint])
    children[0].stderr.write('{"state":"up","path":"direct"}\n{"state":"routes","addrs":["/ip4/203.0.113.9/tcp/4001"],"relays":[],"hints":[]}\n')
    await new Promise((r) => setImmediate(r))
    expect(routes).toEqual([{ addrs: ['/ip4/203.0.113.9/tcp/4001'], relays: [], hints: [] }])
    expect(t.status).toEqual({ state: 'up', path: 'direct' })
    expect(seen.map((s) => s.state)).toEqual(['starting', 'up'])
    t.stop()
  })

  it('starts the forwarder again after it exits, with a growing delay', async () => {
    vi.useFakeTimers()
    try {
      const children: FakeChild[] = []
      const t = new TunnelSupervisor('/bin/local-ai', '/id', target, () => {
        const c = new FakeChild()
        children.push(c)
        return c as unknown as ChildProcess
      })
      t.start()
      children[0].emit('exit', 1)
      expect(t.status.state).toBe('failed')
      vi.advanceTimersByTime(backoffMs(0) - 1)
      expect(children).toHaveLength(1)
      vi.advanceTimersByTime(1)
      expect(children).toHaveLength(2)
      children[1].emit('exit', 1)
      vi.advanceTimersByTime(backoffMs(0))
      expect(children).toHaveLength(2)
      vi.advanceTimersByTime(backoffMs(1))
      expect(children).toHaveLength(3)
      t.stop()
      children[2].emit('exit', 0)
      vi.advanceTimersByTime(120_000)
      expect(children).toHaveLength(3)
    } finally {
      vi.useRealTimers()
    }
    expect(backoffMs(20)).toBe(60_000)
  })
})

describe('servers over P2P in launcher.json', () => {
  const target = { peer: PEER, port: 41231, addrs: [], relays: [] }

  it('stores the forwarder origin and the target, and reads them back', () => {
    const r = addP2PRemote(cfg(), { name: 'Studio', target }, () => 'srv_p2p00001')
    expect(r.ok).toBe(true)
    if (!r.ok) return
    expect(r.remote).toEqual({ id: 'srv_p2p00001', name: 'Studio', url: 'http://127.0.0.1:41231', p2p: target })
    expect(remotes(r.config)).toEqual([r.remote])
    expect(addP2PRemote(r.config, { name: 'Again', target: { ...target, port: 41232 } })).toMatchObject({ ok: false, reason: 'duplicate', existingId: 'srv_p2p00001' })
  })

  it('drops a hand-edited entry whose origin is not its forwarder', () => {
    const c = cfg({ remotes: [{ id: 'srv_p2p00001', name: 'Studio', url: 'https://evil.example.com', p2p: target }] })
    expect(remotes(c)).toEqual([])
    const bad = cfg({ remotes: [{ id: 'srv_p2p00001', name: 'Studio', url: 'http://127.0.0.1:41231', p2p: { ...target, peer: 'x' } }] })
    expect(remotes(bad)).toEqual([])
  })

  it('renames a server over P2P but keeps its address', () => {
    const r = addP2PRemote(cfg(), { name: 'Studio', target }, () => 'srv_p2p00001')
    if (!r.ok) throw new Error('add failed')
    const renamed = updateRemote(r.config, 'srv_p2p00001', { name: 'Home', address: 'http://127.0.0.1:41231' })
    expect(renamed).toMatchObject({ ok: true, remote: { name: 'Home', url: 'http://127.0.0.1:41231', p2p: target } })
    expect(updateRemote(r.config, 'srv_p2p00001', { address: 'https://other.example.com' })).toEqual({ ok: false, reason: 'invalid-url' })
  })
})

describe('routes of a server over P2P', () => {
  const relay = `/ip4/198.51.100.1/tcp/4001/p2p/${PEER}`

  it('reads a routes line, cut to what a saved server keeps', () => {
    const line = JSON.stringify({
      state: 'routes',
      addrs: ['/ip4/203.0.113.1/tcp/4001', 7, 'junk', '/ip4/203.0.113.2/tcp/4001', '/ip4/192.168.1.5/tcp/4001'],
      relays: [relay],
      hints: [],
    })
    expect(parseRoutesLine(line)).toEqual({ addrs: ['/ip4/203.0.113.1/tcp/4001', '/ip4/203.0.113.2/tcp/4001'], relays: [relay], hints: [] })
  })

  it('refuses other lines, a missing list and an answer with no route', () => {
    expect(parseRoutesLine('{"state":"up"}')).toBeUndefined()
    expect(parseRoutesLine('{"state":"routes","addrs":["/ip4/203.0.113.1/tcp/4001"],"relays":[]}')).toBeUndefined()
    expect(parseRoutesLine('{"state":"routes","addrs":[],"relays":[],"hints":[]}')).toBeUndefined()
    expect(parseRoutesLine('nope')).toBeUndefined()
  })

  it('replaces the routes of a saved server and keeps its peer, port and host', () => {
    const target = { peer: PEER, port: 41231, addrs: ['/ip4/192.0.2.1/tcp/4001'], relays: [], hints: [`/ip4/192.0.2.7/tcp/4001/p2p/${PEER}`], host: '127.0.0.2' }
    const r = addP2PRemote(cfg(), { name: 'Studio', target }, () => 'srv_p2p00001')
    if (!r.ok) throw new Error('add failed')
    const next = withP2PRoutes(r.config, 'srv_p2p00001', { addrs: ['/ip4/203.0.113.9/tcp/4001'], relays: [relay], hints: [] })
    expect(next).toMatchObject({ ok: true, changed: true })
    if (!next.ok) return
    expect(remotes(next.config)[0]).toEqual({
      id: 'srv_p2p00001', name: 'Studio', url: 'http://127.0.0.2:41231',
      p2p: { peer: PEER, port: 41231, host: '127.0.0.2', addrs: ['/ip4/203.0.113.9/tcp/4001'], relays: [relay] },
    })
    const same = withP2PRoutes(next.config, 'srv_p2p00001', { addrs: ['/ip4/203.0.113.9/tcp/4001'], relays: [relay], hints: [] })
    expect(same).toMatchObject({ ok: true, changed: false })
    expect(withP2PRoutes(r.config, 'nope', { addrs: [], relays: [], hints: [] })).toEqual({ ok: false, reason: 'not-found' })
  })
})
