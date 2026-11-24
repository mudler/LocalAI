// Security tests of the WebUI view that run under plain Node: the real IPC
// trust check against frames of the WebUI view, and the real Electron wiring
// of webuiView.ts against a fake Electron, driven with hostile input.
// scripts/webui-security.sh runs the same attempts against the built app.
import { EventEmitter } from 'node:events'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { guard, isTrustedSender, UntrustedSenderError, type FrameInfo, type TrustedShell } from '../src/main/ipcGuard'
import { partitionForOrigin } from '../src/main/webuiPolicy'

// ---- a fake Electron, just enough for webuiView.ts -----------------------------

class FakeSession extends EventEmitter {
  permissionRequest?: (...a: unknown[]) => void
  permissionCheck?: (...a: unknown[]) => boolean
  device?: () => boolean
  beforeRequest?: (details: { url: string; resourceType: string }, cb: (r: { cancel?: boolean }) => void) => void
  constructor(readonly partition: string) {
    super()
  }
  setPermissionRequestHandler(h: (...a: unknown[]) => void) {
    this.permissionRequest = h
  }
  setPermissionCheckHandler(h: (...a: unknown[]) => boolean) {
    this.permissionCheck = h
  }
  setDevicePermissionHandler(h: () => boolean) {
    this.device = h
  }
  webRequest = {
    onBeforeRequest: (_f: unknown, h: FakeSession['beforeRequest']) => {
      this.beforeRequest = h
    },
  }
}

const sessions = new Map<string, FakeSession>()
const sessionFor = (p: string) => sessions.get(p) ?? (sessions.set(p, new FakeSession(p)), sessions.get(p)!)

class FakeWebContents extends EventEmitter {
  url = ''
  destroyed = false
  windowOpen?: (d: { url: string }) => { action: string }
  loadURL = vi.fn(async (u: string) => {
    this.url = u
  })
  reload = vi.fn()
  focus = vi.fn()
  constructor(readonly session: FakeSession) {
    super()
  }
  getURL() {
    return this.url
  }
  isDestroyed() {
    return this.destroyed
  }
  close() {
    this.destroyed = true
  }
  setWindowOpenHandler(h: FakeWebContents['windowOpen']) {
    this.windowOpen = h
  }
}

class FakeView {
  static made: FakeView[] = []
  webContents: FakeWebContents
  visible = true
  constructor(readonly options: { webPreferences: Record<string, unknown> }) {
    this.webContents = new FakeWebContents(sessionFor(String(options.webPreferences.partition)))
    FakeView.made.push(this)
  }
  setVisible(v: boolean) {
    this.visible = v
  }
  setBounds() {}
  setBackgroundColor() {}
}

const dialogAnswer = { response: 0, checkboxChecked: true }
vi.mock('electron', () => ({
  WebContentsView: FakeView,
  dialog: { showMessageBox: vi.fn(async () => dialogAnswer) },
}))

const { WebuiHost, webuiWebPreferences } = await import('../src/main/webuiView')

const SERVER = 'http://127.0.0.1:8080'

function setup(store: Record<string, unknown> = {}) {
  const external: string[] = []
  const dispatched: unknown[] = []
  const remembered: unknown[] = []
  const signals: string[] = []
  const win = { isDestroyed: () => false, contentView: { addChildView: vi.fn(), removeChildView: vi.fn() } }
  const host = new WebuiHost({
    window: () => win as never,
    dispatch: (e) => dispatched.push(e),
    permissionStore: () => store as never,
    rememberPermission: async (o, c) => void remembered.push([o, c]),
    openExternal: (u) => external.push(u),
    showStatusBar: vi.fn(),
    downloadsDir: () => '/downloads',
    log: () => undefined,
    signal: (l) => signals.push(l),
  })
  host.apply([{ kind: 'show', origin: SERVER, url: `${SERVER}/app` }])
  const view = FakeView.made.at(-1)!
  const wc = view.webContents
  return { host, view, wc, ses: wc.session, external, dispatched, remembered, signals, win }
}

/** Emits a navigation event like Electron does and reports whether it was prevented. */
function navigate(wc: FakeWebContents, event: 'will-frame-navigate' | 'will-redirect', url: string, isMainFrame = true): boolean {
  let prevented = false
  wc.emit(event, { url, isMainFrame, preventDefault: () => (prevented = true) })
  return prevented
}

beforeEach(() => {
  FakeView.made = []
  sessions.clear()
})

// ---- the IPC trust check against the WebUI view ---------------------------------

