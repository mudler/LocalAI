// The WebUI view and the corner dot view: the Electron side. The decisions are
// in webuiPolicy.ts (pure); this file only wires them to Electron's handlers
// and applies the effects of webuiState.ts.
//
// The WebUI view:
//   - has no preload, so its pages have no bridge, no ipcRenderer, no Node;
//   - runs with contextIsolation, sandbox and webSecurity on, no insecure
//     content and no <webview>. Electron's --no-sandbox (the Linux fallback
//     the AppImage start script adds) is process-wide and outside our control;
//     nothing here turns the sandbox off for this view;
//   - lives in its own persistent session partition per origin, so cookies
//     and logins of the local server, of each remote server and of the shell
//     never mix;
//   - is never a trusted IPC sender: ipcGuard.isTrustedSender only accepts
//     the shell view's main frame (see test/webuiSecurity.test.ts).
import path from 'node:path'
import { dialog, WebContentsView, type BaseWindow, type Session, type WebContents } from 'electron'
import type { WebuiEffect, WebuiEvent } from './webuiState'
import {
  decideNavigation,
  decidePermission,
  decidePermissionCheck,
  decideWindowOpen,
  GESTURE_INPUT_TYPES,
  hasRecentGesture,
  isServerOrigin,
  partitionForOrigin,
  sanitizeFileName,
  type PermissionChoice,
  type PermissionStore,
} from './webuiPolicy'
import { cornerDotPage, DOT_CLICK_URL, type Rect } from './windowLayout'
import type { DotTone } from '../shared/types'

/** Chromium's net error for a navigation that was cancelled (by us, or by a new navigation). */
const ERR_ABORTED = -3

export interface WebuiHostDeps {
  window(): BaseWindow | undefined
  dispatch(e: WebuiEvent): void
  permissionStore(): PermissionStore
  rememberPermission(origin: string, choice: PermissionChoice): Promise<void>
  /** Opens a URL in the system browser after its own scheme check. */
  openExternal(url: string): void
  /** The dot was clicked: show the status bar again. */
  showStatusBar(): void
  downloadsDir(): string
  log(message: string, level?: 'info' | 'warn' | 'error'): void
  /** Milestone line for the packaged test (no URLs of the user's pages beyond what the test serves). */
  signal(line: string): void
}

/** The web preferences of the WebUI view. Exported so the test can check them. */
export function webuiWebPreferences(origin: string): Electron.WebPreferences {
  return {
    partition: partitionForOrigin(origin),
    // No preload: the pages never get the bridge.
    nodeIntegration: false,
    nodeIntegrationInSubFrames: false,
    nodeIntegrationInWorker: false,
    contextIsolation: true,
    sandbox: true,
    webSecurity: true,
    allowRunningInsecureContent: false,
    webviewTag: false,
    experimentalFeatures: false,
    navigateOnDragDrop: false,
    safeDialogs: true,
  }
}

const MEDIA_LABEL: Record<string, string> = { audio: 'microphone', video: 'camera' }

export class WebuiHost {
  private view: WebContentsView | undefined
  private viewOrigin: string | undefined
  private dot: WebContentsView | undefined
  private dotTone: DotTone | undefined
  private dotPage: string | undefined
  private lastInput = new WeakMap<WebContents, number>()
  /** Sessions already given the policy handlers, with the origin each one serves. */
  private sessions = new Map<Session, string>()
  private prompting = false

  constructor(private readonly d: WebuiHostDeps) {}

  /** The WebUI view, if one exists. */
  get webuiView(): WebContentsView | undefined {
    return this.view
  }

  get dotView(): WebContentsView | undefined {
    return this.dot
  }

  currentUrl(): string | undefined {
    const wc = this.view?.webContents
    return wc && !wc.isDestroyed() ? wc.getURL() : undefined
  }

  apply(effects: WebuiEffect[]): void {
    for (const f of effects) {
      switch (f.kind) {
        case 'show':
          this.show(f.origin, f.url)
          break
        case 'navigate':
          if (this.view && this.viewOrigin && isServerOrigin(f.url, this.viewOrigin)) void this.view.webContents.loadURL(f.url).catch(() => undefined)
          break
        case 'close':
          this.close()
          break
        case 'reload':
          if (this.view && !this.view.webContents.isDestroyed()) this.view.webContents.reload()
          break
        case 'open-browser':
          this.d.openExternal(f.url)
          break
      }
    }
  }

  private show(origin: string, url: string): void {
    this.close()
    const win = this.d.window()
    if (!win) return
    const view = new WebContentsView({ webPreferences: webuiWebPreferences(origin) })
    this.view = view
    this.viewOrigin = origin
    this.installSession(view.webContents.session, origin)
    this.installContents(view.webContents, origin)
    view.setVisible(false) // the next layout pass shows it
    win.contentView.addChildView(view)
    this.d.signal('webui-show')
    void view.webContents.loadURL(url).catch(() => undefined) // failures arrive as did-fail-load
  }

