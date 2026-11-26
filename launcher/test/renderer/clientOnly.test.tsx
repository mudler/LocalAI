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

  it('says the server needs a relay when no direct connection could open', async () => {
    const b = fakeClientBridge(baseState())
    b.addServerByInvite = async () => ({ ok: false, reason: 'needs-relay', detail: '' })
    render(<App bridge={b} now={NOW} />)
    await settle()
    fireEvent.click(screen.getByTestId('first-connect-invite'))
    await settle()
    fireEvent.change(screen.getByLabelText('Invite link'), { target: { value: INVITE } })
    fireEvent.click(screen.getByTestId('invite-submit'))
    await settle()
    expect(screen.getByText(/Cannot connect directly to this server\..*local-ai p2p relay.*Then scan a new invite\./)).toBeTruthy()
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

  it('Updates shows the app only: no server version, rollback or channel radios', async () => {
    const b = await renderClient()
    await openSettings()
    fireEvent.click(screen.getByRole('tab', { name: 'Updates' }))
    await settle()
    const tab = within(screen.getByRole('tabpanel'))
    expect(tab.getByText('LocalAI for Android 4.1.3')).toBeTruthy()
    expect(tab.queryByText(/^Launcher/)).toBeNull()
    expect(tab.queryByText(/^LocalAI server/)).toBeNull()
    expect(tab.getByText('Check automatically')).toBeTruthy()
    expect(tab.getByText('Once a day, when the app opens')).toBeTruthy()
    expect(tab.getByTestId('apk-channel').textContent).toBe('Stable')
    expect(screen.queryByTestId('channel-stable')).toBeNull()
    expect(screen.queryByTestId('channel-rc')).toBeNull()
    expect(screen.queryByRole('radiogroup')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Roll back' })).toBeNull()
    fireEvent.click(tab.getByRole('button', { name: 'Get the development app' }))
    await settle()
    expect(called(b, 'getDevApp')).toHaveLength(1)
    fireEvent.click(tab.getByRole('button', { name: 'Check now' }))
    await settle()
    expect(called(b, 'checkForLauncherUpdate')).toHaveLength(1)
  })

  it('the development app names its channel and offers no development app', async () => {
    await renderClient((s) => {
      s.updateInfo = { autoCheck: true, channel: 'dev', devConfirmed: true, remoteDefault: false }
    })
    await openSettings()
    fireEvent.click(screen.getByRole('tab', { name: 'Updates' }))
    await settle()
    expect(screen.getByTestId('apk-channel').textContent).toBe('Development (separate app)')
    expect(screen.queryByRole('button', { name: 'Get the development app' })).toBeNull()
    expect(screen.queryByRole('radiogroup')).toBeNull()
  })

  it('the development app shows the Development chip in the bar, the stable app does not', async () => {
    const { unmount } = render(<App bridge={fakeClientBridge({ ...baseState(), servers: serversView({ rows: [GPU], defaultId: GPU.id }), updateInfo: { autoCheck: true, channel: 'dev', devConfirmed: true, remoteDefault: true } })} now={NOW} />)
    await settle()
    expect(bar().getByTestId('dev-chip').textContent).toBe('Development')
    unmount()
    await renderClient((s) => {
      s.servers = serversView({ rows: [GPU], defaultId: GPU.id })
      s.updateInfo = { autoCheck: true, channel: 'stable', devConfirmed: false, remoteDefault: true }
    })
    expect(bar().queryByTestId('dev-chip')).toBeNull()
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

  it('says when it is up to date', async () => {
    await renderClient((s) => {
      s.updateInfo = { autoCheck: true, channel: 'stable', devConfirmed: true, remoteDefault: false, checkedAt: NOW - 2 * 3_600_000 }
      s.updater = { phase: 'not-available' }
    })
    await openSettings()
    fireEvent.click(screen.getByRole('tab', { name: 'Updates' }))
    await settle()
    expect(screen.getByText('Checked 2 hours ago · up to date')).toBeTruthy()
  })

  it('a finished check shows its own time, not the one from before', async () => {
    const b = await renderClient((s) => {
      s.updateInfo = { autoCheck: true, channel: 'stable', devConfirmed: true, remoteDefault: false, checkedAt: NOW - 2 * 3_600_000 }
      s.updater = { phase: 'checking' }
    })
    await openSettings()
    fireEvent.click(screen.getByRole('tab', { name: 'Updates' }))
    await settle()
    b.state.updateInfo = { ...b.state.updateInfo!, checkedAt: NOW }
    b.emitUpdater({ phase: 'not-available' })
    await settle()
    expect(screen.getByText('Checked just now · up to date')).toBeTruthy()
  })
})

describe('app update (APK)', () => {
  const OFFER = { version: '4.2.0', size: 41_000_000, notesUrl: 'https://github.com/mudler/LocalAI/releases/tag/v4.2.0', downloaded: true }

  it('the bar pill opens the update sheet; it does not install', async () => {
    const b = await renderClient((s) => {
      s.updater = { phase: 'downloaded', version: '4.2.0' }
      s.apkUpdate = { offer: OFFER, canInstall: true }
    })
    fireEvent.click(bar().getByRole('button', { name: 'Update to 4.2.0' }))
    await settle()
    const sheet = within(screen.getByTestId('apk-update-sheet'))
    expect(sheet.getByText('LocalAI 4.2.0 is available')).toBeTruthy()
    expect(sheet.getByText('You have 4.1.3 · 41 MB · Stable')).toBeTruthy()
    expect(sheet.getByText('Downloaded. SHA-256 and signature match this app.')).toBeTruthy()
    expect(sheet.getByText('LocalAI closes and opens again on 4.2.0. Your servers and sign-ins stay.')).toBeTruthy()
    expect(sheet.getByText(/On Android 12 and later the update installs with no more questions/)).toBeTruthy()
    expect(sheet.queryByTestId('apk-consent')).toBeNull()
    expect(called(b, 'installLauncherUpdate')).toHaveLength(0)
    fireEvent.click(sheet.getByRole('button', { name: 'Update now' }))
    await settle()
    expect(called(b, 'installLauncherUpdate')).toHaveLength(1)
  })

  it('shows the consent step when Android does not allow installs yet', async () => {
    const b = await renderClient((s) => {
      s.updater = { phase: 'downloaded', version: '4.2.0' }
      s.apkUpdate = { offer: OFFER, canInstall: false }
    })
    fireEvent.click(bar().getByRole('button', { name: 'Update to 4.2.0' }))
    await settle()
    const sheet = within(screen.getByTestId('apk-update-sheet'))
    expect(sheet.getByText('Allow updates from LocalAI (one time)')).toBeTruthy()
    expect(sheet.getByText('Allow from this source')).toBeTruthy()
    expect((sheet.getByRole('button', { name: 'Update now' }) as HTMLButtonElement).disabled).toBe(true)
    fireEvent.click(sheet.getByRole('button', { name: 'Open Android settings' }))
    await settle()
    expect(called(b, 'openInstallSettings')).toHaveLength(1)
    expect(called(b, 'installLauncherUpdate')).toHaveLength(0)

    // Back from the settings with the switch on: the sheet reads its view again.
    b.state.apkUpdate = { offer: OFFER, canInstall: true }
    document.dispatchEvent(new Event('visibilitychange'))
    await settle()
    expect(sheet.queryByTestId('apk-consent')).toBeNull()
    expect((sheet.getByRole('button', { name: 'Update now' }) as HTMLButtonElement).disabled).toBe(false)
  })

  it('follows the install: installing, then the Android dialog, then a failure message', async () => {
    const b = await renderClient((s) => {
      s.updater = { phase: 'downloaded', version: '4.2.0' }
      s.apkUpdate = { offer: OFFER, canInstall: true, install: 'installing' }
    })
    fireEvent.click(bar().getByRole('button', { name: 'Update to 4.2.0' }))
    await settle()
    const sheet = within(screen.getByTestId('apk-update-sheet'))
    expect(sheet.getByText('Installing… LocalAI restarts by itself.')).toBeTruthy()
    expect(sheet.queryByRole('button', { name: 'Update now' })).toBeNull()

    b.state.apkUpdate = { offer: OFFER, canInstall: true, install: 'confirm' }
    b.emitUpdater({ phase: 'downloaded', version: '4.2.0' })
    await settle()
    expect(sheet.getByTestId('apk-confirm')).toBeTruthy()
    expect(sheet.getByRole('button', { name: 'Update now' })).toBeTruthy()

    b.state.apkUpdate = { offer: OFFER, canInstall: true }
    b.emitUpdater({ phase: 'downloaded', version: '4.2.0', message: 'The update was not installed: aborted.' })
    await settle()
    expect(sheet.getByText('The update was not installed: aborted.')).toBeTruthy()
    fireEvent.click(sheet.getByRole('button', { name: 'Later' }))
    await settle()
    expect(screen.queryByTestId('apk-update-sheet')).toBeNull()
  })

  it('an available update opens the sheet from Settings > Updates, and Update now downloads first', async () => {
    const b = await renderClient((s) => {
      s.updater = { phase: 'available', version: '4.2.0' }
      s.apkUpdate = { offer: { ...OFFER, downloaded: false }, canInstall: true }
    })
    await openSettings()
    fireEvent.click(screen.getByRole('tab', { name: 'Updates' }))
    await settle()
    fireEvent.click(within(screen.getByRole('tabpanel')).getByRole('button', { name: 'Update to 4.2.0' }))
    await settle()
    const sheet = within(screen.getByTestId('apk-update-sheet'))
    expect(sheet.queryByText('Downloaded. SHA-256 and signature match this app.')).toBeNull()
    fireEvent.click(sheet.getByRole('button', { name: 'Update now' }))
    await settle()
    expect(called(b, 'installLauncherUpdate')).toHaveLength(1)
  })
})

describe('about', () => {
  const openAbout = async () => {
    fireEvent.click(bar().getByRole('button', { name: 'Menu' }))
    fireEvent.click(screen.getByRole('menuitem', { name: /About LocalAI/ }))
    await settle()
    return within(screen.getByRole('dialog', { name: 'About LocalAI' }))
  }

  it('shows the version of the server on show, from its last test', async () => {
    await renderClient((s) => {
      s.servers = serversView({ rows: [{ ...GPU, version: '4.0.2' }], defaultId: GPU.id })
    })
    const about = await openAbout()
    expect(about.getByText('LocalAI server')).toBeTruthy()
    expect(about.getByText('4.0.2')).toBeTruthy()
  })

  it('labels the version "App" and shows the application ID', async () => {
    await renderClient((s) => {
      s.info = { ...s.info, version: '5.0.2', applicationId: 'io.localai.client.dev' }
      s.servers = serversView({ rows: [GPU], defaultId: GPU.id })
    })
    const about = await openAbout()
    expect(about.queryByText('Launcher')).toBeNull()
    expect(about.getByText('App')).toBeTruthy()
    expect(about.getByText('5.0.2')).toBeTruthy()
    expect(about.getByText('Application ID')).toBeTruthy()
    expect(about.getByText('io.localai.client.dev')).toBeTruthy()
  })

  it('leaves the server line out while the version is not known', async () => {
    await renderClient((s) => {
      s.servers = serversView({ rows: [GPU], defaultId: GPU.id })
    })
    const about = await openAbout()
    expect(about.queryByText('LocalAI server')).toBeNull()
    expect(about.queryByText('Unknown')).toBeNull()
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

describe('keep P2P connection in the background', () => {
  const KEEP = 'Keep P2P connection in the background'
  const keepSwitch = () => screen.queryByRole('switch', { name: KEEP })

  it('is in Settings on the client with P2P, off by default, and a tap calls the setter', async () => {
    const b = await renderClient()
    await openSettings()
    const sw = keepSwitch()
    expect(sw).toBeTruthy()
    expect(sw!.getAttribute('aria-checked')).toBe('false')
    expect(screen.getByText('Keeps P2P servers connected when you leave the app. Uses more battery and shows a notification.')).toBeTruthy()
    expect(called(b, 'getP2PKeepInBackground')).toHaveLength(1)
    fireEvent.click(sw!)
    await settle()
    expect(called(b, 'setP2PKeepInBackground').map((c) => c.args)).toEqual([[true]])
    expect(keepSwitch()!.getAttribute('aria-checked')).toBe('true')
  })

  it('follows a change from Android, such as Disconnect in the notification', async () => {
    const b = await renderClient((s) => void (s.p2pKeep = true))
    await openSettings()
    expect(keepSwitch()!.getAttribute('aria-checked')).toBe('true')
    b.emitP2PKeep(false)
    await settle()
    expect(keepSwitch()!.getAttribute('aria-checked')).toBe('false')
    expect(called(b, 'setP2PKeepInBackground')).toHaveLength(0)
  })

  it('a change that fails says so and keeps the switch as Android has it', async () => {
    const b = await renderClient()
    b.setP2PKeepInBackground = async () => {
      throw new Error('plugin gone')
    }
    await openSettings()
    fireEvent.click(keepSwitch()!)
    await settle()
    expect(screen.getByRole('alert').textContent).toContain('The setting could not be changed. Try again.')
    expect(keepSwitch()!.getAttribute('aria-checked')).toBe('false')
  })

  it('is not there without P2P, or on a bridge without the methods', async () => {
    const noP2P = fakeClientBridge(baseState())
    noP2P.state.info = { ...noP2P.state.info, capabilities: { localServer: false, p2p: false, selfUpdate: 'apk', qrScan: false } }
    noP2P.state.servers = serversView({ rows: [GPU], defaultId: GPU.id })
    noP2P.state.webui = remoteWebui
    const { unmount } = render(<App bridge={noP2P} now={NOW} />)
    await settle()
    await openSettings()
    expect(screen.getByRole('heading', { level: 1, name: 'Settings' })).toBeTruthy()
    expect(keepSwitch()).toBeNull()
    expect(called(noP2P, 'getP2PKeepInBackground')).toHaveLength(0)
    unmount()
    const old = fakeClientBridge(baseState())
    delete old.getP2PKeepInBackground
    delete old.setP2PKeepInBackground
    delete old.onP2PKeepInBackground
    old.state.servers = serversView({ rows: [GPU], defaultId: GPU.id })
    old.state.webui = remoteWebui
    render(<App bridge={old} now={NOW} />)
    await settle()
    await openSettings()
    expect(screen.getByRole('heading', { level: 1, name: 'Settings' })).toBeTruthy()
    expect(keepSwitch()).toBeNull()
  })
})

describe('certificate test card', () => {
  const testAddress = async (b: FakeBridge) => {
    fireEvent.click(screen.getByTestId('first-connect-address'))
    await settle()
    type('Server address', 'https://gpu.example.com')
    fireEvent.click(screen.getByRole('button', { name: 'Test' }))
    await settle()
    expect(called(b, 'testServer')).toHaveLength(1)
    return within(screen.getByTestId('test-result'))
  }

  for (const reason of ['self-signed', 'untrusted'] as const) {
    it(`explains a private CA for "${reason}" and links the help page`, async () => {
      const b = await renderClient((s) => void (s.testResult = { status: 'tls', reason }))
      const card = await testAddress(b)
      expect(card.getByText('The certificate is not trusted')).toBeTruthy()
      expect(card.getByTestId('ca-hint').textContent).toContain(
        'A server with a private certificate authority (CA) works after you install that CA in the Android settings.',
      )
      fireEvent.click(card.getByRole('button', { name: 'How to install a CA' }))
      await settle()
      expect(called(b, 'openCertificateHelp')).toHaveLength(1)
    })
  }

  it('gives no CA hint for an expired certificate', async () => {
    const b = await renderClient((s) => void (s.testResult = { status: 'tls', reason: 'expired' }))
    const card = await testAddress(b)
    expect(card.getByText('The certificate is not trusted')).toBeTruthy()
    expect(card.queryByTestId('ca-hint')).toBeNull()
  })

  it('gives no CA hint in a client app without the CA help (not the Android app)', async () => {
    const state = baseState()
    state.testResult = { status: 'tls', reason: 'self-signed' }
    const b = fakeClientBridge(state)
    delete b.openCertificateHelp
    render(<App bridge={b} now={NOW} />)
    await settle()
    const card = await testAddress(b)
    expect(card.getByText('The certificate is not trusted')).toBeTruthy()
    expect(card.queryByTestId('ca-hint')).toBeNull()
    expect(card.queryByText(/Android/)).toBeNull()
  })

  it('also shows the hint in the add sheet of Settings > Servers', async () => {
    const b = await renderClient((s) => {
      s.servers = serversView({ rows: [GPU], defaultId: GPU.id })
      s.webui = remoteWebui
      s.testResult = { status: 'tls', reason: 'self-signed' }
    })
    await openSettings()
    fireEvent.click(screen.getByRole('tab', { name: 'Servers' }))
    await settle()
    fireEvent.click(screen.getByTestId('add-server'))
    await settle()
    type('Server address', 'https://office.example.net')
    fireEvent.click(screen.getByRole('button', { name: 'Test' }))
    await settle()
    expect(called(b, 'testServer')).toHaveLength(1)
    expect(within(screen.getByTestId('test-result')).getByTestId('ca-hint')).toBeTruthy()
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

  const STUDIO = remoteRow('srv_00000003', 'Studio', 'http://127.0.0.2:41001', {
    isDefault: true,
    isCurrent: true,
    status: 'unreachable',
    p2p: { state: 'failed', error: 'listen tcp 127.0.0.2:41001: bind: address already in use' },
  })

  it('shows why a P2P server fails, as text, on the screen and in its row (no tooltip only)', async () => {
    await renderClient((s) => {
      s.servers = serversView({ rows: [STUDIO], defaultId: STUDIO.id })
      s.webui = {
        ...webuiView({ target: { kind: 'remote', origin: STUDIO.origin!, host: STUDIO.host } }),
        remote: { kind: 'unreachable', reason: "this app's P2P connection to it is not open", at: NOW - 1_000, nextAt: NOW + 29_000 },
      }
    })
    const tunnel = screen.getByTestId('unreach-tunnel')
    expect(tunnel.textContent).toContain('P2P connection failed')
    expect(tunnel.textContent).toContain('Another app on this device uses the local address of this server')
    await openSettings()
    fireEvent.click(screen.getByRole('tab', { name: 'Servers' }))
    await settle()
    const detail = screen.getByTestId(`server-p2p-detail-${STUDIO.id}`)
    expect(detail.textContent).toContain('P2P connection failed')
    expect(detail.textContent).toContain('Another app on this device uses the local address of this server')
  })

  it('shows any other P2P error as it is', async () => {
    const row = { ...STUDIO, p2p: { state: 'failed' as const, error: 'no route to peer' } }
    await renderClient((s) => {
      s.servers = serversView({ rows: [row], defaultId: row.id })
      s.webui = { ...webuiView({ target: { kind: 'remote', origin: row.origin!, host: row.host } }), remote: { kind: 'unreachable', reason: 'timed out', at: NOW - 1_000, nextAt: NOW + 29_000 } }
    })
    expect(screen.getByTestId('unreach-tunnel').textContent).toBe('P2P connection failed: no route to peer')
  })
})