describe('the WebUI view is never a trusted IPC sender', () => {
  const shell: TrustedShell = { webContentsId: 1, processId: 10, routingId: 1, pageUrl: 'file:///opt/app/dist/renderer/index.html' }
  const webuiFrame = (o: Partial<FrameInfo> = {}): FrameInfo => ({
    webContentsId: 2,
    processId: 11,
    routingId: 1,
    url: `${SERVER}/app`,
    isMainFrame: true,
    ...o,
  })

  it.each<[string, FrameInfo]>([
    ['WebUI main frame', webuiFrame()],
    ['WebUI subframe', webuiFrame({ isMainFrame: false, routingId: 5, url: 'about:srcdoc' })],
    ['WebUI frame showing the shell page URL', webuiFrame({ url: shell.pageUrl })],
    ['WebUI frame with the shell process and routing ids', webuiFrame({ processId: 10, routingId: 1, url: shell.pageUrl })],
    ['WebUI frame with a look-alike file URL', webuiFrame({ url: 'file:///opt/app/dist/renderer/index.html.evil' })],
    ['corner dot view', { webContentsId: 3, processId: 12, routingId: 1, url: 'data:text/html,x', isMainFrame: true }],
    ['shell subframe', { ...webuiFrame({ webContentsId: 1, processId: 10, routingId: 7, url: shell.pageUrl }), isMainFrame: false }],
    ['shell after navigating to the server', { webContentsId: 1, processId: 10, routingId: 1, url: `${SERVER}/app`, isMainFrame: true }],
  ])('%s is refused', (_name, frame) => {
    expect(isTrustedSender(frame, shell)).toBe(false)
    const handler = vi.fn(() => 'secret')
    const refused = vi.fn()
    const g = guard(handler, (f: FrameInfo) => f, () => shell, refused)
    expect(() => g(frame)).toThrow(UntrustedSenderError)
    expect(handler).not.toHaveBeenCalled()
    expect(refused).toHaveBeenCalledWith(frame)
  })

  it('the shell main frame itself is accepted (the check is not refusing everything)', () => {
    const frame: FrameInfo = { webContentsId: 1, processId: 10, routingId: 1, url: `${shell.pageUrl}#/logs`, isMainFrame: true }
    expect(isTrustedSender(frame, shell)).toBe(true)
  })
})

// ---- web preferences ------------------------------------------------------------

describe('web preferences of the WebUI view', () => {
  it('have no preload and every isolation switch on', () => {
    const p = webuiWebPreferences(SERVER)
    expect(p).not.toHaveProperty('preload')
    expect(p).toMatchObject({
      nodeIntegration: false,
      nodeIntegrationInSubFrames: false,
      nodeIntegrationInWorker: false,
      contextIsolation: true,
      sandbox: true,
      webSecurity: true,
      allowRunningInsecureContent: false,
      webviewTag: false,
      partition: partitionForOrigin(SERVER),
    })
    expect(p.partition!.startsWith('persist:')).toBe(true)
  })

  it('the view is made with exactly those preferences, in its origin partition', () => {
    const { view } = setup()
    expect(view.options.webPreferences).toEqual(webuiWebPreferences(SERVER))
    expect(view.webContents.loadURL).toHaveBeenCalledWith(`${SERVER}/app`)
  })

  it('a remote origin gets its own session', () => {
    const { host } = setup()
    host.apply([{ kind: 'show', origin: 'https://remote.example.test', url: 'https://remote.example.test/app' }])
    const [a, b] = FakeView.made
    expect(a.webContents.session).not.toBe(b.webContents.session)
    expect(a.webContents.destroyed).toBe(true) // the local view was closed
  })
})

// ---- navigation, windows, redirects ---------------------------------------------

