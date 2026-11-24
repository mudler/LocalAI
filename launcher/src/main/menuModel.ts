// The application menu and the tray menu as plain data. No Electron import:
// menu.ts and tray.ts pass these templates to Menu.buildFromTemplate.

/**
 * Shows and hides the status bar.
 *
 * Ctrl+Shift+L (Cmd+Shift+L on macOS) because it is free where the
 * shortcut fires: the WebUI view handles Ctrl/Cmd+K, Ctrl/Cmd+Enter, `/`,
 * `j` and `k` (checked in core/http/react-ui/src); Chromium's own text
 * editing and page shortcuts (copy, paste, undo, find, zoom, reload, devtools)
 * do not use it; GTK uses Ctrl+Shift+U, not L. Ctrl+Shift+B was avoided
 * because browsers use it to toggle the bookmarks bar, and Ctrl+Alt
 * combinations were avoided because AltGr is Ctrl+Alt on many European
 * keyboard layouts and would eat typed characters.
 */
export const STATUS_BAR_ACCELERATOR = 'CmdOrCtrl+Shift+L'
/**
 * Opens the server switcher in the bar.
 *
 * Ctrl+Shift+S (Cmd+Shift+S on macOS), checked 2026-11-15 against: the menus
 * of this file (the status bar toggle is Ctrl+Shift+L; the roles in Edit,
 * File and Window carry no Shift+S), Electron's default menu roles (none uses
 * it; "Save as" is not a role here), Chromium's page shortcuts (Save page is
 * Ctrl+S; Shift+S is unused), the WebUI (Ctrl/Cmd+K, Ctrl/Cmd+Enter, `/`,
 * `j`, `k`; a search of core/http/react-ui/src finds no Shift+S) and the GTK
 * and KDE text-edit defaults. It stays in the menu so it works while the
 * WebUI view has the focus, like the status bar toggle.
 */
export const SWITCH_SERVER_ACCELERATOR = 'CmdOrCtrl+Shift+S'
export const SWITCH_SERVER_LABEL = 'Switch server'
export const SWITCH_SERVER_ITEM_ID = 'switch-server'
export const STATUS_BAR_LABEL = 'Show status bar'
export const STATUS_BAR_ITEM_ID = 'show-status-bar'

export interface MenuSpec {
  id?: string
  label?: string
  role?: string
  type?: 'normal' | 'separator' | 'submenu' | 'checkbox'
  checked?: boolean
  accelerator?: string
  submenu?: MenuSpec[]
  click?: (item: { checked: boolean }) => void
}

export interface AppMenuOptions {
  platform: NodeJS.Platform
  statusBarVisible: boolean
  onToggleStatusBar(visible: boolean): void
  /** Opens the server switcher. Without it the menu has no such item. */
  onSwitchServer?(): void
}

function statusBarItem(o: { statusBarVisible: boolean; onToggleStatusBar(visible: boolean): void }, withAccelerator: boolean): MenuSpec {
  return {
    id: STATUS_BAR_ITEM_ID,
    label: STATUS_BAR_LABEL,
    type: 'checkbox',
    checked: o.statusBarVisible,
    // Electron flips `checked` before it calls click, so the item's value is the new choice.
    click: (item) => o.onToggleStatusBar(item.checked),
    ...(withAccelerator ? { accelerator: STATUS_BAR_ACCELERATOR } : {}),
  }
}

/**
 * The window shows no menu bar on Linux and Windows: the mocks have none,
 * and the status bar holds the launcher's actions. The menu is still set,
 * so its accelerators (the status bar toggle, the edit commands) keep
 * working. macOS keeps its native menu bar at the top of the screen.
 */
export function menuBarVisible(platform: NodeJS.Platform): boolean {
  return platform === 'darwin'
}

/**
 * Edit commands as explicit roles. On macOS copy, paste and select all work
 * in a page only through these menu items; on the other platforms they keep
 * the same shortcuts with the menu bar hidden.
 */
const EDIT_MENU: MenuSpec = {
  label: 'Edit',
  submenu: [
    { role: 'undo' },
    { role: 'redo' },
    { type: 'separator' },
    { role: 'cut' },
    { role: 'copy' },
    { role: 'paste' },
    { role: 'selectAll' },
  ],
}

export function appMenuTemplate(o: AppMenuOptions): MenuSpec[] {
  const mac = o.platform === 'darwin'
  return [
    ...(mac ? [{ role: 'appMenu' }] : []),
    { role: 'fileMenu' },
    EDIT_MENU,
    {
      label: 'View',
      submenu: [
        statusBarItem(o, true),
        ...(o.onSwitchServer ? [{ id: SWITCH_SERVER_ITEM_ID, label: SWITCH_SERVER_LABEL, accelerator: SWITCH_SERVER_ACCELERATOR, click: () => o.onSwitchServer?.() } as MenuSpec] : []),
        { type: 'separator' },
        { role: 'togglefullscreen' },
      ],
    },
    { role: 'windowMenu' },
  ]
}

export interface TrayMenuOptions {
  statusBarVisible: boolean
  onShow(): void
  onToggleStatusBar(visible: boolean): void
  onQuit(): void
}

export function trayMenuTemplate(o: TrayMenuOptions): MenuSpec[] {
  return [
    { label: 'Show window', click: () => o.onShow() },
    // No accelerator in the tray: a tray menu does not show or use one on every desktop.
    statusBarItem(o, false),
    { type: 'separator' },
    { label: 'Quit', click: () => o.onQuit() },
  ]
}
