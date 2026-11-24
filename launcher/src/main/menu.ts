// The application menu. Imports Electron; the content lives in menuModel.ts.
import { Menu, type MenuItemConstructorOptions } from 'electron'
import { appMenuTemplate, type AppMenuOptions } from './menuModel'

/** Builds and sets the application menu. Call again when the status bar choice changes. */
export function setAppMenu(o: AppMenuOptions): void {
  Menu.setApplicationMenu(Menu.buildFromTemplate(appMenuTemplate(o) as MenuItemConstructorOptions[]))
}
