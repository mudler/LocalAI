import { describe, expect, it } from 'vitest'
import { migrate } from '../src/main/config'
import {
  addP2PRemote,
  addRemote,
  defaultTarget,
  keepLocalRunning,
  migrateRemoteUrl,
  newRemoteId,
  p2pOrigin,
  parseP2PTarget,
  removeRemote,
  remotes,
  updateChannel,
  updateRemote,
  withDefaultTarget,
  withKeepLocalRunning,
  withUpdateChannel,
  MAX_REMOTES,
} from '../src/core/serversConfig'

const cfg = (raw: Record<string, unknown> = {}) => migrate(raw, { home: '/h' }).config

describe('remotes in launcher.json', () => {
  it('reads nothing from a fresh config', () => {
    expect(remotes(cfg())).toEqual([])
    expect(defaultTarget(cfg())).toBe('local')
    expect(keepLocalRunning(cfg())).toBe(false)
    expect(updateChannel(cfg())).toBe('stable')
  })

  it('keeps only well-formed entries and drops duplicates by address', () => {
    const c = cfg({
      remotes: [
        { id: 'srv_aaaa1111', name: 'One', url: 'https://one.example.com' },
        { id: 'srv_bbbb2222', name: 'Dup', url: 'https://one.example.com/' },
        { id: 'srv_cccc3333', name: '', url: 'https://two.example.com' },
        { id: 'srv_dddd4444', name: 'Creds', url: 'https://user:pw@three.example.com' },
        { id: 'bad id', name: 'Bad', url: 'https://four.example.com' },
        'junk',
      ],
    })
    expect(remotes(c).map((r) => r.id)).toEqual(['srv_aaaa1111'])
  })

  it('stores the origin only, never a path', () => {
    const r = addRemote(cfg(), { name: 'Lab', address: 'https://lab.example.com/app/chat?x=1' }, () => 'srv_00000001')
    expect(r.ok && remotes(r.config)[0]).toEqual({ id: 'srv_00000001', name: 'Lab', url: 'https://lab.example.com' })
  })

  it('refuses a duplicate address, a bad name and a 21st server', () => {
    const one = addRemote(cfg(), { name: 'A', address: 'https://a.example.com' }, () => 'srv_00000001')
    expect(one.ok).toBe(true)
    if (!one.ok) return
    expect(addRemote(one.config, { name: 'B', address: 'https://a.example.com' }, () => 'srv_00000002')).toMatchObject({ ok: false, reason: 'duplicate' })
    expect(addRemote(one.config, { name: '', address: 'https://b.example.com' }, () => 'srv_00000002')).toMatchObject({ ok: false, reason: 'name' })
    expect(addRemote(one.config, { name: 'x'.repeat(61), address: 'https://b.example.com' }, () => 'srv_00000002')).toMatchObject({ ok: false, reason: 'name' })
    expect(addRemote(one.config, { name: 'B', address: 'ftp://b.example.com' }, () => 'srv_00000002')).toMatchObject({ ok: false, reason: 'scheme' })
    let c = cfg()
    for (let i = 0; i < MAX_REMOTES; i++) {
      const r = addRemote(c, { name: `S${i}`, address: `https://h${i}.example.com` }, () => `srv_${String(i).padStart(8, '0')}`)
      if (!r.ok) throw new Error('setup')
      c = r.config
    }
    expect(addRemote(c, { name: 'More', address: 'https://more.example.com' }, () => 'srv_ffffffff')).toMatchObject({ ok: false, reason: 'limit' })
  })

  it('generates ids in the main process', () => {
    expect(newRemoteId((n) => Buffer.alloc(n, 0xab))).toBe('srv_abababab')
  })

  it('updates and removes; removing the default falls back to local', () => {
    let c = (addRemote(cfg(), { name: 'A', address: 'https://a.example.com' }, () => 'srv_00000001') as { config: ReturnType<typeof cfg> }).config
    c = withDefaultTarget(c, 'srv_00000001')
    expect(defaultTarget(c)).toBe('srv_00000001')
    const u = updateRemote(c, 'srv_00000001', { name: 'Renamed' })
    expect(u.ok && remotes(u.config)[0].name).toBe('Renamed')
    expect(updateRemote(c, 'srv_zzzzzzzz', { name: 'x' })).toMatchObject({ ok: false, reason: 'not-found' })
    const rm = removeRemote(c, 'srv_00000001')
    expect(remotes(rm)).toEqual([])
    expect(defaultTarget(rm)).toBe('local')
  })

  it('a default that names no saved server reads as local', () => {
    expect(defaultTarget(cfg({ default_target: 'srv_gone0000' }))).toBe('local')
  })

  it('writes default_target only when it changes', () => {
    const c = cfg()
    expect(withDefaultTarget(c, 'local')).toBe(c)
    expect('default_target' in withDefaultTarget(c, 'local').extra).toBe(false)
  })

  it('keep_local_running is opt-in', () => {
    expect(keepLocalRunning(withKeepLocalRunning(cfg(), true))).toBe(true)
    expect(keepLocalRunning(cfg({ keep_local_running: 'yes' }))).toBe(false)
  })

  it('keeps Go keys and unknown keys', () => {
    const c = cfg({ weird: 1, remotes: [], address: '127.0.0.1:9' })
    expect(c.extra.weird).toBe(1)
    expect(c.address).toBe('127.0.0.1:9')
  })
})

