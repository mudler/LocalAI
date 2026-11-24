import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { ShellInsets, ShellScreen } from '../shared/types'
import { Button, Callout } from '@localai/ui-kit'
import { FirstChooseScreen, RemoteUnreachableScreen, SwitchFailedCard, SwitchingCard } from './screens/ServerScreens'
import { ServerForm } from './ServerForm'
import type { ShellScreenId } from './bridge'
import { useBridge } from './bridge'
import type { MenuAction } from './BarMenu'
import { ConnectScreen } from './screens/ConnectScreen'
import { LogsScreen } from './screens/LogsScreen'
import { SettingsScreen, type SettingsTab } from './screens/SettingsScreen'
import {
  BrowserModeScreen,
  FirstRunScreen,
  InstallScreen,
  ProgressScreen,
  StartFailedScreen,
  StartingScreen,
  StoppedScreen,
} from './screens/StateScreens'
import { WorkerRunningScreen, WorkerSettingsScreen } from './screens/WorkerScreens'
import { CornerDot, StatusBar, type BarPopover } from './StatusBar'
import { S } from './strings'
import { useShellData } from './useLauncher'
import {
  addressOf,
  announcement,
  barActions,
  barNeeds,
  barVisibility,
  baseScreen,
  chipView,
  currentRow,
  devChipShown,
  dotTone,
  formatBytes,
  isRemote,
  keepLocalNote,
  sandboxOff,
  serverVersion,
  updateOffer,
} from './viewModel'

/**
 * The shell: the status bar, and the screen that fills the area when the
 * WebUI view is not on show. A screen opened from the bar (Logs, Settings,
 * Connect) covers the WebUI view; "Back to LocalAI" uncovers it. The view is
 * never reloaded by this: the main process keeps it alive underneath.
 */
