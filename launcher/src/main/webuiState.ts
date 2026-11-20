// The lifecycle of the WebUI view as a reducer. Pure, no Electron import.
//
// Inputs (server state, install, settings, shell screen, view events) go in as
// events; the model answers with effects for the Electron side to apply
// (create and load a view, navigate it, close it, reload it, open the system
// browser) and a WebuiViewState for the shell. Rules:
//   - the view loads `/app` once, when the local server first becomes ready;
//     shell screens hide it without reloading it;
//   - when the server stops while the view is up, the view stays (dimmed,
//     banner); it is not reloaded when the server is ready again;
//   - a different server (a remote one, or a new local address) closes the
//     view and opens a new one in that origin's own session;
//   - with "open in browser", or after two crashes, there is no view.
import type {
  DotTone,
  LayoutMode,
  ServerState,
  ShellInsets,
  RemoteStatusView,
  ShellScreen,
  WebuiPlacement,
  WebuiProblem,
  WebuiTarget,
  WebuiViewState,
} from '../shared/types'
import { isServerOrigin } from './webuiPolicy'

export interface WebuiInputs {
  server: ServerState
  /** Origin of the local server, from the configured address. */
  localOrigin: string
  installActive: boolean
  /** The saved "Show status bar" choice. */
  statusBar: boolean
  /** A launcher update is downloaded and waits for a restart. */
  updateReady: boolean
  /** Chromium runs without its sandbox (the Linux fallback). */
  sandboxOff: boolean
  /** The sandbox notice is shown and not yet answered. */
  sandboxNoticeShown: boolean
  /** Setting: open the WebUI in the system browser instead. */
  openInBrowser: boolean
  /** Worker mode (not built yet): the window shows the worker screen, no WebUI. */
  workerMode: boolean
  /** A LocalAI server update is offered (it brings a hidden bar back, like a launcher update). */
  serverUpdate: boolean
  /**
   * The launcher started without showing its window (tray start or login
   * item). With "open in browser" on, the browser then opens only on an
   * explicit user action, never by itself.
   */
  startedHidden: boolean
}

export type WebuiEvent =
  | { type: 'inputs'; patch: Partial<WebuiInputs> }
  | { type: 'screen'; screen: ShellScreen | null }
  | { type: 'overlay'; open: boolean }
  | { type: 'insets'; insets: ShellInsets }
  | { type: 'connect'; origin: string; host: string }
  | { type: 'back-to-local' }
  /** The remote does not answer (set), or answers again (null). No view is kept for an unreachable remote. */
  | { type: 'remote-status'; status: RemoteStatusView | null }
  /** Open the WebUI home, or the chat with a model preselected (`/app?model=`). */
  | { type: 'deep-link'; url: string }
  | { type: 'open-in-browser'; currentUrl?: string }
  | { type: 'reload' }
  | { type: 'view-loaded' }
  | { type: 'view-load-failed'; code: number; description: string }
  | { type: 'view-crashed'; reason: string }
  | { type: 'view-unresponsive' }
  | { type: 'view-responsive' }

export type WebuiEffect =
  /** Create the view for `origin` (closing any other) and load `url`. */
  | { kind: 'show'; origin: string; url: string }
  /** Load `url` in the existing view (same origin). */
  | { kind: 'navigate'; url: string }
  | { kind: 'close' }
  | { kind: 'reload' }
  | { kind: 'open-browser'; url: string }

interface LiveView {
  origin: string
  loaded: boolean
  crashes: number
  problem: WebuiProblem | null
}

/** The view crashes this many times in a session before the launcher falls back to the browser. */
export const CRASHES_BEFORE_FALLBACK = 2

export const appUrl = (origin: string) => `${origin}/app`

export class WebuiModel {
  private inputs: WebuiInputs
  private remote: { origin: string; host: string } | undefined
  private remoteStatus: RemoteStatusView | null = null
  private screen: ShellScreen | null = null
  private overlay = false
  private insets: ShellInsets = { top: 0, bottom: 0 }
  private view: LiveView | undefined
  private fallback = false
  private browserOpened = false
  /** Crashes survive a view being replaced for the same origin within the session. */
  private crashCount = 0

