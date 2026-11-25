// SPDX-License-Identifier: MIT
import { afterEach, describe, expect, it } from 'vitest'
import { CONFIG_FILE } from '../src/core/configIO'
import { ClientCore, profileFor, type ClientEvent } from '../src/core/clientCore'
import { MOBILE_BAR_HEIGHT } from '../src/mobileShell'
import { fakePlatform, flush, type FakePlatform } from './fakes/plugins'

const AI = 'https://ai.example.com'
const OFFICE = 'https://office.example.com'
const PROFILE = /^webui-[0-9a-f]{24}$/

let open: ClientCore[] = []
afterEach(async () => {
  for (const c of open) await c.shutdown()
  open = []
})

async function start(p: FakePlatform): Promise<ClientCore> {
  const core = await ClientCore.open(p)
  open.push(core)
  await flush()
  return core
}

const shows = (p: FakePlatform) => p.webui.calls.filter((c) => c.name === 'show').map((c) => c.args[0] as { origin: string; url: string; profile: string })
const named = (p: FakePlatform, name: string) => p.webui.calls.filter((c) => c.name === name)
const savedConfig = (p: FakePlatform) => JSON.parse(p.files.files.get(CONFIG_FILE) ?? '{}')

const savedWithDefault = (url: string) =>
  JSON.stringify({ remotes: [{ id: 'srv_00000001', name: 'GPU box', url }], default_target: 'srv_00000001' })

describe('profileFor', () => {
  it('gives one profile per origin', async () => {
    const a = await profileFor(AI)
    expect(a).toMatch(PROFILE)
    expect(await profileFor(AI)).toBe(a)
    expect(await profileFor(OFFICE)).not.toBe(a)
  })
})

