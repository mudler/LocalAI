import { describe, expect, it } from 'vitest'
import { WebuiModel, type WebuiInputs } from '../src/main/webuiState'

const LOCAL = 'http://127.0.0.1:8080'
const REMOTE = 'https://remote.example.test'

const inputs = (o: Partial<WebuiInputs> = {}): WebuiInputs => ({
  server: 'stopped',
  localOrigin: LOCAL,
  installActive: false,
  statusBar: true,
  updateReady: false,
  sandboxOff: false,
  sandboxNoticeShown: false,
  openInBrowser: false,
  workerMode: false,
  serverUpdate: false,
  startedHidden: false,
  ...o,
})

function ready(m = new WebuiModel(inputs())) {
  const fx = m.dispatch({ type: 'inputs', patch: { server: 'ready' } })
  m.dispatch({ type: 'view-loaded' })
  return { m, fx }
}

describe('WebuiModel: local server', () => {
  it('shows the shell only until the server is ready, then loads /app once', () => {
    const m = new WebuiModel(inputs())
    expect(m.state().layout).toBe('shell-only')
    expect(m.dispatch({ type: 'inputs', patch: { server: 'starting' } })).toEqual([])
    expect(m.state().layout).toBe('shell-only')
    expect(m.dispatch({ type: 'inputs', patch: { server: 'ready' } })).toEqual([{ kind: 'show', origin: LOCAL, url: `${LOCAL}/app` }])
    expect(m.state().layout).toBe('webui')
    expect(m.state().loaded).toBe(false)
    m.dispatch({ type: 'view-loaded' })
    expect(m.state().loaded).toBe(true)
    // More ready events never reload.
    expect(m.dispatch({ type: 'inputs', patch: { server: 'ready' } })).toEqual([])
  })

  it('keeps the view alive and never reloads it across shell screens', () => {
    const { m } = ready()
    for (const screen of ['logs', 'settings', 'connect'] as const) {
      expect(m.dispatch({ type: 'screen', screen })).toEqual([])
      expect(m.state().layout).toBe('shell-only')
      expect(m.state().screen).toBe(screen)
      expect(m.state().loaded).toBe(true)
    }
    expect(m.dispatch({ type: 'screen', screen: null })).toEqual([])
    expect(m.state().layout).toBe('webui')
  })

  it('install or update covers the view with the shell, without closing it', () => {
    const { m } = ready()
    expect(m.dispatch({ type: 'inputs', patch: { installActive: true, server: 'stopping' } })).toEqual([])
    expect(m.state().layout).toBe('shell-only')
    expect(m.dispatch({ type: 'inputs', patch: { installActive: false, server: 'ready' } })).toEqual([])
    expect(m.state().layout).toBe('webui')
  })

  it('shows a banner when the server stops, keeps the page and does not raise the shell', () => {
    const { m } = ready()
    expect(m.dispatch({ type: 'inputs', patch: { server: 'stopping' } })).toEqual([])
    expect(m.state()).toMatchObject({ layout: 'webui', home: 'webui', banner: 'stopped', shellAbove: false, loaded: true })
    m.dispatch({ type: 'inputs', patch: { server: 'stopped' } })
    expect(m.state().banner).toBe('stopped')
    m.dispatch({ type: 'inputs', patch: { server: 'starting' } })
    expect(m.state().banner).toBe('starting')
    expect(m.dispatch({ type: 'inputs', patch: { server: 'ready' } })).toEqual([])
    expect(m.state()).toMatchObject({ banner: null, shellAbove: false })
  })

  it('a failed server covers the kept page with the shell (start-failed screen) and forces the bar', () => {
    const { m } = ready(new WebuiModel(inputs({ statusBar: false })))
    expect(m.dispatch({ type: 'inputs', patch: { server: 'failed' } })).toEqual([])
    expect(m.state()).toMatchObject({ layout: 'shell-only', home: 'shell-only', banner: null, barVisible: true, barForced: true, dot: null, loaded: true })
    // Started again: the same page comes back, not reloaded.
    m.dispatch({ type: 'inputs', patch: { server: 'starting' } })
    expect(m.state()).toMatchObject({ layout: 'webui', banner: 'starting' })
    expect(m.dispatch({ type: 'inputs', patch: { server: 'ready' } })).toEqual([])
    expect(m.state()).toMatchObject({ layout: 'webui', banner: null })
  })

  it('home ignores the shell screen, layout does not', () => {
    const { m } = ready()
    m.dispatch({ type: 'screen', screen: 'logs' })
    expect(m.state()).toMatchObject({ layout: 'shell-only', home: 'webui' })
    const fresh = new WebuiModel(inputs())
    fresh.dispatch({ type: 'screen', screen: 'settings' })
    expect(fresh.state()).toMatchObject({ layout: 'shell-only', home: 'shell-only' })
  })

  it('keeps the insets the shell reports', () => {
    const { m } = ready()
    expect(m.state().insets).toEqual({ top: 0, bottom: 0 })
    expect(m.dispatch({ type: 'insets', insets: { top: 52, bottom: 0 } })).toEqual([])
    expect(m.state().insets).toEqual({ top: 52, bottom: 0 })
  })

  it('a new local address closes the old view and opens the new origin when ready', () => {
    const { m } = ready()
    const other = 'http://127.0.0.1:9090'
    expect(m.dispatch({ type: 'inputs', patch: { localOrigin: other, server: 'stopped' } })).toEqual([{ kind: 'close' }])
    expect(m.dispatch({ type: 'inputs', patch: { server: 'ready' } })).toEqual([{ kind: 'show', origin: other, url: `${other}/app` }])
  })

  it('reloads a view whose load failed once the server is ready again', () => {
    const m = new WebuiModel(inputs())
    m.dispatch({ type: 'inputs', patch: { server: 'ready' } })
    m.dispatch({ type: 'view-load-failed', code: -102, description: 'ERR_CONNECTION_REFUSED' })
    expect(m.state()).toMatchObject({ layout: 'shell-only', problem: { kind: 'load-failed', code: -102 } })
    m.dispatch({ type: 'inputs', patch: { server: 'stopped' } })
    expect(m.dispatch({ type: 'inputs', patch: { server: 'ready' } })).toEqual([{ kind: 'reload' }])
    m.dispatch({ type: 'view-loaded' })
    expect(m.state()).toMatchObject({ layout: 'webui', problem: null })
  })
})

