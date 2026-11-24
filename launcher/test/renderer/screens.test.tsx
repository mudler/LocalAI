// @vitest-environment jsdom
import { fireEvent, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { EXTRA_NAMES, missingExtras } from '../../src/renderer/bridge'
import { fakeBridge, KEYCHAIN, webuiView } from '../../src/renderer/dev/fakeBridge'
import { fixture } from '../../src/renderer/dev/fixtures'
import { called, NOW, renderShell, screen, settle } from './render'

const bar = () => within(screen.getByRole('contentinfo', { name: 'LocalAI status bar' }))
const openMenu = (name: string | RegExp) => {
  fireEvent.click(bar().getByRole('button', { name: 'Menu' }))
  fireEvent.click(screen.getByRole('menuitem', { name }))
}

describe('install', () => {
  it('shows the version, the build and the folder, then installs and starts', async () => {
    const b = await renderShell('install')
    expect(screen.getByRole('heading', { name: 'Install LocalAI' })).toBeTruthy()
    expect(screen.getByText('LocalAI 4.2.1')).toBeTruthy()
    expect(screen.getByText('linux · amd64 · CUDA 12 build · 1.1 GB')).toBeTruthy()
    expect(screen.getByText('Installs to ~/.localai/bin')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Install and start' }))
    expect(called(b, 'installServer')).toHaveLength(1)
    b.emitInstall({ kind: 'downloading', done: 1e6, total: 1e9 })
    await settle()
    expect(screen.getByRole('heading', { name: 'Installing LocalAI 4.2.1' })).toBeTruthy()
    b.emitInstall({ kind: 'done', version: '4.2.1', adopted: false })
    await settle()
    expect(called(b, 'start')).toHaveLength(1)
  })

  it('checks for the latest release when it does not know one', async () => {
    const b = await renderShell('install', { edit: (s) => void (s.install = { kind: 'idle' }) })
    expect(called(b, 'checkServerUpdate')).toHaveLength(1)
  })

  it('says when the release cannot be found and offers Try again', async () => {
    const b = await renderShell('install', { edit: (s) => void (s.install = { kind: 'failed', reason: 'network', message: 'offline', rolledBack: false }) })
    // A failed check shows on the progress screen first; Back returns to Install.
    fireEvent.click(screen.getByRole('button', { name: 'Back' }))
    await settle()
    expect(screen.getByText('The launcher could not find the latest release.')).toBeTruthy()
    expect((screen.getByRole('button', { name: 'Install and start' }) as HTMLButtonElement).disabled).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(called(b, 'checkServerUpdate').length).toBeGreaterThan(0)
  })
})

describe('installing and updating', () => {
  it('shows the named steps, the bytes and Cancel', async () => {
    const b = await renderShell('installing')
    expect(screen.getByRole('heading', { name: 'Updating to LocalAI 4.2.1' })).toBeTruthy()
    expect(screen.getByText('640 MB of 1.1 GB')).toBeTruthy()
    expect(screen.getByRole('progressbar', { name: 'Downloading' }).getAttribute('aria-valuenow')).toBe('58')
    expect(screen.getByText('If a step fails, 4.1.3 stays installed and starts as before.')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    await settle()
    expect(called(b, 'cancelInstall')).toHaveLength(1)
    expect(bar().getByText('Updating')).toBeTruthy()
  })

  it('closes a bar screen when an update starts', async () => {
    const b = await renderShell('ready-update')
    fireEvent.click(bar().getByRole('button', { name: 'Logs' }))
    expect(screen.getByRole('heading', { name: 'Logs' })).toBeTruthy()
    b.emitInstall({ kind: 'downloading', done: 0, total: 10 })
    await settle()
    expect(screen.queryByRole('heading', { name: 'Logs' })).toBeNull()
    expect(screen.getByRole('heading', { name: 'Updating to LocalAI 4.2.1' })).toBeTruthy()
  })

  it('shows a failure with its cause, Try again, Open logs and Back', async () => {
    const b = await renderShell('install-failed')
    expect(screen.getByRole('heading', { name: 'The last install did not finish' })).toBeTruthy()
    const alert = screen.getByRole('alert')
    expect(alert.textContent).toContain('The download does not match its checksum.')
    expect(alert.textContent).toContain('The previous version was put back.')
    fireEvent.click(within(alert).getByRole('button', { name: 'Try again' }))
    expect(called(b, 'installServer')[0]?.args).toEqual([{ stopFirst: true }])
    fireEvent.click(screen.getByRole('button', { name: 'Back' }))
    await settle()
    expect(screen.getByTestId('webui-slot')).toBeTruthy()
  })

  it('shows the rollback step', async () => {
    await renderShell('ready', { edit: (s) => void (s.install = { kind: 'rolling-back' }) })
    expect(screen.getByRole('heading', { name: 'Going back to the previous version' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull()
  })
})

describe('start failed', () => {
  it('names the port, offers the free one and starts on it', async () => {
    const b = await renderShell('start-failed')
    expect(screen.getByRole('alert').textContent).toContain('Port 8080 is already in use.')
    expect(screen.getByText(/^The last start attempt failed at \d\d:\d\d$/)).toBeTruthy()
    expect(screen.getByText('8081 is free. Settings will change from 8080 to 8081.')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Use 8081 and start' }))
    await settle()
    expect(called(b, 'saveSettings')[0]?.args).toEqual([{ address: '127.0.0.1:8081' }])
    expect(called(b, 'start')).toHaveLength(1)
  })

  it('offers the process, a reinstall and the last log lines', async () => {
    const b = await renderShell('start-failed')
    fireEvent.click(screen.getByRole('button', { name: 'Show process' }))
    expect(called(b, 'showPortProcess')[0]?.args).toEqual([8080])
    await settle()
    expect(screen.getByTestId('port-user').textContent).toBe('In use by local-ai (pid 48213).')
    fireEvent.click(screen.getByRole('button', { name: 'Reinstall' }))
    expect(called(b, 'installServer')).toHaveLength(1)
    expect(screen.getByText(/ERR listen tcp/).className).toContain('lnr-log__error')
    fireEvent.click(screen.getByRole('button', { name: 'Copy' }))
    await settle()
    expect(String(called(b, 'copyText')[0]?.args[0])).toContain('14:21:08 INF Exiting')
    fireEvent.click(screen.getByRole('button', { name: 'Open full logs' }))
    expect(screen.getByRole('heading', { name: 'Logs' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Back' })).toBeTruthy()
  })

  it('without the port helper, points to Settings and makes Try again the primary action', async () => {
    await renderShell('start-failed', { extras: false })
    expect(screen.getByText('Choose a free port in Settings.')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Show process' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Try again' }).className).toContain('dk-btn--primary')
    fireEvent.click(screen.getByRole('button', { name: 'Open settings' }))
    expect(screen.getByRole('tab', { name: 'Network' }).getAttribute('aria-selected')).toBe('true')
  })
})

describe('first run and other server states', () => {
  it('lists the steps and loads LocalAI in the window', async () => {
    await renderShell('first-run')
    expect(screen.getByRole('heading', { name: 'Opening LocalAI' })).toBeTruthy()
    expect(screen.getByText('Found an NVIDIA graphics driver and 32.0 GB of memory. LocalAI will use the GPU.')).toBeTruthy()
    expect(screen.getByText('Loading LocalAI in this window')).toBeTruthy()
    expect(bar().getByText('Starting')).toBeTruthy()
    expect(bar().queryByRole('button', { name: 'Stop' })).toBeNull()
  })

  it('ends first run when the WebUI is on show', async () => {
    const b = await renderShell('first-run', { edit: (s) => void (s.webui = webuiView()) })
    expect(called(b, 'saveSettings')[0]?.args).toEqual([{ showWelcome: false }])
  })

  it('shows starting, stopping and stopped screens', async () => {
    const b = await renderShell('starting')
    expect(screen.getByRole('heading', { name: 'Starting LocalAI' })).toBeTruthy()
    b.emitServer({ state: 'stopping', endpoint: 'http://127.0.0.1:8080', logTail: [] })
    await settle()
    expect(screen.getByRole('heading', { name: 'Stopping LocalAI' })).toBeTruthy()
    b.emitServer({ state: 'stopped', endpoint: 'http://127.0.0.1:8080', logTail: [] })
    await settle()
    fireEvent.click(screen.getByRole('button', { name: 'Start' }))
    expect(called(b, 'start')).toHaveLength(1)
  })

  it('shows the browser-mode screen', async () => {
    const b = await renderShell('ready', { edit: (s) => void (s.windowSettings = { openInBrowser: true, granted: [] }) })
    fireEvent.click(screen.getByRole('button', { name: 'Open in browser' }))
    expect(called(b, 'openWebuiInBrowser')).toHaveLength(1)
  })

  it('shows a recovered config file on the shell screens', async () => {
    await renderShell('config-recovered')
    expect(screen.getByTestId('config-notice').textContent).toContain('The launcher settings could not be read.')
  })
})

describe('logs', () => {
  it('filters by level and text, copies what it shows and exports through the bridge', async () => {
    const b = await renderShell('logs')
    fireEvent.click(bar().getByRole('button', { name: 'Logs' }))
    await settle()
    expect(bar().getByRole('button', { name: 'Logs' }).getAttribute('aria-current')).toBe('page')
    const log = screen.getByRole('log', { name: 'Log lines' })
    expect(log.textContent).toContain('Starting LocalAI 4.1.3')
    expect(log.textContent).not.toContain('37 layers')
    fireEvent.click(screen.getByRole('button', { name: 'Debug' }))
    expect(log.textContent).toContain('37 layers')
    fireEvent.click(screen.getByRole('button', { name: 'Error' }))
    expect(log.querySelectorAll('.lnr-log__line')).toHaveLength(1)
    fireEvent.click(screen.getByRole('button', { name: 'Info' }))
    fireEvent.change(screen.getByRole('searchbox', { name: 'Filter' }), { target: { value: 'QWEN3' } })
    expect(log.querySelectorAll('.lnr-log__line')).toHaveLength(2)
    fireEvent.click(screen.getByRole('button', { name: 'Copy' }))
    await settle()
    const copied = String(called(b, 'copyText')[0]?.args[0])
    expect(copied.split('\n')).toHaveLength(2)
    expect(copied).toMatch(/^\d\d:\d\d:\d\d INF Loading model qwen3-8b/)
    fireEvent.change(screen.getByRole('searchbox', { name: 'Filter' }), { target: { value: 'nothing-like-this' } })
    expect(log.textContent).toBe('No line matches the filter.')
    fireEvent.click(screen.getByRole('button', { name: 'Export' }))
    await settle()
    expect(called(b, 'exportLogs')).toHaveLength(1)
    expect(screen.getByText('Saved localai-logs.txt.')).toBeTruthy()
  })

  it('appends live lines and goes back to LocalAI without a reload', async () => {
    const b = await renderShell('logs')
    fireEvent.click(bar().getByRole('button', { name: 'Logs' }))
    await settle()
    b.emitLogs([{ seq: 999, ts: NOW, source: 'server', level: 'info', text: 'a new line' }])
    await settle()
    expect(screen.getByRole('log').textContent).toContain('a new line')
    expect(called(b, 'setShellScreen').at(-1)?.args).toEqual(['logs'])
    expect(screen.getByText('The LocalAI page stays open behind this screen.')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Back to LocalAI' }))
    await settle()
    expect(called(b, 'setShellScreen').at(-1)?.args).toEqual([null])
    expect(screen.getByTestId('webui-slot')).toBeTruthy()
    // Logs toggles: a second press goes back too.
    fireEvent.click(bar().getByRole('button', { name: 'Logs' }))
    fireEvent.click(bar().getByRole('button', { name: 'Logs' }))
    expect(screen.getByTestId('webui-slot')).toBeTruthy()
  })

  it('says when there is nothing yet', async () => {
    await renderShell('ready', { edit: (s) => void (s.logs = []) })
    fireEvent.click(bar().getByRole('button', { name: 'Logs' }))
    await settle()
    expect(screen.getByRole('log').textContent).toBe('No log lines yet.')
  })
})

describe('settings', () => {
  it('shows the updates, window, sandbox and startup rows', async () => {
    const b = await renderShell('settings')
    openMenu('Settings')
    await settle()
    expect(screen.getByText('Microphone and camera allowed')).toBeTruthy()
    expect(screen.getByText('Ubuntu 24.04 blocks it for this app')).toBeTruthy()
    // The server updates have their own tab.
    fireEvent.click(screen.getByRole('tab', { name: 'Updates' }))
    expect(screen.getByText('LocalAI 4.1.3')).toBeTruthy()
    expect(screen.getByText('Checked 10 minutes ago · 4.2.1 is available')).toBeTruthy()
    expect(screen.getByText('4.1.2 kept after the last update')).toBeTruthy()
    const main = within(screen.getByRole('main'))
    fireEvent.click(main.getByRole('button', { name: 'Update to 4.2.1' }))
    expect(called(b, 'installServer')[0]?.args).toEqual([{ stopFirst: true }])
    fireEvent.click(screen.getByRole('button', { name: 'Roll back' }))
    expect(called(b, 'rollbackServer')[0]?.args).toEqual([{ stopFirst: true }])
    fireEvent.click(screen.getByRole('tab', { name: 'General' }))
    fireEvent.click(screen.getByRole('button', { name: 'Reset' }))
    await settle()
    expect(called(b, 'resetWebuiPermissions')).toHaveLength(1)
    expect(screen.getByText('Nothing allowed')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Reset' }).hasAttribute('disabled')).toBe(true)
  })

  it('saves a switch at once, with no Save button', async () => {
    const b = await renderShell('settings')
    openMenu('Settings')
    await settle()
    fireEvent.click(screen.getByRole('switch', { name: 'Launch at login' }))
    await settle()
    expect(called(b, 'saveSettings')[0]?.args).toEqual([{ startOnBoot: true }])
    expect(screen.queryByRole('button', { name: /Apply|Save/ })).toBeNull()
    fireEvent.click(screen.getByRole('switch', { name: 'Show status bar' }))
    await settle()
    expect(called(b, 'setStatusBarVisible')[0]?.args).toEqual([false])
    fireEvent.click(screen.getByRole('switch', { name: 'Open the WebUI in my browser instead' }))
    await settle()
    expect(called(b, 'setOpenInBrowser')[0]?.args).toEqual([true])
    const notifications = screen.getByRole('switch', { name: 'Desktop notifications' })
    expect(notifications.getAttribute('aria-checked')).toBe('true')
    fireEvent.click(notifications)
    await settle()
    expect(called(b, 'setDesktopNotifications')[0]?.args).toEqual([false])
    fireEvent.click(screen.getByRole('tab', { name: 'Updates' }))
    fireEvent.click(screen.getByRole('switch', { name: 'Check automatically' }))
    await settle()
    expect(called(b, 'setAutoCheck')[0]?.args).toEqual([false])
  })

  it('says why "Launch at login" did not turn on, and keeps the switch off', async () => {
    const b = await renderShell('settings', { edit: (s) => void (s.loginItem = { status: 'unavailable', reason: 'not-appimage' }) })
    openMenu('Settings')
    await settle()
    const sw = screen.getByRole('switch', { name: 'Launch at login' })
    fireEvent.click(sw)
    await settle()
    expect(called(b, 'saveSettings')[0]?.args).toEqual([{ startOnBoot: true }])
    expect(sw.getAttribute('aria-checked')).toBe('false')
    expect(screen.getByText('Works in the installed app only (the AppImage), not in a run from source.')).toBeTruthy()
  })

  it('keeps text fields as a draft until Apply and restart', async () => {
    const b = await renderShell('settings')
    openMenu('Settings')
    await settle()
    fireEvent.click(screen.getByRole('tab', { name: 'Network' }))
    fireEvent.change(screen.getByLabelText('Address'), { target: { value: '127.0.0.1:9090' } })
    expect(screen.getByText('1 change needs a restart')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Discard' }))
    expect(screen.queryByText('1 change needs a restart')).toBeNull()
    fireEvent.change(screen.getByLabelText('Address'), { target: { value: '127.0.0.1:9090' } })
    fireEvent.click(screen.getByRole('button', { name: 'Apply and restart' }))
    await settle()
    expect(called(b, 'saveSettings')[0]?.args).toEqual([{ address: '127.0.0.1:9090' }])
    expect(called(b, 'restart')).toHaveLength(1)
    expect(screen.queryByText('1 change needs a restart')).toBeNull()
  })

  it('shows the data folders read-only; "Change…" lets the main process pick and save', async () => {
    const b = await renderShell('settings', { edit: (s) => void (s.pickedFolder = '/srv/localai/models') })
    openMenu('Settings')
    await settle()
    const models = screen.getByLabelText('Models folder') as HTMLInputElement
    expect(models.readOnly).toBe(true)
    expect(models.value).toBe('~/.localai/models')
    // Typing does nothing: the field is not a draft.
    fireEvent.change(models, { target: { value: '/etc' } })
    expect(screen.queryByText(/change needs a restart|change is not saved/)).toBeNull()
    const row = models.closest('.lnr-field') as HTMLElement
    fireEvent.click(within(row).getByRole('button', { name: 'Change…' }))
    await settle()
    // The renderer sends only the kind, never a path.
    expect(called(b, 'pickFolder')[0]?.args).toEqual(['models'])
    expect(called(b, 'saveSettings')).toHaveLength(0)
    expect((screen.getByLabelText('Models folder') as HTMLInputElement).value).toBe('/srv/localai/models')
    expect(screen.getByText('Saved. A restart applies the change.')).toBeTruthy()
    fireEvent.click(screen.getByRole('tab', { name: 'Advanced' }))
    const backends = screen.getByLabelText('Backends folder') as HTMLInputElement
    expect(backends.readOnly).toBe(true)
    fireEvent.click(within(backends.closest('.lnr-field') as HTMLElement).getByRole('button', { name: 'Change…' }))
    await settle()
    expect(called(b, 'pickFolder')[1]?.args).toEqual(['backends'])
    for (const c of called(b, 'saveSettings')) expect(JSON.stringify(c.args)).not.toMatch(/modelsPath|backendsPath/)
  })

  it('says why a picked folder was not saved, and keeps the old one', async () => {
    const b = await renderShell('settings', { edit: (s) => void (s.pickedFolder = { refused: 'home' }) })
    openMenu('Settings')
    await settle()
    fireEvent.click(within(screen.getByLabelText('Models folder').closest('.lnr-field') as HTMLElement).getByRole('button', { name: 'Change…' }))
    await settle()
    expect(called(b, 'pickFolder')).toHaveLength(1)
    expect(screen.getByRole('alert').textContent).toContain('Your home folder cannot be used. Choose a folder inside it.')
    expect((screen.getByLabelText('Models folder') as HTMLInputElement).value).toBe('~/.localai/models')
  })

  it('says Save when the server is not running', async () => {
    const b = await renderShell('stopped')
    openMenu('Settings')
    await settle()
    fireEvent.click(screen.getByRole('tab', { name: 'Network' }))
    fireEvent.change(screen.getByLabelText('Address'), { target: { value: '0.0.0.0:9090' } })
    expect(screen.getByText('1 change is not saved')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await settle()
    expect(called(b, 'saveSettings')[0]?.args).toEqual([{ address: '0.0.0.0:9090' }])
    expect(called(b, 'restart')).toHaveLength(0)
  })

  it('moves between tabs with the arrow keys', async () => {
    await renderShell('settings')
    openMenu('Settings')
    await settle()
    const general = screen.getByRole('tab', { name: 'General' })
    fireEvent.keyDown(general, { key: 'ArrowRight' })
    expect(screen.getByRole('tab', { name: 'Servers' }).getAttribute('aria-selected')).toBe('true')
    expect(document.activeElement).toBe(screen.getByRole('tab', { name: 'Servers' }))
    fireEvent.keyDown(document.activeElement!, { key: 'End' })
    expect(screen.getByRole('tab', { name: 'Advanced' }).getAttribute('aria-selected')).toBe('true')
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowRight' })
    expect(screen.getByRole('tab', { name: 'General' }).getAttribute('aria-selected')).toBe('true')
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowLeft' })
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowLeft' })
    expect(screen.getByRole('tab', { name: 'Performance' }).getAttribute('aria-selected')).toBe('true')
    expect(screen.getByText('No performance settings here')).toBeTruthy()
  })

  it('lists environment variables by name with masked values; values go to the main process only', async () => {
    const b = await renderShell('settings-env')
    openMenu('Settings')
    await settle()
    fireEvent.click(screen.getByRole('tab', { name: 'Advanced' }))
    await settle()
    const group = within(screen.getByTestId('env-vars'))
    expect(group.getByText('HF_TOKEN')).toBeTruthy()
    expect(group.getAllByTestId('env-masked').map((e) => e.textContent)).toEqual(['••••••••', '••••••••', '••••••••'])
    expect(group.getByText('Not passed')).toBeTruthy()
    expect(group.getByText('Not passed: the launcher strips or sets these names itself.')).toBeTruthy()

    // A name the launcher controls is refused before anything is sent.
    fireEvent.click(screen.getByRole('button', { name: 'Add variable' }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'PATH' } })
    fireEvent.change(screen.getByLabelText('Value'), { target: { value: '/x' } })
    expect((screen.getByLabelText('Value') as HTMLInputElement).type).toBe('password')
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await settle()
    expect(screen.getByRole('alert').textContent).toBe('The launcher sets this variable itself. Use the typed settings instead.')
    expect(called(b, 'setEnvVar')).toHaveLength(0)

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'THREADS' } })
    fireEvent.change(screen.getByLabelText('Value'), { target: { value: '8' } })
    fireEvent.click(screen.getByRole('button', { name: 'Show the value of THREADS' }))
    expect((screen.getByLabelText('Value') as HTMLInputElement).type).toBe('text')
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await settle()
    expect(called(b, 'setEnvVar')[0]?.args).toEqual(['THREADS', '8'])
    expect(screen.getByText('Saved. A restart applies the change.')).toBeTruthy()
    // The saved value is not kept in the page.
    expect(document.body.innerHTML).not.toContain('value="8"')

    fireEvent.click(screen.getByRole('button', { name: 'Replace the value of HF_TOKEN' }))
    const nv = screen.getByLabelText('New value of HF_TOKEN') as HTMLInputElement
    expect(nv.type).toBe('password')
    fireEvent.change(nv, { target: { value: 'hf_new' } })
    fireEvent.keyDown(nv, { key: 'Enter' })
    await settle()
    expect(called(b, 'setEnvVar')[1]?.args).toEqual(['HF_TOKEN', 'hf_new'])

    fireEvent.click(screen.getByRole('button', { name: 'Remove LD_LIBRARY_PATH' }))
    await settle()
    expect(called(b, 'deleteEnvVar')[0]?.args).toEqual(['LD_LIBRARY_PATH'])
    expect(within(screen.getByTestId('env-vars')).queryByText('LD_LIBRARY_PATH')).toBeNull()
    expect(called(b, 'saveSettings')).toHaveLength(0)
  })

  it('says when there is no keyring and offers the plain file', async () => {
    const b = await renderShell('settings-no-keyring')
    openMenu('Settings')
    await settle()
    fireEvent.click(screen.getByRole('tab', { name: 'Advanced' }))
    await settle()
    expect(screen.getByTestId('secrets-notice').textContent).toContain('Your system has no keyring')
    expect(screen.getByText('This session only')).toBeTruthy()
    fireEvent.click(screen.getByRole('switch', { name: 'Keep secrets in a file' }))
    await settle()
    expect(called(b, 'saveSettings')[0]?.args).toEqual([{ secretsPlaintextFile: true }])
    expect(screen.getByTestId('secrets-notice').textContent).toContain('Secrets are in a plain file')
    expect(screen.queryByText('This session only')).toBeNull()
  })

  it('the Network tab says whether other devices are allowed', async () => {
    await renderShell('settings-env')
    openMenu('Settings')
    await settle()
    fireEvent.click(screen.getByRole('tab', { name: 'Network' }))
    expect(screen.getByTestId('network-status').textContent).toBe('Allowed · API key on')
  })

  it('opens the sandbox guide and the worker mode from Settings', async () => {
    await renderShell('settings', { edit: (s) => void (s.worker = { mode: 'local', running: false, status: 'idle', config: { serverUrl: '', natsUrl: '', advertiseAddress: '', nodeName: '' }, tokenStored: false }) })
    openMenu('Settings')
    await settle()
    fireEvent.click(screen.getByRole('button', { name: 'Set up sandbox profile' }))
    expect(screen.getByRole('dialog', { name: 'The sandbox is off for this window' })).toBeTruthy()
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' })
    fireEvent.click(screen.getByRole('tab', { name: 'Advanced' }))
    fireEvent.click(screen.getAllByRole('button', { name: 'Change…' }).at(-1)!)
    expect(screen.getByRole('heading', { name: 'How should this computer run LocalAI?' })).toBeTruthy()
  })

  it('hides the rows whose channel the bridge does not have', async () => {
    await renderShell('settings', { extras: false })
    openMenu('Settings')
    await settle()
    expect(screen.queryByText('Open the WebUI in my browser instead')).toBeNull()
    expect(screen.queryByText('Reset permissions for this server')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Change…' })).toBeNull()
    expect(screen.getByText('Show status bar')).toBeTruthy()
    fireEvent.click(screen.getByRole('tab', { name: 'Updates' }))
    expect(screen.queryByText('Check automatically')).toBeNull()
    expect(screen.getByText('Previous version')).toBeTruthy()
    fireEvent.click(screen.getByRole('tab', { name: 'General' }))
    fireEvent.click(screen.getByRole('tab', { name: 'Advanced' }))
    expect(screen.queryByText('Environment variables')).toBeNull()
  })
})

describe('connect', () => {
  it('shows the address for other devices and a masked key; the key comes only on Show, copies go through the main process', async () => {
    const b = await renderShell('connect')
    openMenu('Connect')
    await settle()
    expect(screen.getByText('http://192.0.2.24:8080/v1')).toBeTruthy()
    expect(screen.getByTestId('api-key').textContent).toBe('lai-••••••••••••••••3f9a')
    expect(document.body.innerHTML).not.toContain(b.state.apiKey!)
    expect(called(b, 'revealApiKey')).toHaveLength(0)
    const code = () => screen.getByLabelText(/OpenAI-compatible|Anthropic-compatible|curl/, { selector: 'pre' }).textContent
    expect(code()).toBe('export OPENAI_BASE_URL=http://127.0.0.1:8080/v1\nexport OPENAI_API_KEY=lai-••••3f9a')

    fireEvent.click(screen.getByRole('button', { name: 'Show' }))
    await settle()
    expect(called(b, 'revealApiKey')).toHaveLength(1)
    expect(screen.getByTestId('api-key').textContent).toBe(b.state.apiKey)
    fireEvent.click(screen.getByRole('button', { name: 'Hide' }))
    expect(screen.getByTestId('api-key').textContent).toBe('lai-••••••••••••••••3f9a')
    expect(document.body.innerHTML).not.toContain(b.state.apiKey!)

    const copyButtons = screen.getAllByRole('button', { name: 'Copy' })
    fireEvent.click(copyButtons[copyButtons.length - 1])
    await settle()
    expect(called(b, 'copyApiKey').at(-1)?.args).toEqual(['openai'])
    fireEvent.click(copyButtons[1])
    await settle()
    expect(called(b, 'copyApiKey').at(-1)?.args).toEqual(['key'])
    // The key never went through copyText (the renderer does not have it).
    expect(called(b, 'copyText')).toHaveLength(0)
    fireEvent.click(screen.getByRole('button', { name: 'Anthropic-compatible' }))
    expect(code()).toContain('ANTHROPIC_BASE_URL=http://127.0.0.1:8080')
    fireEvent.click(screen.getByRole('button', { name: 'curl' }))
    expect(code()).toContain('/v1/chat/completions')
    expect(code()).toContain('Bearer lai-••••3f9a')
  })

  it('turning off applies at once and keeps the key', async () => {
    const b = await renderShell('connect')
    openMenu('Connect')
    await settle()
    fireEvent.click(screen.getByRole('switch', { name: 'Allow other devices on my network' }))
    await settle()
    expect(called(b, 'setNetworkExposed')[0]?.args).toEqual([false])
    expect(called(b, 'getNetworkPlan')).toHaveLength(0)
    expect(screen.queryByText('http://192.0.2.24:8080/v1')).toBeNull()
    expect(screen.getByTestId('api-key').textContent).toBe('lai-••••••••••••••••3f9a')
  })

  it('turning on shows the address and the key step first, and applies only on Allow', async () => {
    const b = await renderShell('connect-local')
    openMenu('Connect')
    await settle()
    expect(screen.queryByTestId('api-key')).toBeNull()
    const sw = () => screen.getByRole('switch', { name: 'Allow other devices on my network' })
    fireEvent.click(sw())
    await settle()
    expect(called(b, 'getNetworkPlan')).toHaveLength(1)
    expect(called(b, 'setNetworkExposed')).toHaveLength(0)
    const plan = within(screen.getByRole('alertdialog', { name: 'Allow other devices on your network?' }))
    expect(plan.getByText(/all network interfaces \(0\.0\.0\.0:8080\)/)).toBeTruthy()
    expect(plan.getByText('http://192.0.2.24:8080/v1')).toBeTruthy()
    expect(plan.getByText('An API key is created now. Apps on other devices must send it.')).toBeTruthy()
    expect(plan.getByText('LocalAI restarts to apply this.')).toBeTruthy()

    fireEvent.click(plan.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect(called(b, 'setNetworkExposed')).toHaveLength(0)

    fireEvent.click(sw())
    await settle()
    fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Allow' }))
    await settle()
    expect(called(b, 'setNetworkExposed')[0]?.args).toEqual([true, '0123456789abcdef0123456789abcdef'])
    expect(screen.getByText('http://192.0.2.24:8080/v1')).toBeTruthy()
    expect(screen.getByTestId('api-key').textContent).toBe('lai-••••••••••••••••0new')
  })

  it('an expired plan is refused and says so', async () => {
    const b = await renderShell('connect-local')
    // A plan the fake main process does not know, as after the expiry.
    b.getNetworkPlan = async () => ({ planId: 'f'.repeat(32), bindAddress: '0.0.0.0:8080', lanAddresses: [], createsKey: true, restartRequired: false, storage: KEYCHAIN })
    openMenu('Connect')
    await settle()
    fireEvent.click(screen.getByRole('switch', { name: 'Allow other devices on my network' }))
    await settle()
    expect(screen.getByText('No network address was found on this computer. Check the network connection.')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Allow' }))
    await settle()
    expect(screen.getByRole('alert').textContent).toBe('This took too long. Try again.')
    expect(screen.queryByTestId('api-key')).toBeNull()
  })

  it('regenerates the key only after the confirm, then offers the restart', async () => {
    const b = await renderShell('connect')
    openMenu('Connect')
    await settle()
    fireEvent.click(screen.getByRole('button', { name: 'Regenerate' }))
    const confirm = within(screen.getByRole('alertdialog', { name: 'Make a new API key?' }))
    fireEvent.click(confirm.getByRole('button', { name: 'Cancel' }))
    expect(called(b, 'regenerateApiKey')).toHaveLength(0)
    fireEvent.click(screen.getByRole('button', { name: 'Regenerate' }))
    fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Make new key' }))
    await settle()
    expect(called(b, 'regenerateApiKey')).toHaveLength(1)
    expect(screen.getByTestId('api-key').textContent).toBe('lai-••••••••••••••••9b1c')
    fireEvent.click(screen.getByRole('button', { name: 'Restart now' }))
    await settle()
    expect(called(b, 'restart')).toHaveLength(1)
  })

  it('warns when the server is open to the network without a key', async () => {
    const b = await renderShell('connect', { edit: (s) => void ((s.network = { ...s.network!, apiKeySet: false, apiKeyHint: undefined }), (s.apiKey = undefined)) })
    openMenu('Connect')
    await settle()
    expect(screen.getByText('Other devices can use LocalAI without a key.')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Create key' }))
    await settle()
    expect(called(b, 'regenerateApiKey')).toHaveLength(1)
    expect(screen.queryByText('Other devices can use LocalAI without a key.')).toBeNull()
  })

  it('says when the key is kept only for this session', async () => {
    await renderShell('connect-no-keyring')
    openMenu('Connect')
    await settle()
    expect(screen.getByTestId('secrets-notice').textContent).toContain('Your system has no keyring')
  })

  it('has no field for another server: servers live in Settings', async () => {
    const b = await renderShell('connect')
    openMenu('Connect')
    await settle()
    expect(screen.queryByLabelText('Server address')).toBeNull()
    expect(called(b, 'connectRemote')).toHaveLength(0)
  })

  it('works with the current bridge: examples only, no key', async () => {
    await renderShell('connect', { extras: false })
    openMenu('Connect')
    await settle()
    expect(screen.queryByText('Allow other devices on my network')).toBeNull()
    expect(screen.queryByText('Use a server on another computer')).toBeNull()
    expect(screen.getByLabelText('OpenAI-compatible', { selector: 'pre' }).textContent).toContain('OPENAI_API_KEY=not-needed')
  })
})

describe('distributed worker', () => {
  it('tests and starts a worker, with Start off until the fields are there', async () => {
    const b = await renderShell('worker-settings')
    openMenu('Settings')
    await settle()
    fireEvent.click(screen.getByRole('tab', { name: 'Advanced' }))
    fireEvent.click(screen.getAllByRole('button', { name: 'Change…' }).at(-1)!)
    expect(screen.getByText('Reachable · waiting for approval')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Test connection' }))
    await settle()
    expect(called(b, 'testWorker')[0]?.args[0]).toMatchObject({ serverUrl: 'https://ai.example.test', token: '' })
    fireEvent.change(screen.getByLabelText('Server URL'), { target: { value: '' } })
    expect((screen.getByRole('button', { name: 'Start worker' }) as HTMLButtonElement).disabled).toBe(true)
    fireEvent.change(screen.getByLabelText('Server URL'), { target: { value: 'https://ai.example.test' } })
    fireEvent.click(screen.getByRole('button', { name: 'Start worker' }))
    await settle()
    expect(called(b, 'startWorker')).toHaveLength(1)
    expect(screen.getByRole('heading', { name: 'This computer is a worker' })).toBeTruthy()
  })

  it('switches the run mode', async () => {
    const b = await renderShell('worker-settings')
    openMenu('Settings')
    await settle()
    fireEvent.click(screen.getByRole('tab', { name: 'Advanced' }))
    fireEvent.click(screen.getAllByRole('button', { name: 'Change…' }).at(-1)!)
    fireEvent.click(screen.getByRole('radio', { name: /Run locally/ }))
    await settle()
    expect(called(b, 'setRunMode')[0]?.args).toEqual(['local'])
    expect(screen.queryByRole('button', { name: 'Start worker' })).toBeNull()
  })

  it('shows the running worker and stops it', async () => {
    const b = await renderShell('worker-running')
    expect(screen.getByText('Joined https://coord.example.test')).toBeTruthy()
    expect(screen.getByText('Remembered')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Stop worker' }))
    await settle()
    expect(called(b, 'stopWorker')).toHaveLength(1)
  })
})

describe('connect: key created at start', () => {
  it('says once that a key was created because LocalAI was open to the network, until OK', async () => {
    const b = await renderShell('connect', { edit: (s) => void (s.network = { ...s.network!, keyCreatedAtStart: true }) })
    openMenu('Connect')
    await settle()
    const notice = screen.getByTestId('key-created-notice')
    expect(notice.textContent).toContain('A new API key was created because LocalAI was open to your network without one.')
    fireEvent.click(within(notice).getByRole('button', { name: 'OK' }))
    await settle()
    expect(called(b, 'dismissKeyNotice')).toHaveLength(1)
    expect(screen.queryByTestId('key-created-notice')).toBeNull()
  })

  it('shows nothing without the flag', async () => {
    await renderShell('connect')
    openMenu('Connect')
    await settle()
    expect(screen.queryByTestId('key-created-notice')).toBeNull()
  })
})

describe('sandbox guide', () => {
  it('opens once by itself when the notice is not yet seen; setup is marked not available and shows no command', async () => {
    await renderShell('sandbox-off')
    const guide = screen.getByRole('dialog', { name: 'The sandbox is off for this window' })
    expect(guide.textContent).toContain('Ubuntu 24.04 blocks the sandbox')
    expect(guide.textContent).toContain('A sandbox profile turns the isolation back on.')
    expect(guide.textContent).toContain('Setup is not available yet.')
    // Said once, in the not-available callout after a try; never twice in the open guide.
    expect(guide.textContent).not.toContain('works as before')
    expect(guide.textContent).not.toMatch(/pkexec|apparmor_parser|Command ·/)
    const setUp = within(guide).getByRole('button', { name: 'Set up sandbox profile' }) as HTMLButtonElement
    expect(setUp.disabled).toBe(true)
    expect(document.getElementById(setUp.getAttribute('aria-describedby')!)?.textContent).toBe('Setup is not available yet.')
    expect(within(guide).getByRole('button', { name: 'Not now' })).toBeTruthy()
    expect(within(guide).getByRole('button', { name: 'open the WebUI in my browser instead' })).toBeTruthy()
    expect(bar().getByRole('button', { name: /Sandbox off/ }).getAttribute('aria-expanded')).toBe('true')
  })

  it('does not open by itself before the server is ready, nor after "Not now" for this version', async () => {
    const b = await renderShell('sandbox-off', { edit: (s) => void ((s.server = { ...s.server, state: 'starting' }), (s.webui = undefined)) })
    expect(screen.queryByRole('dialog')).toBeNull()
    b.emitServer({ ...b.state.server, state: 'ready' })
    await settle()
    expect(screen.getByRole('dialog', { name: 'The sandbox is off for this window' })).toBeTruthy()
    document.body.innerHTML = ''
    await renderShell('sandbox-off', { edit: (s) => void (s.sandbox = { ...s.sandbox, notice: { kind: 'dismissed' } }) })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(bar().getByRole('button', { name: /Sandbox off/ })).toBeTruthy()
  })

  it('a disabled setup button runs nothing', async () => {
    const b = await renderShell('sandbox-off')
    fireEvent.click(screen.getByRole('button', { name: 'Set up sandbox profile' }))
    await settle()
    expect(called(b, 'runSandboxSetup')).toHaveLength(0)
  })

  it('shows a refused setup gracefully and changes nothing, where setup is offered', async () => {
    // The main process refuses at run time (for example a test attach rule that turned out invalid).
    const b = await renderShell('sandbox-off', {
      edit: (s) => {
        s.setupResult = { ...s.sandbox!, setupAvailable: false }
        s.sandbox = { ...s.sandbox!, setupAvailable: true }
      },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Set up sandbox profile' }))
    await settle()
    expect(called(b, 'runSandboxSetup')).toHaveLength(1)
    expect(screen.getByTestId('sandbox-not-available').textContent).toContain('Setup is not available in this version.')
  })

  it('"Not now" dismisses the notice and keeps the chip', async () => {
    const b = await renderShell('sandbox-off')
    fireEvent.click(screen.getByRole('button', { name: 'Not now' }))
    await settle()
    expect(called(b, 'dismissSandboxNotice')).toHaveLength(1)
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(bar().getByRole('button', { name: /Sandbox off/ })).toBeTruthy()
    fireEvent.click(bar().getByRole('button', { name: /Sandbox off/ }))
    expect(screen.queryByRole('button', { name: 'Not now' })).toBeNull()
  })

  it('offers the browser instead', async () => {
    const b = await renderShell('sandbox-off')
    fireEvent.click(screen.getByRole('button', { name: 'open the WebUI in my browser instead' }))
    await settle()
    expect(called(b, 'setOpenInBrowser')[0]?.args).toEqual([true])
  })

  it('shows the real plan before anything is written, when there is one', async () => {
    await renderShell('sandbox-off', {
      edit: (s) => {
        s.sandbox = { ...s.sandbox, setupAvailable: true }
        s.plan = { kind: 'setup', available: true, profilePath: '/etc/apparmor.d/localai-launcher', profileText: 'profile localai {}', steps: [{ description: 'Load', command: 'pkexec apparmor_parser -r /etc/apparmor.d/localai-launcher' }], terminalText: '' }
      },
    })
    const guide = screen.getByRole('dialog')
    expect(guide.textContent).toContain('pkexec apparmor_parser -r /etc/apparmor.d/localai-launcher')
    expect(guide.textContent).toContain('Profile file · /etc/apparmor.d/localai-launcher')
    expect(guide.textContent).toContain('profile localai {}')
  })

  it('follows the notice through setup, restart, failure and removal', async () => {
    const b = await renderShell('sandbox-off')
    const sb = b.state.sandbox
    b.emitSandbox({ ...sb, notice: { kind: 'installing' } })
    await settle()
    expect(screen.getByRole('button', { name: 'Setting up the profile…' })).toBeTruthy()
    b.emitSandbox({ ...sb, notice: { kind: 'installed-needs-restart' } })
    await settle()
    expect(screen.getByText('The profile is set up.')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Restart the launcher' }))
    expect(called(b, 'relaunch')).toHaveLength(1)
    fireEvent.click(screen.getByRole('button', { name: 'Remove profile' }))
    await settle()
    expect(screen.getByRole('dialog').textContent).toContain('apparmor_parser -R')
    fireEvent.click(screen.getByRole('button', { name: 'Remove profile' }))
    await settle()
    expect(called(b, 'runSandboxRemove')).toHaveLength(1)
    expect(screen.getByText('The profile is removed.')).toBeTruthy()
    b.emitSandbox({ ...sb, notice: { kind: 'failed', reason: 'cancelled' } })
    await settle()
    expect(screen.getByText('You closed the password dialog. Nothing was changed.')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy()
  })
})

describe('bridge adapter', () => {
  it('lists the optional channels the current preload does not have', () => {
    const b = fakeBridge(fixture('ready'), { extras: false })
    expect(missingExtras(b)).toEqual(EXTRA_NAMES)
    expect(missingExtras(fakeBridge(fixture('ready')))).toEqual([])
  })

  it('renders every fixture with the current preload only, without errors', async () => {
    for (const name of ['ready', 'install', 'installing', 'start-failed', 'first-run', 'remote', 'server-stopped', 'sandbox-off', 'bar-hidden', 'starting'] as const) {
      await renderShell(name, { extras: false })
      expect(document.querySelector('.lnr-shell')).toBeTruthy()
      document.body.innerHTML = ''
    }
    await waitFor(() => true)
  })
})
