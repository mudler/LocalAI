// The screens the server and install state ask for: install, progress, start
// failed, first run, starting, stopped, and "open in browser" mode.
import { useEffect, useState } from 'react'
import { Button, Callout, Page, StatusChip } from '@localai/ui-kit'
import type { InstallStateView } from '../../shared/types'
import type { InstallDetailsView } from '../bridge'
import { useBridge } from '../bridge'
import { Card, Code, CopyButton, StepList } from '../parts'
import { S } from '../strings'
import {
  addressOf,
  portProcessLine,
  clockTime,
  firstRunView,
  formatBytes,
  progressView,
  startFailure,
  withPort,
  type ShellInput,
} from '../viewModel'
import logo from '../../../build/icon.png'

// ---- Install ------------------------------------------------------------------

export function InstallScreen({ install, details, onInstall }: { install?: InstallStateView; details?: InstallDetailsView; onInstall: () => void }) {
  const b = useBridge()
  const [checking, setChecking] = useState(false)
  const check = async () => {
    setChecking(true)
    try {
      await b.checkServerUpdate()
    } finally {
      setChecking(false)
    }
  }
  useEffect(() => {
    if (!install || install.kind === 'idle') void check()
    // Check once when the screen opens.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  const version = install?.kind === 'update-available' || install?.kind === 'up-to-date' ? install.version : undefined
  const busy = checking || install?.kind === 'checking'
  const failed = install?.kind === 'failed'
  const meta = [details?.build, details?.size ? formatBytes(details.size) : undefined].filter(Boolean).join(' · ')

  return (
    <div className="lnr-center lnr-center--hero" data-screen="install">
      <img className="lnr-hero" src={logo} alt="" />
      <h1 className="lnr-h">{S.install.title}</h1>
      <p className="lnr-sub lnr-sub--narrow">{S.install.sub}</p>
      <Card className="lnr-install-card">
        {failed ? (
          <Callout tone="error" role="alert" title={S.install.cannotFind} actions={<Button size="sm" onClick={check}>{S.install.tryAgain}</Button>}>
            {install.message}
          </Callout>
        ) : (
          <div className="lnr-row">
            <div className="lnr-grow">
              {version ? (
                <>
                  <strong>{S.install.version(version)}</strong> <StatusChip tone="ok" dot>{S.install.latest}</StatusChip>
                  {meta && <div className="dk-small dk-muted">{meta}</div>}
                </>
              ) : (
                <span className="dk-muted" role="status">{busy ? S.install.finding : S.install.cannotFind}</span>
              )}
            </div>
          </div>
        )}
      </Card>
      <Button variant="primary" size="lg" onClick={onInstall} disabled={busy || failed} data-testid="install-start">
        {S.install.installAndStart}
      </Button>
      {details?.installDir && <p className="dk-small dk-muted lnr-hero__foot">{S.install.installsTo(details.installDir)}</p>}
    </div>
  )
}

// ---- Installing / updating ----------------------------------------------------

export function ProgressScreen({
  install,
  updating,
  target,
  previous,
  rate,
  onSeen,
  onRetry,
  onLogs,
}: {
  install: InstallStateView
  updating: boolean
  target?: string
  previous?: string
  rate?: number
  onSeen: () => void
  onRetry: () => void
  onLogs: () => void
}) {
  const b = useBridge()
  const v = progressView(install, { updating, target, previous, rate })
  const [cancelling, setCancelling] = useState(false)
  return (
    <div className="lnr-center lnr-center--progress" data-screen="installing">
      <h1 className="lnr-h">{v.title}</h1>
      {v.sub && <p className="lnr-sub">{v.sub}</p>}
      {v.failure ? (
        <div className="lnr-progress-card">
          <Callout
            tone={v.failure.reason === 'cancelled' ? 'info' : 'error'}
            role="alert"
            title={S.progress.reasons[v.failure.reason]}
            actions={
              <>
                <Button size="sm" variant="primary" onClick={onRetry}>{S.progress.tryAgain}</Button>
                <Button size="sm" onClick={onLogs}>{S.progress.openLogs}</Button>
              </>
            }
          >
            {v.failure.rolledBack ? S.progress.failedRolledBack : null}
          </Callout>
          <div className="lnr-row lnr-progress-actions">
            <Button onClick={onSeen}>{S.back.plain}</Button>
          </div>
        </div>
      ) : (
        <>
          <Card className="lnr-progress-card">
            <div role="status" aria-live="polite">
              <StepList steps={v.steps} label={v.title} />
            </div>
          </Card>
          {v.done && <p role="status" className="lnr-progress-done">{v.done}</p>}
          {v.canCancel && (
            <div className="lnr-row lnr-progress-actions">
              <Button
                busy={cancelling}
                onClick={async () => {
                  setCancelling(true)
                  try {
                    await b.cancelInstall()
                  } finally {
                    setCancelling(false)
                  }
                }}
              >
                {S.progress.cancel}
              </Button>
            </div>
          )}
          <p className="dk-small dk-muted lnr-hero__foot">{v.keepLine}</p>
        </>
      )}
    </div>
  )
}

// ---- Start failed -------------------------------------------------------------

export function StartFailedScreen({ input, failedAt, version, onLogs, onSettings }: { input: ShellInput; failedAt?: number; version?: string; onLogs: () => void; onSettings: () => void }) {
  const b = useBridge()
  const f = startFailure(input.server, input.settings)
  const address = input.settings?.address ?? addressOf(input.server.endpoint)
  const [freePort, setFreePort] = useState<number | null>()
  const [busy, setBusy] = useState<'retry' | 'port' | 'reinstall' | 'process' | null>(null)
  const [portUser, setPortUser] = useState<string>()

  useEffect(() => {
    if (f.cause !== 'port-in-use' || !f.port || !b.findFreePort) return
    let live = true
    void b.findFreePort(f.port + 1).then((p) => live && setFreePort(p)).catch(() => undefined)
    return () => {
      live = false
    }
  }, [b, f.cause, f.port])

  const run = (k: 'retry' | 'port' | 'reinstall' | 'process', fn: () => Promise<unknown>) => async () => {
    setBusy(k)
    try {
      await fn()
    } finally {
      setBusy(null)
    }
  }
  const usePort = run('port', async () => {
    if (!freePort) return
    await b.saveSettings({ address: withPort(address, freePort) })
    await b.start()
  })
  const portFix = f.cause === 'port-in-use' && f.port !== undefined

  return (
    <Page className="lnr-screen" data-screen="start-failed">
      <Callout tone="error" role="alert" title={S.failed.bannerTitle} className="lnr-screen__banner">
        {f.message}
      </Callout>
      <div className="lnr-row lnr-screen__head">
        <div className="lnr-grow">
          <h1 className="lnr-h">{S.failed.title}</h1>
          <div className="dk-muted">{failedAt ? S.failed.failedAt(clockTime(failedAt)) : S.failed.failedNoTime}</div>
        </div>
        <Button variant={portFix && freePort ? 'secondary' : 'primary'} busy={busy === 'retry'} onClick={run('retry', () => b.start())}>
          {S.failed.tryAgain}
        </Button>
      </div>
      <Card title={S.failed.fixes} className="lnr-gap">
        {portFix && (
          <div className="lnr-fix">
            <div className="lnr-grow">
              <strong>{S.failed.otherPort}</strong>
              <div className="dk-small dk-muted">{freePort ? S.failed.otherPortFree(freePort, f.port!) : S.failed.otherPortManual}</div>
            </div>
            {freePort ? (
              <Button size="sm" variant="primary" busy={busy === 'port'} onClick={usePort}>{S.failed.useAndStart(freePort)}</Button>
            ) : (
              <Button size="sm" onClick={onSettings}>{S.failed.openNetwork}</Button>
            )}
          </div>
        )}
        {portFix && b.showPortProcess && (
          <div className="lnr-fix">
            <div className="lnr-grow">
              <strong>{S.failed.findProcess(f.port!)}</strong>
              <div className="dk-small dk-muted" role="status" data-testid="port-user">{portUser ?? S.failed.findProcessNote}</div>
            </div>
            <Button size="sm" busy={busy === 'process'} onClick={run('process', async () => setPortUser(portProcessLine(await b.showPortProcess!(f.port!))))}>
              {S.failed.showProcess}
            </Button>
          </div>
        )}
        <div className="lnr-fix">
          <div className="lnr-grow">
            <strong>{S.failed.reinstall(version)}</strong>
            <div className="dk-small dk-muted">{S.failed.reinstallNote}</div>
          </div>
          <Button size="sm" busy={busy === 'reinstall'} onClick={run('reinstall', () => b.installServer())}>{S.failed.reinstallButton}</Button>
        </div>
      </Card>
      <Card>
        <div className="lnr-row lnr-card__bar">
          <h3 className="dk-eyebrow lnr-grow">{S.failed.lastLines}</h3>
          <Button size="sm" onClick={onLogs}>{S.failed.openLogs}</Button>
          <CopyButton label={S.failed.copy} copied={S.failed.copied} onCopy={() => b.copyText(input.server.logTail.join('\n'))} />
        </div>
        <Code label={S.failed.lastLines}>
          {f.lines.length === 0
            ? S.failed.noLines
            : f.lines.map((l, i) => (
                <span key={i} className={l.error ? 'lnr-log__error' : undefined}>
                  {l.text}
                  {'\n'}
                </span>
              ))}
        </Code>
      </Card>
    </Page>
  )
}

// ---- First run ----------------------------------------------------------------

export function FirstRunScreen({ input, hardware }: { input: ShellInput; hardware?: string }) {
  const v = firstRunView(input, hardware)
  return (
    <div className="lnr-center lnr-center--firstrun" data-screen="first-run">
      <div className="lnr-firstrun">
        <div className="lnr-stepper" aria-label={S.firstRun.stepOf(3, 3, S.firstRun.openStep)}>
          <span className="lnr-step__mark lnr-step__mark--done" aria-hidden="true">✓</span>
          <span className="lnr-stepper__line" aria-hidden="true" />
          <span className="lnr-step__mark lnr-step__mark--done" aria-hidden="true">✓</span>
          <span className="lnr-stepper__line" aria-hidden="true" />
          <span className="lnr-step__mark lnr-step__mark--active" aria-hidden="true">3</span>
          <span className="dk-small dk-muted lnr-stepper__text">{S.firstRun.stepOf(3, 3, S.firstRun.openStep)}</span>
        </div>
        <h1 className="lnr-h">{v.title}</h1>
        <p className="lnr-sub">{v.sub}</p>
        <Card>
          <div role="status" aria-live="polite">
            <StepList steps={v.steps} label={v.title} />
          </div>
        </Card>
        <Card className="lnr-next">
          <div className="lnr-row">
            <span className="lnr-next__icon" aria-hidden="true">↓</span>
            <div className="lnr-grow">
              <strong>{S.firstRun.nextTitle}</strong>
              <div className="dk-small dk-muted">{S.firstRun.nextBody}</div>
            </div>
          </div>
        </Card>
      </div>
    </div>
  )
}

// ---- Starting, stopping, stopped, browser mode -------------------------------

export function StartingScreen({ stopping, address }: { stopping?: boolean; address: string }) {
  const label = stopping ? S.starting.stopping : S.starting.title
  return (
    <div className="lnr-center lnr-center--progress" data-screen={stopping ? 'stopping' : 'starting'}>
      <h1 className="lnr-h">{label}</h1>
      <p className="lnr-sub">{stopping ? S.starting.stoppingSub : S.starting.sub}</p>
      <Card className="lnr-progress-card">
        <div role="status" aria-live="polite">
          <StepList steps={[{ label: stopping ? S.starting.stoppingSub : S.firstRun.starting(address), status: 'active' }]} label={label} />
        </div>
      </Card>
    </div>
  )
}

export function StoppedScreen() {
  const b = useBridge()
  const [busy, setBusy] = useState(false)
  return (
    <div className="lnr-center lnr-center--hero" data-screen="stopped">
      <img className="lnr-hero" src={logo} alt="" />
      <h1 className="lnr-h">{S.stopped.title}</h1>
      <p className="lnr-sub">{S.stopped.sub}</p>
      <Button
        variant="primary"
        size="lg"
        busy={busy}
        onClick={async () => {
          setBusy(true)
          try {
            await b.start()
          } finally {
            setBusy(false)
          }
        }}
      >
        {S.stopped.start}
      </Button>
    </div>
  )
}

export function BrowserModeScreen({ address }: { address: string }) {
  const b = useBridge()
  return (
    <div className="lnr-center lnr-center--hero" data-screen="browser">
      <img className="lnr-hero" src={logo} alt="" />
      <h1 className="lnr-h">{S.browser.title}</h1>
      <p className="lnr-sub">{S.browser.sub}</p>
      <p className="dk-mono dk-muted lnr-sub">{address}</p>
      {b.openWebuiInBrowser && (
        <Button variant="primary" size="lg" iconEnd="external-link" onClick={() => void b.openWebuiInBrowser?.()}>
          {S.browser.open}
        </Button>
      )}
    </div>
  )
}
