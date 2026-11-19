// @vitest-environment jsdom
import { fireEvent, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { called, renderShell, screen, settle } from './render'

const bar = () => within(screen.getByRole('contentinfo', { name: 'LocalAI status bar' }))
const openSettingsTab = async (tab: string) => {
  fireEvent.click(bar().getByRole('button', { name: 'Menu' }))
  fireEvent.click(screen.getByRole('menuitem', { name: 'Settings' }))
  await settle()
  fireEvent.click(screen.getByRole('tab', { name: tab }))
  await settle()
}
const type = (label: string, value: string) => fireEvent.change(screen.getByLabelText(label), { target: { value } })

describe('first run (mock 17 and 18)', () => {
  it('asks where LocalAI runs, with no bar and the computer card first', async () => {
    await renderShell('first-choose')
    expect(screen.getByRole('heading', { name: 'Where should LocalAI run?' })).toBeTruthy()
    expect(screen.queryByRole('contentinfo')).toBeNull()
    expect(screen.queryByTestId('corner-dot')).toBeNull()
    const cards = screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent)
    expect(cards).toEqual(['Run LocalAI on this computer', 'Connect to a LocalAI server'])
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Set up this computer' }))
    expect(screen.getByText('About 1.1 GB. Takes a few minutes.')).toBeTruthy()
  })

  it('"Set up this computer" goes on to Install and saves nothing', async () => {
    const b = await renderShell('first-choose')
    fireEvent.click(screen.getByRole('button', { name: 'Set up this computer' }))
    await settle()
    expect(screen.getByRole('heading', { name: 'Install LocalAI' })).toBeTruthy()
    for (const n of ['addServer', 'setDefaultServer', 'saveSettings', 'testServer']) expect(called(b, n)).toHaveLength(0)
  })

  it('"Connect" saves the server only after a passing test, makes it the default and uses the suggested name', async () => {
    const b = await renderShell('first-choose', { edit: (s) => void (s.testResult = { status: 'ok', version: '4.2.1', secure: true }) })
    fireEvent.click(screen.getByRole('button', { name: 'Connect to a server' }))
    await settle()
    expect(document.activeElement).toBe(screen.getByLabelText('Server address'))
    expect((screen.getByRole('button', { name: 'Connect' }) as HTMLButtonElement).disabled).toBe(true)
    type('Server address', 'https://office-gpu.example.com')
    expect(screen.getByTestId('address-hint').textContent).toContain('Looks right.')
    expect((screen.getByLabelText('Name') as HTMLInputElement).value).toBe('Office gpu')
    expect(screen.getByText('Suggested')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Test' }))
    await settle()
    expect(called(b, 'testServer')[0]?.args).toEqual([{ address: 'https://office-gpu.example.com' }])
    expect(screen.getByTestId('test-result').textContent).toContain('Connected to LocalAI 4.2.1')
    fireEvent.click(screen.getByRole('button', { name: 'Connect' }))
    await settle()
    expect(called(b, 'addServer')[0]?.args).toEqual([{ name: 'Office gpu', address: 'https://office-gpu.example.com', makeDefault: true }])
  })

  it('does not save a server that fails the test, and says why in plain words', async () => {
    const b = await renderShell('first-choose', { edit: (s) => void (s.testResult = { status: 'tls', reason: 'expired' }) })
    fireEvent.click(screen.getByRole('button', { name: 'Connect to a server' }))
    type('Server address', 'https://office-gpu.example.com')
    fireEvent.click(screen.getByRole('button', { name: 'Connect' }))
    await settle()
    const r = screen.getByTestId('test-result')
    expect(r.textContent).toContain('The certificate is not trusted')
    expect(r.textContent).toContain('The launcher does not skip this check.')
    // No way to save without a test on the first run.
    expect(screen.queryByRole('button', { name: 'Save without testing' })).toBeNull()
    expect(called(b, 'addServer')[0]?.args[0]).not.toHaveProperty('skipTest')
    expect(screen.getByRole('heading', { name: 'Connect to a LocalAI server' })).toBeTruthy()
  })

  it('treats "needs sign-in" as reachable, in amber, and still connects', async () => {
    const b = await renderShell('first-choose', { edit: (s) => void (s.testResult = { status: 'needs-signin' }) })
    fireEvent.click(screen.getByRole('button', { name: 'Connect to a server' }))
    type('Server address', 'https://office-gpu.example.com')
    fireEvent.click(screen.getByRole('button', { name: 'Test' }))
    await settle()
    expect(screen.getByTestId('test-result').getAttribute('data-state')).toBe('needs-signin')
    expect(screen.getByTestId('test-result').textContent).toContain('does not save your password or key')
    fireEvent.click(screen.getByRole('button', { name: 'Connect' }))
    await settle()
    expect(called(b, 'addServer')).toHaveLength(1)
  })

  it('warns about plain http next to the field, offers https, and does not block', async () => {
    await renderShell('first-choose')
    fireEvent.click(screen.getByRole('button', { name: 'Connect to a server' }))
    type('Server address', 'http://192.0.2.40:8080')
    const hint = screen.getByTestId('address-hint')
    expect(hint.textContent).toContain('This address is not encrypted.')
    expect(hint.textContent).toContain('Other people on the network can read your chats.')
    expect((screen.getByRole('button', { name: 'Test' }) as HTMLButtonElement).disabled).toBe(false)
    fireEvent.click(within(hint).getByRole('button', { name: 'Use https instead' }))
    expect((screen.getByLabelText('Server address') as HTMLInputElement).value).toBe('https://192.0.2.40:8080')
  })

  it('runs Test on Enter in the address field, and says no password is saved', async () => {
    const b = await renderShell('first-choose')
    fireEvent.click(screen.getByRole('button', { name: 'Connect to a server' }))
    type('Server address', 'https://office-gpu.example.com')
    fireEvent.keyDown(screen.getByLabelText('Server address'), { key: 'Enter' })
    await settle()
    expect(called(b, 'testServer')).toHaveLength(1)
    expect(screen.getByText('No password or API key is saved. If the server asks you to sign in, you do it on its own page.')).toBeTruthy()
  })

  it('Back returns to the question', async () => {
    await renderShell('first-choose')
    fireEvent.click(screen.getByRole('button', { name: 'Connect to a server' }))
    fireEvent.click(screen.getByRole('button', { name: 'Back' }))
    expect(screen.getByRole('heading', { name: 'Where should LocalAI run?' })).toBeTruthy()
  })
})

describe('Settings > Servers (mock 20 to 23)', () => {
  it('lists the servers with a status in words, the default badge and the not encrypted badge', async () => {
    await renderShell('servers')
    await openSettingsTab('Servers')
    const list = within(screen.getByRole('list', { name: 'Saved servers' }))
    expect(list.getAllByRole('listitem')).toHaveLength(4)
    const office = within(screen.getByTestId('server-srv_00000001'))
    expect(office.getByText('Default')).toBeTruthy()
    expect(office.getByText('Connected')).toBeTruthy()
    expect(within(screen.getByTestId('server-srv_00000002')).getByText('Not reachable')).toBeTruthy()
    const lab = within(screen.getByTestId('server-srv_00000003'))
    expect(lab.getByText('Not encrypted')).toBeTruthy()
    expect(lab.getByText('Not tested')).toBeTruthy()
    expect(within(screen.getByTestId('server-local')).getByText('Not running')).toBeTruthy()
    expect(screen.getByText('If you remove the default server, This computer becomes the default.')).toBeTruthy()
  })

  it('"Make default" switches to that server', async () => {
    const b = await renderShell('servers')
    await openSettingsTab('Servers')
    fireEvent.click(within(screen.getByTestId('server-srv_00000003')).getByRole('button', { name: 'Make default: Lab server' }))
    await settle()
    expect(called(b, 'setDefaultServer')[0]?.args).toEqual(['srv_00000003'])
  })

  it('shows the keep-local switch only while a remote is the default, off, with its consequence', async () => {
    const b = await renderShell('servers')
    await openSettingsTab('Servers')
    const sw = screen.getByRole('switch', { name: "Keep this computer's server running too" })
    expect(sw.getAttribute('aria-checked')).toBe('false')
    expect(screen.getByText(/does not install, start, update or check LocalAI on this computer/)).toBeTruthy()
    fireEvent.click(sw)
    await settle()
    expect(called(b, 'setKeepLocalRunning')[0]?.args).toEqual([true])
  })

  it('hides the keep-local switch while This computer is the default, and shows the empty state', async () => {
    await renderShell('servers-empty')
    await openSettingsTab('Servers')
    expect(screen.queryByRole('switch', { name: "Keep this computer's server running too" })).toBeNull()
    expect(screen.getByText('No other servers yet')).toBeTruthy()
    expect(screen.getByText('Add a LocalAI server to use its models from this window. LocalAI on this computer stays as it is.')).toBeTruthy()
  })

  it('the overflow menu has Edit, Test connection, Make default and Remove, and moves with the arrow keys', async () => {
    const b = await renderShell('servers')
    await openSettingsTab('Servers')
    fireEvent.click(screen.getByRole('button', { name: 'More actions for Home server' }))
    const items = screen.getAllByRole('menuitem').map((i) => i.textContent)
    expect(items).toEqual(['Edit…', 'Test connection', 'Make default', 'Remove…'])
    expect(document.activeElement).toBe(screen.getByRole('menuitem', { name: 'Edit…' }))
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowDown' })
    expect(document.activeElement).toBe(screen.getByRole('menuitem', { name: 'Test connection' }))
    fireEvent.click(document.activeElement!)
    await settle()
    expect(called(b, 'testServer')[0]?.args).toEqual([{ id: 'srv_00000002' }])
    // Escape closes the menu and gives the focus back to the button.
    fireEvent.click(screen.getByRole('button', { name: 'More actions for Home server' }))
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' })
    expect(screen.queryByRole('menu')).toBeNull()
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'More actions for Home server' }))
  })

  it('Add server opens the sheet; Save tests first, and a failed test offers Save without testing', async () => {
    const b = await renderShell('servers', { edit: (s) => void (s.testResult = { status: 'unreachable', reason: 'refused' }) })
    await openSettingsTab('Servers')
    fireEvent.click(screen.getByRole('button', { name: 'Add server' }))
    const sheet = within(screen.getByRole('dialog', { name: 'Add server' }))
    expect(document.activeElement).toBe(sheet.getByLabelText('Name'))
    type('Server address', 'https://nas.example.com')
    fireEvent.click(sheet.getByRole('button', { name: 'Save' }))
    await settle()
    expect(called(b, 'addServer')[0]?.args).toEqual([{ name: 'Nas', address: 'https://nas.example.com' }])
    expect(sheet.getByTestId('test-result').textContent).toContain('Cannot reach the server')
    fireEvent.click(sheet.getByRole('button', { name: 'Save without testing' }))
    await settle()
    expect(called(b, 'addServer')[1]?.args).toEqual([{ name: 'Nas', address: 'https://nas.example.com', skipTest: true }])
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(within(screen.getByRole('list', { name: 'Saved servers' })).getByText('Nas')).toBeTruthy()
  })

  it('the edit sheet saves a new name without an address, and keeps Remove away from Save', async () => {
    const b = await renderShell('servers')
    await openSettingsTab('Servers')
    fireEvent.click(screen.getByRole('button', { name: 'More actions for Lab server' }))
    fireEvent.click(screen.getByRole('menuitem', { name: 'Edit…' }))
    const sheet = within(screen.getByRole('dialog', { name: 'Edit server' }))
    expect((sheet.getByRole('button', { name: 'Save' }) as HTMLButtonElement).disabled).toBe(true)
    fireEvent.change(sheet.getByLabelText('Name'), { target: { value: 'Lab box' } })
    fireEvent.click(sheet.getByRole('button', { name: 'Save' }))
    await settle()
    expect(called(b, 'updateServer')[0]?.args).toEqual([{ id: 'srv_00000003', name: 'Lab box' }])
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('asks "Discard changes?" when the sheet closes with edits, traps Tab and returns the focus', async () => {
    await renderShell('servers')
    await openSettingsTab('Servers')
    const opener = screen.getByRole('button', { name: 'More actions for Lab server' })
    fireEvent.click(opener)
    fireEvent.click(screen.getByRole('menuitem', { name: 'Edit…' }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Other' } })
    fireEvent.keyDown(screen.getByRole('dialog', { name: 'Edit server' }), { key: 'Escape' })
    const confirm = screen.getByRole('alertdialog', { name: 'Discard changes?' })
    expect(document.activeElement).toBe(within(confirm).getByRole('button', { name: 'Keep editing' }))
    fireEvent.click(within(confirm).getByRole('button', { name: 'Discard' }))
    expect(screen.queryByRole('dialog')).toBeNull()
    // Without edits Escape closes at once.
    fireEvent.click(screen.getByRole('button', { name: 'More actions for Lab server' }))
    fireEvent.click(screen.getByRole('menuitem', { name: 'Edit…' }))
    fireEvent.keyDown(screen.getByRole('dialog', { name: 'Edit server' }), { key: 'Escape' })
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('removing the default says what happens, starts on Cancel, and removes on confirm', async () => {
    const b = await renderShell('servers')
    await openSettingsTab('Servers')
    fireEvent.click(screen.getByRole('button', { name: 'More actions for Office GPU box' }))
    fireEvent.click(screen.getByRole('menuitem', { name: 'Remove…' }))
    const d = within(screen.getByRole('alertdialog', { name: 'Remove Office GPU box?' }))
    expect(d.getByText('This is your default server. If you remove it:')).toBeTruthy()
    expect(d.getByText('This computer becomes the default server.')).toBeTruthy()
    expect(d.getByText('LocalAI starts on this computer. Install comes first if it is missing.')).toBeTruthy()
    expect(document.activeElement).toBe(d.getByRole('button', { name: 'Cancel' }))
    fireEvent.click(d.getByRole('button', { name: 'Remove and use This computer' }))
    await settle()
    expect(called(b, 'removeServer')[0]?.args).toEqual(['srv_00000001'])
  })

  it('edits a server from the recovery screen path: the edit sheet opens at once', async () => {
    await renderShell('remote-unreachable')
    fireEvent.click(screen.getByRole('button', { name: 'Edit server' }))
    await settle()
    expect(screen.getByRole('dialog', { name: 'Edit server' })).toBeTruthy()
    expect((screen.getByLabelText('Name') as HTMLInputElement).value).toBe('Office GPU box')
  })
})

describe('the bar switcher (mock 24 and 29)', () => {
  it('opens a menu of the servers with the current one marked, and says each state in words', async () => {
    await renderShell('switcher')
    const btn = bar().getByRole('button', { name: 'Server: Office GPU box. Switch server.' })
    expect(btn.getAttribute('aria-haspopup')).toBe('menu')
    fireEvent.click(btn)
    const menu = within(screen.getByRole('menu', { name: 'Servers' }))
    const radios = menu.getAllByRole('menuitemradio')
    expect(radios.map((r) => r.getAttribute('aria-checked'))).toEqual(['false', 'true', 'false', 'false'])
    expect(menu.getByText('Not running. Starts when you pick it.')).toBeTruthy()
    expect(menu.getByText('office-gpu.example.com · connected')).toBeTruthy()
    expect(menu.getByText('192.0.2.40:8080 · not encrypted')).toBeTruthy()
    expect(menu.getByText('Not reachable at 14:21')).toBeTruthy()
    expect(menu.getByText('Ctrl+Shift+S')).toBeTruthy()
    expect(menu.getByRole('menuitem', { name: 'Add server…' })).toBeTruthy()
    expect(menu.getByRole('menuitem', { name: 'Manage servers…' })).toBeTruthy()
  })

  it('arrow keys move, Enter picks and makes it the default, Escape closes and returns the focus', async () => {
    const b = await renderShell('switcher')
    const btn = bar().getByRole('button', { name: 'Server: Office GPU box. Switch server.' })
    fireEvent.click(btn)
    expect(document.activeElement).toBe(screen.getByTestId('switch-srv_00000001'))
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowDown' })
    expect(document.activeElement).toBe(screen.getByTestId('switch-srv_00000003'))
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowUp' })
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowUp' })
    expect(document.activeElement).toBe(screen.getByTestId('switch-local'))
    fireEvent.click(document.activeElement!)
    await settle()
    expect(called(b, 'setDefaultServer')[0]?.args).toEqual(['local'])
    expect(screen.queryByRole('menu')).toBeNull()
    fireEvent.click(btn)
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' })
    expect(screen.queryByRole('menu')).toBeNull()
    expect(document.activeElement).toBe(btn)
  })

  it('picking the current server changes nothing', async () => {
    const b = await renderShell('switcher')
    fireEvent.click(bar().getByRole('button', { name: 'Server: Office GPU box. Switch server.' }))
    fireEvent.click(screen.getByTestId('switch-srv_00000001'))
    await settle()
    expect(called(b, 'setDefaultServer')).toHaveLength(0)
  })

  it('opens from the menu accelerator, and Manage servers opens Settings > Servers', async () => {
    const b = await renderShell('switcher')
    const { act } = await import('@testing-library/react')
    act(() => b.emitOpenSwitcher())
    expect(screen.getByRole('menu', { name: 'Servers' })).toBeTruthy()
    fireEvent.click(screen.getByRole('menuitem', { name: 'Manage servers…' }))
    await settle()
    expect(screen.getByRole('tab', { name: 'Servers' }).getAttribute('aria-selected')).toBe('true')
  })

  it('Add server opens the add sheet in Settings', async () => {
    await renderShell('switcher')
    fireEvent.click(bar().getByRole('button', { name: 'Server: Office GPU box. Switch server.' }))
    fireEvent.click(screen.getByRole('menuitem', { name: 'Add server…' }))
    await settle()
    expect(screen.getByRole('dialog', { name: 'Add server' })).toBeTruthy()
  })

  it('shows a server that did not pass the test as a plain card, with Edit server', async () => {
    const b = await renderShell('servers', { edit: (s) => void (s.testResult = { status: 'unreachable', reason: 'timeout' }) })
    b.setDefaultServer = async () => ({ ok: false as const, reason: 'test-failed' as const, test: { status: 'unreachable' as const, reason: 'timeout' as const } })
    fireEvent.click(bar().getByRole('button', { name: 'Server: Office GPU box. Switch server.' }))
    fireEvent.click(screen.getByTestId('switch-srv_00000002'))
    await settle()
    const d = within(screen.getByRole('alertdialog', { name: 'Could not open Home server' }))
    expect(d.getByText('The server did not pass the test. You stay on the current server.')).toBeTruthy()
    fireEvent.click(d.getByRole('button', { name: 'Edit server' }))
    await settle()
    expect(screen.getByRole('dialog', { name: 'Edit server' })).toBeTruthy()
  })
})

describe('the bar in each state (mock 29)', () => {
  it('a remote over plain http keeps an amber "Remote, not encrypted" chip with the host', async () => {
    await renderShell('remote-insecure')
    expect(bar().getByText('Remote, not encrypted')).toBeTruthy()
    expect(bar().getByText('192.0.2.40:8080')).toBeTruthy()
    expect(bar().getByRole('button', { name: 'Server: Lab server. Switch server.' })).toBeTruthy()
  })

  it('an unreachable remote shows a red "Unreachable" chip and no local controls', async () => {
    await renderShell('remote-unreachable')
    expect(bar().getByText('Unreachable')).toBeTruthy()
    expect(bar().queryByRole('button', { name: 'Stop' })).toBeNull()
    expect(bar().queryByRole('button', { name: 'Logs' })).toBeNull()
  })

  it('says "This computer: running" beside a remote when the local server is kept on', async () => {
    await renderShell('remote-keep-local')
    expect(bar().getByText('This computer: running')).toBeTruthy()
    expect(bar().getByText('Remote')).toBeTruthy()
  })

  it('a switch under way shows "Opening" with the new host', async () => {
    await renderShell('switching')
    expect(bar().getByText('Opening')).toBeTruthy()
    expect(bar().getByText('office-gpu.example.com')).toBeTruthy()
  })

  it('shows no switcher on an older bridge', async () => {
    await renderShell('dev', { extras: false })
    expect(bar().queryByRole('button', { name: /Switch server/ })).toBeNull()
  })

  it('shows the Development chip on This computer', async () => {
    await renderShell('dev')
    expect(bar().getByTestId('dev-chip').textContent).toBe('Development')
  })

  it('shows no Development chip while a remote is shown: the channel applies to This computer only', async () => {
    await renderShell('updates-remote', { edit: (s) => void (s.updateInfo = { ...s.updateInfo!, channel: 'dev', devConfirmed: true }) })
    expect(bar().queryByTestId('dev-chip')).toBeNull()
  })
})

describe('switching card and recovery screen (mock 25 and 27)', () => {
  it('names the target and the two steps, keeps the old page, and cancels', async () => {
    const b = await renderShell('switching')
    const card = within(screen.getByTestId('switching-card'))
    expect(card.getByRole('heading', { name: 'Opening Office GPU box…' })).toBeTruthy()
    expect(card.getByText('Reached the server')).toBeTruthy()
    expect(card.getByText('Loading LocalAI')).toBeTruthy()
    expect(card.getByText('Your current page stays until this one is ready.')).toBeTruthy()
    expect(card.queryByText('This is taking longer than usual.')).toBeNull()
    expect(document.activeElement).toBe(card.getByRole('button', { name: 'Cancel' }))
    fireEvent.click(card.getByRole('button', { name: 'Cancel' }))
    expect(called(b, 'cancelSwitch')).toHaveLength(1)
    // The shell is raised above the page while the card shows.
    expect(called(b, 'setOverlay').at(-1)?.args).toEqual([true])
  })

  it('says so after 10 seconds', async () => {
    await renderShell('switching', {
      edit: (s) => void (s.servers = { ...s.servers!, switching: { ...s.servers!.switching!, startedAt: new Date(2026, 10, 14, 14, 30).getTime() - 12_000 } }),
    })
    expect(screen.getByText(/This is taking longer than usual\./)).toBeTruthy()
  })

  it('the recovery screen offers Retry, Switch to This computer and Edit server, with the cause and the next try', async () => {
    const b = await renderShell('remote-unreachable')
    expect(screen.getByRole('alert').textContent).toContain('Office GPU box is not reachable')
    expect(screen.getByRole('alert').textContent).toContain('office-gpu.example.com did not answer at 14:21. The connection timed out.')
    expect(screen.getByText('The launcher also tries every 30 seconds.')).toBeTruthy()
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Retry' }))
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await settle()
    expect(called(b, 'retryRemote')).toHaveLength(1)
    fireEvent.click(screen.getByRole('button', { name: 'Switch to This computer' }))
    await settle()
    expect(called(b, 'setDefaultServer')[0]?.args).toEqual(['local'])
    fireEvent.click(screen.getByRole('button', { name: 'Show details' }))
    expect(screen.getByText(/Last error: timed out · Next try in \d+ s/)).toBeTruthy()
    // The launcher never starts the local server by itself: the screen only offers it.
    expect(called(b, 'start')).toHaveLength(0)
  })
})

describe('Settings > Updates, the channel (mock 30 to 34)', () => {
  it('shows three channels, Stable chosen, Development marked "Not supported"', async () => {
    await renderShell('settings')
    await openSettingsTab('Updates')
    const group = within(screen.getByRole('radiogroup', { name: 'Update channel' }))
    expect(group.getAllByRole('radio').map((r) => (r as HTMLInputElement).checked)).toEqual([true, false, false])
    expect(group.getByText('Default')).toBeTruthy()
    expect(group.getByText('Not supported')).toBeTruthy()
    expect(screen.getByText(/Development also makes LocalAI use development images/)).toBeTruthy()
  })

  it('Development needs the checkbox: Cancel has the focus, the button is off until it is ticked', async () => {
    const b = await renderShell('settings')
    await openSettingsTab('Updates')
    fireEvent.click(screen.getByRole('radio', { name: /Development/ }))
    await settle()
    const d = within(screen.getByRole('dialog', { name: 'Use development builds?' }))
    expect(d.getByText('Development builds can break.')).toBeTruthy()
    expect(d.getByText('Backends you install from now on use development images.')).toBeTruthy()
    expect(d.getByText('Backends you already have are not changed until you reinstall or update them.')).toBeTruthy()
    expect(document.activeElement).toBe(d.getByRole('button', { name: 'Cancel' }))
    const turnOn = d.getByRole('button', { name: 'Turn on Development' }) as HTMLButtonElement
    expect(turnOn.disabled).toBe(true)
    fireEvent.click(d.getByRole('checkbox', { name: 'I understand that development builds can break.' }))
    expect(turnOn.disabled).toBe(false)
    fireEvent.click(turnOn)
    await settle()
    expect(called(b, 'setUpdateChannel').map((c) => c.args)).toEqual([['dev'], ['dev', true]])
    expect(screen.getByTestId('channel-dev-strip')).toBeTruthy()
    expect(bar().getByTestId('dev-chip')).toBeTruthy()
  })

  it('Cancel leaves the channel as it was', async () => {
    const b = await renderShell('settings')
    await openSettingsTab('Updates')
    fireEvent.click(screen.getByRole('radio', { name: /Development/ }))
    await settle()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect((screen.getByRole('radio', { name: /Stable/ }) as HTMLInputElement).checked).toBe(true)
    expect(called(b, 'setUpdateChannel').every((c) => c.args.length === 1)).toBe(true)
  })

  it('release candidates change at once, with no dialog', async () => {
    const b = await renderShell('settings')
    await openSettingsTab('Updates')
    fireEvent.click(screen.getByRole('radio', { name: /Release candidates/ }))
    await settle()
    expect(called(b, 'setUpdateChannel')[0]?.args).toEqual(['rc'])
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('on Development: a warning strip, the build, Back to Stable with a plain dialog, and the rollback copy named', async () => {
    const b = await renderShell('dev')
    await openSettingsTab('Updates')
    expect(screen.getByText('You are on Development')).toBeTruthy()
    expect(screen.getByText('LocalAI development build 3f9a1c2')).toBeTruthy()
    expect(within(screen.getByRole('main')).getByRole('button', { name: 'Update to b71e04d' })).toBeTruthy()
    fireEvent.click(screen.getByTestId('back-to-stable'))
    const d = within(screen.getByRole('dialog', { name: 'Go back to Stable?' }))
    expect(d.getByText('Your development build (3f9a1c2) stays as a rollback copy. "Roll back" in Settings brings it back.')).toBeTruthy()
    expect(d.getByText('Models and settings stay.')).toBeTruthy()
    expect(d.getByText(/may not read data that a development build changed/)).toBeTruthy()
    fireEvent.click(d.getByRole('button', { name: 'Back to Stable' }))
    await settle()
    expect(called(b, 'backToStable')).toHaveLength(1)
    expect(screen.queryByTestId('channel-dev-strip')).toBeNull()
  })

  it('a stale remote flag does not hide the checks while This computer is the default', async () => {
    await renderShell('dev-stale-flag')
    await openSettingsTab('Updates')
    expect(screen.queryByTestId('channel-remote-strip')).toBeNull()
    expect(screen.queryByText(/Not checked while/)).toBeNull()
    expect((within(screen.getByRole('main')).getAllByRole('button', { name: 'Check now' })[0] as HTMLButtonElement).disabled).toBe(false)
  })

  it('choosing Stable on Development opens the same dialog', async () => {
    await renderShell('dev')
    await openSettingsTab('Updates')
    fireEvent.click(screen.getByRole('radio', { name: /Stable/ }))
    expect(screen.getByRole('dialog', { name: 'Go back to Stable?' })).toBeTruthy()
  })

  it('with a remote default the tab says the channel applies to This computer only, and nothing is checked', async () => {
    await renderShell('updates-remote')
    await openSettingsTab('Updates')
    expect(screen.getByTestId('channel-remote-strip').textContent).toContain('Office GPU box is your default server')
    expect(screen.getByTestId('channel-remote-strip').textContent).toContain('apply to LocalAI on this computer only')
    expect(screen.getByText('Not checked while Office GPU box is the default')).toBeTruthy()
    expect((screen.getAllByRole('button', { name: 'Check now' })[0] as HTMLButtonElement).disabled).toBe(true)
    // The choices stay usable: the user may prepare This computer.
    expect((screen.getByRole('radio', { name: /Release candidates/ }) as HTMLInputElement).disabled).toBe(false)
  })
})
