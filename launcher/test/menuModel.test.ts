import { describe, expect, it, vi } from 'vitest'
import { appMenuTemplate, menuBarVisible, STATUS_BAR_ACCELERATOR, STATUS_BAR_ITEM_ID, SWITCH_SERVER_ACCELERATOR, SWITCH_SERVER_ITEM_ID, trayMenuTemplate, type MenuSpec } from '../src/main/menuModel'

const find = (items: MenuSpec[], id: string): MenuSpec | undefined => {
  for (const i of items) {
    if (i.id === id) return i
    const sub = i.submenu && find(i.submenu, id)
    if (sub) return sub
  }
  return undefined
}

describe('application menu', () => {
  it('has a View menu with the status bar checkbox bound to the config', () => {
    const on = vi.fn()
    const menu = appMenuTemplate({ platform: 'linux', statusBarVisible: false, onToggleStatusBar: on })
    const view = menu.find((m) => m.label === 'View')!
    const item = find(view.submenu!, STATUS_BAR_ITEM_ID)!
    expect(item).toMatchObject({ label: 'Show status bar', type: 'checkbox', checked: false, accelerator: STATUS_BAR_ACCELERATOR })
    item.click!({ checked: true })
    expect(on).toHaveBeenCalledWith(true)
    expect(find(appMenuTemplate({ platform: 'linux', statusBarVisible: true, onToggleStatusBar: on }), STATUS_BAR_ITEM_ID)!.checked).toBe(true)
  })

  it('adds the app menu on macOS only', () => {
    const roles = (p: NodeJS.Platform) => appMenuTemplate({ platform: p, statusBarVisible: true, onToggleStatusBar: () => {} }).map((m) => m.role)
    expect(roles('darwin')[0]).toBe('appMenu')
    expect(roles('linux')).not.toContain('appMenu')
  })

  it('the accelerator does not clash with WebUI, browser or editing shortcuts', () => {
    const taken = [
      'CmdOrCtrl+K', 'CmdOrCtrl+Enter', 'CmdOrCtrl+C', 'CmdOrCtrl+V', 'CmdOrCtrl+X', 'CmdOrCtrl+A', 'CmdOrCtrl+Z',
      'CmdOrCtrl+Shift+Z', 'CmdOrCtrl+F', 'CmdOrCtrl+R', 'CmdOrCtrl+Shift+R', 'CmdOrCtrl+Shift+I', 'CmdOrCtrl+Shift+J',
      'CmdOrCtrl+Shift+B', 'CmdOrCtrl+Shift+U', 'CmdOrCtrl+Shift+T', 'CmdOrCtrl+Shift+N', 'CmdOrCtrl+Shift+Delete',
    ]
    expect(taken).not.toContain(STATUS_BAR_ACCELERATOR)
    expect(STATUS_BAR_ACCELERATOR).not.toMatch(/Ctrl\+Alt|Alt\+/) // AltGr is Ctrl+Alt on many layouts
  })
})

describe('server switcher accelerator', () => {
  it('is in the View menu and calls the handler', () => {
    const on = vi.fn()
    const menu = appMenuTemplate({ platform: 'linux', statusBarVisible: true, onToggleStatusBar: () => {}, onSwitchServer: on })
    const item = find(menu, SWITCH_SERVER_ITEM_ID)!
    expect(item.accelerator).toBe('CmdOrCtrl+Shift+S')
    item.click!({ checked: false })
    expect(on).toHaveBeenCalled()
    expect(find(appMenuTemplate({ platform: 'linux', statusBarVisible: true, onToggleStatusBar: () => {} }), SWITCH_SERVER_ITEM_ID)).toBeUndefined()
  })

  it('collides with no other accelerator of the menus', () => {
    const accel: string[] = []
    const walk = (items: MenuSpec[]) => items.forEach((i) => (i.accelerator && accel.push(i.accelerator), i.submenu && walk(i.submenu)))
    walk(appMenuTemplate({ platform: 'darwin', statusBarVisible: true, onToggleStatusBar: () => {}, onSwitchServer: () => {} }))
    expect(accel.filter((a) => a === SWITCH_SERVER_ACCELERATOR)).toHaveLength(1)
    expect(SWITCH_SERVER_ACCELERATOR).not.toBe(STATUS_BAR_ACCELERATOR)
    expect(SWITCH_SERVER_ACCELERATOR).not.toMatch(/Alt/)
  })
})

describe('menu bar', () => {
  it('is hidden on Linux and Windows and stays on macOS', () => {
    expect(menuBarVisible('linux')).toBe(false)
    expect(menuBarVisible('win32')).toBe(false)
    expect(menuBarVisible('darwin')).toBe(true)
  })

  it('keeps the edit commands in the menu on every platform, so their shortcuts work with the bar hidden', () => {
    for (const platform of ['linux', 'win32', 'darwin'] as const) {
      const menu = appMenuTemplate({ platform, statusBarVisible: true, onToggleStatusBar: () => {} })
      const edit = menu.find((m) => m.label === 'Edit')!
      const roles = edit.submenu!.map((i) => i.role).filter(Boolean)
      expect(roles).toEqual(expect.arrayContaining(['undo', 'redo', 'cut', 'copy', 'paste', 'selectAll']))
    }
  })

  it('keeps the status bar accelerator in the menu while the bar is hidden', () => {
    const menu = appMenuTemplate({ platform: 'linux', statusBarVisible: true, onToggleStatusBar: () => {} })
    expect(find(menu, STATUS_BAR_ITEM_ID)!.accelerator).toBe(STATUS_BAR_ACCELERATOR)
  })
})

describe('tray menu', () => {
  it('has the same status bar item, without an accelerator', () => {
    const on = vi.fn()
    const menu = trayMenuTemplate({ statusBarVisible: true, onShow: () => {}, onToggleStatusBar: on, onQuit: () => {} })
    const item = find(menu, STATUS_BAR_ITEM_ID)!
    expect(item).toMatchObject({ type: 'checkbox', checked: true })
    expect(item.accelerator).toBeUndefined()
    item.click!({ checked: false })
    expect(on).toHaveBeenCalledWith(false)
    expect(menu.map((m) => m.label ?? m.type)).toEqual(['Show window', 'Show status bar', 'separator', 'Quit'])
  })
})
