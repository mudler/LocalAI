// Add a server with an invite link. The main process pairs this computer with
// the server over P2P and saves it at a local address; the form only sends
// the link and shows the confirmation phrase the server shows too.
import { useId, useRef, useState } from 'react'
import { Button, Icon } from '@localai/ui-kit'
import { useBridge } from './bridge'
import { S } from './strings'

type Phase = { kind: 'idle' } | { kind: 'pairing' } | { kind: 'paired'; phrase: string }

export interface InviteFormProps {
  /** The first run: the paired server becomes the default and opens. */
  makeDefault?: boolean
  onDone: () => void
  onCancel: () => void
  /** Back to the address form. */
  onUseAddress: () => void
}

/** The link names an open relay (its `o` parameter). Only a hint: the main process decides. */
export function namesOpenRelay(link: string): boolean {
  const t = link.trim()
  const i = t.indexOf('?')
  if (!t.startsWith('localai://pair?') || i < 0) return false
  return (new URLSearchParams(t.slice(i + 1)).get('o') ?? '') !== ''
}

export function InviteForm(p: InviteFormProps) {
  const b = useBridge()
  const ids = useId()
  const [link, setLink] = useState('')
  const [name, setName] = useState('')
  const [allowOpen, setAllowOpen] = useState(false)
  const [phase, setPhase] = useState<Phase>({ kind: 'idle' })
  const [error, setError] = useState<string>()
  const token = useRef(0)

  const open = namesOpenRelay(link)
  const usable = link.trim().startsWith('localai://pair?') && (!open || allowOpen) && name.trim().length <= 60

  const submit = async () => {
    if (!usable || phase.kind === 'pairing') return
    const mine = ++token.current
    setError(undefined)
    setPhase({ kind: 'pairing' })
    const r = await b.addServerByInvite?.({
      link: link.trim(),
      ...(name.trim() ? { name: name.trim() } : {}),
      ...(allowOpen ? { allowOpenRelay: true } : {}),
      ...(p.makeDefault ? { makeDefault: true } : {}),
    })
    if (token.current !== mine || !r) return
    if (r.ok) {
      setPhase({ kind: 'paired', phrase: r.phrase })
      return
    }
    setPhase({ kind: 'idle' })
    const base = S.invite.failed[r.reason] ?? S.invite.failed['pair-failed']
    setError(r.detail ? `${base} ${r.detail}` : base)
  }

  const linkId = `${ids}-link`
  const nameId = `${ids}-name`
  const openId = `${ids}-open`

  if (phase.kind === 'paired') {
    return (
      <section className="dk-card lnr-card lnr-form__card" aria-label={S.invite.pairedTitle} data-testid="invite-paired">
        <div className="lnr-result lnr-result--ok" role="status">
          <Icon name="check-circle" className="lnr-result__icon" />
          <div className="lnr-grow">
            <strong>{S.invite.pairedTitle}</strong>
            <p className="lnr-result__p">{S.invite.phrase}</p>
            <p className="lnr-result__p dk-mono" data-testid="invite-phrase"><strong>{phase.phrase}</strong></p>
            <p className="lnr-result__p dk-muted">{S.invite.phraseNote}</p>
          </div>
        </div>
        <div className="lnr-row lnr-dialog__actions">
          <span className="lnr-grow" />
          <Button variant="primary" onClick={p.onDone} data-testid="invite-done">{S.invite.done}</Button>
        </div>
      </section>
    )
  }

  const pairing = phase.kind === 'pairing'
  return (
    <div className="lnr-form" data-testid="invite-form">
      <p className="lnr-sub">{S.invite.sub}</p>
      <label className="lnr-form__label" htmlFor={linkId}>{S.invite.link}</label>
      <div className="lnr-form__input">
        <textarea
          id={linkId}
          className="dk-input dk-input--mono"
          rows={3}
          value={link}
          placeholder={S.invite.linkPlaceholder}
          spellCheck={false}
          autoComplete="off"
          autoFocus
          disabled={pairing}
          onChange={(e) => {
            setLink(e.target.value)
            setError(undefined)
          }}
          data-testid="invite-link"
        />
      </div>

      <label className="lnr-form__label" htmlFor={nameId}>{S.invite.name}</label>
      <div className="lnr-form__input">
        <input id={nameId} className="dk-input" value={name} maxLength={60} autoComplete="off" disabled={pairing} onChange={(e) => setName(e.target.value)} />
      </div>
      <p className="lnr-form__note">{S.invite.nameNote}</p>

      {open && (
        <div className="lnr-form__input" data-testid="invite-open-relay">
          <label htmlFor={openId} className="lnr-row lnr-row--tight">
            <input id={openId} type="checkbox" checked={allowOpen} disabled={pairing} onChange={(e) => setAllowOpen(e.target.checked)} />
            <span>{S.invite.openRelay}</span>
          </label>
          <p className="lnr-form__note">{S.invite.openRelayNote}</p>
        </div>
      )}

      {pairing && (
        <div className="lnr-result lnr-result--busy" role="status" data-testid="invite-pairing">
          <Icon name="spinner" spin className="lnr-result__icon" />
          <div className="lnr-grow">
            <strong>{S.invite.pairing}</strong>
            <p className="lnr-result__p dk-muted">{S.invite.pairingNote}</p>
          </div>
        </div>
      )}
      {error && <p className="dk-field-error" role="alert" data-testid="invite-error">{error}</p>}

      <div className="lnr-row lnr-dialog__actions">
        <button type="button" className="lnr-link" onClick={p.onUseAddress} disabled={pairing}>{S.invite.useAddress}</button>
        <span className="lnr-grow" />
        <Button onClick={p.onCancel} disabled={pairing}>{S.servers.cancel}</Button>
        <Button variant="primary" disabled={!usable} busy={pairing} onClick={() => void submit()} data-testid="invite-submit">{S.invite.pair}</Button>
      </div>
    </div>
  )
}
