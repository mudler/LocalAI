// @vitest-environment jsdom
import { fireEvent, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { called, renderShell, screen, settle } from './render'

const bar = () => within(screen.getByRole('contentinfo', { name: 'LocalAI status bar' }))

describe('status bar', () => {
  it('shows the state, the address and the verbs of a ready server', async () => {
    const b = await renderShell('ready')
    expect(bar().getByText('Ready')).toBeTruthy()
    expect(bar().getByText('127.0.0.1:8080')).toBeTruthy()
    fireEvent.click(bar().getByRole('button', { name: 'Stop' }))
    fireEvent.click(bar().getByRole('button', { name: 'Restart' }))
    expect(called(b, 'stop')).toHaveLength(1)
    expect(called(b, 'restart')).toHaveLength(1)
    expect(bar().queryByRole('button', { name: /Update to/ })).toBeNull()
    expect(bar().queryByRole('button', { name: /Sandbox off/ })).toBeNull()
  })

  it('announces the state politely', async () => {
    await renderShell('ready')
    expect(screen.getByTestId('live').getAttribute('aria-live')).toBe('polite')
    expect(screen.getByTestId('live').textContent).toBe('LocalAI Ready, 127.0.0.1:8080')
  })

  it('reports the first state to the smoke test hook', async () => {
    const b = await renderShell('ready')
    await waitFor(() => expect(called(b, 'firstRender')[0]?.args).toEqual(['Ready']))
  })

  it('shows only the menu on a fresh install, and Logs while installing', async () => {
    await renderShell('install')
    expect(bar().getByText('Not installed')).toBeTruthy()
    expect(bar().queryByRole('button', { name: 'Logs' })).toBeNull()
    expect(bar().queryByRole('button', { name: /Update to/ })).toBeNull()
    expect(bar().getByRole('button', { name: 'Menu' })).toBeTruthy()
  })

  it('offers the server update in the bar and in a banner that can be closed', async () => {
    const b = await renderShell('ready-update')
    fireEvent.click(bar().getByRole('button', { name: 'Update to 4.2.1' }))
    expect(called(b, 'installServer')[0]?.args).toEqual([{ stopFirst: true }])
    const banner = screen.getByTestId('update-banner')
    expect(banner.textContent).toContain('LocalAI 4.2.1 is available.')
    expect(banner.textContent).toContain('You have 4.1.3.')
    fireEvent.click(within(banner).getByRole('button', { name: 'Release notes' }))
    expect(called(b, 'openReleaseNotes')[0]?.args).toEqual(['4.2.1'])
    fireEvent.click(within(banner).getByRole('button', { name: 'Close' }))
    expect(screen.queryByTestId('update-banner')).toBeNull()
    expect(bar().getByRole('button', { name: 'Update to 4.2.1' })).toBeTruthy()
  })

  it('offers "Restart to update" for a downloaded launcher update', async () => {
    const b = await renderShell('ready', { edit: (s) => void (s.updater = { phase: 'downloaded', version: '0.2.0' }) })
    fireEvent.click(bar().getByRole('button', { name: 'Restart to update' }))
    expect(called(b, 'installLauncherUpdate')).toHaveLength(1)
  })

  it('shows the remote host and the switcher, without Stop, Restart and Logs', async () => {
    const b = await renderShell('remote')
    expect(bar().getByText('Remote')).toBeTruthy()
    expect(bar().getByText('gpu-box.example.test')).toBeTruthy()
    expect(bar().getByRole('button', { name: 'Server: Office GPU box. Switch server.' })).toBeTruthy()
    expect(bar().queryByRole('button', { name: 'Stop' })).toBeNull()
    expect(bar().queryByRole('button', { name: 'Logs' })).toBeNull()
    expect(bar().queryByRole('button', { name: 'Back to this computer' })).toBeNull()
    expect(called(b, 'backToLocal')).toHaveLength(0)
  })

  it('shows the worker chip with the server it joined', async () => {
    await renderShell('worker-running')
    expect(bar().getByText('Worker')).toBeTruthy()
    expect(bar().getByText('coord.example.test')).toBeTruthy()
  })

  it('shows a stopped banner over the WebUI with the one Start button', async () => {
    const b = await renderShell('server-stopped')
    expect(bar().getByText('Stopped')).toBeTruthy()
    expect(bar().queryByRole('button', { name: 'Start' })).toBeNull()
    const banner = screen.getByTestId('stopped-banner')
    fireEvent.click(within(banner).getByRole('button', { name: 'Start' }))
    expect(called(b, 'start')).toHaveLength(1)
    expect(banner.getAttribute('data-banner')).toBe('stopped')
  })

  it('shows a starting banner without a button while the server starts under the kept page', async () => {
    await renderShell('server-stopped', { edit: (s) => void ((s.server = { ...s.server, state: 'starting' }), (s.webui = { ...s.webui!, banner: 'starting' })) })
    const banner = screen.getByTestId('stopped-banner')
    expect(banner.textContent).toContain('LocalAI is starting.')
    expect(within(banner).queryByRole('button')).toBeNull()
  })

  it('raises the shell only while a popover is open, and reports the strips around the WebUI view', async () => {
    const b = await renderShell('ready')
    expect(called(b, 'setOverlay').at(-1)?.args).toEqual([false])
    // jsdom has no layout, so the strips measure 0; what matters is that they are sent once per change.
    expect(called(b, 'setInsets').map((c) => c.args[0])).toEqual([{ top: 0, bottom: 0 }])
    fireEvent.click(bar().getByRole('button', { name: 'Menu' }))
    expect(called(b, 'setOverlay').at(-1)?.args).toEqual([true])
    fireEvent.mouseDown(document.body)
    await settle()
    expect(called(b, 'setOverlay').at(-1)?.args).toEqual([false])
  })
})

describe('bar menu', () => {
  it('opens upward, moves with the arrow keys and closes with Escape on the trigger', async () => {
    await renderShell('ready')
    const trigger = bar().getByRole('button', { name: 'Menu' })
    fireEvent.click(trigger)
    expect(trigger.getAttribute('aria-expanded')).toBe('true')
    const items = screen.getAllByRole('menuitem')
    expect(items.map((i) => i.textContent)).toEqual(['Settings', 'Connect', 'Open in browser', 'About LocalAI4.1.3'])
    expect(document.activeElement).toBe(items[0])
    fireEvent.keyDown(items[0], { key: 'ArrowDown' })
    expect(document.activeElement).toBe(items[1])
    fireEvent.keyDown(items[1], { key: 'ArrowUp' })
    fireEvent.keyDown(items[0], { key: 'ArrowUp' })
    expect(document.activeElement).toBe(items[3])
    fireEvent.keyDown(items[3], { key: 'Home' })
    expect(document.activeElement).toBe(items[0])
    fireEvent.keyDown(items[0], { key: 'Escape' })
    expect(screen.queryByRole('menu')).toBeNull()
    expect(document.activeElement).toBe(trigger)
  })

  it('opens from the keyboard with an arrow key', async () => {
    await renderShell('ready')
    fireEvent.keyDown(bar().getByRole('button', { name: 'Menu' }), { key: 'ArrowUp' })
    expect(screen.getByRole('menu')).toBeTruthy()
  })

  it('closes on a click outside', async () => {
    await renderShell('ready')
    fireEvent.click(bar().getByRole('button', { name: 'Menu' }))
    fireEvent.mouseDown(document.body)
    expect(screen.queryByRole('menu')).toBeNull()
  })

  it('opens Settings, Connect, the browser and About', async () => {
    const b = await renderShell('ready')
    fireEvent.click(bar().getByRole('button', { name: 'Menu' }))
    fireEvent.click(screen.getByRole('menuitem', { name: 'Open in browser' }))
    expect(called(b, 'openWebuiInBrowser')).toHaveLength(1)
    fireEvent.click(bar().getByRole('button', { name: 'Menu' }))
    fireEvent.click(screen.getByRole('menuitem', { name: /About LocalAI/ }))
    const about = screen.getByRole('dialog', { name: 'About LocalAI' })
    expect(about.textContent).toContain('Launcher4.1.3')
    fireEvent.click(within(about).getByRole('button', { name: 'Close' }))
    fireEvent.click(bar().getByRole('button', { name: 'Menu' }))
    fireEvent.click(screen.getByRole('menuitem', { name: 'Connect' }))
    expect(screen.getByRole('heading', { name: 'Connect' })).toBeTruthy()
  })

  it('About shows the launcher and the server version as two numbers, also before any update check', async () => {
    await renderShell('ready', {
      edit: (s) => {
        s.info = { ...s.info, version: '0.1.0', packaged: false }
        s.install = { kind: 'idle' }
        s.installDetails = { ...s.installDetails, version: '4.2.0' }
      },
    })
    fireEvent.click(bar().getByRole('button', { name: 'Menu' }))
    fireEvent.click(screen.getByRole('menuitem', { name: /About LocalAI/ }))
    const about = screen.getByRole('dialog', { name: 'About LocalAI' })
    expect(about.textContent).toContain('LocalAI server4.2.0')
    expect(about.textContent).toContain('Launcher0.1.0')
    expect(about.textContent).not.toMatch(/unknown/i)
  })

  it('has no "Back to this computer" item: the switcher does that', async () => {
    await renderShell('remote')
    fireEvent.click(bar().getByRole('button', { name: 'Menu' }))
    expect(screen.getAllByRole('menuitem')[0].textContent).toBe('Settings')
    expect(screen.queryByRole('menuitem', { name: 'Back to this computer' })).toBeNull()
  })

  it('hides "Open in browser" when the bridge cannot do it', async () => {
    await renderShell('ready', { extras: false })
    fireEvent.click(bar().getByRole('button', { name: 'Menu' }))
    expect(screen.queryByRole('menuitem', { name: 'Open in browser' })).toBeNull()
  })
})

describe('hidden bar', () => {
  it('over the WebUI leaves the dot to the main process', async () => {
    await renderShell('bar-hidden')
    expect(screen.queryByRole('contentinfo')).toBeNull()
    expect(screen.queryByTestId('corner-dot')).toBeNull()
  })

  it('over a shell screen leaves a corner dot that brings the bar back', async () => {
    const b = await renderShell('stopped', { edit: (s) => void ((s.statusBar = false), (s.webui = undefined)) })
    expect(screen.queryByRole('contentinfo')).toBeNull()
    const dot = screen.getByTestId('corner-dot')
    expect(dot.getAttribute('aria-label')).toBe('LocalAI: Stopped. Show status bar')
    expect(dot.getAttribute('data-tone')).toBe('idle')
    fireEvent.click(dot)
    await settle()
    expect(called(b, 'setStatusBarVisible')[0]?.args).toEqual([true])
    expect(screen.getByRole('contentinfo')).toBeTruthy()
  })

  it('comes back by itself when the server fails, and goes away again when it recovers', async () => {
    const b = await renderShell('bar-hidden-failed')
    expect(screen.getByRole('contentinfo')).toBeTruthy()
    expect(screen.queryByTestId('corner-dot')).toBeNull()
    b.emitServer({ state: 'ready', endpoint: 'http://127.0.0.1:8080', logTail: [] })
    await settle()
    expect(screen.queryByRole('contentinfo')).toBeNull()
    // The WebUI is on show again; the main process draws the dot over it.
    expect(screen.queryByTestId('corner-dot')).toBeNull()
  })

  it('comes back for a remote origin and for an unseen sandbox notice', async () => {
    await renderShell('remote', { edit: (s) => void (s.statusBar = false) })
    expect(screen.getByRole('contentinfo')).toBeTruthy()
    cleanupAndCheck()
    await renderShell('sandbox-off', { edit: (s) => void (s.statusBar = false) })
    expect(screen.getByRole('contentinfo')).toBeTruthy()
  })

  it('follows the View menu and the tray through the status bar event', async () => {
    const b = await renderShell('ready')
    b.emitStatusBar(false)
    await settle()
    expect(screen.queryByRole('contentinfo')).toBeNull()
    b.emitStatusBar(true)
    await settle()
    expect(screen.getByRole('contentinfo')).toBeTruthy()
  })

  it('colours the dot amber while starting and grey when stopped', async () => {
    const b = await renderShell('bar-hidden')
    b.emitServer({ state: 'starting', endpoint: 'http://127.0.0.1:8080', logTail: [] })
    await settle()
    expect(screen.getByTestId('corner-dot').getAttribute('data-tone')).toBe('warn')
    b.emitServer({ state: 'stopped', endpoint: 'http://127.0.0.1:8080', logTail: [] })
    await settle()
    expect(screen.getByTestId('corner-dot').getAttribute('data-tone')).toBe('idle')
  })
})

function cleanupAndCheck() {
  // Two shells in one test: unmount the first before the second.
  document.body.innerHTML = ''
}
