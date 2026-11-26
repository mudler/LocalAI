// A fake client-only bridge for the component tests: the desktop fake with
// every local-server method removed, and an info() that says so. It starts
// with no saved server, and its WebUI follows the default server as a
// client main process would. No server and no main process.
import type { Capabilities, ServersView, WebuiViewState } from '../../shared/types'
import { LOCAL_SERVER_METHODS } from '../../shared/apiGroups'
import { fakeBridge, serversView, webuiView, type FakeBridge, type FakeState } from './fakeBridge'

export const CLIENT_CAPABILITIES: Capabilities = { localServer: false, p2p: true, selfUpdate: 'apk', qrScan: true }

/** The WebUI view a client main process sends: the default server's page, or nothing to show. */
function clientWebui(v: ServersView): WebuiViewState {
  const row = v.rows.find((r) => r.id === v.defaultId && r.kind === 'remote' && r.origin)
  if (!row?.origin) {
    // The type needs a target; with nothing to show the shell does not read it.
    return webuiView({ target: { kind: 'local', origin: '' }, layout: 'shell-only', home: 'shell-only', loaded: false })
  }
  return webuiView({ target: { kind: 'remote', origin: row.origin, host: row.host } })
}

export function fakeClientBridge(state: FakeState): FakeBridge {
  state.servers ??= serversView({ rows: [], defaultId: 'local' })
  state.info = { ...state.info, capabilities: CLIENT_CAPABILITIES }
  const b = fakeBridge(state, { extras: true })
  for (const k of LOCAL_SERVER_METHODS) delete (b as unknown as Record<string, unknown>)[k]

  // The WebUI follows the default unless the test fixed it.
  const listeners = new Set<(v: WebuiViewState) => void>()
  const webui = () => state.webui ?? clientWebui(state.servers ?? serversView({ rows: [], defaultId: 'local' }))
  b.getWebui = () => {
    b.calls.push({ name: 'getWebui', args: [] })
    return Promise.resolve(webui())
  }
  b.onWebui = (cb) => {
    listeners.add(cb)
    return () => void listeners.delete(cb)
  }
  // Subscribed first, so the WebUI changes before the shell sees the new list.
  b.onServers?.(() => {
    if (!state.webui) listeners.forEach((cb) => cb(webui()))
  })
  return b
}