  constructor(inputs: WebuiInputs) {
    this.inputs = { ...inputs }
  }

  get placement(): WebuiPlacement {
    if (this.fallback) return 'browser-fallback'
    return this.inputs.openInBrowser ? 'browser' : 'embedded'
  }

  get target(): WebuiTarget {
    return this.remote ? { kind: 'remote', ...this.remote } : { kind: 'local', origin: this.inputs.localOrigin }
  }

  /** The origin the view (if any) shows. undefined when there is no view. */
  get viewOrigin(): string | undefined {
    return this.view?.origin
  }

  dispatch(e: WebuiEvent): WebuiEffect[] {
    const fx: WebuiEffect[] = []
    switch (e.type) {
      case 'inputs': {
        const before = this.inputs
        this.inputs = { ...before, ...e.patch }
        // Turning "open in browser" off is also how the user leaves the crash fallback.
        if (before.openInBrowser && !this.inputs.openInBrowser) this.fallback = false
        // The server is back: a view whose load failed tries again.
        if (before.server !== 'ready' && this.inputs.server === 'ready' && this.view?.problem?.kind === 'load-failed' && !this.remote) {
          this.view.problem = null
          fx.push({ kind: 'reload' })
        }
        break
      }
      case 'screen':
        this.screen = e.screen
        break
      case 'overlay':
        this.overlay = e.open
        break
      case 'insets':
        this.insets = { top: e.insets.top, bottom: e.insets.bottom }
        break
      case 'connect':
        if (!this.remote || this.remote.origin !== e.origin) this.remoteStatus = null
        this.remote = { origin: e.origin, host: e.host }
        this.screen = null
        break
      case 'back-to-local':
        this.remote = undefined
        this.remoteStatus = null
        this.screen = null
        break
      case 'remote-status':
        this.remoteStatus = this.remote ? e.status : null
        break
      case 'deep-link': {
        // A page of the remote server on show opens in the window; any other remote page opens in the browser.
        if (this.placement === 'embedded' && this.remote && !this.inputs.workerMode && this.view?.origin === this.remote.origin && isServerOrigin(e.url, this.remote.origin)) {
          this.screen = null
          fx.push({ kind: 'navigate', url: e.url })
          return [...this.reconcile(), ...fx]
        }
        if (this.placement !== 'embedded' || this.remote || this.inputs.workerMode || !isServerOrigin(e.url, this.inputs.localOrigin)) {
          fx.push({ kind: 'open-browser', url: e.url })
          return fx
        }
        if (this.inputs.server !== 'ready' && !this.view) return fx
        this.screen = null
        if (this.view && this.view.origin === this.inputs.localOrigin) {
          fx.push({ kind: 'navigate', url: e.url })
          return [...this.reconcile(), ...fx]
        }
        if (this.view) {
          fx.push({ kind: 'close' })
          this.view = undefined
        }
        this.view = { origin: this.inputs.localOrigin, loaded: false, crashes: 0, problem: null }
        fx.push({ kind: 'show', origin: this.inputs.localOrigin, url: e.url })
        return fx
      }
      case 'open-in-browser': {
        const origin = this.target.origin
        const cur = e.currentUrl
        let url = appUrl(origin)
        if (cur) {
          try {
            if (new URL(cur).origin === origin) url = cur
          } catch {
            /* keep the home */
          }
        }
        fx.push({ kind: 'open-browser', url })
        return fx
      }
      case 'reload':
        // In the crash fallback, Reload is the user asking to try the window again.
        if (this.fallback) {
          this.fallback = false
          this.crashCount = 0
        }
        if (this.view) {
          this.view.problem = null
          fx.push({ kind: 'reload' })
        }
        break
      case 'view-loaded':
        if (this.view) {
          this.view.loaded = true
          if (this.view.problem?.kind === 'load-failed') this.view.problem = null
        }
        break
      case 'view-load-failed':
        if (this.view) {
          this.view.loaded = false
          this.view.problem = { kind: 'load-failed', code: e.code, description: e.description }
        }
        break
      case 'view-crashed':
        if (this.view) {
          this.crashCount++
          this.view.crashes++
          this.view.loaded = false
          this.view.problem = { kind: 'crashed', reason: e.reason, count: this.crashCount }
          if (this.crashCount >= CRASHES_BEFORE_FALLBACK) {
            const url = appUrl(this.view.origin)
            this.fallback = true
            this.view = undefined
            fx.push({ kind: 'close' }, { kind: 'open-browser', url })
            return fx
          }
        }
        break
      case 'view-unresponsive':
        if (this.view && !this.view.problem) this.view.problem = { kind: 'unresponsive' }
        break
      case 'view-responsive':
        if (this.view?.problem?.kind === 'unresponsive') this.view.problem = null
        break
    }
    return [...fx, ...this.reconcile()]
  }