  private close(): void {
    const view = this.view
    this.view = undefined
    this.viewOrigin = undefined
    if (!view) return
    const win = this.d.window()
    try {
      if (win && !win.isDestroyed()) win.contentView.removeChildView(view)
    } catch {
      /* already removed */
    }
    if (!view.webContents.isDestroyed()) view.webContents.close()
    this.d.signal('webui-close')
  }

  /** Is this webContents the current WebUI view? Events from a closed view are dropped. */
  private current(wc: WebContents): boolean {
    return this.view?.webContents === wc
  }

  private installContents(wc: WebContents, origin: string): void {
    // Navigation of any frame the page starts: links, forms, location, iframes.
    wc.on('will-frame-navigate', (e) => {
      const frame = e.isMainFrame ? 'main' : 'sub'
      const d = decideNavigation(e.url, origin, { frame })
      if (d.action === 'allow') return
      e.preventDefault()
      if (d.action === 'external') this.d.openExternal(d.url)
      this.d.signal(`webui-nav ${frame} ${d.action}${d.action === 'deny' ? ` ${d.reason}` : ''}`)
    })
    wc.on('will-redirect', (e) => {
      const frame = e.isMainFrame ? 'main' : 'sub'
      const d = decideNavigation(e.url, origin, { frame })
      if (d.action === 'allow') return
      e.preventDefault()
      if (d.action === 'external') this.d.openExternal(d.url)
      this.d.signal(`webui-redirect ${frame} ${d.action}`)
    })
    // window.open, target=_blank, shift-click: never a new window.
    wc.setWindowOpenHandler(({ url }) => {
      const d = decideWindowOpen(url, origin)
      if (d.route === 'same-view') void wc.loadURL(d.url).catch(() => undefined)
      else if (d.route === 'external') this.d.openExternal(d.url)
      this.d.signal(`webui-window-open ${d.route}`)
      return { action: 'deny' }
    })
    wc.on('will-attach-webview', (e) => e.preventDefault())
    // The time of the last user action, for the "asked in response to a user action" rule.
    wc.on('input-event', (_e, input) => {
      if (GESTURE_INPUT_TYPES.has(input.type)) this.lastInput.set(wc, Date.now())
    })
    wc.on('certificate-error', (e, _url, error, _cert, callback) => {
      e.preventDefault()
      callback(false) // never bypassed
      this.d.log(`webui: certificate error (${error}), page refused`, 'warn')
      this.d.signal('webui-certificate-error')
    })
    wc.on('select-client-certificate', (e, _url, _list, callback) => {
      e.preventDefault()
      // No certificate: never hand a client certificate to a page.
      ;(callback as (certificate?: Electron.Certificate) => void)()
    })
    wc.on('did-finish-load', () => {
      if (this.current(wc)) this.d.dispatch({ type: 'view-loaded' })
      this.d.signal('webui-loaded')
    })
    wc.on('did-fail-load', (_e, code, description, _url, isMainFrame) => {
      if (!isMainFrame || code === ERR_ABORTED || !this.current(wc)) return
      this.d.log(`webui: page did not load (${code} ${description})`, 'warn')
      this.d.dispatch({ type: 'view-load-failed', code, description })
    })
    wc.on('render-process-gone', (_e, details) => {
      if (!this.current(wc) || details.reason === 'clean-exit') return
      this.d.log(`webui: the page process ended (${details.reason})`, 'error')
      this.d.dispatch({ type: 'view-crashed', reason: details.reason })
    })
    wc.on('unresponsive', () => {
      if (this.current(wc)) this.d.dispatch({ type: 'view-unresponsive' })
    })
    wc.on('responsive', () => {
      if (this.current(wc)) this.d.dispatch({ type: 'view-responsive' })
    })
  }

