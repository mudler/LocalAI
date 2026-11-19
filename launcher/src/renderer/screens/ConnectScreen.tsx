import { useEffect, useId, useRef, useState } from 'react'
import { Button, Callout, Page, Segmented, Switch } from '@localai/ui-kit'
import { snippet as buildSnippet, type AppKind } from '../../shared/connectSnippets'
import type { NetworkPlanView, NetworkView } from '../bridge'
import { useBridge } from '../bridge'
import { Card, Code, CopyButton, Field, fieldIds } from '../parts'
import { SecretStorageNotice } from '../SecretStorageNotice'
import { S } from '../strings'
import { maskedKey } from '../viewModel'

export type { AppKind }

/** A shown key hides itself after this long. */
export const REVEAL_MS = 30_000

/**
 * Connect shows how other devices and apps reach LocalAI on this computer.
 * Saved servers live in Settings > Servers.
 *
 * The API key stays in the main process. This screen gets a hint (prefix and
 * last 4 characters); Show asks for the key once and hides it again after
 * REVEAL_MS; Copy asks the main process to put the key, or an example with
 * it, on the clipboard.
 */
export function ConnectScreen({ endpoint }: { endpoint: string }) {
  const b = useBridge()
  const ids = useId()
  const [net, setNet] = useState<NetworkView>()
  const [plan, setPlan] = useState<NetworkPlanView>()
  const [planError, setPlanError] = useState<string>()
  const [confirmRegen, setConfirmRegen] = useState(false)
  const [revealed, setRevealed] = useState<string>()
  const [revealNote, setRevealNote] = useState<string>()
  const hideTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const [kind, setKind] = useState<AppKind>('openai')
  const [netBusy, setNetBusy] = useState(false)

  useEffect(() => {
    let live = true
    void b.getNetwork?.().then((n) => live && setNet(n)).catch(() => undefined)
    return () => {
      live = false
      clearTimeout(hideTimer.current)
    }
  }, [b])

  const hide = () => {
    clearTimeout(hideTimer.current)
    setRevealed(undefined)
  }
  // A new key or no key: whatever was shown is stale.
  const applyNet = (n: NetworkView) => {
    hide()
    setNet(n)
  }

  const show = async () => {
    setRevealNote(undefined)
    const r = await b.revealApiKey?.()
    if (!r) return
    if (!r.ok) {
      if (r.reason === 'rate-limited') setRevealNote(S.connect.showLimited)
      return
    }
    setRevealed(r.key)
    clearTimeout(hideTimer.current)
    hideTimer.current = setTimeout(() => setRevealed(undefined), REVEAL_MS)
  }

  const askExpose = async (on: boolean) => {
    setPlanError(undefined)
    if (!on) {
      setPlan(undefined)
      const r = await b.setNetworkExposed?.(false)
      if (r?.ok) applyNet(r.network)
      return
    }
    // Show what will be exposed first; nothing changes until Allow.
    const p = await b.getNetworkPlan?.()
    if (p) setPlan(p)
  }

  const confirmExpose = async () => {
    if (!plan) return
    setNetBusy(true)
    try {
      const r = await b.setNetworkExposed?.(true, plan.planId)
      if (!r) return
      if (!r.ok) {
        setPlanError(S.connect.plan.expired)
        setPlan(undefined)
        return
      }
      setPlan(undefined)
      applyNet(r.network)
      // The plan said it restarts; the user confirmed that.
      if (r.network.restartRequired) {
        await b.restart()
        const n = await b.getNetwork?.()
        if (n) applyNet(n)
      }
    } finally {
      setNetBusy(false)
    }
  }

  const regenerate = async () => {
    setConfirmRegen(false)
    const r = await b.regenerateApiKey?.()
    if (r) applyNet(r)
  }

  const restartNow = async () => {
    await b.restart()
    const n = await b.getNetwork?.()
    if (n) applyNet(n)
  }

  const hasKey = !!net?.apiKeySet
  const keyText = revealed ?? maskedKey(net?.apiKeyHint, true)
  // The example shows a masked key; the copy has the real one, built in the main process.
  const shownSnippet = buildSnippet(kind, endpoint, hasKey ? maskedKey(net?.apiKeyHint) : undefined)
  const copySnippet = async () => {
    if (hasKey && b.copyApiKey) await b.copyApiKey(kind)
    else await b.copyText(buildSnippet(kind, endpoint, undefined))
  }
  const lan = net?.lanAddresses ?? []

  return (
    <Page className="lnr-screen" data-screen="connect">
      <h1 className="lnr-h">{S.connect.title}</h1>
      <p className="lnr-sub">{S.connect.sub}</p>

      {b.getNetwork && net && (
        <Card className="lnr-gap">
          {net.keyCreatedAtStart && (
            <Callout
              tone="info"
              role="status"
              title={S.connect.keyCreated}
              className="lnr-gap-sm"
              testId="key-created-notice"
              actions={
                b.dismissKeyNotice && (
                  <Button size="sm" onClick={async () => { const n = await b.dismissKeyNotice?.(); if (n) applyNet(n) }}>
                    {S.connect.keyCreatedOk}
                  </Button>
                )
              }
            />
          )}
          {b.setNetworkExposed && (
            <Field id={`${ids}-lan`} name={S.connect.allow} note={S.connect.allowNote}>
              <span className="lnr-grow" />
              <Switch checked={net.exposed} disabled={netBusy} onChange={(v) => void askExpose(v)} {...fieldIds(`${ids}-lan`)} />
            </Field>
          )}
          {plan && (
            <Callout
              tone="warn"
              role="alertdialog"
              aria-label={S.connect.plan.title}
              title={S.connect.plan.title}
              className="lnr-gap-sm lnr-plan"
              testId="network-plan"
              actions={
                <>
                  <Button size="sm" onClick={() => setPlan(undefined)}>{S.connect.plan.cancel}</Button>
                  <Button size="sm" variant="primary" busy={netBusy} onClick={confirmExpose}>{S.connect.plan.confirm}</Button>
                </>
              }
            >
              <p>{S.connect.plan.bind(plan.bindAddress)}</p>
              {plan.lanAddresses.length > 0 ? (
                <ul className="lnr-plan__list dk-mono">
                  {plan.lanAddresses.map((a) => (
                    <li key={a}>{a}/v1</li>
                  ))}
                </ul>
              ) : (
                <p>{S.connect.plan.noAddresses}</p>
              )}
              <p>{plan.createsKey ? S.connect.plan.keyNew : S.connect.plan.keyKept}</p>
              {plan.restartRequired && <p>{S.connect.plan.restart}</p>}
            </Callout>
          )}
          {planError && <p className="dk-field-error" role="alert">{planError}</p>}
          {net.exposed &&
            lan.map((a, i) => (
              <Field key={a} name={i === 0 ? S.connect.lanAddress : ''}>
                <span className="lnr-value dk-mono lnr-grow">{`${a}/v1`}</span>
                <CopyButton label={S.connect.copy} copied={S.connect.copied} onCopy={() => b.copyText(`${a}/v1`)} />
              </Field>
            ))}
          {hasKey && (
            <Field name={S.connect.apiKey}>
              <span className={`lnr-value dk-mono lnr-grow${revealed ? ' lnr-value--wrap' : ''}`} data-testid="api-key">{keyText}</span>
              {b.revealApiKey && (
                <Button size="sm" variant="ghost" icon={revealed ? 'eye-off' : 'eye'} onClick={() => (revealed ? hide() : void show())}>
                  {revealed ? S.connect.hide : S.connect.show}
                </Button>
              )}
              {b.copyApiKey && (
                <CopyButton label={S.connect.copy} copied={S.connect.copied} variant="ghost" onCopy={async () => void (await b.copyApiKey?.('key'))} />
              )}
              {b.regenerateApiKey && <Button size="sm" onClick={() => setConfirmRegen(true)}>{S.connect.regenerate}</Button>}
            </Field>
          )}
          {revealNote && <p className="dk-small dk-muted" role="status">{revealNote}</p>}
          {confirmRegen && (
            <Callout
              tone="warn"
              role="alertdialog"
              aria-label={S.connect.regenerateConfirm.title}
              title={S.connect.regenerateConfirm.title}
              className="lnr-gap-sm"
              actions={
                <>
                  <Button size="sm" onClick={() => setConfirmRegen(false)}>{S.connect.regenerateConfirm.cancel}</Button>
                  <Button size="sm" variant="primary" onClick={regenerate}>{S.connect.regenerateConfirm.confirm}</Button>
                </>
              }
            >
              {S.connect.regenerateConfirm.body}
            </Callout>
          )}
          {net.exposed && !hasKey && (
            <Callout
              tone="error"
              title={S.connect.exposedNoKey}
              className="lnr-gap-sm"
              actions={b.regenerateApiKey && <Button size="sm" variant="primary" onClick={regenerate}>{S.connect.createKey}</Button>}
            >
              {S.connect.exposedNoKeyBody}
            </Callout>
          )}
          {net.restartRequired && (
            <Callout tone="info" role="status" title={S.connect.restartNeeded} className="lnr-gap-sm" actions={<Button size="sm" onClick={restartNow}>{S.connect.restartNow}</Button>} />
          )}
          <SecretStorageNotice storage={net.storage} className="lnr-gap-sm" />
        </Card>
      )}

      <h2 className="dk-eyebrow lnr-section">{S.connect.setUpApp}</h2>
      <Segmented
        className="lnr-gap-sm"
        label={S.connect.tabsLabel}
        value={kind}
        onChange={(v) => setKind(v as AppKind)}
        options={(['openai', 'anthropic', 'curl'] as AppKind[]).map((k) => ({ id: k, label: S.connect.tabs[k] }))}
      />
      <Card className="lnr-gap">
        <Code label={S.connect.tabs[kind]}>{shownSnippet}</Code>
        <div className="lnr-row lnr-card__foot">
          <span className="dk-small dk-muted lnr-grow">{kind === 'curl' ? S.connect.curlNote : S.connect.exampleNote}</span>
          <CopyButton label={S.connect.copy} copied={S.connect.copied} onCopy={copySnippet} />
        </div>
      </Card>

    </Page>
  )
}
