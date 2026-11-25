// SPDX-License-Identifier: MIT
import { createRoot } from 'react-dom/client'
import { App as CapApp } from '@capacitor/app'
import { Browser } from '@capacitor/browser'
import { Clipboard } from '@capacitor/clipboard'
import { webRandomBytes } from '@launcher/core/serversConfig'
import type { HttpGet, TestResult } from '@launcher/core/testClassify'
import { App } from '@launcher/renderer/App'
import '@launcher/renderer/styles'
import './mobile.css'
import { installMobileShell } from './mobileShell'
import { createAndroidBridge, idleUpdater } from './bridge/androidBridge'
import { ClientCore, NO_P2P } from './core/clientCore'
import { capacitorFileApi } from './core/configIO'
import { startLifecycle } from './lifecycle'
import { AppLinks, P2P, Scanner, WebuiView } from './plugins'

// The phone layout classes go on before anything renders.
installMobileShell(window)

// Placeholders until the native HTTP transport lands: every server check
// answers "the network does not answer".
const http: HttpGet = async () => {
  throw new Error('not wired: native HTTP')
}
const classifyError = (): TestResult => ({ status: 'unreachable', reason: 'network' })

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
    http,
    classifyError,
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
    invites: { appLinks: AppLinks, scanner: Scanner },
  })
  createRoot(document.getElementById('root')!).render(<App bridge={bridge} />)
}

void main().catch((e) => {
  console.error('the app could not start', e)
})