describe('WebuiModel: deep links', () => {
  it('navigates the existing view, closing a shell screen', () => {
    const { m } = ready()
    m.dispatch({ type: 'screen', screen: 'logs' })
    const url = `${LOCAL}/app?model=qwen`
    expect(m.dispatch({ type: 'deep-link', url })).toEqual([{ kind: 'navigate', url }])
    expect(m.state()).toMatchObject({ screen: null, layout: 'webui' })
  })

  it('opens the browser when the WebUI is not embedded', () => {
    const m = new WebuiModel(inputs({ openInBrowser: true, server: 'ready' }))
    const url = `${LOCAL}/app?model=x`
    expect(m.dispatch({ type: 'deep-link', url })).toEqual([{ kind: 'open-browser', url }])
  })

  it('opens a page of another origin in the browser, never in the local view', () => {
    const { m } = ready()
    const url = `${REMOTE}/app/chats/1`
    expect(m.dispatch({ type: 'deep-link', url })).toEqual([{ kind: 'open-browser', url }])
  })

  it('navigates the view of the remote server on show for a page of that server only', () => {
    const { m } = ready()
    m.dispatch({ type: 'connect', origin: REMOTE, host: 'remote.example.test' })
    m.dispatch({ type: 'view-loaded' })
    const url = `${REMOTE}/app/chats/1`
    expect(m.dispatch({ type: 'deep-link', url })).toContainEqual({ kind: 'navigate', url })
    const other = `${LOCAL}/app/chats/1`
    expect(m.dispatch({ type: 'deep-link', url: other })).toEqual([{ kind: 'open-browser', url: other }])
  })

  it('does nothing while the server is not ready and there is no view', () => {
    const m = new WebuiModel(inputs())
    expect(m.dispatch({ type: 'deep-link', url: `${LOCAL}/app?model=x` })).toEqual([])
  })
})

