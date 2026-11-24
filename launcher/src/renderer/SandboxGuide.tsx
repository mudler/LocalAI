import { useEffect, useId, useState, type RefObject } from 'react'
import { Button, Callout, Icon } from '@localai/ui-kit'
import type { SandboxPlanView, SandboxView } from '../shared/types'
import { useBridge } from './bridge'
import { Code } from './parts'
import { Popover } from './Popover'
import { S } from './strings'
import { guideView } from './viewModel'

/**
 * The sandbox guide, opened upward from the "Sandbox off" chip. It shows the
 * plan (the profile text and the command) before anything is written. While
 * the profile attach rule is not decided (`setupAvailable` false, the
 * AppImage today), "Set up sandbox profile" is disabled with one sentence,
 * and no command is shown; "Not now" and the browser link stay.
 */
export function SandboxGuide({
  open,
  onClose,
  trigger,
  sandbox,
  onSandbox,
  onBrowserInstead,
}: {
  open: boolean
  onClose: () => void
  trigger: RefObject<HTMLElement | null>
  sandbox?: SandboxView
  onSandbox: (s: SandboxView) => void
  /** Missing when the bridge cannot open the WebUI in the browser. */
  onBrowserInstead?: () => void
}) {
  const b = useBridge()
  const titleId = useId()
  const notYetId = useId()
  const [plan, setPlan] = useState<SandboxPlanView>()
  const [removePlan, setRemovePlan] = useState<SandboxPlanView>()
  const [notAvailable, setNotAvailable] = useState(false)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!open) return
    setNotAvailable(false)
    setRemovePlan(undefined)
    let live = true
    void b.getSandboxSetupPlan('setup').then((p) => live && setPlan(p)).catch(() => undefined)
    return () => {
      live = false
    }
  }, [open, b])

  const g = guideView(sandbox, plan)

  const setUp = async () => {
    setBusy(true)
    try {
      const r = await b.runSandboxSetup()
      onSandbox(r)
      if (!r.setupAvailable) setNotAvailable(true)
    } finally {
      setBusy(false)
    }
  }
  const notNow = async () => {
    onSandbox(await b.dismissSandboxNotice())
    onClose()
    trigger.current?.focus()
  }
  const askRemove = async () => setRemovePlan(await b.getSandboxSetupPlan('remove'))
  const remove = async () => {
    setBusy(true)
    try {
      onSandbox(await b.runSandboxRemove())
      setRemovePlan(undefined)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Popover open={open} onClose={onClose} trigger={trigger} kind="popover" className="lnr-guide" role="dialog" labelledBy={titleId}>
      <div className="lnr-guide__head">
        <Icon name="shield" className="lnr-guide__icon" />
        <strong id={titleId} className="lnr-grow">{S.sandbox.title}</strong>
        <Button size="sm" variant="ghost" icon="close" aria-label={S.sandbox.close} onClick={onClose} />
      </div>
      <p className="dk-small lnr-guide__p">{g.why}</p>
      <p className="dk-small dk-muted lnr-guide__p">{S.sandbox.works}</p>

      {g.mode === 'restart' && (
        <Callout tone="ok" title={S.sandbox.restartTitle} actions={b.relaunch && <Button size="sm" variant="primary" onClick={() => void b.relaunch?.()}>{S.sandbox.restart}</Button>}>
          {S.sandbox.restartBody}
        </Callout>
      )}
      {g.mode === 'failed' && (
        <Callout tone="error" role="alert" title={S.sandbox.failedTitle}>
          {g.failure}
        </Callout>
      )}
      {g.mode === 'removed' && (
        <Callout tone="info" title={S.sandbox.removedTitle}>
          {S.sandbox.removedBody}
        </Callout>
      )}
      {notAvailable && (
        <Callout tone="info" role="status" title={S.sandbox.notAvailableTitle} testId="sandbox-not-available">
          {S.sandbox.notAvailableBody}
        </Callout>
      )}

      {removePlan ? (
        <>
          <div className="dk-eyebrow lnr-guide__label">{S.sandbox.commandLabel}</div>
          <Code label={S.sandbox.commandLabel}>{removePlan.steps.map((s) => s.command).join('\n')}</Code>
          <div className="lnr-row lnr-guide__actions">
            <Button size="sm" variant="danger" busy={busy} onClick={remove}>{S.sandbox.remove}</Button>
            <Button size="sm" onClick={() => setRemovePlan(undefined)}>{S.sandbox.notNow}</Button>
          </div>
        </>
      ) : (
        (g.canSetUp || g.mode === 'busy') && (
          <>
            {!g.setupAvailable ? (
              <p id={notYetId} className="dk-small dk-muted lnr-guide__p" data-testid="sandbox-setup-not-yet">
                {S.sandbox.setupNotYet}
              </p>
            ) : (
              <div className="dk-eyebrow lnr-guide__label">{S.sandbox.commandLabel}</div>
            )}
            {!g.setupAvailable ? null : g.planShown && plan ? (
              <div className="lnr-guide__plan">
                <Code label={S.sandbox.commandLabel}>{plan.steps.map((s) => s.command).join('\n')}</Code>
                {plan.profileText && (
                  <details className="lnr-guide__details">
                    <summary className="dk-small">{S.sandbox.profileLabel(plan.profilePath)}</summary>
                    <Code label={S.sandbox.profileLabel(plan.profilePath)}>{plan.profileText}</Code>
                  </details>
                )}
                <p className="dk-small dk-muted lnr-guide__p">{S.sandbox.passwordNote}</p>
              </div>
            ) : (
              <div className="lnr-placeholder dk-small dk-muted">
                <div className="dk-mono lnr-placeholder__cmd">{S.sandbox.placeholderCommand}</div>
                {S.sandbox.placeholderNote}
              </div>
            )}
            <div className="lnr-row lnr-guide__actions">
              {g.mode === 'busy' ? (
                <Button size="sm" variant="primary" busy>{g.busyLabel}</Button>
              ) : (
                <Button
                  size="sm"
                  variant="primary"
                  busy={busy}
                  disabled={!g.setupAvailable}
                  aria-describedby={g.setupAvailable ? undefined : notYetId}
                  onClick={setUp}
                >
                  {g.mode === 'failed' ? S.sandbox.tryAgain : S.sandbox.setUp}
                </Button>
              )}
              {g.canDismiss && <Button size="sm" onClick={notNow}>{S.sandbox.notNow}</Button>}
              {g.canRemove && <Button size="sm" variant="ghost" onClick={askRemove}>{S.sandbox.remove}</Button>}
            </div>
          </>
        )
      )}
      {!removePlan && g.mode === 'restart' && g.canRemove && (
        <div className="lnr-row lnr-guide__actions">
          <Button size="sm" variant="ghost" onClick={askRemove}>{S.sandbox.remove}</Button>
        </div>
      )}

      {onBrowserInstead && (
        <p className="dk-small dk-muted lnr-guide__foot">
          {S.sandbox.or}{' '}
          <button type="button" className="dk-link lnr-guide__link" onClick={onBrowserInstead}>
            {S.sandbox.browserLink}
          </button>
          {S.sandbox.later}
        </p>
      )}
    </Popover>
  )
}