  private installSession(ses: Session, origin: string): void {
    if (this.sessions.has(ses)) return
    this.sessions.set(ses, origin)

    ses.setPermissionRequestHandler((wc, permission, callback, details) => {
      const media = 'mediaTypes' in details ? (details.mediaTypes ?? []) : []
      const decision = decidePermission(
        {
          permission,
          requestingUrl: details.requestingUrl,
          topUrl: wc.getURL(),
          mediaTypes: media,
          userGesture: hasRecentGesture(this.lastInput.get(wc), Date.now()),
        },
        origin,
        this.d.permissionStore(),
      )
      this.d.signal(`webui-permission ${permission} ${decision}`)
      if (decision === 'allow') return callback(true)
      if (decision !== 'ask') return callback(false)
      void this.ask(origin, media).then(callback, () => callback(false))
    })
    ses.setPermissionCheckHandler((wc, permission, requestingOrigin) =>
      decidePermissionCheck(permission, requestingOrigin, wc && !wc.isDestroyed() ? wc.getURL() : '', origin, this.d.permissionStore()),
    )
    ses.setDevicePermissionHandler(() => false)

    // A second line behind will-frame-navigate: no document from another
    // origin is ever fetched into a frame of this session, including after a
    // redirect. Other resources (images, fonts, API calls) follow the page's own rules.
    ses.webRequest.onBeforeRequest({ urls: ['<all_urls>'] }, (details, callback) => {
      if (details.resourceType !== 'mainFrame' && details.resourceType !== 'subFrame') return callback({})
      const d = decideNavigation(details.url, origin, { frame: details.resourceType === 'mainFrame' ? 'main' : 'sub' })
      if (d.action === 'allow') return callback({})
      this.d.signal(`webui-request-blocked ${details.resourceType}`)
      callback({ cancel: true })
    })

    ses.on('will-download', (_e, item, wc) => {
      if (!wc || wc.isDestroyed() || !isServerOrigin(wc.getURL(), origin)) {
        item.cancel()
        this.d.signal('webui-download cancelled')
        return
      }
      const name = sanitizeFileName(item.getFilename())
      // No save path: Electron shows the native save dialog, starting here.
      item.setSaveDialogOptions({ title: 'Save download', defaultPath: path.join(this.d.downloadsDir(), name) })
      this.d.signal(`webui-download dialog ${name}`)
    })
  }

  /** The prompt for microphone and camera. One at a time; a second request meanwhile is refused. */
  private async ask(origin: string, media: string[]): Promise<boolean> {
    const win = this.d.window()
    if (!win || this.prompting) return false
    this.prompting = true
    try {
      const what = media.length ? media.map((m) => MEDIA_LABEL[m] ?? m).join(' and ') : 'microphone or camera'
      const host = new URL(origin).host
      const r = await dialog.showMessageBox(win, {
        type: 'question',
        buttons: ['Allow', 'Block'],
        defaultId: 1,
        cancelId: 1,
        message: `Allow ${host} to use your ${what}?`,
        detail: 'Only pages of this LocalAI server can ask. You can reset this choice in Settings.',
        checkboxLabel: 'Remember my choice for this server',
        checkboxChecked: true,
      })
      const allow = r.response === 0
      if (r.checkboxChecked) await this.d.rememberPermission(origin, allow ? 'allow' : 'deny').catch(() => undefined)
      return allow
    } finally {
      this.prompting = false
    }
  }

  // ---- the corner dot -----------------------------------------------------------

  /** Shows the dot with this tone at these bounds, creating its view on first use. */
  showDot(tone: DotTone, bounds: Rect): WebContentsView | undefined {
    const win = this.d.window()
    if (!win) return undefined
    if (!this.dot) {
      const dot = new WebContentsView({
        webPreferences: {
          partition: 'launcher-dot',
          nodeIntegration: false,
          contextIsolation: true,
          sandbox: true,
          javascript: false,
          webviewTag: false,
        },
      })
      dot.setBackgroundColor('#00000000')
      const wc = dot.webContents
      const ses = wc.session
      // Only the dot page itself loads (Electron passes data: URLs through webRequest too); nothing else.
      ses.webRequest.onBeforeRequest({ urls: ['<all_urls>'] }, (d, cb) => cb({ cancel: !(d.resourceType === 'mainFrame' && d.url === this.dotPage) }))
      ses.setPermissionRequestHandler((_w, _p, cb) => cb(false))
      ses.setPermissionCheckHandler(() => false)
      wc.setWindowOpenHandler(() => ({ action: 'deny' }))
      wc.on('will-navigate', (e) => {
        e.preventDefault()
        this.d.signal(`dot-navigate ${e.url === DOT_CLICK_URL ? 'show-bar' : 'refused'}`)
        if (e.url === DOT_CLICK_URL) this.d.showStatusBar()
      })
      win.contentView.addChildView(dot)
      this.dot = dot
    }
    if (tone !== this.dotTone) {
      this.dotTone = tone
      this.dotPage = cornerDotPage(tone)
      void this.dot.webContents.loadURL(this.dotPage).catch(() => undefined)
    }
    this.dot.setBounds(bounds)
    this.dot.setVisible(true)
    return this.dot
  }

  hideDot(): void {
    this.dot?.setVisible(false)
  }

  /** Closes every view this host made; for window close and quit. */
  destroy(): void {
    this.close()
    if (this.dot && !this.dot.webContents.isDestroyed()) this.dot.webContents.close()
    this.dot = undefined
    this.dotTone = undefined
  }
}