describe('WebuiModel: remote servers', () => {
  it('connect closes the local view and opens the remote one; back to local reverses it', () => {
    const { m } = ready()
    m.dispatch({ type: 'screen', screen: 'connect' })
    expect(m.dispatch({ type: 'connect', origin: REMOTE, host: 'remote.example.test' })).toEqual([
      { kind: 'close' },
      { kind: 'show', origin: REMOTE, url: `${REMOTE}/app` },
    ])
    expect(m.state()).toMatchObject({
      target: { kind: 'remote', origin: REMOTE, host: 'remote.example.test' },
      layout: 'webui',
      screen: null,
      barVisible: true,
    })
    expect(m.dispatch({ type: 'back-to-local' })).toEqual([{ kind: 'close' }, { kind: 'show', origin: LOCAL, url: `${LOCAL}/app` }])
    expect(m.state().target).toEqual({ kind: 'local', origin: LOCAL })
  })

  it('a remote view does not depend on the local server, and has no banner', () => {
    const m = new WebuiModel(inputs({ server: 'stopped' }))
    expect(m.dispatch({ type: 'connect', origin: REMOTE, host: 'remote.example.test' })).toEqual([
      { kind: 'show', origin: REMOTE, url: `${REMOTE}/app` },
    ])
    m.dispatch({ type: 'view-loaded' })
    expect(m.state()).toMatchObject({ layout: 'webui', banner: null })
    expect(m.dispatch({ type: 'back-to-local' })).toEqual([{ kind: 'close' }])
    expect(m.state().layout).toBe('shell-only')
  })

  it('the remote origin forces the bar so the user sees where pages come from', () => {
    const m = new WebuiModel(inputs({ statusBar: false }))
    m.dispatch({ type: 'connect', origin: REMOTE, host: 'remote.example.test' })
    expect(m.state()).toMatchObject({ barVisible: true, barForced: true, dot: null })
  })

  it('open in browser uses the current page only on the shown origin', () => {
    const { m } = ready()
    expect(m.dispatch({ type: 'open-in-browser', currentUrl: `${LOCAL}/app/chat/1` })).toEqual([
      { kind: 'open-browser', url: `${LOCAL}/app/chat/1` },
    ])
    expect(m.dispatch({ type: 'open-in-browser', currentUrl: 'https://evil.example.test/' })).toEqual([
      { kind: 'open-browser', url: `${LOCAL}/app` },
    ])
    expect(m.dispatch({ type: 'open-in-browser', currentUrl: 'not a url' })).toEqual([{ kind: 'open-browser', url: `${LOCAL}/app` }])
    expect(m.dispatch({ type: 'open-in-browser' })).toEqual([{ kind: 'open-browser', url: `${LOCAL}/app` }])
  })
})

