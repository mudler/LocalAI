// The address, name and Test of one server. Used by the first-run "Connect to
// a server" screen (variant page) and by the Add and Edit sheet in Settings
// (variant sheet). The address is only text: the main process reduces it to an
// origin, tests it and saves it. Nothing secret is asked or kept.
import { useId, useRef, useState } from 'react'
import { Button, Icon } from '@localai/ui-kit'
import type { ServerRowView, TestResultView } from '../shared/types'
import { useBridge } from './bridge'
import { Dialog } from './Dialog'
import { S } from './strings'
import { addressHint, addressUsable, suggestName, testCard } from './serversUi'

type Phase = { kind: 'idle' } | { kind: 'testing'; host: string } | { kind: 'done'; result: TestResultView; host: string }

export interface ServerFormProps {
  variant: 'page' | 'sheet'
  servers: ServerRowView[]
  /** Editing this saved server. */
  editing?: ServerRowView
  /** The first run: Connect makes the server the default and opens it. */
  makeDefault?: boolean
  onDone: () => void
  onCancel: () => void
  /** The sheet's Remove… button (edit only). */
  onRemove?: () => void
}

const hostOf = (text: string) => {
  try {
    return new URL(text.trim()).host
  } catch {
    return text.trim()
  }
}

export function ServerForm(p: ServerFormProps) {
  const b = useBridge()
  const ids = useId()
  const [address, setAddress] = useState(p.editing?.origin ?? '')
  const [name, setName] = useState(p.editing?.name ?? '')
  const [touched, setTouched] = useState(!!p.editing)
  const [phase, setPhase] = useState<Phase>({ kind: 'idle' })
  const [error, setError] = useState<string>()
  const [details, setDetails] = useState(false)
  const [confirmDiscard, setConfirmDiscard] = useState(false)
  const token = useRef(0)

  const hint = addressHint(address, p.servers, p.editing?.id)
  const usable = addressUsable(hint, address)
  const shownName = touched ? name : suggestName(address)
  const nameOk = shownName.trim().length >= 1 && shownName.trim().length <= 60
  const dirty = p.editing ? address.trim() !== (p.editing.origin ?? '') || shownName.trim() !== p.editing.name : address.trim() !== '' || touched
  const testing = phase.kind === 'testing'

  const runTest = async () => {
    if (!usable) return
    const mine = ++token.current
    setError(undefined)
    setDetails(false)
    setPhase({ kind: 'testing', host: hostOf(address) })
    const r = await b.testServer?.({ address })
    if (token.current !== mine) return
    if (!r || !r.ok) {
      setPhase({ kind: 'idle' })
      setError(S.hint.invalid)
      return
    }
    setPhase({ kind: 'done', result: r.result, host: r.host })
  }
  const cancelTest = () => {
    token.current++
    setPhase({ kind: 'idle' })
  }

  const submit = async (skipTest: boolean) => {
    if (!usable || !nameOk) return
    const mine = ++token.current
    setError(undefined)
    if (!skipTest) setPhase({ kind: 'testing', host: hostOf(address) })
    const moved = !p.editing || address.trim() !== p.editing.origin
    const r = p.editing
      ? await b.updateServer?.({ id: p.editing.id, name: shownName.trim(), ...(moved ? { address: address.trim() } : {}), ...(skipTest ? { skipTest: true } : {}) })
      : await b.addServer?.({ name: shownName.trim(), address: address.trim(), ...(p.makeDefault ? { makeDefault: true } : {}), ...(skipTest ? { skipTest: true } : {}) })
    if (token.current !== mine || !r) return
    if (r.ok) {
      setPhase({ kind: 'idle' })
      p.onDone()
      return
    }
    if (r.reason === 'test-failed' && r.test) setPhase({ kind: 'done', result: r.test, host: hostOf(address) })
    else {
      setPhase({ kind: 'idle' })
      setError(S.servers.failed[r.reason] ?? S.hint.invalid)
    }
  }

  const close = () => (p.variant === 'sheet' && dirty ? setConfirmDiscard(true) : p.onCancel())
  const card = phase.kind === 'done' ? testCard(phase.result, phase.host) : undefined
  // Save without testing is a Settings choice. The first run only connects after a passing test.
  const canSkip = p.variant === 'sheet' && (phase.kind === 'done' && card?.tone === 'error')

  const addrId = `${ids}-addr`
  const nameId = `${ids}-name`
  const fields = (
    <>
      <label className="lnr-form__label" htmlFor={addrId}>{S.servers.address}</label>
      <div className={`lnr-form__input${hint?.kind === 'warn' ? ' is-warn' : hint?.kind === 'ok' ? ' is-ok' : hint?.kind === 'error' ? ' is-error' : ''}`}>
        <input
          id={addrId}
          className="dk-input dk-input--mono"
          value={address}
          placeholder={S.servers.addressPlaceholder}
          spellCheck={false}
          autoComplete="off"
          autoFocus={p.variant === 'page'}
          aria-describedby={hint ? `${ids}-hint` : undefined}
          aria-invalid={hint?.kind === 'error' ? true : undefined}
          onChange={(e) => {
            setAddress(e.target.value)
            setPhase({ kind: 'idle' })
            setError(undefined)
          }}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              void runTest()
            }
          }}
        />
        {hint?.kind === 'warn' && <Icon name="warning" className="lnr-form__flag" />}
        {hint?.kind === 'ok' && <Icon name="check-circle" className="lnr-form__flag" />}
      </div>
      <div id={`${ids}-hint`} className="lnr-hint" role="status" aria-live="polite" data-testid="address-hint">
        {hint && (
          <p className={`lnr-hint__p lnr-hint__p--${hint.kind}`} data-kind={hint.kind}>
            {hint.kind === 'warn' ? <Icon name="warning" /> : <span className={`lnr-hint__tag lnr-hint__tag--${hint.kind}`}>{S.hint.kinds[hint.kind]}</span>}
            <span>
              {hint.title && <strong>{hint.title} </strong>}
              {hint.message}{' '}
              {hint.action && (
                <button type="button" className="lnr-link" onClick={() => setAddress(hint.action!.apply)}>{hint.action.label}</button>
              )}
              {hint.editId && (
                <button
                  type="button"
                  className="lnr-link"
                  onClick={() => {
                    // Opening the saved server in the sheet is the list's job; the form only closes.
                    p.onCancel()
                  }}
                >
                  {S.hint.editIt}
                </button>
              )}
            </span>
          </p>
        )}
      </div>

      <label className="lnr-form__label" htmlFor={nameId}>{S.servers.name}</label>
      <div className="lnr-form__input">
        <input
          id={nameId}
          className="dk-input"
          value={shownName}
          maxLength={60}
          autoFocus={p.variant === 'sheet'}
          autoComplete="off"
          aria-describedby={`${nameId}-note`}
          aria-invalid={touched && !nameOk ? true : undefined}
          onChange={(e) => {
            setName(e.target.value)
            setTouched(true)
          }}
        />
        {!touched && shownName && <span className="lnr-form__badge">{S.servers.suggested}</span>}
      </div>
      <p id={`${nameId}-note`} className="lnr-form__note">{touched && !nameOk ? S.servers.nameProblem : S.servers.nameNote}</p>

      {phase.kind === 'idle' && (
        <div className="lnr-row lnr-form__test">
          <Button onClick={() => void runTest()} disabled={!usable} data-testid="test-button">{S.servers.test}</Button>
          <span className="dk-small dk-muted">{S.servers.testNote}</span>
        </div>
      )}
      {error && <p className="dk-field-error" role="alert">{error}</p>}
      {phase.kind === 'testing' && (
        <div className="lnr-result lnr-result--busy" role="status" data-testid="test-result" data-state="testing">
          <Icon name="spinner" spin className="lnr-result__icon" />
          <div className="lnr-grow">
            <strong>{S.testing(phase.host)}</strong>
            <p className="lnr-result__p dk-muted">{S.testingNote}</p>
            <Button size="sm" onClick={cancelTest}>{S.testResult.cancel}</Button>
          </div>
        </div>
      )}
      {card && phase.kind === 'done' && (
        <div className={`lnr-result lnr-result--${card.tone}`} role="status" data-testid="test-result" data-state={phase.result.status}>
          <Icon name={card.icon} className="lnr-result__icon" />
          <div className="lnr-grow">
            <strong>{card.title}</strong>
            <p className="lnr-result__p">{card.body}</p>
            {card.next && <p className="lnr-result__p"><strong>{S.testResult.next}</strong> {card.next}</p>}
            {phase.result.status === 'ok' && <p className="lnr-result__p dk-muted">{S.testResult.testedNow}</p>}
            <div className="lnr-row lnr-row--tight lnr-result__actions">
              {card.actions.includes('edit') && (
                <Button size="sm" onClick={() => document.getElementById(addrId)?.focus()}>{S.testResult.editAddress}</Button>
              )}
              {card.actions.includes('again') && <Button size="sm" onClick={() => void runTest()}>{phase.result.status === 'ok' ? S.servers.testAgain : S.servers.testAgain}</Button>}
              {card.actions.includes('details') && (
                <Button size="sm" aria-expanded={details} onClick={() => setDetails(!details)}>{details ? S.testResult.hideDetails : S.testResult.showDetails}</Button>
              )}
            </div>
            {details && phase.result.status === 'tls' && <p className="lnr-result__p dk-small dk-mono">{phase.result.reason}</p>}
          </div>
        </div>
      )}
    </>
  )

  const primaryLabel = p.variant === 'page' ? S.servers.connect : S.servers.save
  const actions = (
    <>
      {canSkip && <Button onClick={() => void submit(true)}>{S.servers.saveWithoutTest}</Button>}
      <Button onClick={close} data-testid="form-cancel">{S.servers.cancel}</Button>
      <Button variant="primary" disabled={!usable || !nameOk || testing || (!!p.editing && !dirty)} busy={testing} onClick={() => void submit(false)} data-testid="form-submit">
        {primaryLabel}
      </Button>
    </>
  )

  if (p.variant === 'page') {
    return (
      <div className="lnr-form lnr-form--page">
        <button type="button" className="lnr-backlink" onClick={p.onCancel}>
          <Icon name="arrow-left" /> <span>{S.servers.back}</span>
        </button>
        <h1 className="lnr-h">{S.servers.connectTitle}</h1>
        <p className="lnr-sub">{S.servers.connectSub}</p>
        <section className="dk-card lnr-card lnr-form__card" aria-label={S.servers.connectTitle}>{fields}</section>
        <div className="lnr-row lnr-form__foot">
          <p className="lnr-form__lock lnr-grow"><Icon name="lock" /> <span>{S.servers.noSecret}</span></p>
          {actions}
        </div>
      </div>
    )
  }

  const titleId = `${ids}-title`
  return (
    <>
      <Dialog title={p.editing ? S.servers.editTitle : S.servers.addTitle} titleId={titleId} onClose={close} initialFocus={`#${nameId}`} className="lnr-dialog--form" testId="server-sheet">
        <div className="lnr-form">{fields}</div>
        {p.editing?.isDefault && (
          <p className="lnr-form__default"><span className="lnr-badge lnr-badge--accent">{S.servers.isDefault}</span> <span>{S.servers.defaultNote}</span></p>
        )}
        <p className="lnr-form__lock"><Icon name="lock" /> <span>{S.servers.noSecretShort}</span></p>
        <div className="lnr-row lnr-dialog__actions">
          {p.onRemove && <Button variant="danger-ghost" onClick={p.onRemove} data-testid="sheet-remove">{S.servers.remove}</Button>}
          <span className="lnr-grow" />
          {actions}
        </div>
      </Dialog>
      {confirmDiscard && (
        <Dialog title={S.servers.discard} titleId={`${ids}-discard`} role="alertdialog" onClose={() => setConfirmDiscard(false)} initialFocus="[data-keep]" testId="discard-dialog">
          <p className="lnr-dialog__p">{S.servers.discardBody}</p>
          <div className="lnr-row lnr-dialog__actions">
            <span className="lnr-grow" />
            <Button data-keep onClick={() => setConfirmDiscard(false)}>{S.servers.keepEditing}</Button>
            <Button variant="danger" onClick={p.onCancel}>{S.servers.discardAction}</Button>
          </div>
        </Dialog>
      )}
    </>
  )
}
