// SPDX-License-Identifier: MIT
// What the client core tells the bridge, which passes it on to the shell's
// listeners (onServers, onWebui, onUpdater, onStatusBar, onOpenSwitcher).
import type { ServersView, UpdaterStatus, WebuiViewState } from '@launcher/shared/types'

export type ClientEvent =
  | { kind: 'servers'; view: ServersView }
  | { kind: 'webui'; state: WebuiViewState }
  | { kind: 'updater'; status: UpdaterStatus }
  | { kind: 'statusBar'; visible: boolean }
  | { kind: 'openSwitcher' }
  /** "Keep P2P connection in the background" changed, for example by the notification's Disconnect. */
  | { kind: 'p2pKeep'; on: boolean }
