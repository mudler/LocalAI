// The dim layer over the WebUI view while the local server is stopped (see
// windowLayout.ts). Imports Electron. A static data page with JavaScript off,
// its own session that loads nothing else, and no navigation.
import { WebContentsView, type BaseWindow } from 'electron'
import { DIM_PAGE, type Rect } from './windowLayout'

export class DimLayer {
  private view: WebContentsView | undefined

  constructor(private readonly window: () => BaseWindow | undefined) {}

  /** Shows the layer at these bounds, creating its view on first use. */
  show(bounds: Rect): WebContentsView | undefined {
    const win = this.window()
    if (!win) return undefined
    if (!this.view) {
      const v = new WebContentsView({
        webPreferences: {
          partition: 'launcher-dim',
          nodeIntegration: false,
          contextIsolation: true,
          sandbox: true,
          javascript: false,
          webviewTag: false,
        },
      })
      v.setBackgroundColor('#00000000')
      const wc = v.webContents
      const ses = wc.session
      ses.webRequest.onBeforeRequest({ urls: ['<all_urls>'] }, (d, cb) => cb({ cancel: !(d.resourceType === 'mainFrame' && d.url === DIM_PAGE) }))
      ses.setPermissionRequestHandler((_w, _p, cb) => cb(false))
      ses.setPermissionCheckHandler(() => false)
      wc.setWindowOpenHandler(() => ({ action: 'deny' }))
      wc.on('will-navigate', (e) => e.preventDefault())
      win.contentView.addChildView(v)
      void wc.loadURL(DIM_PAGE).catch(() => undefined)
      this.view = v
    }
    this.view.setBounds(bounds)
    this.view.setVisible(true)
    return this.view
  }

  hide(): void {
    this.view?.setVisible(false)
  }

  destroy(): void {
    if (this.view && !this.view.webContents.isDestroyed()) this.view.webContents.close()
    this.view = undefined
  }
}
