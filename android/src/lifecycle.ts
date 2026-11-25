// SPDX-License-Identifier: MIT
// The app's life on the phone: on screen or not (the monitor pauses in the
// background and checks once on return), and the back button.
import { App } from '@capacitor/app'
import type { PluginListenerHandle } from '@capacitor/core'
import { runBack } from './bridge/backButton'
import type { ClientCore } from './core/clientCore'

/** Starts listening; the returned function stops it. */
export function startLifecycle(core: ClientCore): () => void {
  const handles: Promise<PluginListenerHandle>[] = [
    App.addListener('appStateChange', ({ isActive }) => core.setForeground(isActive)),
    // A listener here replaces Capacitor's default (history back, then exit).
    App.addListener('backButton', () => {
      void runBack(core, { doc: document, minimize: () => App.minimizeApp() }).catch((e) => console.warn('back button failed', e))
    }),
  ]
  return () => {
    for (const h of handles) void h.then((x) => x.remove()).catch(() => undefined)
  }
}
