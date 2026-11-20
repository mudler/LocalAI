// Named states for the screenshot harness and the component tests. Each one is
// a screen of the design mocks (the number in the comment) or a state the mocks
// do not draw. Hosts use the reserved *.example.test names and the 192.0.2.0/24
// documentation range.
import type { LogLineView } from '../../shared/types'
import { baseState, DEFAULT_SETTINGS, ENDPOINT, FAKE_LAN, localRow, NO_KEYRING, networkView, remoteRow, serversView, webuiView, type FakeState } from './fakeBridge'

/** A made-up key in the launcher's format. Only revealApiKey hands it out. */
const FAKE_KEY = 'lai-EXAMPLEexampleEXAMPLEexampleEXAMPLEexam3f9a'

const T0 = new Date(2026, 10, 14, 12, 48, 2).getTime()
const at = (min: number, sec = 0) => T0 + min * 60000 + sec * 1000
let seq = 0
const line = (ts: number, level: LogLineView['level'], text: string): LogLineView => ({ seq: ++seq, ts, source: 'server', level, text })

export const LOGS: LogLineView[] = [
  line(at(0), 'info', 'Starting LocalAI 4.1.3'),
  line(at(0), 'info', 'Models path ~/.localai/models'),
  line(at(0, 1), 'info', 'Detected GPU: NVIDIA RTX 4070 (12 GB)'),
  line(at(0, 2), 'info', 'Backend llama-cpp ready'),
  line(at(0, 2), 'info', 'API server listening on 127.0.0.1:8080'),
  line(at(70, 9), 'info', 'Download whisper-small complete (498 MB)'),
  line(at(74, 35), 'debug', 'Loading model qwen3-8b: 37 layers'),
  line(at(74, 35), 'info', 'Loading model qwen3-8b'),
  line(at(74, 39), 'warn', 'Context 32768 exceeds trained 8192, clamped'),
  line(at(74, 42), 'info', 'Model qwen3-8b loaded in 6.9s'),
  line(at(83, 18), 'error', 'flux-schnell: backend diffusers not installed'),
  line(at(83, 18), 'info', 'Hint: install it from LocalAI > Models'),
]

/** The server is ready but the WebUI page is not on show yet. */
const LOADING = webuiView({ layout: 'shell-only', home: 'shell-only', loaded: false })
/** No page: the shell fills the window. */
const NO_VIEW = webuiView({ layout: 'shell-only', home: 'shell-only', loaded: false })

const updateOffer = { kind: 'update-available' as const, version: '4.2.1', tag: 'v4.2.1', installed: '4.1.3' }
const restricted = (kind: 'shown' | 'dismissed') => ({
  state: 'unsandboxed-restricted' as const,
  notice: { kind },
  profile: 'unknown' as const,
  setupAvailable: false,
})
const workerConfig = (host: string) => ({
  serverUrl: `https://${host}`,
  natsUrl: `nats://${host}:4222`,
  advertiseAddress: '192.0.2.24:50051',
  nodeName: 'studio-desktop',
})

export type FixtureName =
  | 'ready'
  | 'ready-update'
  | 'install'
  | 'installing'
  | 'start-failed'
  | 'first-run'
  | 'logs'
  | 'settings'
  | 'connect'
  | 'worker-settings'
  | 'remote'
  | 'server-stopped'
  | 'sandbox-off'
  | 'bar-hidden'
  | 'worker-running'
  | 'starting'
  | 'stopped'
  | 'install-failed'
  | 'bar-hidden-failed'
  | 'config-recovered'
  | 'connect-local'
  | 'connect-no-keyring'
  | 'settings-env'
  | 'settings-no-keyring'
  | 'first-choose'
  | 'first-choose-tls'
  | 'servers'
  | 'servers-empty'
  | 'switcher'
  | 'switching'
  | 'remote-unreachable'
  | 'remote-insecure'
  | 'remote-keep-local'
  | 'updates-remote'
  | 'dev'
  | 'dev-stale-flag'

