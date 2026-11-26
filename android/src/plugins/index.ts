// SPDX-License-Identifier: MIT
import { registerPlugin } from '@capacitor/core'
import type {
  AppLinksPlugin,
  NativeHttpPlugin,
  P2PPlugin,
  ScannerPlugin,
  UpdaterPlugin,
  WebuiViewPlugin,
} from './definitions'

export * from './definitions'

// The names match the @CapacitorPlugin(name = ...) of each Kotlin class.
export const NativeHttp = registerPlugin<NativeHttpPlugin>('NativeHttp')
export const WebuiView = registerPlugin<WebuiViewPlugin>('WebuiView')
export const P2P = registerPlugin<P2PPlugin>('P2P')
export const Updater = registerPlugin<UpdaterPlugin>('Updater')
export const Scanner = registerPlugin<ScannerPlugin>('Scanner')
export const AppLinks = registerPlugin<AppLinksPlugin>('AppLinks')