describe('WebuiModel: browser placement and crashes', () => {
  it('with "open in browser" there is no view; the browser opens once when the server is ready', () => {
    const m = new WebuiModel(inputs({ openInBrowser: true }))
    expect(m.dispatch({ type: 'inputs', patch: { server: 'ready' } })).toEqual([{ kind: 'open-browser', url: `${LOCAL}/app` }])
    expect(m.state()).toMatchObject({ placement: 'browser', layout: 'shell-only' })
    m.dispatch({ type: 'inputs', patch: { server: 'stopped' } })
    expect(m.dispatch({ type: 'inputs', patch: { server: 'ready' } })).toEqual([])
  })

  it('after a hidden start the browser does not open by itself, only on a user action', () => {
    const m = new WebuiModel(inputs({ openInBrowser: true, startedHidden: true }))
    expect(m.dispatch({ type: 'inputs', patch: { server: 'ready' } })).toEqual([])
    expect(m.dispatch({ type: 'open-in-browser' })).toEqual([{ kind: 'open-browser', url: `${LOCAL}/app` }])
  })

  it('switching the setting at run time closes or opens the view', () => {
    const { m } = ready()
    expect(m.dispatch({ type: 'inputs', patch: { openInBrowser: true } })).toEqual([{ kind: 'close' }, { kind: 'open-browser', url: `${LOCAL}/app` }])
    expect(m.dispatch({ type: 'inputs', patch: { openInBrowser: false } })).toEqual([{ kind: 'show', origin: LOCAL, url: `${LOCAL}/app` }])
  })

  it('one crash offers a reload; the second falls back to the browser', () => {
    const { m } = ready()
    expect(m.dispatch({ type: 'view-crashed', reason: 'crashed' })).toEqual([])
    expect(m.state()).toMatchObject({ layout: 'shell-only', problem: { kind: 'crashed', count: 1 }, placement: 'embedded' })
    expect(m.dispatch({ type: 'reload' })).toEqual([{ kind: 'reload' }])
    m.dispatch({ type: 'view-loaded' })
    expect(m.state()).toMatchObject({ layout: 'webui', problem: null })
    expect(m.dispatch({ type: 'view-crashed', reason: 'oom' })).toEqual([{ kind: 'close' }, { kind: 'open-browser', url: `${LOCAL}/app` }])
    expect(m.state()).toMatchObject({ placement: 'browser-fallback', layout: 'shell-only', problem: null })
    // No view comes back by itself.
    expect(m.dispatch({ type: 'inputs', patch: { server: 'ready' } })).toEqual([])
    // Reload is the user asking to try the window again.
    expect(m.dispatch({ type: 'reload' })).toEqual([{ kind: 'show', origin: LOCAL, url: `${LOCAL}/app` }])
    expect(m.state().placement).toBe('embedded')
  })

  it('a hang keeps the view and forces the bar until it answers again', () => {
    const { m } = ready(new WebuiModel(inputs({ statusBar: false })))
    m.dispatch({ type: 'view-unresponsive' })
    expect(m.state()).toMatchObject({ layout: 'webui', problem: { kind: 'unresponsive' }, barVisible: true })
    m.dispatch({ type: 'view-responsive' })
    expect(m.state()).toMatchObject({ problem: null, barVisible: false, dot: 'ok' })
  })

  it('worker mode shows no WebUI', () => {
    const { m } = ready()
    expect(m.dispatch({ type: 'inputs', patch: { workerMode: true } })).toEqual([{ kind: 'close' }])
    expect(m.state().layout).toBe('shell-only')
  })
})

describe('WebuiModel: bar and dot', () => {
  it('the dot replaces the hidden bar in webui layout only', () => {
    const m = new WebuiModel(inputs({ statusBar: false }))
    expect(m.state().dot).toBeNull()
    m.dispatch({ type: 'inputs', patch: { server: 'ready' } })
    expect(m.state()).toMatchObject({ barVisible: false, dot: 'ok' })
    m.dispatch({ type: 'inputs', patch: { sandboxOff: true } })
    expect(m.state().dot).toBe('busy')
    m.dispatch({ type: 'inputs', patch: { server: 'stopped' } })
    expect(m.state().dot).toBe('stopped')
    m.dispatch({ type: 'screen', screen: 'logs' })
    expect(m.state().dot).toBeNull()
  })

  it.each([
    ['updateReady', { updateReady: true }],
    ['serverUpdate', { serverUpdate: true }],
    ['sandboxNoticeShown', { sandboxNoticeShown: true }],
    ['server failed', { server: 'failed' as const }],
  ])('%s brings a hidden bar back', (_n, patch) => {
    const m = new WebuiModel(inputs({ statusBar: false, server: 'ready' }))
    m.dispatch({ type: 'inputs', patch })
    expect(m.state()).toMatchObject({ barVisible: true, barForced: true })
  })

  it('an open popover raises the shell above the WebUI, a banner does not', () => {
    const { m } = ready()
    m.dispatch({ type: 'inputs', patch: { server: 'stopped' } })
    expect(m.state()).toMatchObject({ banner: 'stopped', shellAbove: false })
    m.dispatch({ type: 'overlay', open: true })
    expect(m.state()).toMatchObject({ overlay: true, shellAbove: true })
    m.dispatch({ type: 'overlay', open: false })
    expect(m.state().shellAbove).toBe(false)
  })
})