  /** Brings the live view in line with the inputs. */
  private reconcile(): WebuiEffect[] {
    const fx: WebuiEffect[] = []
    const embedded = this.placement === 'embedded' && !this.inputs.workerMode
    const origin = this.target.origin
    if (this.view && (!embedded || this.view.origin !== origin || (this.remote && this.remoteStatus))) {
      fx.push({ kind: 'close' })
      this.view = undefined
    }
    if (embedded && !this.view) {
      const want = this.remote ? !this.remoteStatus : this.inputs.server === 'ready'
      if (want) {
        this.view = { origin, loaded: false, crashes: 0, problem: null }
        fx.push({ kind: 'show', origin, url: appUrl(origin) })
      }
    }
    // "Open in browser" chosen: open it once per session, when the local server
    // first becomes ready. Not after a hidden start: then only a user action opens it.
    if (this.placement === 'browser' && !this.remote && this.inputs.server === 'ready' && !this.browserOpened && !this.inputs.startedHidden) {
      this.browserOpened = true
      fx.push({ kind: 'open-browser', url: appUrl(this.inputs.localOrigin) })
    }
    return fx
  }

  /** What the window shows when no shell screen is open. */
  get home(): LayoutMode {
    if (this.remote && this.remoteStatus) return 'shell-only'
    if (!this.view || this.placement !== 'embedded') return 'shell-only'
    if (this.inputs.workerMode || this.inputs.installActive) return 'shell-only'
    const p = this.view.problem?.kind
    if (p === 'crashed' || p === 'load-failed') return 'shell-only'
    // A failed local server shows the start-failed screen with its fixes; the page is kept.
    if (!this.remote && this.inputs.server === 'failed') return 'shell-only'
    return 'webui'
  }

  get layout(): LayoutMode {
    return this.screen !== null ? 'shell-only' : this.home
  }

  private banner(): WebuiViewState['banner'] {
    if (this.layout !== 'webui' || this.remote) return null
    switch (this.inputs.server) {
      case 'ready':
      case 'failed':
        return null
      case 'starting':
        return 'starting'
      default:
        return 'stopped'
    }
  }

  private dotTone(): DotTone {
    if (this.inputs.installActive) return 'busy'
    switch (this.inputs.server) {
      case 'ready':
        return this.inputs.sandboxOff ? 'busy' : 'ok'
      case 'starting':
      case 'stopping':
        return 'busy'
      case 'failed':
        return 'failed'
      default:
        return 'stopped'
    }
  }

  state(): WebuiViewState {
    const layout = this.layout
    const banner = this.banner()
    const problem = this.view?.problem ?? null
    const forced =
      this.inputs.server === 'failed' ||
      this.inputs.updateReady ||
      this.inputs.serverUpdate ||
      this.inputs.sandboxNoticeShown ||
      !!this.remote ||
      problem !== null
    const barVisible = this.inputs.statusBar || forced
    return {
      placement: this.placement,
      target: this.target,
      layout,
      home: this.home,
      screen: this.screen,
      barVisible,
      barForced: barVisible && !this.inputs.statusBar,
      // Only a popover raises the shell; banners take their own strip (insets).
      shellAbove: layout === 'webui' && this.overlay,
      overlay: this.overlay,
      dot: layout === 'webui' && !barVisible ? this.dotTone() : null,
      banner,
      insets: { ...this.insets },
      problem,
      loaded: this.view?.loaded ?? false,
      remote: this.remote ? this.remoteStatus : null,
    }
  }
}
