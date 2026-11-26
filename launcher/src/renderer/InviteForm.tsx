// Add a server with an invite link. The main process pairs this computer with
// the server over P2P and saves it at a local address; the form only sends
// the link and shows the confirmation phrase the server shows too. Before
// that, it shows what the link says, and it never pairs by itself:
// a link that arrives from outside or from a scan only fills the field.
import { useEffect, useId, useRef, useState } from 'react'
import { Button, Callout, Icon } from '@localai/ui-kit'
import type { ServerRowView } from '../shared/types'
import { hasLocal, useBridge } from './bridge'
import { cleanInviteText, invitePreview, isInviteText, scanErrorMessage, type InvitePreviewView } from './invitePreview'
import { S } from './strings'
import { clockTime } from './viewModel'

type Phase = { kind: 'idle' } | { kind: 'pairing' } | { kind: 'paired'; phrase: string } | { kind: 'routes' }

export interface InviteFormProps {
  /** The first run: the paired server becomes the default and opens. */
  makeDefault?: boolean
  onDone: () => void
  onCancel: () => void
  /** Back to the address form. */
  onUseAddress: () => void
  /** A link that reached the app from outside; it fills the field and nothing more. */
  initialLink?: string
  /** Scans a QR code: its text, or undefined when cancelled. Shows "Scan QR code". */
  onScan?: () => Promise<string | undefined>
  /** A fixed clock (tests). Without it the countdown follows the real time. */
  now?: number
  /** The saved servers: an invite of one of them updates its routes instead of pairing. */
  servers?: ServerRowView[]
}

/** When the invite expires, in words: "today at 14:15", or the day as well. */
function expiryText(expiresAt: number, now: number): string {
  const at = new Date(expiresAt)
  const today = new Date(now)
  const sameDay = at.getFullYear() === today.getFullYear() && at.getMonth() === today.getMonth() && at.getDate() === today.getDate()
  const clock = clockTime(expiresAt)
  return sameDay ? S.invitePreview.today(clock) : S.invitePreview.onDay(at.toLocaleDateString(undefined, { month: 'short', day: 'numeric' }), clock)
}

function PreviewCard({ v, now }: { v: InvitePreviewView; now: number }) {
  const label = v.label || S.invitePreview.unnamed
  return (
    <section className="lnr-ipreview" aria-label={label} data-testid="invite-preview">
      <div className="lnr-ipreview__head">
        <span className="lnr-ipreview__icon" aria-hidden="true"><Icon name="server" /></span>
        <div className="lnr-grow">
          <strong className="lnr-ipreview__label">{label}</strong>
          <p className="lnr-ipreview__kind">{S.invitePreview.kind[v.kind]}</p>
        </div>
        <span className="lnr-badge lnr-badge--accent lnr-ipreview__left"><Icon name="clock" />{S.invitePreview.left(v.secondsLeft)}</span>
      </div>
      <dl className="lnr-ipreview__facts">
        <dt>{S.invitePreview.peer}</dt>
        <dd className="dk-mono">{v.peerShort}</dd>
        <dt>{S.invitePreview.routes}</dt>
        <dd>{S.invitePreview.routeCounts(v.routes.addrs, v.routes.relays)}</dd>
        <dt>{S.invitePreview.expires}</dt>
        <dd>{expiryText(v.expiresAt, now)}</dd>
      </dl>
    </section>
  )
}