describe('hostile navigation through the Electron handlers', () => {
  it.each([
    ['javascript:alert(1)', true, []],
    ['file:///etc/passwd', true, []],
    ['data:text/html,<script>alert(1)</script>', true, []],
    ['blob:http://127.0.0.1:8080/x', true, []],
    ['about:blank', true, []],
    ['chrome://gpu', true, []],
    ['custom-proto://run', true, []],
    ['http://127.0.0.1:8080@evil.example.test/', true, []],
    ['https://evil.example.test/phish', true, ['https://evil.example.test/phish']],
    ['mailto:x@example.test', true, ['mailto:x@example.test']],
    [`${SERVER}/app/chat`, false, []],
  ])('main frame to %s: prevented=%s, browser gets %j', (url, prevented, opened) => {
    const { wc, external } = setup()
    expect(navigate(wc, 'will-frame-navigate', url)).toBe(prevented)
    expect(external).toEqual(opened)
  })

  it('a cross-origin iframe is refused and never opens the browser', () => {
    const { wc, external } = setup()
    expect(navigate(wc, 'will-frame-navigate', 'https://evil.example.test/frame', false)).toBe(true)
    expect(navigate(wc, 'will-frame-navigate', 'about:srcdoc', false)).toBe(false)
    expect(external).toEqual([])
  })

  it('a redirect to another origin is stopped', () => {
    const { wc, external } = setup()
    expect(navigate(wc, 'will-redirect', 'https://evil.example.test/')).toBe(true)
    expect(external).toEqual(['https://evil.example.test/'])
    expect(navigate(wc, 'will-redirect', 'https://evil.example.test/', false)).toBe(true)
    expect(external).toHaveLength(1)
  })

  it('the session refuses documents from another origin even if an event was missed', () => {
    const { ses } = setup()
    const run = (url: string, resourceType: string) => {
      let r: { cancel?: boolean } = {}
      ses.beforeRequest!({ url, resourceType }, (x) => (r = x))
      return !!r.cancel
    }
    expect(run('https://evil.example.test/', 'mainFrame')).toBe(true)
    expect(run('https://evil.example.test/', 'subFrame')).toBe(true)
    expect(run(`${SERVER}/app`, 'mainFrame')).toBe(false)
    expect(run(`${SERVER}/api/files/a.pdf`, 'subFrame')).toBe(false)
    // Images and API calls follow the page's own rules (the gallery shows external images).
    expect(run('https://images.example.test/a.png', 'image')).toBe(false)
  })

  it('window.open never creates a window', () => {
    const { wc, external } = setup()
    for (const url of ['https://evil.example.test/', 'javascript:alert(1)', 'file:///etc/passwd', 'about:blank', `${SERVER}/swagger`, `${SERVER}/app/x`]) {
      expect(wc.windowOpen!({ url })).toEqual({ action: 'deny' })
    }
    expect(external).toEqual(['https://evil.example.test/', `${SERVER}/swagger`])
    expect(wc.loadURL).toHaveBeenLastCalledWith(`${SERVER}/app/x`)
  })

  it('a webview tag is never attached', () => {
    const { wc } = setup()
    let prevented = false
    wc.emit('will-attach-webview', { preventDefault: () => (prevented = true) })
    expect(prevented).toBe(true)
  })

  it('certificate errors are never bypassed and no client certificate is sent', () => {
    const { wc } = setup()
    const cb = vi.fn()
    wc.emit('certificate-error', { preventDefault() {} }, 'https://remote.example.test', 'net::ERR_CERT_AUTHORITY_INVALID', {}, cb, true)
    expect(cb).toHaveBeenCalledWith(false)
    const cc = vi.fn()
    wc.emit('select-client-certificate', { preventDefault() {} }, 'https://remote.example.test', [{}], cc)
    expect(cc).toHaveBeenCalledWith()
  })
})

// ---- permissions ----------------------------------------------------------------

describe('permissions through the Electron handlers', () => {
  function request(ses: FakeSession, wc: FakeWebContents, permission: string, details: Record<string, unknown> = {}) {
    return new Promise<boolean>((resolve) =>
      ses.permissionRequest!(wc, permission, resolve, { requestingUrl: `${SERVER}/app`, isMainFrame: true, ...details }),
    )
  }

  it('denies without a user action, asks after one, and remembers the answer', async () => {
    const { ses, wc, remembered } = setup()
    wc.url = `${SERVER}/app`
    expect(await request(ses, wc, 'media', { mediaTypes: ['audio'] })).toBe(false)
    wc.emit('input-event', {}, { type: 'mouseDown' })
    expect(await request(ses, wc, 'media', { mediaTypes: ['audio'] })).toBe(true) // the fake dialog answers Allow
    expect(remembered).toEqual([[SERVER, 'allow']])
  })

  it('denies everything else, and anything from another frame origin', async () => {
    const { ses, wc } = setup()
    wc.url = `${SERVER}/app`
    wc.emit('input-event', {}, { type: 'keyDown' })
    for (const p of ['clipboard-read', 'notifications', 'geolocation', 'midi', 'usb', 'serial', 'hid', 'display-capture', 'openExternal']) {
      expect(await request(ses, wc, p), p).toBe(false)
    }
    expect(await request(ses, wc, 'media', { mediaTypes: ['audio'], requestingUrl: 'https://evil.example.test/' })).toBe(false)
    expect(await request(ses, wc, 'clipboard-sanitized-write')).toBe(true)
    expect(ses.device!()).toBe(false)
    expect(ses.permissionCheck!(wc, 'clipboard-read', SERVER, {})).toBe(false)
    expect(ses.permissionCheck!(wc, 'media', SERVER, {})).toBe(false)
    expect(ses.permissionCheck!(null, 'clipboard-sanitized-write', SERVER, {})).toBe(false)
  })

  it('mouse moves do not count as a user action', async () => {
    const { ses, wc } = setup()
    wc.url = `${SERVER}/app`
    wc.emit('input-event', {}, { type: 'mouseMove' })
    expect(await request(ses, wc, 'media', { mediaTypes: ['audio'] })).toBe(false)
  })
})