describe('servers over P2P on a loopback host of their own', () => {
  const PEER = '12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGm'
  const target = (extra: Record<string, unknown> = {}) => ({ peer: PEER, port: 41001, addrs: [], relays: [], ...extra })

  it('keeps a 127.x.y.z host and refuses any other host', () => {
    expect(parseP2PTarget(target({ host: '127.0.0.3' }))).toEqual({ peer: PEER, port: 41001, addrs: [], relays: [], host: '127.0.0.3' })
    expect(parseP2PTarget(target({ host: '127.1.2.254' }))?.host).toBe('127.1.2.254')
    for (const host of ['10.0.0.1', '192.0.2.1', '127.0.0.0', '127.0.0.256', '127.0.0', 'localhost', '::1', ' 127.0.0.2', 3, '']) {
      expect(parseP2PTarget(target({ host })), String(host)).toBeNull()
    }
  })

  it('still reads a target without a host (the desktop)', () => {
    const t = parseP2PTarget(target())
    expect(t).toEqual({ peer: PEER, port: 41001, addrs: [], relays: [] })
    expect(t && 'host' in t).toBe(false)
  })

  it('builds the origin from the host, 127.0.0.1 when there is none', () => {
    expect(p2pOrigin(41001, '127.0.0.3')).toBe('http://127.0.0.3:41001')
    expect(p2pOrigin(41001)).toBe('http://127.0.0.1:41001')
  })

  it('saves a server at its own host and reads it back', () => {
    const r = addP2PRemote(cfg(), { name: 'Studio', target: target({ host: '127.0.0.2' }) }, () => 'srv_00000001')
    if (!r.ok) throw new Error(r.reason)
    expect(r.remote.url).toBe('http://127.0.0.2:41001')
    expect(remotes(r.config)).toEqual([r.remote])
    // The same port on another host is another origin, so another sign-in.
    const PEER2 = '12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGn'
    const r2 = addP2PRemote(r.config, { name: 'Office', target: { ...target({ host: '127.0.0.3' }), peer: PEER2 } }, () => 'srv_00000002')
    if (!r2.ok) throw new Error(r2.reason)
    expect(remotes(r2.config).map((x) => x.url)).toEqual(['http://127.0.0.2:41001', 'http://127.0.0.3:41001'])
  })

  it('keeps the hints of a saved server, and saves none when there are none', () => {
    const hint = '/ip4/192.0.2.7/udp/4001/quic-v1/p2p/12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGo'
    expect(parseP2PTarget(target({ hints: [hint] }))).toEqual({ peer: PEER, port: 41001, addrs: [], relays: [], hints: [hint] })
    expect(parseP2PTarget(target({ hints: [] }))).toEqual({ peer: PEER, port: 41001, addrs: [], relays: [] })
    expect(parseP2PTarget(target({ hints: [hint, hint, hint] }))).toBeNull()
    expect(parseP2PTarget(target({ hints: ['not an address'] }))).toBeNull()
    const r = addP2PRemote(cfg(), { name: 'Studio', target: target({ host: '127.0.0.2', hints: [hint] }) }, () => 'srv_00000001')
    if (!r.ok) throw new Error(r.reason)
    expect(remotes(r.config)[0]?.p2p?.hints).toEqual([hint])
  })

  it('drops a stored server whose address does not match its host', () => {
    const c = cfg({ remotes: [{ id: 'srv_00000001', name: 'Studio', url: 'http://127.0.0.1:41001', p2p: target({ host: '127.0.0.2' }) }] })
    expect(remotes(c)).toEqual([])
  })
})

describe('update channel', () => {
  it('reads include_prereleases true as rc, and the explicit key wins', () => {
    expect(updateChannel(cfg({ include_prereleases: true }))).toBe('rc')
    expect(updateChannel(cfg({ include_prereleases: true, update_channel: 'dev' }))).toBe('dev')
    expect(updateChannel(cfg({ update_channel: 'nightly' }))).toBe('stable')
  })
  it('never writes include_prereleases', () => {
    const c = withUpdateChannel(cfg({ include_prereleases: true }), 'stable')
    expect(updateChannel(c)).toBe('stable')
    expect(c.extra.include_prereleases).toBe(true) // read-only alias stays as it was
  })
})

describe('migration of remote_server_url', () => {
  it('moves it into the list once and never makes it the default', () => {
    const m = migrateRemoteUrl(cfg({ remote_server_url: 'https://gpu.example.com' }), () => 'srv_00000001')
    expect(m.changed).toBe(true)
    expect(remotes(m.config)).toEqual([{ id: 'srv_00000001', name: 'gpu.example.com', url: 'https://gpu.example.com' }])
    expect(defaultTarget(m.config)).toBe('local')
    expect('default_target' in m.config.extra).toBe(false)
    expect(m.config.extra.remote_server_url).toBe('https://gpu.example.com')
    expect(migrateRemoteUrl(m.config, () => 'srv_00000002').changed).toBe(false)
  })
  it('keeps a recorded default', () => {
    const m = migrateRemoteUrl(cfg({ remote_server_url: 'https://gpu.example.com', default_target: 'local' }), () => 'srv_00000001')
    expect(defaultTarget(m.config)).toBe('local')
    expect(remotes(m.config)).toHaveLength(1)
  })
  it('ignores a bad or missing url', () => {
    expect(migrateRemoteUrl(cfg({ remote_server_url: 'javascript:1' }), () => 'srv_00000001').changed).toBe(false)
    expect(migrateRemoteUrl(cfg(), () => 'srv_00000001').changed).toBe(false)
  })
})
