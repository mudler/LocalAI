import { describe, expect, it } from 'vitest'
import { migrate } from '../src/main/config'
import {
  addRemote,
  defaultTarget,
  keepLocalRunning,
  migrateRemoteUrl,
  newRemoteId,
  removeRemote,
  remotes,
  updateChannel,
  updateRemote,
  withDefaultTarget,
  withKeepLocalRunning,
  withUpdateChannel,
  MAX_REMOTES,
} from '../src/main/serversConfig'

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
