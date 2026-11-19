// The optional tray. Imports Electron; the decisions live in trayPolicy.ts
// and the menu content in menuModel.ts.
import { Tray, Menu, nativeImage, type MenuItemConstructorOptions } from 'electron'
import { execFile } from 'node:child_process'
import { trayMenuTemplate } from './menuModel'
import { trayWasCreated, unreadTooltip } from './trayPolicy'

/** Asks the session bus whether a StatusNotifier host exists. undefined = could not tell. */
function probeStatusNotifierHost(): Promise<boolean | undefined> {
  return new Promise((resolve) => {
    execFile(
      'gdbus',
      ['call', '--session', '--dest', 'org.freedesktop.DBus', '--object-path', '/org/freedesktop/DBus',
        '--method', 'org.freedesktop.DBus.NameHasOwner', 'org.kde.StatusNotifierWatcher'],
      { timeout: 1500 },
      (err, stdout) => {
        if (err) return resolve(undefined)
        resolve(/true/.test(stdout))
      },
    )
  })
}

export interface TrayHandlers {
  iconPath: string
  statusBarVisible: boolean
  onShow(): void
  onToggleStatusBar(visible: boolean): void
  onQuit(): void
}

export interface LauncherTray {
  tray: Tray
  /** Rebuilds the menu so its "Show status bar" item matches the saved choice. */
  setStatusBarVisible(visible: boolean): void
  /** Shows the unread notification count in the tooltip (and beside the icon on macOS). */
  setUnread(n: number): void
}

/** Returns the tray when one was created and has a host, else undefined. */
export async function createTray(h: TrayHandlers): Promise<LauncherTray | undefined> {
  if (process.platform === 'linux') {
    // A tray constructed without a host draws nothing, so ask the bus first.
    if ((await probeStatusNotifierHost()) === false) return undefined
  }
  let tray: Tray | undefined
  let constructed = false
  try {
    // The Windows notification area draws 16 px icons (at 100 % scale).
    const size = process.platform === 'win32' ? 16 : 22
    tray = new Tray(nativeImage.createFromPath(h.iconPath).resize({ width: size, height: size }))
    constructed = true
  } catch {
    constructed = false
  }
  if (!tray || !trayWasCreated(constructed, true)) return undefined
  const t = tray
  const setMenu = (statusBarVisible: boolean) =>
    t.setContextMenu(Menu.buildFromTemplate(trayMenuTemplate({ ...h, statusBarVisible }) as MenuItemConstructorOptions[]))
  t.setToolTip('LocalAI Launcher')
  setMenu(h.statusBarVisible)
  t.on('click', h.onShow)
  return {
    tray: t,
    setStatusBarVisible: setMenu,
    setUnread: (n) => {
      t.setToolTip(unreadTooltip(n))
      if (process.platform === 'darwin') t.setTitle(n > 0 ? String(n) : '')
    },
  }
}
