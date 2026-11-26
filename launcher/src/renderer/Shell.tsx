import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { ShellInsets, ShellScreen } from '../shared/types'
import { Button, Callout, Icon } from '@localai/ui-kit'
import { FirstChooseScreen, FirstConnectScreen, RemoteUnreachableScreen, SwitchFailedCard, SwitchingCard } from './screens/ServerScreens'
import { InviteForm } from './InviteForm'
import { cleanInviteText, inviteScanner, isInviteText, scanErrorMessage } from './invitePreview'
import { ServerForm } from './ServerForm'
import type { ShellScreenId } from './bridge'
import { useBridge } from './bridge'
import type { MenuAction } from './BarMenu'
import { ApkUpdateSheet } from './screens/ApkUpdateSheet'
import { ConnectScreen } from './screens/ConnectScreen'
import { LogsScreen } from './screens/LogsScreen'
import { SettingsScreen, type SettingsTab } from './screens/SettingsScreen'
import type { ServersOpen } from './screens/ServersTab'
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
  const [serversOpen, setServersOpen] = useState<ServersOpen>()
  const [firstStep, setFirstStep] = useState<'choose' | 'connect' | 'computer'>('choose')
  // The first-connect screen (no local server): the choices, or the address or invite form.
  const [connectStep, setConnectStep] = useState<'choose' | 'address' | 'invite'>('choose')
  const [switchFailed, setSwitchFailed] = useState<{ name: string; id: string }>()
  // An invite text from outside (link, share sheet, QR scan), waiting until the shell knows its screen.
  const [incoming, setIncoming] = useState<{ text: string; seq: number }>()
  const incomingSeq = useRef(0)
  // The link that the first-connect invite step opened with, and the one that reopened Settings > Servers.
  const [firstInvite, setFirstInvite] = useState<{ link: string; seq: number }>()
  const [settingsSeq, setSettingsSeq] = useState(0)
  const [notice, setNotice] = useState<string>()
  const [apkSheet, setApkSheet] = useState(false)
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

  // A link or shared text that reaches the app. It only opens the invite form:
  // pairing always waits for a tap on Pair.
  useEffect(() => b.onInviteLink?.((text) => setIncoming({ text, seq: ++incomingSeq.current })), [b])
  useEffect(() => {
    if (!incoming || !base) return
    setIncoming(undefined)
    const link = cleanInviteText(incoming.text)
    if (!isInviteText(link, now ?? Date.now())) {
      setNotice(S.invitePreview.notInvite)
      return
    }
    setNotice(undefined)
    setPopover(null)
    if (base === 'first-connect') {
      setScreen(null)
      setConnectStep('invite')
      setFirstInvite({ link, seq: incoming.seq })
    } else {
      open('settings', 'servers')
      setServersOpen({ kind: 'invite', link })
      setSettingsSeq(incoming.seq)
    }
  }, [incoming, base, now, open])

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
      void b.start?.()
    }
    if (data?.install?.kind === 'failed') startAfterInstall.current = false
  }, [b, data?.install?.kind])

  // First run ends when LocalAI is on show; the welcome steps do not come back.
  useEffect(() => {
    if (base === 'web' && data?.settings?.showWelcome && data.server?.state === 'ready') {
      void b.saveSettings?.({ showWelcome: false }).then((r) => controls.setSettings(r.settings))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [b, base, data?.settings?.showWelcome, data?.server?.state])

  // The sandbox guide opens by itself once, over the WebUI, when the server is
  // first ready and the notice is not answered. "Not now" stores the launcher
  // version in the main process, and the notice (and this) stays quiet for it.
  useEffect(() => {
    if (!guideOpened.current && data?.sandbox?.notice.kind === 'shown' && data.server?.state === 'ready' && base === 'web' && screen === null) {
      guideOpened.current = true
      setPopover('guide')
    }
  }, [data?.sandbox?.notice.kind, data?.server?.state, base, screen])

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
  // The app runs no LocalAI server on this device (the mobile client).
  const clientOnly = !data.capabilities.localServer
  const apk = data.capabilities.selfUpdate === 'apk'
  // A client knows the version of the server it shows only from that server's last test.
  const aboutServerVersion = clientOnly ? currentRow(data)?.version : undefined
  const scanner = inviteScanner(b, data.capabilities)
  // A scan from the first-connect screen goes the same way as a link from outside.
  const scanFirst = async () => {
    if (!scanner) return
    setNotice(undefined)
    try {
      const text = await scanner()
      if (text !== undefined) setIncoming({ text, seq: ++incomingSeq.current })
    } catch (e) {
      setNotice(scanErrorMessage(e))
    }
  }
  const leaveInvite = () => {
    setFirstInvite(undefined)
    setConnectStep('choose')
  }
  const noticeStrip = notice ? (
    <div className="lnr-strip">
      <Callout
        tone="warn"
        role="status"
        title={notice}
        testId="invite-notice"
        actions={<Button size="sm" variant="ghost" icon="close" aria-label={S.invitePreview.close} onClick={() => setNotice(undefined)} />}
      />
    </div>
  ) : null
  const version = serverVersion(data.install, data.installedVersion)
  const showUpdateBanner = web && offer?.kind === 'server' && hiddenUpdate !== offer.version
  // The main process says which banner the kept page needs; without it, a stopped server is enough.
  const banner = !web || !local || clientOnly ? null : data.webui ? data.webui.banner : data.server?.state === 'stopped' ? 'stopped' : null
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
    if (offer?.kind === 'server') void b.installServer?.({ stopFirst: true })
    else if (offer?.kind === 'launcher') {
      // An APK installs only from the sheet's "Update now", after the consent step if Android needs it.
      if (apk && b.getApkUpdate) setApkSheet(true)
      else if (b.installLauncherUpdate) void b.installLauncherUpdate()
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
            key={`${settingsTab}-${settingsSeq}`}
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
        return <ConnectScreen endpoint={data.server?.endpoint ?? ''} />
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
              void b.installServer?.()
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
            onRetry={() => void b.installServer?.({ stopFirst: data.server?.state !== 'not-installed' })}
            onLogs={() => open('logs')}
          />
        )
      case 'start-failed':
        return data.server ? <StartFailedScreen input={{ ...data, server: data.server }} failedAt={data.failedAt} version={version} onLogs={() => open('logs')} onSettings={() => open('settings', 'network')} /> : null
      case 'first-run':
        return <FirstRunScreen input={data} hardware={data.hardware} />
      case 'starting':
        return <StartingScreen address={addressOf(data.server?.endpoint ?? '')} />
      case 'stopping':
        return <StartingScreen stopping address={addressOf(data.server?.endpoint ?? '')} />
      case 'stopped':
        return <StoppedScreen />
      case 'browser':
        return <BrowserModeScreen address={addressOf(data.server?.endpoint ?? '')} />
      case 'first-choose':
        return firstStep === 'connect' ? (
          <div className="lnr-center lnr-center--connectfirst">
            <ServerForm variant="page" servers={data.servers?.rows ?? []} makeDefault p2p={data.capabilities.p2p} client={!data.capabilities.localServer} now={now} onDone={() => setFirstStep('choose')} onCancel={() => setFirstStep('choose')} />
          </div>
        ) : (
          <FirstChooseScreen onComputer={() => setFirstStep('computer')} onServer={() => setFirstStep('connect')} sizeNote={data.installDetails?.size ? S.first.computerNote.replace('1.1 GB', formatBytes(data.installDetails.size)) : undefined} />
        )
      case 'first-connect':
        if (connectStep === 'address') {
          return (
            <div className="lnr-center lnr-center--connectfirst">
              <ServerForm variant="page" servers={data.servers?.rows ?? []} makeDefault p2p={data.capabilities.p2p} client={!data.capabilities.localServer} onScan={scanner} now={now} onDone={() => setConnectStep('choose')} onCancel={() => setConnectStep('choose')} />
            </div>
          )
        }
        if (connectStep === 'invite') {
          return (
            <div className="lnr-center lnr-center--connectfirst">
              <div className="lnr-form lnr-form--page">
                <button type="button" className="lnr-backlink" onClick={leaveInvite}>
                  <Icon name="arrow-left" /> <span>{S.servers.back}</span>
                </button>
                <h1 className="lnr-h">{S.invite.title}</h1>
                <section className="dk-card lnr-card lnr-form__card" aria-label={S.invite.title}>
                  <InviteForm
                    key={firstInvite?.seq ?? 0}
                    makeDefault
                    initialLink={firstInvite?.link}
                    onScan={scanner}
                    now={now}
                    onDone={leaveInvite}
                    onCancel={leaveInvite}
                    onUseAddress={() => {
                      setFirstInvite(undefined)
                      setConnectStep('address')
                    }}
                  />
                </section>
              </div>
            </div>
          )
        }
        return (
          <FirstConnectScreen
            p2p={data.capabilities.p2p}
            onAddress={() => setConnectStep('address')}
            onInvite={() => setConnectStep('invite')}
            onScan={scanner ? () => void scanFirst() : undefined}
          />
        )
      case 'remote-unreachable': {
        const cur = currentRow(data)
        return data.webui?.remote ? (
          <RemoteUnreachableScreen
            server={cur}
            status={data.webui.remote}
            now={tick}
            showTunnel={clientOnly}
            onSwitchLocal={clientOnly ? undefined : () => void switchTo('local')}
            onChooseOther={clientOnly ? () => setPopover('switcher') : undefined}
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
          {noticeStrip}
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
          {noticeStrip}
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
          {noticeStrip}
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
            {banner === 'stopped' && <Button size="sm" variant="primary" onClick={() => void b.start?.()}>{S.banner.start}</Button>}
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
            appLabel={clientOnly}
            applicationId={clientOnly ? data.info?.applicationId : undefined}
            serverVersion={clientOnly ? aboutServerVersion : version}
            hideUnknownServerVersion={clientOnly}
            appUpdate={apk}
            canOpenInBrowser={!!b.openWebuiInBrowser && (data.server?.state === 'ready' || !local)}
            canConnect={!clientOnly}
            onStop={() => void b.stop?.()}
            onRestart={() => void b.restart?.()}
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
      {apkSheet && data.info && (
        <ApkUpdateSheet
          installed={data.info.version}
          channel={S.apkUpdate.channelShort[data.updateInfo?.channel ?? 'stable']}
          updater={data.updater}
          onClose={() => setApkSheet(false)}
        />
      )}
      {/* Over the WebUI the main process draws the dot in its own view; here only over shell screens. */}
      {!barShown && !web && base !== 'first-choose' && <CornerDot tone={dotTone(data)} stateLabel={chip.label} onShow={() => void b.setStatusBarVisible(true).then(controls.setStatusBarSaved)} />}
    </div>
  )
}
