// SPDX-License-Identifier: MIT
import { createRoot } from 'react-dom/client'
import { App as CapApp } from '@capacitor/app'
import { Browser } from '@capacitor/browser'
import { Clipboard } from '@capacitor/clipboard'
import { webRandomBytes } from '@launcher/core/serversConfig'
import { App } from '@launcher/renderer/App'
import '@launcher/renderer/styles'
import './mobile.css'
import { IME_CLASS, installMobileShell, onKeyboardChange } from './mobileShell'
import { createAndroidBridge } from './bridge/androidBridge'
import { appSelf, createAndroidUpdater, idleUpdater, type AndroidUpdater } from './core/androidUpdater'
import type { AppSelf } from './core/apkUpdate'
import { ClientCore } from './core/clientCore'
import { capacitorFileApi } from './core/configIO'
import { startLifecycle } from './lifecycle'
import { AppLinks, NativeHttp, P2P, Scanner, Updater, WebuiView } from './plugins'
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
    p2p: P2P,
    openExternal: (url) => Browser.open({ url }),
    now: () => Date.now(),
    randomBytes: webRandomBytes,
    appVersion: version,
    deviceName: async () => (await P2P.deviceName()).name,
    log: (m, level) => (level === 'warn' ? console.warn(m) : console.info(m)),
  })
  startLifecycle(core)
  core.setKeyboardOpen(document.documentElement.classList.contains(IME_CLASS))
  onKeyboardChange(window, (open) => core.setKeyboardOpen(open))
  const self = await appInfo()
  const updater = appUpdater(core, self)
  const bridge = createAndroidBridge(core, {
    clipboard: (text) => Clipboard.write({ string: text }),
    updater,
    info: { version, ...(self ? { applicationId: self.applicationId } : {}) },
    invites: { appLinks: AppLinks, scanner: Scanner },
  })
  createRoot(document.getElementById('root')!).render(<App bridge={bridge} />)
  // The daily update check: on open and on every return to the screen.
  void updater.maybeAutoCheck(Date.now())
  void CapApp.addListener('appStateChange', ({ isActive }) => {
    if (!isActive) return
    // Back from the Android settings: an open update sheet reads the install consent again
    // (the WebView does not always fire visibilitychange on resume).
    updater.refresh()
    void updater.maybeAutoCheck(Date.now())
  }).catch(() => undefined)
}

/** The running app from the Updater plugin; undefined without the plugin (a web preview). */
async function appInfo(): Promise<AppSelf | undefined> {
  try {
    return appSelf(await Updater.appInfo())
  } catch {
    return undefined
  }
}

/** The APK updater over the Updater plugin; without the plugin nothing is offered. */
function appUpdater(core: ClientCore, self: AppSelf | undefined): AndroidUpdater {
  if (!self) return idleUpdater()
  return createAndroidUpdater({ plugin: Updater, store: core.configStore(), self, openExternal: (url) => core.openExternal(url) })
}

void main().catch((e) => {
  console.error('the app could not start', e)
})