export function InviteForm(p: InviteFormProps) {
  const b = useBridge()
  const ids = useId()
  const [link, setLink] = useState(() => p.initialLink?.trim() ?? '')
  const [name, setName] = useState('')
  const [allowOpen, setAllowOpen] = useState(false)
  const [phase, setPhase] = useState<Phase>({ kind: 'idle' })
  const [error, setError] = useState<string>()
  const [scanning, setScanning] = useState(false)
  const [clock, setClock] = useState(() => Date.now())
  const token = useRef(0)

  const at = p.now ?? clock
  const preview = invitePreview(link, at)
  const ok = preview && !('problem' in preview) ? preview : undefined
  const problem = preview && 'problem' in preview ? preview.problem : undefined

  // The countdown moves while a valid link is on show; it also turns into
  // "expired" (and Pair goes off) when the time is up.
  const ticking = !!ok && p.now === undefined
  useEffect(() => {
    if (!ticking) return
    const t = setInterval(() => setClock(Date.now()), 1000)
    return () => clearInterval(t)
  }, [ticking])

  const known = ok ? p.servers?.find((r) => r.peer !== undefined && r.peer === ok.peer) : undefined
  const open = !!ok?.openRelay
  const usable = !!ok && (!open || allowOpen) && name.trim().length <= 60

  // The consent to an open relay belongs to one link.
  const fill = (text: string) => {
    setLink(text)
    setAllowOpen(false)
    setClock(Date.now())
  }

  const submit = async () => {
    if (!usable || phase.kind === 'pairing') return
    const mine = ++token.current
    setError(undefined)
    setPhase({ kind: 'pairing' })
    const r = await b.addServerByInvite?.({
      link: link.trim(),
      ...(name.trim() && !known ? { name: name.trim() } : {}),
      ...(allowOpen ? { allowOpenRelay: true } : {}),
      ...(p.makeDefault ? { makeDefault: true } : {}),
    })
    if (token.current !== mine || !r) return
    if (r.ok) {
      setPhase(r.routesUpdated ? { kind: 'routes' } : { kind: 'paired', phrase: r.phrase })
      return
    }
    setPhase({ kind: 'idle' })
    // Without a local server there is nothing to install: the app itself lacks P2P.
    const base = r.reason === 'no-binary' && !hasLocal(b) ? S.clientApp.noP2P : (S.invite.failed[r.reason] ?? S.invite.failed['pair-failed'])
    setError(r.detail ? `${base} ${r.detail}` : base)
  }

  // A scan fills the field only with an invite; other text is refused and the field stays.
  const scan = async () => {
    if (!p.onScan || scanning || phase.kind === 'pairing') return
    setError(undefined)
    setScanning(true)
    let text: string | undefined
    try {
      text = await p.onScan()
    } catch (e) {
      setError(scanErrorMessage(e))
      return
    } finally {
      setScanning(false)
    }
    if (text === undefined) return
    const link = cleanInviteText(text)
    if (!isInviteText(link, p.now ?? Date.now())) {
      setError(S.invitePreview.notInvite)
      return
    }
    fill(link)
  }

  const linkId = `${ids}-link`
  const nameId = `${ids}-name`
  const openId = `${ids}-open`

  if (phase.kind === 'routes') {
    return (
      <section className="dk-card lnr-card lnr-form__card" aria-label={S.invite.routesTitle} data-testid="invite-routes">
        <div className="lnr-result lnr-result--ok" role="status">
          <Icon name="check-circle" className="lnr-result__icon" />
          <div className="lnr-grow">
            <strong>{S.invite.routesTitle}</strong>
            <p className="lnr-result__p">{S.invite.routesNote}</p>
          </div>
        </div>
        <div className="lnr-row lnr-dialog__actions">
          <span className="lnr-grow" />
          <Button variant="primary" onClick={p.onDone} data-testid="invite-done">{S.invite.done}</Button>
        </div>
      </section>
    )
  }

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
          // A link that came from outside needs no typing: no keyboard pops up for it.
          autoFocus={!p.initialLink}
          disabled={pairing}
          onChange={(e) => {
            fill(e.target.value)
            setError(undefined)
          }}
          data-testid="invite-link"
        />
      </div>
      {p.onScan && (
        <div className="lnr-row lnr-row--tight lnr-ipreview__tools">
          <Button size="sm" icon="camera" busy={scanning} disabled={pairing} onClick={() => void scan()}>{S.invitePreview.scan}</Button>
        </div>
      )}
      {problem && <p className="dk-field-error" data-testid="invite-problem">{S.invite.failed[problem]}</p>}

      {ok && <PreviewCard v={ok} now={at} />}

      {known && (
        <Callout title={S.invite.knownTitle(known.name)} testId="invite-known" className="lnr-ipreview__relay">
          <span className="lnr-form__note lnr-ipreview__relaynote">{S.invite.knownNote}</span>
        </Callout>
      )}

      {open && (
        <Callout tone="warn" title={S.invitePreview.openRelayTitle} testId="invite-open-relay" className="lnr-ipreview__relay">
          <span className="lnr-form__note lnr-ipreview__relaynote">{S.invite.openRelayNote}</span>
          <label htmlFor={openId} className="lnr-row lnr-row--tight">
            <input id={openId} type="checkbox" checked={allowOpen} disabled={pairing} onChange={(e) => setAllowOpen(e.target.checked)} />
            <span>{S.invite.openRelay}</span>
          </label>
        </Callout>
      )}

      {!known && (
        <>
          <label className="lnr-form__label" htmlFor={nameId}>{S.invite.name}</label>
          <div className="lnr-form__input">
            <input
              id={nameId}
              className="dk-input"
              value={name}
              maxLength={60}
              autoComplete="off"
              placeholder={ok?.label ? S.invitePreview.namePlaceholder(ok.label) : undefined}
              disabled={pairing}
              onChange={(e) => setName(e.target.value)}
            />
          </div>
          <p className="lnr-form__note">{S.invite.nameNote}</p>
        </>
      )}

      {pairing && (
        <div className="lnr-result lnr-result--busy" role="status" data-testid="invite-pairing">
          <Icon name="spinner" spin className="lnr-result__icon" />
          <div className="lnr-grow">
            <strong>{known ? S.invite.updatingRoutes : S.invite.pairing}</strong>
            {!known && <p className="lnr-result__p dk-muted">{S.invite.pairingNote}</p>}
          </div>
        </div>
      )}
      {error && <p className="dk-field-error" role="alert" data-testid="invite-error">{error}</p>}

      <div className="lnr-row lnr-dialog__actions">
        <button type="button" className="lnr-link" onClick={p.onUseAddress} disabled={pairing}>{S.invite.useAddress}</button>
        <span className="lnr-grow" />
        <Button onClick={p.onCancel} disabled={pairing}>{S.servers.cancel}</Button>
        <Button variant="primary" disabled={!usable} busy={pairing} onClick={() => void submit()} data-testid="invite-submit">{known ? S.invite.updateRoutes : S.invite.pair}</Button>
      </div>
    </div>
  )
}
