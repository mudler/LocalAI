// @vitest-environment jsdom
// The shell on a client-only bridge (no local server), as the mobile app runs
// it. A call to a missing local method throws a TypeError in the shell: the
// test fails on any console.error and on any unhandled rejection, so an
// unguarded call is caught.
import { fireEvent, render, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from '../../src/renderer/App'
import { LOCAL_SERVER_METHODS } from '../../src/shared/apiGroups'
import { baseState, remoteRow, serversView, webuiView, type FakeBridge, type FakeState } from '../../src/renderer/dev/fakeBridge'
import { fakeClientBridge } from '../../src/renderer/dev/fakeClientBridge'
import { called, NOW, screen, settle } from './render'

const GPU = remoteRow('srv_00000001', 'GPU box', 'https://gpu.example.com', { isDefault: true, isCurrent: true, status: 'connected' })
const OTHER = remoteRow('srv_00000002', 'Office', 'https://office.example.com', { status: 'connected' })
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
    expect(screen.queryByTestId('first-connect-scan')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Set up this computer' })).toBeNull()
    expect(called(b, 'getServers')).toHaveLength(1)
    expect(called(b, 'info')).toHaveLength(1)
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
    fireEvent.change(screen.getByLabelText('Invite link'), { target: { value: 'localai://pair?t=example' } })
    fireEvent.click(screen.getByTestId('invite-submit'))
    await settle()
    expect(called(b, 'addServerByInvite')[0]?.args[0]).toMatchObject({ link: 'localai://pair?t=example', makeDefault: true })
  })

  it('shows first-connect, not a blank area, when the saved default names no server', async () => {
    await renderClient((s) => void (s.servers = serversView({ rows: [{ ...OTHER }], defaultId: 'srv_gone0000' })))
    expect(document.querySelector('[data-screen="first-connect"]')).toBeTruthy()
    expect(document.querySelector('.lnr-shell')?.getAttribute('aria-busy')).toBeNull()
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