describe('ClientCore', () => {
  it('opens with no file: no servers and no WebUI', async () => {
    const p = fakePlatform()
    const core = await start(p)
    const v = core.servers.serversView()
    expect(v.rows).toEqual([])
    expect(v.firstRun).toBe(true)
    expect(core.webuiState().layout).not.toBe('webui')
    expect(shows(p)).toEqual([])
    expect(core.configNotice()).toBeUndefined()
  })

  it('reports a recovered config', async () => {
    const core = await start(fakePlatform({ files: { [CONFIG_FILE]: 'nope' } }))
    expect(core.configNotice()).toEqual({ status: 'recovered', reason: 'config file is not valid JSON' })
  })

  it('shows a server added as the default in its own profile', async () => {
    const p = fakePlatform({ answers: { [AI]: 'ok' } })
    const core = await start(p)
    const events: ClientEvent[] = []
    core.on((e) => events.push(e))
    const r = await core.servers.addServer({ name: 'GPU box', address: AI, makeDefault: true })
    await flush()
    expect(r.ok).toBe(true)
    const s = shows(p)
    expect(s).toHaveLength(1)
    expect(s[0]).toMatchObject({ origin: AI, url: `${AI}/app` })
    expect(s[0].profile).toMatch(PROFILE)
    expect(s[0].profile).toBe(await profileFor(AI))
    expect(events.some((e) => e.kind === 'servers')).toBe(true)
    expect(events.some((e) => e.kind === 'webui' && e.state.target.kind === 'remote')).toBe(true)
    // The layout follows the state: the view is placed and visible.
    const layout = named(p, 'setLayout').at(-1)?.args[0]
    expect(layout).toMatchObject({ visible: true, shellAbove: false })
  })

  it('sends the shell sizes without system insets, the phone bar, and no bar while the keyboard is open', async () => {
    const p = fakePlatform({ answers: { [AI]: 'ok' } })
    const core = await start(p)
    await core.servers.addServer({ name: 'GPU box', address: AI, makeDefault: true })
    core.webuiDispatch({ type: 'insets', insets: { top: 36, bottom: 0 } })
    await flush()
    const last = () => named(p, 'setLayout').at(-1)?.args[0]
    // The native side adds the status bar and navigation bar insets itself.
    expect(last()).toEqual({ visible: true, shellAbove: false, top: 36, bottom: 0, barHeight: MOBILE_BAR_HEIGHT })
    expect(MOBILE_BAR_HEIGHT).toBe(48)
    const before = named(p, 'setLayout').length
    core.setKeyboardOpen(true)
    await flush()
    expect(last()).toEqual({ visible: true, shellAbove: false, top: 36, bottom: 0, barHeight: 0 })
    core.setKeyboardOpen(true)
    await flush()
    expect(named(p, 'setLayout')).toHaveLength(before + 1)
    core.setKeyboardOpen(false)
    await flush()
    expect(last()).toMatchObject({ barHeight: MOBILE_BAR_HEIGHT })
  })

  it('keeps one profile per origin across switches', async () => {
    const p = fakePlatform({ answers: { [AI]: 'ok', [OFFICE]: 'ok' } })
    const core = await start(p)
    const a = await core.servers.addServer({ name: 'GPU box', address: AI, makeDefault: true })
    const b = await core.servers.addServer({ name: 'Office', address: OFFICE })
    await flush()
    p.webui.emit('loaded')
    await core.servers.setDefaultServer((b.ok && b.id) || '')
    await flush()
    p.webui.emit('loaded')
    await core.servers.setDefaultServer((a.ok && a.id) || '')
    await flush()
    const s = shows(p)
    expect(s.map((x) => x.origin)).toEqual([AI, OFFICE, AI])
    expect(s[2].profile).toBe(s[0].profile)
    expect(s[1].profile).not.toBe(s[0].profile)
  })

  it('maps the view events into the state', async () => {
    const p = fakePlatform({ answers: { [AI]: 'ok' } })
    const core = await start(p)
    await core.servers.addServer({ name: 'GPU box', address: AI, makeDefault: true })
    await flush()
    expect(core.servers.serversView().switching).not.toBeNull()
    p.webui.emit('loaded')
    await flush()
    expect(core.webuiState().loaded).toBe(true)
    expect(core.servers.serversView().switching).toBeNull()
    p.webui.emit('loadFailed', { code: -2, description: 'net::ERR_NAME_NOT_RESOLVED' })
    expect(core.webuiState().problem).toMatchObject({ kind: 'load-failed', code: -2 })
    p.webui.emit('crashed', { reason: 'oom' })
    expect(core.webuiState().problem).toMatchObject({ kind: 'crashed', reason: 'oom' })
  })

  it('closes the view when the default server is removed, and stays without a local origin', async () => {
    const p = fakePlatform({ answers: { [AI]: 'ok' } })
    const core = await start(p)
    const r = await core.servers.addServer({ name: 'GPU box', address: AI, makeDefault: true })
    await flush()
    p.webui.emit('loaded')
    await core.servers.removeServer((r.ok && r.id) || '')
    await flush()
    expect(named(p, 'close')).toHaveLength(1)
    const st = core.webuiState()
    expect(st.layout).not.toBe('webui')
    expect(st.target.kind).toBe('local')
    expect(shows(p)).toHaveLength(1)
    expect(named(p, 'setLayout').at(-1)?.args[0]).toMatchObject({ visible: false })
    // No local server: "open in browser" has nothing to open.
    await core.openInBrowser()
    expect(p.opened).toEqual([])
  })

  it('remembers a permission choice and gives the store to the view', async () => {
    const p = fakePlatform({ answers: { [AI]: 'ok' } })
    const core = await start(p)
    await core.servers.addServer({ name: 'GPU box', address: AI, makeDefault: true })
    await flush()
    p.webui.emit('permissionChoice', { origin: AI, choice: 'allow', remember: true })
    await flush()
    expect(savedConfig(p).webui_permissions).toEqual({ [AI]: { media: 'allow' } })
    const last = named(p, 'setPermissionStore').at(-1)?.args[0] as { json: string }
    expect(JSON.parse(last.json)).toEqual({ [AI]: { media: 'allow' } })
    expect(core.windowSettings().anyRemembered).toBe(true)
    await core.resetWebuiPermissions()
    await flush()
    expect(savedConfig(p).webui_permissions).toBeUndefined()
    expect(JSON.parse((named(p, 'setPermissionStore').at(-1)?.args[0] as { json: string }).json)).toEqual({})
  })

  it('does not remember a choice for an origin the view does not show', async () => {
    const p = fakePlatform({ answers: { [AI]: 'ok' } })
    const core = await start(p)
    await core.servers.addServer({ name: 'GPU box', address: AI, makeDefault: true })
    await flush()
    p.webui.emit('permissionChoice', { origin: OFFICE, choice: 'allow', remember: true })
    await flush()
    expect(savedConfig(p).webui_permissions).toBeUndefined()
  })

  it('opens external links from the view through the platform', async () => {
    const p = fakePlatform()
    await start(p)
    p.webui.emit('openExternal', { url: 'https://example.com/docs' })
    await flush()
    expect(p.opened).toEqual(['https://example.com/docs'])
  })

  it('saves "open in browser" and closes the view', async () => {
    const p = fakePlatform({ answers: { [AI]: 'ok' }, files: { [CONFIG_FILE]: savedWithDefault(AI) } })
    const core = await start(p)
    expect(shows(p)).toHaveLength(1)
    const w = await core.setOpenInBrowser(true)
    await flush()
    expect(w.openInBrowser).toBe(true)
    expect(savedConfig(p).open_webui_in_browser).toBe(true)
    expect(named(p, 'close')).toHaveLength(1)
  })

  it('pauses the monitor in the background and checks once on resume', async () => {
    const p = fakePlatform({ files: { [CONFIG_FILE]: savedWithDefault(AI) } })
    const core = await start(p)
    expect(p.fakeHttp.checks(AI)).toBe(1)
    expect(core.webuiState().remote).toMatchObject({ kind: 'unreachable' })
    core.setForeground(false)
    await flush()
    expect(p.fakeHttp.checks(AI)).toBe(1)
    core.setForeground(true)
    await flush()
    expect(p.fakeHttp.checks(AI)).toBe(2)
    // A second "active" without a pause in between does not check again.
    core.setForeground(true)
    await flush()
    expect(p.fakeHttp.checks(AI)).toBe(2)
  })

  it('opens the saved default at start', async () => {
    const p = fakePlatform({ answers: { [AI]: 'ok' }, files: { [CONFIG_FILE]: savedWithDefault(AI) } })
    const core = await start(p)
    expect(shows(p).map((s) => s.origin)).toEqual([AI])
    expect(core.servers.serversView().defaultId).toBe('srv_00000001')
    expect(named(p, 'setPermissionStore')).toHaveLength(1)
  })

  it('passes the updater state on and brings the bar back for a downloaded update', async () => {
    const p = fakePlatform()
    const core = await start(p)
    const events: ClientEvent[] = []
    core.on((e) => events.push(e))
    core.setUpdaterStatus({ phase: 'downloaded', version: '5.1.0' })
    expect(events).toContainEqual({ kind: 'updater', status: { phase: 'downloaded', version: '5.1.0' } })
  })
})
