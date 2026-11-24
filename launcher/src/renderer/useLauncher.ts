import { useCallback, useEffect, useRef, useState } from 'react'
import type {
  HardwareSummaryView,
  InstallStateView,
  LauncherInfo,
  LauncherSettings,
  SandboxView,
  ServersView,
  ServerSnapshot,
  ServerState,
  UpdaterStatus,
  WebuiViewState,
} from '../shared/types'
import type { Bridge, InstallDetailsView, UpdateInfoView, WindowSettingsView, WorkerView } from './bridge'
import { hardwareLine, INITIAL_TRACK, trackInstall, type InstallTrack, type ShellInput } from './viewModel'

/** A value read once and then pushed by the main process. `get` and `on` may be missing (optional channel). */
function useLive<T>(get: (() => Promise<T>) | undefined, on: ((cb: (v: T) => void) => () => void) | undefined): [T | undefined, (v: T) => void] {
  const [v, setV] = useState<T>()
  useEffect(() => {
    let live = true
    if (get) void get().then((x) => live && setV(x)).catch(() => undefined)
    const off = on?.((x) => setV(x))
    return () => {
      live = false
      off?.()
    }
    // The bridge does not change while the shell runs.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  return [v, setV]
}

export interface ShellData extends ShellInput {
  updateInfo?: UpdateInfoView
  installDetails?: InstallDetailsView
  hardware?: string
  /** When the server last went to failed. */
  failedAt?: number
  /** The version an install or update goes to, remembered from the last offer. */
  targetVersion?: string
  /** A server was installed when the running install started. */
  updating: boolean
  /** Bytes per second of the running download, once it is known. */
  downloadRate?: number
}

export interface ShellControls {
  setSettings(s: LauncherSettings): void
  setInstall(s: InstallStateView): void
  setSandbox(s: SandboxView): void
  setWindowSettings(s: WindowSettingsView): void
  setUpdateInfo(s: UpdateInfoView): void
  setWorker(s: WorkerView): void
  setStatusBarSaved(v: boolean): void
  markInstallFailureSeen(): void
  reloadSettings(): Promise<void>
}

/** Everything the shell shows, read from the bridge and kept current. Undefined until the server state is known. */
export function useShellData(b: Bridge): [ShellData | undefined, ShellControls] {
  const [server] = useLive<ServerSnapshot>(b.getServer, b.onServer)
  const [info] = useLive<LauncherInfo>(b.info, undefined)
  const [updater] = useLive<UpdaterStatus>(b.getUpdater, b.onUpdater)
  const [sandbox, setSandbox] = useLive<SandboxView>(b.getSandboxState, b.onSandbox)
  const [settings, setSettings] = useLive<LauncherSettings>(b.getSettings, undefined)
  const [statusBarSaved, setStatusBarSaved] = useLive<boolean>(b.getStatusBarVisible, b.onStatusBar)
  const [webui] = useLive<WebuiViewState>(b.getWebui, b.onWebui)
  const [servers] = useLive<ServersView>(b.getServers, b.onServers)
  const [worker, setWorker] = useLive<WorkerView>(b.getWorker, b.onWorker)
  const [windowSettings, setWindowSettings] = useLive<WindowSettingsView>(b.getWindowSettings, undefined)
  const [updateInfo, setUpdateInfo] = useLive<UpdateInfoView>(b.getUpdateInfo, undefined)
  const [installDetails] = useLive<InstallDetailsView>(b.getInstallDetails, undefined)
  const [hardwareView] = useLive<HardwareSummaryView>(b.getHardwareSummary, undefined)
  const hardware = hardwareView ? hardwareLine(hardwareView) : undefined

  const [installFailureSeen, setInstallFailureSeen] = useState(false)
  const failedAt = useRef<number | undefined>(undefined)
  if (server?.state === 'failed' && failedAt.current === undefined) failedAt.current = Date.now()
  if (server && server.state !== 'failed') failedAt.current = undefined

  // Every install value goes through trackInstall, in order, also when React
  // batches several events into one render (an offer, then the download).
  const serverState = useRef<ServerState | undefined>(undefined)
  serverState.current = server?.state
  const [install, setInstallRaw] = useState<InstallStateView>()
  const [track, setTrack] = useState<InstallTrack>(INITIAL_TRACK)
  const setInstall = useCallback((v: InstallStateView) => {
    setTrack((t) => trackInstall(t, v, serverState.current, Date.now()))
    if (v.kind === 'failed') setInstallFailureSeen(false)
    setInstallRaw(v)
  }, [])
  useEffect(() => {
    let live = true
    void b.getInstallState().then((v) => live && setInstall(v)).catch(() => undefined)
    const off = b.onInstall(setInstall)
    return () => {
      live = false
      off()
    }
  }, [b, setInstall])

  const reloadSettings = useCallback(async () => {
    setSettings(await b.getSettings())
  }, [b, setSettings])

  const controls: ShellControls = {
    setSettings,
    setInstall,
    setSandbox,
    setWindowSettings,
    setUpdateInfo,
    setWorker,
    setStatusBarSaved,
    markInstallFailureSeen: () => setInstallFailureSeen(true),
    reloadSettings,
  }

  if (!server) return [undefined, controls]
  return [
    {
      server,
      info,
      updater,
      install,
      sandbox,
      settings,
      statusBarSaved,
      webui,
      worker,
      servers,
      windowSettings,
      updateInfo,
      installDetails,
      hardware,
      // Before any check or install the install state does not say; the install details do.
      installedVersion: track.known ?? installDetails?.version,
      installFailureSeen,
      failedAt: failedAt.current,
      targetVersion: track.target,
      updating: track.updating,
      downloadRate: track.rate,
    },
    controls,
  ]
}