const OFFICE = remoteRow('srv_00000001', 'Office GPU box', 'https://office-gpu.example.com', { status: 'connected', version: '4.2.1' })
const HOME = remoteRow('srv_00000002', 'Home server', 'https://home.example.net', { status: 'unreachable' })
const LAB = remoteRow('srv_00000003', 'Lab server', 'http://192.0.2.40:8080')
const remoteDefault = (extra: Parameters<typeof serversView>[0] = {}) =>
  serversView({
    rows: [localRow({ isDefault: false, isCurrent: false, status: 'not-running' }), { ...OFFICE, isDefault: true, isCurrent: true }, HOME, LAB],
    defaultId: OFFICE.id,
    ...extra,
  })
const remoteWebui = (origin: string, host: string, over = {}) => webuiView({ target: { kind: 'remote', origin, host }, barForced: false, ...over })
const STOPPED = { state: 'stopped' as const, endpoint: ENDPOINT, logTail: [] }
const devInfo = (now: number) => ({ checkedAt: now - 5 * 60000, autoCheck: true, previousVersion: '4.2.1', channel: 'dev' as const, devConfirmed: true, devBuild: '3f9a1c2', remoteDefault: false })

export function fixture(name: FixtureName, now = Date.now()): FakeState {
  switch (name) {
    case 'ready': // 1, with the menu open
    case 'logs': // 7
      return baseState({ logs: LOGS, windowSettings: { openInBrowser: false, desktopNotifications: true, anyRemembered: false } })
    case 'ready-update': // 2
      return baseState({ install: updateOffer })
    case 'install': // 3
      return baseState({
        server: { state: 'not-installed', endpoint: ENDPOINT, logTail: [] },
        install: { kind: 'update-available', version: '4.2.1', tag: 'v4.2.1' },
        installDetails: { build: 'linux · amd64 · CUDA 12 build', size: 1.1e9, installDir: '~/.localai/bin' },
      })
    case 'installing': // 4
      return baseState({
        server: { state: 'stopped', endpoint: ENDPOINT, logTail: [] },
        install: updateOffer,
        installEvents: [{ kind: 'downloading', done: 640e6, total: 1.1e9 }],
      })
    case 'start-failed': // 5
      return baseState({
        server: {
          state: 'failed',
          endpoint: ENDPOINT,
          detail: 'listen tcp 127.0.0.1:8080: bind: address already in use',
          logTail: [
            '14:21:07 INF Starting LocalAI 4.1.3',
            '14:21:07 INF Models path ~/.localai/models',
            '14:21:08 ERR listen tcp 127.0.0.1:8080: bind: address already in use',
            '14:21:08 INF Exiting',
          ],
        },
        freePort: 8081,
        portProcess: { status: 'found', processes: [{ name: 'local-ai', pid: 48213 }] },
      })
    case 'first-run': // 6
      return baseState({
        install: { kind: 'up-to-date', version: '4.2.1' },
        settings: { ...DEFAULT_SETTINGS, showWelcome: true },
        webui: LOADING,
        hardware: { cpuModel: 'Example CPU', cpuCount: 16, memoryBytes: 32e9, gpu: 'nvidia' },
      })
    case 'settings': // 8
      return baseState({
        install: updateOffer,
        sandbox: restricted('dismissed'),
        info: { version: '4.1.3', packaged: true, trayActive: true, sandboxed: false },
        updateInfo: { checkedAt: now - 10 * 60000, autoCheck: true, previousVersion: '4.1.2', channel: 'stable', devConfirmed: false, remoteDefault: false },
        windowSettings: { openInBrowser: false, desktopNotifications: true, media: 'allow', anyRemembered: true },
      })
    case 'connect': // 9
      return baseState({
        network: networkView({ exposed: true, address: '0.0.0.0:8080', lanAddresses: [FAKE_LAN], apiKeySet: true, apiKeyHint: { prefix: 'lai-', last4: '3f9a' } }),
        apiKey: FAKE_KEY,
      })
    case 'connect-local': // 9, before "Allow other devices"
      return baseState({ network: networkView() })
    case 'connect-no-keyring': // 9 on Linux with no keyring: the key lives for this session
      return baseState({
        network: networkView({ exposed: true, address: '0.0.0.0:8080', lanAddresses: [FAKE_LAN], apiKeySet: true, apiKeyHint: { prefix: 'lai-', last4: '3f9a' }, storage: NO_KEYRING }),
        apiKey: FAKE_KEY,
        storage: NO_KEYRING,
      })
    case 'settings-env': // 8, Advanced
      return {
        ...fixture('settings', now),
        settings: { ...DEFAULT_SETTINGS, address: '0.0.0.0:8080' },
        envVars: {
          vars: [
            { name: 'HF_TOKEN', ignored: false, sessionOnly: false },
            { name: 'LOCALAI_THREADS', ignored: false, sessionOnly: false },
            { name: 'LD_LIBRARY_PATH', ignored: true, sessionOnly: false },
          ],
          restartRequired: false,
          storage: { backend: 'keychain', encryptionAvailable: true, plaintextOptIn: false, unreadable: 0 },
        },
        network: networkView({ exposed: true, address: '0.0.0.0:8080', lanAddresses: [FAKE_LAN], apiKeySet: true, apiKeyHint: { prefix: 'lai-', last4: '3f9a' } }),
      }
    case 'settings-no-keyring': // 8, Advanced, Linux with no keyring
      return {
        ...fixture('settings', now),
        storage: NO_KEYRING,
        envVars: { vars: [{ name: 'HF_TOKEN', ignored: false, sessionOnly: true }], restartRequired: false, storage: NO_KEYRING },
      }
    case 'first-choose': // 17
      return baseState({
        server: { state: 'not-installed', endpoint: ENDPOINT, logTail: [] },
        install: { kind: 'update-available', version: '4.2.1', tag: 'v4.2.1' },
        installDetails: { build: 'linux · amd64', size: 1.1e9, installDir: '~/.localai/bin' },
        settings: { ...DEFAULT_SETTINGS, showWelcome: true },
        servers: serversView({ rows: [localRow({ status: 'not-running', version: undefined })], firstRun: true }),
        webui: NO_VIEW,
      })
    case 'first-choose-tls': // 19, a failed test
      return { ...fixture('first-choose', now), testResult: { status: 'tls', reason: 'expired' } }
    case 'servers': // 20
      return baseState({
        server: STOPPED,
        servers: remoteDefault(),
        webui: remoteWebui(OFFICE.origin!, OFFICE.host),
        updateInfo: { checkedAt: now - 10 * 60000, autoCheck: true, previousVersion: '4.1.2', channel: 'stable', devConfirmed: false, remoteDefault: true },
      })
    case 'servers-empty': // 21
      return baseState({ servers: serversView() })
    case 'switcher': // 24, after a click on the switcher
      return baseState({
        servers: serversView({
          rows: [localRow({ isDefault: false, isCurrent: false, status: 'not-running' }), { ...OFFICE, isDefault: true, isCurrent: true }, LAB, { ...HOME, checkedAt: new Date(2026, 10, 14, 14, 21).getTime() }],
          defaultId: OFFICE.id,
        }),
        server: STOPPED,
        webui: remoteWebui(OFFICE.origin!, OFFICE.host),
      })
    case 'switching': // 25
      return baseState({
        server: STOPPED,
        servers: remoteDefault({ switching: { targetId: OFFICE.id, name: OFFICE.name, host: OFFICE.host, step: 'loading', startedAt: now - 2000 } }),
        webui: remoteWebui(OFFICE.origin!, OFFICE.host),
      })
    case 'remote-unreachable': // 27
      return baseState({
        server: STOPPED,
        servers: remoteDefault(),
        webui: remoteWebui(OFFICE.origin!, OFFICE.host, {
          layout: 'shell-only',
          home: 'shell-only',
          loaded: false,
          remote: { kind: 'unreachable', reason: 'timed out', at: new Date(2026, 10, 14, 14, 21).getTime(), nextAt: now + 22000 },
        }),
      })
    case 'remote-insecure': // 29
      return baseState({
        server: STOPPED,
        servers: remoteDefault({ rows: [localRow({ isDefault: false, isCurrent: false, status: 'not-running' }), { ...LAB, isDefault: true, isCurrent: true, status: 'connected' }], defaultId: LAB.id }),
        webui: remoteWebui(LAB.origin!, LAB.host),
      })
    case 'remote-keep-local': // 29
      return baseState({
        servers: remoteDefault({ keepLocal: true, rows: [localRow({ isDefault: false, isCurrent: false, status: 'running' }), { ...OFFICE, isDefault: true, isCurrent: true }] }),
        webui: remoteWebui(OFFICE.origin!, OFFICE.host),
      })
    case 'updates-remote': // 30
      return baseState({
        server: STOPPED,
        servers: remoteDefault(),
        webui: remoteWebui(OFFICE.origin!, OFFICE.host),
        updateInfo: { checkedAt: now - 10 * 60000, autoCheck: true, previousVersion: '4.1.2', channel: 'stable', devConfirmed: false, remoteDefault: true },
      })
    case 'dev-stale-flag': // a stale remoteDefault flag while This computer is the default
      return baseState({
        install: { kind: 'up-to-date', version: '3f9a1c2' },
        updateInfo: { ...devInfo(now), remoteDefault: true },
        servers: serversView(),
      })
    case 'dev': // 32, bar chip and the Updates tab
      return baseState({
        install: { kind: 'update-available', version: 'b71e04d', tag: 'dev-master', installed: '3f9a1c2' },
        updateInfo: devInfo(now),
        servers: serversView(),
      })
    case 'worker-settings': // 10
      return baseState({
        worker: { mode: 'worker', running: false, status: 'waiting-approval', config: workerConfig('ai.example.test'), tokenStored: true },
      })
    case 'remote': // 12
      return baseState({
        webui: webuiView({ target: { kind: 'remote', origin: 'https://gpu-box.example.test', host: 'gpu-box.example.test' }, barForced: false }),
        servers: serversView({
          rows: [localRow({ isDefault: false, isCurrent: false, status: 'not-running' }), remoteRow('srv_00000001', 'Office GPU box', 'https://gpu-box.example.test', { isDefault: true, isCurrent: true, status: 'connected' })],
          defaultId: 'srv_00000001',
        }),
      })
    case 'server-stopped': // 13
      return baseState({
        server: { state: 'stopped', endpoint: ENDPOINT, logTail: [] },
        webui: webuiView({ banner: 'stopped' }),
      })
    case 'sandbox-off': // 14
      return baseState({ sandbox: restricted('shown'), info: { version: '4.1.3', packaged: true, trayActive: true, sandboxed: false } })
    case 'bar-hidden': // 15
      return baseState({ statusBar: false })
    case 'worker-running': // 16
      return baseState({
        server: { state: 'stopped', endpoint: ENDPOINT, logTail: [] },
        worker: { mode: 'worker', running: true, status: 'connected', config: workerConfig('coord.example.test'), tokenStored: true },
      })
    case 'starting':
      return baseState({ server: { state: 'starting', endpoint: ENDPOINT, logTail: [] }, webui: NO_VIEW })
    case 'stopped':
      return baseState({ server: { state: 'stopped', endpoint: ENDPOINT, logTail: [] }, webui: NO_VIEW })
    case 'install-failed':
      return baseState({
        install: { kind: 'failed', reason: 'checksum-mismatch', message: 'sha256 mismatch', rolledBack: true },
      })
    case 'bar-hidden-failed':
      return { ...fixture('start-failed', now), statusBar: false }
    case 'config-recovered':
      return baseState({
        server: { state: 'stopped', endpoint: ENDPOINT, logTail: [] },
        webui: NO_VIEW,
        info: { version: '4.1.3', packaged: true, trayActive: true, sandboxed: true, configNotice: { status: 'recovered', reason: 'invalid JSON' } },
      })
  }
}
