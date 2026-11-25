// SPDX-License-Identifier: MIT
import type { CapacitorConfig } from '@capacitor/cli'

// The Capacitor WebView loads only the bundled app: no remote URL and no
// navigation allow-list. Server WebUIs open in the separate WebuiView.
const config: CapacitorConfig = {
  appId: 'io.localai.client',
  appName: 'LocalAI',
  webDir: 'dist',
  android: { path: 'native' },
  server: { androidScheme: 'https', allowNavigation: [] },
}

export default config