// ---- downloads and lifecycle ----------------------------------------------------

describe('downloads', () => {
  function download(ses: FakeSession, wc: FakeWebContents | null, filename: string) {
    const item = { getFilename: () => filename, setSaveDialogOptions: vi.fn(), setSavePath: vi.fn(), cancel: vi.fn() }
    ses.emit('will-download', {}, item, wc)
    return item
  }

  it('always shows the save dialog with a cleaned name, never a page-chosen path', () => {
    const { ses, wc } = setup()
    wc.url = `${SERVER}/app`
    for (const [name, clean] of [
      ['../../../.bashrc', 'bashrc'],
      ['/etc/cron.d/job', 'job'],
      ['invoice‮fdp.exe', 'invoicefdp.exe'],
      ['report.pdf', 'report.pdf'],
    ]) {
      const item = download(ses, wc, name)
      expect(item.setSavePath).not.toHaveBeenCalled()
      expect(item.setSaveDialogOptions).toHaveBeenCalledWith({ title: 'Save download', defaultPath: `/downloads/${clean}` })
    }
  })

  it('cancels a download that does not come from a server page', () => {
    const { ses, wc } = setup()
    wc.url = 'https://evil.example.test/'
    expect(download(ses, wc, 'x.bin').cancel).toHaveBeenCalled()
    expect(download(ses, null, 'x.bin').cancel).toHaveBeenCalled()
  })
})

describe('view lifecycle events', () => {
  it('reports loads, failures, crashes and hangs of the current view only', () => {
    const { host, wc, dispatched } = setup()
    wc.emit('did-finish-load')
    wc.emit('did-fail-load', {}, -3, 'ERR_ABORTED', '', true)
    wc.emit('did-fail-load', {}, -102, 'ERR_CONNECTION_REFUSED', '', false)
    wc.emit('did-fail-load', {}, -102, 'ERR_CONNECTION_REFUSED', '', true)
    wc.emit('render-process-gone', {}, { reason: 'crashed', exitCode: 1 })
    wc.emit('unresponsive')
    wc.emit('responsive')
    expect(dispatched).toEqual([
      { type: 'view-loaded' },
      { type: 'view-load-failed', code: -102, description: 'ERR_CONNECTION_REFUSED' },
      { type: 'view-crashed', reason: 'crashed' },
      { type: 'view-unresponsive' },
      { type: 'view-responsive' },
    ])
    host.apply([{ kind: 'close' }])
    wc.emit('render-process-gone', {}, { reason: 'killed', exitCode: 1 })
    expect(dispatched).toHaveLength(5)
  })

  it('navigate effects only load server pages', () => {
    const { host, wc } = setup()
    wc.loadURL.mockClear()
    host.apply([{ kind: 'navigate', url: 'https://evil.example.test/app' }])
    expect(wc.loadURL).not.toHaveBeenCalled()
    host.apply([{ kind: 'navigate', url: `${SERVER}/app?model=x` }])
    expect(wc.loadURL).toHaveBeenCalledWith(`${SERVER}/app?model=x`)
  })
})

describe('the corner dot view', () => {
  it('runs no script, loads nothing from the network and only shows the bar on click', () => {
    const { host } = setup()
    const showStatusBar = (host as unknown as { d: { showStatusBar: ReturnType<typeof vi.fn> } }).d.showStatusBar
    host.showDot('ok', { x: 1, y: 1, width: 28, height: 28 })
    const dot = FakeView.made.at(-1)!
    expect(dot.options.webPreferences).toMatchObject({ javascript: false, sandbox: true, contextIsolation: true, nodeIntegration: false })
    expect(dot.options.webPreferences).not.toHaveProperty('preload')
    let r: { cancel?: boolean } = {}
    dot.webContents.session.beforeRequest!({ url: 'https://evil.example.test/', resourceType: 'mainFrame' }, (x) => (r = x))
    expect(r.cancel).toBe(true)
    // Only its own page loads.
    dot.webContents.session.beforeRequest!({ url: dot.webContents.loadURL.mock.calls[0][0], resourceType: 'mainFrame' }, (x) => (r = x))
    expect(r.cancel).toBe(false)
    dot.webContents.session.beforeRequest!({ url: 'data:text/html,<script>x</script>', resourceType: 'mainFrame' }, (x) => (r = x))
    expect(r.cancel).toBe(true)
    let prevented = false
    dot.webContents.emit('will-navigate', { url: 'https://evil.example.test/', preventDefault: () => (prevented = true) })
    expect(prevented).toBe(true)
    expect(showStatusBar).not.toHaveBeenCalled()
    dot.webContents.emit('will-navigate', { url: 'http://show-status-bar.invalid/', preventDefault() {} })
    expect(showStatusBar).toHaveBeenCalledTimes(1)
  })
})
