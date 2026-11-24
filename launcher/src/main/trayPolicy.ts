// Decides how the window behaves, from facts about the tray. No Electron import.
//
// The tray is optional. What counts is whether a tray icon was actually
// created, never the name of the desktop environment.

export type CloseAction = 'hide' | 'quit' | 'confirm-quit'

export interface TrayFacts {
  /** The tray icon was created and a host is present to show it. */
  trayCreated: boolean
  serverRunning: boolean
  /** The user chose Quit from the tray menu or the app is shutting down. */
  quitRequested: boolean
}

export function decideClose(f: TrayFacts): CloseAction {
  if (f.quitRequested) return 'quit'
  if (f.trayCreated) return 'hide'
  return f.serverRunning ? 'confirm-quit' : 'quit'
}

/** The tray tooltip, with the unread notification count when there is one. */
export function unreadTooltip(unread: number): string {
  return unread > 0 ? `LocalAI Launcher (${unread} unread)` : 'LocalAI Launcher'
}

/** With a tray the window may start hidden when the user asked for it; without one it never does. */
export function shouldStartVisible(trayCreated: boolean, startHidden: boolean): boolean {
  return !(trayCreated && startHidden)
}

/**
 * Electron creates a Tray object on Linux even when no desktop shows it, so
 * constructor success alone is not proof. `hostPresent` is the answer of a
 * StatusNotifier probe (true, false, or undefined when it could not run).
 */
export function trayWasCreated(constructed: boolean, hostPresent: boolean | undefined): boolean {
  return constructed && hostPresent !== false
}
