// @vitest-environment jsdom
// The shell on a client-only bridge (no local server), as the mobile app runs
// it. A call to a missing local method throws a TypeError in the shell: the
// test fails on any console.error and on any unhandled rejection, so an
// unguarded call is caught.
import { cleanup, fireEvent, render, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from '../../src/renderer/App'
import { LOCAL_SERVER_METHODS } from '../../src/shared/apiGroups'
import { baseState, remoteRow, serversView, webuiView, type FakeBridge, type FakeState } from '../../src/renderer/dev/fakeBridge'
import { fakeClientBridge } from '../../src/renderer/dev/fakeClientBridge'
import { called, NOW, screen, settle } from './render'

const GPU = remoteRow('srv_00000001', 'GPU box', 'https://gpu.example.com', { isDefault: true, isCurrent: true, status: 'connected' })
const OTHER = remoteRow('srv_00000002', 'Office', 'https://office.example.com', { status: 'connected' })
const PEER = '12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGm'
const INVITE = `localai://pair?v=1&k=device&p=${PEER}&c=ABCDEFGHJK&e=${Math.floor(NOW / 1000) + 600}&n=GPU%20box`
const remoteWebui = webuiView({ target: { kind: 'remote', origin: GPU.origin!, host: GPU.host } })

let errors: unknown[][] = []
let rejections: unknown[] = []
const onRejection = (r: unknown) => void rejections.push(r)

beforeEach(() => {
  errors = []
  rejections = []
  vi.spyOn(console, 'error').mockImplementation((...a: unknown[]) => void errors.push(a))
  process.on('unhandledRejection', onRejection)
})

afterEach(async () => {
  await settle()
  process.off('unhandledRejection', onRejection)
  vi.restoreAllMocks()
  expect(errors).toEqual([])
  expect(rejections).toEqual([])
})

async function renderClient(edit?: (s: FakeState) => void): Promise<FakeBridge> {
  const state = baseState()
  edit?.(state)
  const b = fakeClientBridge(state)
  render(<App bridge={b} now={NOW} />)
  await settle()
  return b
}

const bar = () => within(screen.getByRole('contentinfo', { name: 'LocalAI status bar' }))
const type = (label: string, value: string) => fireEvent.change(screen.getByLabelText(label), { target: { value } })
const openSettings = async () => {
  fireEvent.click(bar().getByRole('button', { name: 'Menu' }))
  fireEvent.click(screen.getByRole('menuitem', { name: 'Settings' }))
  await settle()
}

describe('the client-only fake bridge', () => {
  it('has no local-server method and no This computer row', async () => {
    const b = fakeClientBridge(baseState())
    for (const k of LOCAL_SERVER_METHODS) expect(k in b, k).toBe(false)
    expect((await b.info()).capabilities?.localServer).toBe(false)
    expect((await b.getServers!()).rows.some((r) => r.kind === 'local')).toBe(false)
  })
})

describe('first connect', () => {
  it('asks for a server, with address and invite choices and no local setup', async () => {
    const b = await renderClient()
    expect(screen.getByRole('heading', { level: 1, name: 'Connect to a LocalAI server' })).toBeTruthy()
    expect(document.querySelector('[data-screen="first-connect"]')).toBeTruthy()
    expect(screen.getByTestId('first-connect-address')).toBeTruthy()
    expect(screen.getByTestId('first-connect-invite')).toBeTruthy()
    expect(screen.getByTestId('first-connect-scan')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Set up this computer' })).toBeNull()
    expect(called(b, 'getServers')).toHaveLength(1)
    expect(called(b, 'info')).toHaveLength(1)
  })

  it('offers no scan without the QR capability or without a scanner', async () => {
    const noQr = fakeClientBridge(baseState())
    noQr.state.info = { ...noQr.state.info, capabilities: { localServer: false, p2p: true, selfUpdate: 'apk', qrScan: false } }
    const { unmount } = render(<App bridge={noQr} now={NOW} />)
    await settle()
    expect(document.querySelector('[data-screen="first-connect"]')).toBeTruthy()
    expect(screen.queryByTestId('first-connect-scan')).toBeNull()
    unmount()
    const noScanner = fakeClientBridge(baseState())
    delete noScanner.scanInvite
    render(<App bridge={noScanner} now={NOW} />)
    await settle()
    expect(document.querySelector('[data-screen="first-connect"]')).toBeTruthy()
    expect(screen.queryByTestId('first-connect-scan')).toBeNull()
  })

  it('a scanned invite opens the invite step filled in; Pair is still a tap away', async () => {
    const b = await renderClient((s) => void (s.scan = INVITE))
    fireEvent.click(screen.getByTestId('first-connect-scan'))
    await settle()
    expect(screen.getByRole('heading', { level: 1, name: 'Add server by invite link' })).toBeTruthy()
    expect((screen.getByLabelText('Invite link') as HTMLTextAreaElement).value).toBe(INVITE)
    expect(screen.getByTestId('invite-preview')).toBeTruthy()
    expect(called(b, 'addServerByInvite')).toHaveLength(0)
    fireEvent.click(screen.getByTestId('invite-submit'))
    await settle()
    expect(called(b, 'addServerByInvite')[0]?.args[0]).toMatchObject({ link: INVITE, makeDefault: true })
  })

  it('a scanned text that is not an invite says so and stays on the choices', async () => {
    const b = await renderClient((s) => void (s.scan = 'WIFI:S:home;T:WPA;P:secret;;'))
    fireEvent.click(screen.getByTestId('first-connect-scan'))
    await settle()
    expect(screen.getByTestId('invite-notice').textContent).toContain('This is not a LocalAI invite link.')
    expect(document.querySelector('[data-screen="first-connect"]')).toBeTruthy()
    expect(screen.queryByLabelText('Invite link')).toBeNull()
    expect(called(b, 'addServerByInvite')).toHaveLength(0)
  })

  it('a scanner without a camera or without the camera permission says so, not "not an invite"', async () => {
    for (const [code, text] of [
      ['no-camera', 'This device has no camera.'],
      ['permission-denied', 'The app may not use the camera.'],
    ]) {
      const b = await renderClient()
      b.scanInvite = async () => {
        throw Object.assign(new Error('scanner refused'), { code })
      }
      fireEvent.click(screen.getAllByTestId('first-connect-scan').at(-1)!)
      await settle()
      const notice = screen.getAllByTestId('invite-notice').at(-1)!.textContent ?? ''
      expect(notice).toContain(text)
      expect(notice).not.toContain('not a LocalAI invite')
      expect(called(b, 'addServerByInvite')).toHaveLength(0)
      cleanup()
    }
  })

  it('a shared link wrapped in <>, quotes and a full stop still opens the invite step', async () => {
    const b = await renderClient()
    b.emitInviteLink(`"<${INVITE}>".`)
    await settle()
    expect((screen.getByLabelText('Invite link') as HTMLTextAreaElement).value).toBe(INVITE)
    expect(screen.queryByTestId('invite-notice')).toBeNull()
    expect(called(b, 'addServerByInvite')).toHaveLength(0)
  })

  it('a cancelled scan does nothing', async () => {
    await renderClient()
    fireEvent.click(screen.getByTestId('first-connect-scan'))
    await settle()
    expect(document.querySelector('[data-screen="first-connect"]')).toBeTruthy()
    expect(screen.queryByTestId('invite-notice')).toBeNull()
  })

  it('a link that arrives before the server list is read still opens the invite step', async () => {
    const state = baseState()
    const b = fakeClientBridge(state)
    let release!: () => void
    const gate = new Promise<void>((r) => (release = r))
    const getServers = b.getServers!
    b.getServers = async () => (await gate, getServers())
    render(<App bridge={b} now={NOW} />)
    await settle()
    b.emitInviteLink(INVITE)
    await settle()
    release()
    await settle()
    expect((screen.getByLabelText('Invite link') as HTMLTextAreaElement).value).toBe(INVITE)
    expect(called(b, 'addServerByInvite')).toHaveLength(0)
  })

  it('with a saved server, a link opens Settings > Servers with the invite sheet', async () => {
    const b = await renderClient((s) => void (s.servers = serversView({ rows: [{ ...GPU }], defaultId: GPU.id })))
    b.emitInviteLink(`  ${INVITE}\n`)
    await settle()
    expect(screen.getByTestId('invite-sheet')).toBeTruthy()
    expect((screen.getByLabelText('Invite link') as HTMLTextAreaElement).value).toBe(INVITE)
    expect(called(b, 'addServerByInvite')).toHaveLength(0)
  })

  it('adds a server by address as the default and shows it', async () => {
    const b = await renderClient()
    fireEvent.click(screen.getByTestId('first-connect-address'))
    await settle()
    type('Server address', 'https://ai.example.com')
    fireEvent.click(screen.getByRole('button', { name: 'Test' }))
    await settle()
    expect(called(b, 'testServer')[0]?.args).toEqual([{ address: 'https://ai.example.com' }])
    fireEvent.click(screen.getByRole('button', { name: 'Connect' }))
    await settle()
    expect(called(b, 'addServer')[0]?.args[0]).toMatchObject({ address: 'https://ai.example.com', makeDefault: true })
    expect(document.querySelector('.lnr-shell')?.getAttribute('data-base')).toBe('web')
    expect(screen.getByTestId('bar-status').textContent).toContain('Remote')
  })

  it('opens the invite form with the default flag', async () => {
    const b = await renderClient()
    fireEvent.click(screen.getByTestId('first-connect-invite'))
    await settle()
    expect(screen.getByRole('heading', { level: 1, name: 'Add server by invite link' })).toBeTruthy()
    fireEvent.change(screen.getByLabelText('Invite link'), { target: { value: INVITE } })
    fireEvent.click(screen.getByTestId('invite-submit'))
    await settle()
    expect(called(b, 'addServerByInvite')[0]?.args[0]).toMatchObject({ link: INVITE, makeDefault: true })
  })

  it('shows first-connect, not a blank area, when the saved default names no server', async () => {
    await renderClient((s) => void (s.servers = serversView({ rows: [{ ...OTHER }], defaultId: 'srv_gone0000' })))
    expect(document.querySelector('[data-screen="first-connect"]')).toBeTruthy()
    expect(document.querySelector('.lnr-shell')?.getAttribute('aria-busy')).toBeNull()
  })

  it('waits for the server list, so a saved default never shows first-connect', async () => {
    const state = baseState()
    state.servers = serversView({ rows: [{ ...GPU }], defaultId: GPU.id })
    const b = fakeClientBridge(state)
    let release!: () => void
    const gate = new Promise<void>((r) => (release = r))
    const getServers = b.getServers!
    const getWebui = b.getWebui!
    b.getServers = async () => (await gate, getServers())
    b.getWebui = async () => (await gate, getWebui())
    render(<App bridge={b} now={NOW} />)
    await settle()
    expect(document.querySelector('[data-screen="first-connect"]')).toBeNull()
    release()
    await settle()
    expect(document.querySelector('[data-screen="first-connect"]')).toBeNull()
    expect(document.querySelector('.lnr-shell')?.getAttribute('data-base')).toBe('web')
  })

  it('says P2P is missing from the app, not that LocalAI must be installed', async () => {
    const b = fakeClientBridge(baseState())
    b.addServerByInvite = async () => ({ ok: false, reason: 'no-binary', detail: '' })
    render(<App bridge={b} now={NOW} />)
    await settle()
    fireEvent.click(screen.getByTestId('first-connect-invite'))
    await settle()
    fireEvent.change(screen.getByLabelText('Invite link'), { target: { value: INVITE } })
    fireEvent.click(screen.getByTestId('invite-submit'))
    await settle()
    expect(screen.getByText('P2P is not available in this build of the app.')).toBeTruthy()
    expect(screen.queryByText(/Install it first/)).toBeNull()
  })
})

describe('the bar', () => {
  it('says "No server" and has no Stop, Restart or Logs', async () => {
    await renderClient()
    expect(screen.getByTestId('bar-status').textContent).toContain('No server')
    for (const n of ['Stop', 'Restart', 'Logs']) expect(bar().queryByRole('button', { name: n })).toBeNull()
  })

  it('has no Connect item in the menu (it shares a local server)', async () => {
    await renderClient()
    fireEvent.click(bar().getByRole('button', { name: 'Menu' }))
    expect(screen.queryByRole('menuitem', { name: 'Connect' })).toBeNull()
    expect(screen.getByRole('menuitem', { name: 'Settings' })).toBeTruthy()
  })
})

describe('settings', () => {
  it('has only the General, Servers and Updates tabs', async () => {
    await renderClient()
    await openSettings()
    expect(screen.getAllByRole('tab').map((t) => t.textContent)).toEqual(['General', 'Servers', 'Updates'])
    for (const n of ['Show status bar', 'Launch at login', 'Start server when the launcher opens', 'Models folder', 'Sandbox']) {
      expect(screen.queryByText(n)).toBeNull()
    }
  })

  it('Updates shows the launcher only: no server version, rollback or channels', async () => {
    await renderClient()
    await openSettings()
    fireEvent.click(screen.getByRole('tab', { name: 'Updates' }))
    await settle()
    expect(screen.getByText('Launcher 4.1.3')).toBeTruthy()
    expect(screen.getByText('Check automatically')).toBeTruthy()
    expect(screen.queryByTestId('channel-stable')).toBeNull()
    expect(screen.queryByRole('radiogroup')).toBeNull()
    expect(within(screen.getByRole('tabpanel')).queryByText(/^LocalAI/)).toBeNull()
    expect(screen.queryByRole('button', { name: 'Roll back' })).toBeNull()
  })

  it('removing the default server says the app asks for a server', async () => {
    const b = await renderClient((s) => {
      s.servers = serversView({ rows: [GPU, OTHER], defaultId: GPU.id })
    })
    await openSettings()
    fireEvent.click(screen.getByRole('tab', { name: 'Servers' }))
    await settle()
    expect(screen.getByText('If you remove the default server, the app asks you to choose or add a server.')).toBeTruthy()
    expect(screen.queryByTestId('keep-local')).toBeNull()
    fireEvent.click(screen.getByTestId(`row-menu-${GPU.id}`))
    fireEvent.click(screen.getByRole('menuitem', { name: 'Remove…' }))
    const d = within(screen.getByTestId('remove-dialog'))
    expect(d.getByText('No server is the default until you choose one.')).toBeTruthy()
    expect(d.queryByText(/This computer/)).toBeNull()
    fireEvent.click(screen.getByTestId('remove-confirm'))
    await settle()
    expect(called(b, 'removeServer')[0]?.args).toEqual([GPU.id])
  })
})

describe('client leftovers', () => {
  it('Settings > Servers with no server does not talk about this computer', async () => {
    await renderClient()
    await openSettings()
    fireEvent.click(screen.getByRole('tab', { name: 'Servers' }))
    await settle()
    expect(screen.getByText('Add a LocalAI server to use its models in this app.')).toBeTruthy()
    expect(screen.queryByText(/this computer/i)).toBeNull()
  })

  it('without P2P, the address form offers no invite link', async () => {
    const b = fakeClientBridge(baseState())
    b.state.info = { ...b.state.info, capabilities: { localServer: false, p2p: false, selfUpdate: 'apk', qrScan: false } }
    render(<App bridge={b} now={NOW} />)
    await settle()
    expect(screen.queryByTestId('first-connect-invite')).toBeNull()
    fireEvent.click(screen.getByTestId('first-connect-address'))
    await settle()
    expect(screen.getByLabelText('Server address')).toBeTruthy()
    expect(screen.queryByTestId('use-invite')).toBeNull()
  })
})

describe('unreachable default', () => {
  it('offers "Choose another server", which opens the switcher', async () => {
    await renderClient((s) => {
      s.servers = serversView({ rows: [GPU, OTHER], defaultId: GPU.id })
      s.webui = { ...remoteWebui, remote: { kind: 'unreachable', reason: 'timed out', at: NOW - 60_000, nextAt: NOW + 20_000 } }
    })
    expect(document.querySelector('[data-screen="remote-unreachable"]')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Switch to This computer' })).toBeNull()
    expect(screen.getByText('GPU box stays saved.')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Choose another server' }))
    await settle()
    expect(screen.getByRole('menu', { name: 'Servers' })).toBeTruthy()
  })
})