export function Shell({ now }: { now?: number }) {
  const b = useBridge()
  const [data, controls] = useShellData(b)
  const [screen, setScreen] = useState<ShellScreenId | null>(null)
  const [settingsTab, setSettingsTab] = useState<SettingsTab>('general')
  const [popover, setPopover] = useState<BarPopover>(null)
  const [hiddenUpdate, setHiddenUpdate] = useState<string>()
  const [serversOpen, setServersOpen] = useState<{ kind: 'add' } | { kind: 'edit'; id: string }>()
  const [firstStep, setFirstStep] = useState<'choose' | 'connect' | 'computer'>('choose')
  const [switchFailed, setSwitchFailed] = useState<{ name: string; id: string }>()
  const [tick, setTick] = useState(() => now ?? Date.now())
  const startAfterInstall = useRef(false)
  const guideOpened = useRef(false)
  const reported = useRef(false)
  const topRef = useRef<HTMLDivElement>(null)
  const bannerRef = useRef<HTMLDivElement>(null)
  const sentInsets = useRef('')

  const base0 = data ? baseScreen(data) : undefined
  // "Set up this computer" continues with Install; it saves nothing, because This computer is the default.
  const base = base0 === 'first-choose' && firstStep === 'computer' ? 'install' : base0
  const web = base === 'web' && screen === null
  const offer = data ? updateOffer(data) : null
  const chip = data ? chipView(data) : undefined
  const visibility = data ? barVisibility(data.statusBarSaved, barNeeds(data)) : undefined

  const open = useCallback((s: ShellScreenId | null, tab?: SettingsTab) => {
    if (tab) setSettingsTab(tab)
    setServersOpen(undefined)
    setScreen(s)
  }, [])

  // The recovery screen counts down to the next try, and the switch card says when it takes long.
  const ticking = !!data?.servers?.switching || !!data?.webui?.remote
  useEffect(() => {
    if (!ticking) return
    const t = setInterval(() => setTick(Date.now()), 1000)
    return () => clearInterval(t)
  }, [ticking])

  // The menu accelerator (Ctrl+Shift+S, Cmd+Shift+S on macOS) works while the WebUI has the focus.
  useEffect(() => b.onOpenSwitcher?.(() => setPopover('switcher')), [b])

  // An install or update takes the whole area; a bar screen gives way to it.
  useEffect(() => {
    if (base === 'installing' || base === 'install') setScreen(null)
    // A started worker shows its own status screen.
    if (base === 'worker') setScreen((s) => (s === 'worker-settings' ? null : s))
  }, [base])

  // The main process hides the WebUI view (alive, not reloaded) while a bar screen covers it.
  useEffect(() => {
    const covering: ShellScreen | null = screen === 'worker-settings' ? 'settings' : screen
    void b.setShellScreen?.(covering)
  }, [b, screen])

  // A popover of the bar opens upward over the WebUI: the main process raises the shell meanwhile.
  useEffect(() => {
    void b.setOverlay?.(popover !== null || !!data?.servers?.switching || !!switchFailed)
  }, [b, popover, data?.servers?.switching, switchFailed])

  // A fresh install does not start the server by itself; "Install and start" does.
  useEffect(() => {
    if (data?.install?.kind === 'done' && startAfterInstall.current) {
      startAfterInstall.current = false
      void b.start()
    }
    if (data?.install?.kind === 'failed') startAfterInstall.current = false
  }, [b, data?.install?.kind])

  // First run ends when LocalAI is on show; the welcome steps do not come back.
  useEffect(() => {
    if (base === 'web' && data?.settings?.showWelcome && data.server.state === 'ready') {
      void b.saveSettings({ showWelcome: false }).then((r) => controls.setSettings(r.settings))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [b, base, data?.settings?.showWelcome, data?.server.state])

  // The sandbox guide opens by itself once, over the WebUI, when the server is
  // first ready and the notice is not answered. "Not now" stores the launcher
  // version in the main process, and the notice (and this) stays quiet for it.
  useEffect(() => {
    if (!guideOpened.current && data?.sandbox?.notice.kind === 'shown' && data.server.state === 'ready' && base === 'web' && screen === null) {
      guideOpened.current = true
      setPopover('guide')
    }
  }, [data?.sandbox?.notice.kind, data?.server.state, base, screen])

  // Smoke test hook: the first real state is on screen.
  useEffect(() => {
    if (chip && !reported.current) {
      reported.current = true
      const frame = typeof requestAnimationFrame === 'function' ? requestAnimationFrame : (f: () => void) => setTimeout(f, 0)
      frame(() => b.firstRender(chip.label))
    }
  }, [b, chip])

  // Tell the main process how tall the strips around the WebUI view are, so
  // the view gives way to them. The bar's own height is fixed (shared/layout.ts).
  useLayoutEffect(() => {
    if (!b.setInsets || !base) return
    const send = () => {
      const insets: ShellInsets = web
        ? { top: topRef.current?.offsetHeight ?? 0, bottom: bannerRef.current?.offsetHeight ?? 0 }
        : { top: 0, bottom: 0 }
      const key = `${insets.top},${insets.bottom}`
      if (key === sentInsets.current) return
      sentInsets.current = key
      b.setInsets?.(insets)
    }
    send()
    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(send)
    if (topRef.current) ro.observe(topRef.current)
    if (bannerRef.current) ro.observe(bannerRef.current)
    return () => ro.disconnect()
  })

  if (!data || !base || !chip || !visibility) return <div className="lnr-shell" aria-busy="true" />

  const actions = barActions(data)
  const local = !isRemote(data)
  const version = serverVersion(data.install, data.installedVersion)
  const showUpdateBanner = web && offer?.kind === 'server' && hiddenUpdate !== offer.version
  // The main process says which banner the kept page needs; without it, a stopped server is enough.
  const banner = !web || !local ? null : data.webui ? data.webui.banner : data.server.state === 'stopped' ? 'stopped' : null
  // The main process also places the WebUI view around the bar, so its answer
  // counts; both apply the same rule, and either one asking shows the bar.
  const barShown = (visibility.visible || !!data.webui?.barVisible) && base !== 'first-choose'

  // Picking a server makes it the default. A server that does not answer is not switched to.
  const switchTo = async (id: string) => {
    setPopover(null)
    open(null)
    const row = data.servers?.rows.find((r) => r.id === id)
    const r = await b.setDefaultServer?.(id)
    if (r && !r.ok && r.reason === 'test-failed' && row) setSwitchFailed({ name: row.name, id })
  }
  const manageServers = (add?: boolean) => {
    open('settings', 'servers')
    if (add) setServersOpen({ kind: 'add' })
  }
  const update = () => {
    if (offer?.kind === 'server') void b.installServer({ stopFirst: true })
    else if (offer?.kind === 'launcher') {
      if (b.installLauncherUpdate) void b.installLauncherUpdate()
      else open('settings', 'general')
    }
  }
  const onMenu = (a: MenuAction) => {
    if (a === 'settings') open('settings', 'general')
    else if (a === 'connect') open('connect')
    else if (a === 'open-in-browser') void b.openWebuiInBrowser?.()
  }
  const browserInstead = b.setOpenInBrowser
    ? async () => {
        const r = await b.setOpenInBrowser?.(true)
        if (r) controls.setWindowSettings(r)
        setPopover(null)
      }
    : b.openWebuiInBrowser
      ? () => {
          void b.openWebuiInBrowser?.()
          setPopover(null)
        }
      : undefined

  const userScreen = () => {
    switch (screen) {
      case 'logs':
        return <LogsScreen />
      case 'settings':
        return (
          <SettingsScreen
            key={settingsTab}
            data={data}
            controls={controls}
            initialTab={settingsTab}
            now={now}
            onOpenGuide={() => setPopover('guide')}
            serversOpen={serversOpen}
            onSwitch={(id) => void switchTo(id)}
            onConnect={() => open('connect')}
            onWorker={() => open('worker-settings')}
          />
        )
      case 'connect':
        return <ConnectScreen endpoint={data.server.endpoint} />
      case 'worker-settings':
        return data.worker ? <WorkerSettingsScreen worker={data.worker} onWorker={controls.setWorker} /> : null
      default:
        return null
    }
  }

  const baseArea = () => {
    switch (base) {
      case 'install':
        return (
          <InstallScreen
            install={data.install}
            details={data.installDetails}
            onInstall={() => {
              startAfterInstall.current = true
              void b.installServer()
            }}
          />
        )
      case 'installing':
        return (
          <ProgressScreen
            install={data.install!}
            updating={data.updating}
            target={data.targetVersion}
            previous={data.installedVersion}
            rate={data.downloadRate}
            onSeen={controls.markInstallFailureSeen}
            onRetry={() => void b.installServer({ stopFirst: data.server.state !== 'not-installed' })}
            onLogs={() => open('logs')}
          />
        )
      case 'start-failed':
        return <StartFailedScreen input={data} failedAt={data.failedAt} version={version} onLogs={() => open('logs')} onSettings={() => open('settings', 'network')} />
      case 'first-run':
        return <FirstRunScreen input={data} hardware={data.hardware} />
      case 'starting':
        return <StartingScreen address={addressOf(data.server.endpoint)} />
      case 'stopping':
        return <StartingScreen stopping address={addressOf(data.server.endpoint)} />
      case 'stopped':
        return <StoppedScreen />
      case 'browser':
        return <BrowserModeScreen address={addressOf(data.server.endpoint)} />
      case 'first-choose':
        return firstStep === 'connect' ? (
          <div className="lnr-center lnr-center--connectfirst">
            <ServerForm variant="page" servers={data.servers?.rows ?? []} makeDefault onDone={() => setFirstStep('choose')} onCancel={() => setFirstStep('choose')} />
          </div>
        ) : (
          <FirstChooseScreen onComputer={() => setFirstStep('computer')} onServer={() => setFirstStep('connect')} sizeNote={data.installDetails?.size ? S.first.computerNote.replace('1.1 GB', formatBytes(data.installDetails.size)) : undefined} />
        )
      case 'remote-unreachable': {
        const cur = currentRow(data)
        return data.webui?.remote ? (
          <RemoteUnreachableScreen
            server={cur}
            status={data.webui.remote}
            now={tick}
            onSwitchLocal={() => void switchTo('local')}
            onEdit={() => {
              open('settings', 'servers')
              if (cur) setServersOpen({ kind: 'edit', id: cur.id })
            }}
          />
        ) : null
      }
      case 'worker':
        return data.worker ? <WorkerRunningScreen worker={data.worker} onWorker={controls.setWorker} /> : null
      default:
        return null
    }
  }

  return (
    <div className={`lnr-shell${web ? ' lnr-shell--web' : ''}`} data-base={base} data-screen-open={screen ?? undefined}>
      <div className="dk-sr-only" role="status" aria-live="polite" data-testid="live">
        {announcement(chip)}
      </div>

      {web && (
        <div ref={topRef} className="lnr-strips">
          {showUpdateBanner && offer?.kind === 'server' && (
            <div className="lnr-strip">
              <Callout
                tone="info"
                icon="arrow-up"
                role="status"
                title={S.banner.updateTitle(offer.version)}
                testId="update-banner"
                actions={
                  <>
                    {b.openReleaseNotes && <Button size="sm" onClick={() => void b.openReleaseNotes?.(offer.version)}>{S.banner.releaseNotes}</Button>}
                    <Button size="sm" variant="primary" onClick={update}>{S.banner.update}</Button>
                    <Button size="sm" variant="ghost" icon="close" aria-label={S.banner.close} onClick={() => setHiddenUpdate(offer.version)} />
                  </>
                }
              >
                <span className="dk-muted">{S.banner.updateBody(offer.installed)}</span>
              </Callout>
            </div>
          )}
        </div>
      )}

      {screen !== null ? (
        <main className="lnr-area">
          <div className="lnr-back">
            <Button size="sm" icon="arrow-left" onClick={() => open(null)} data-testid="back">
              {base === 'web' ? S.back.toLocalAI : S.back.plain}
            </Button>
            {base === 'web' && <span className="dk-small dk-muted">{S.back.note}</span>}
          </div>
          {userScreen()}
        </main>
      ) : web ? (
        <div className="lnr-webslot" data-testid="webui-slot" />
      ) : (
        <main className="lnr-area">
          {data.info?.configNotice && (
            <div className="lnr-strip">
              <Callout tone="warn" title={S.banner.configTitle} testId="config-notice">
                {S.banner.configBody}
              </Callout>
            </div>
          )}
          {baseArea()}
        </main>
      )}

      {data.servers?.switching && (
        <SwitchingCard switching={data.servers.switching} now={tick} onCancel={() => void b.cancelSwitch?.()} />
      )}
      {switchFailed && (
        <SwitchFailedCard
          name={switchFailed.name}
          onClose={() => setSwitchFailed(undefined)}
          onEdit={() => {
            const id = switchFailed.id
            setSwitchFailed(undefined)
            open('settings', 'servers')
            setServersOpen({ kind: 'edit', id })
          }}
        />
      )}

      <div className="lnr-bottom">
        {banner && (
          <div ref={bannerRef} className="lnr-strip lnr-strip--inset lnr-row" role="status" data-testid="stopped-banner" data-banner={banner}>
            <span className="lnr-grow">
              {banner === 'starting' ? (
                <>
                  <strong>{S.banner.startingTitle}</strong> <span className="dk-muted">{S.banner.startingBody}</span>
                </>
              ) : (
                <>
                  <strong>{S.banner.stoppedTitle}</strong> <span className="dk-muted">{S.banner.stoppedBody}</span>
                </>
              )}
            </span>
            {banner === 'stopped' && <Button size="sm" variant="primary" onClick={() => void b.start()}>{S.banner.start}</Button>}
          </div>
        )}
        {barShown && (
          <StatusBar
            chip={chip}
            actions={actions}
            offer={offer}
            sandboxOff={sandboxOff(data)}
            sandbox={data.sandbox}
            active={screen}
            popover={popover}
            setPopover={setPopover}
            launcherVersion={data.info?.version}
            serverVersion={version}
            canOpenInBrowser={!!b.openWebuiInBrowser && (data.server.state === 'ready' || !local)}
            onStop={() => void b.stop()}
            onRestart={() => void b.restart()}
            onLogs={() => open(screen === 'logs' ? null : 'logs')}
            onUpdate={update}
            servers={data.servers}
            current={currentRow(data)}
            devChip={devChipShown(data)}
            keepLocalNote={keepLocalNote(data)}
            channel={data.updateInfo?.channel}
            devBuild={data.updateInfo?.devBuild}
            onSwitch={(id) => void switchTo(id)}
            onAddServer={() => manageServers(true)}
            onManageServers={() => manageServers(false)}
            onMenu={onMenu}
            onSandbox={controls.setSandbox}
            onBrowserInstead={browserInstead}
          />
        )}
      </div>
      {/* Over the WebUI the main process draws the dot in its own view; here only over shell screens. */}
      {!barShown && !web && base !== 'first-choose' && <CornerDot tone={dotTone(data)} stateLabel={chip.label} onShow={() => void b.setStatusBarVisible(true).then(controls.setStatusBarSaved)} />}
    </div>
  )
}
