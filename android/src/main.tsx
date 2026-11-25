// SPDX-License-Identifier: MIT
import { createRoot } from 'react-dom/client'
import { App as CapApp } from '@capacitor/app'
import { Browser } from '@capacitor/browser'
import { Clipboard } from '@capacitor/clipboard'
import { webRandomBytes } from '@launcher/core/serversConfig'
import { App } from '@launcher/renderer/App'
import '@launcher/renderer/styles'
import './mobile.css'
import { installMobileShell } from './mobileShell'
import { createAndroidBridge, idleUpdater } from './bridge/androidBridge'
import { ClientCore, NO_P2P } from './core/clientCore'
import { capacitorFileApi } from './core/configIO'
import { startLifecycle } from './lifecycle'
import { NativeHttp, P2P, WebuiView } from './plugins'
import { nativeServerChecks } from './plugins/nativeTransport'

// The phone layout classes go on before anything renders.
installMobileShell(window)

async function appVersion(): Promise<string> {
  try {
    return (await CapApp.getInfo()).version
  } catch {
    return '0.0.0'
  }
}

async function main(): Promise<void> {
  const version = await appVersion()
  const core = await ClientCore.open({
    files: capacitorFileApi(),
    // A WebView fetch cannot read a cross-origin answer or a TLS error.
    ...nativeServerChecks(NativeHttp),
    webui: WebuiView,
    p2p: NO_P2P,
    openExternal: (url) => Browser.open({ url }),
    now: () => Date.now(),
    randomBytes: webRandomBytes,
    appVersion: version,
    deviceName: async () => (await P2P.deviceName()).name,
    log: (m, level) => (level === 'warn' ? console.warn(m) : console.info(m)),
  })
  startLifecycle(core)
  const bridge = createAndroidBridge(core, {
    clipboard: (text) => Clipboard.write({ string: text }),
    updater: idleUpdater(),
    info: { version },
  })
  createRoot(document.getElementById('root')!).render(<App bridge={bridge} />)
}

void main().catch((e) => {
  console.error('the app could not start', e)
})
