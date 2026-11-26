// The screens of the server model: the first question, the recovery screen for
// a remote that does not answer, and the card that shows a switch under way.
import { useEffect, useId, useRef, useState } from 'react'
import { Button, Icon } from '@localai/ui-kit'
import type { RemoteStatusView, ServerRowView, SwitchingView } from '../../shared/types'
import { useBridge } from '../bridge'
import { tunnelLine } from '../serversUi'
import { S } from '../strings'
import { clockTime } from '../viewModel'
import logo from '../../../build/icon.png'

/** Screen 17: "Where should LocalAI run?" Two cards, the left one is the first stop of Tab. */
export function FirstChooseScreen({ onComputer, onServer, sizeNote }: { onComputer: () => void; onServer: () => void; sizeNote?: string }) {
  return (
    <div className="lnr-center lnr-center--firstchoose" data-screen="first-choose">
      <img className="lnr-hero" src={logo} alt="" />
      <h1 className="lnr-h">{S.first.title}</h1>
      <p className="lnr-sub lnr-sub--narrow">{S.first.sub}</p>
      <div className="lnr-choice" role="group" aria-label={S.first.groupLabel}>
        <section className="lnr-choice__card lnr-choice__card--primary" aria-labelledby="first-computer">
          <span className="lnr-choice__icon"><Icon name="monitor" /></span>
          <h2 id="first-computer" className="lnr-choice__title">{S.first.computerTitle}</h2>
          <p className="lnr-choice__p">{S.first.computerBody}</p>
          <p className="lnr-choice__p">{sizeNote ?? S.first.computerNote}</p>
          <Button variant="primary" autoFocus onClick={onComputer} data-testid="first-computer-button">{S.first.computerAction}</Button>
        </section>
        <section className="lnr-choice__card" aria-labelledby="first-server">
          <span className="lnr-choice__icon"><Icon name="server" /></span>
          <h2 id="first-server" className="lnr-choice__title">{S.first.serverTitle}</h2>
          <p className="lnr-choice__p">{S.first.serverBody}</p>
          <p className="lnr-choice__p">{S.first.serverNote}</p>
          <Button onClick={onServer} data-testid="first-server-button">{S.first.serverAction}</Button>
        </section>
      </div>
      <p className="lnr-first__foot dk-small dk-muted"><Icon name="lock" /> <span>{S.first.foot}</span></p>
    </div>
  )
}

/**
 * The first screen of an app with no local server: add a server to use. The
 * invite choice needs P2P; the scan choice needs a camera (the caller passes
 * `onScan` only where the platform can scan).
 */
export function FirstConnectScreen({ p2p, onAddress, onInvite, onScan }: { p2p: boolean; onAddress(): void; onInvite(): void; onScan?: () => void }) {
  const ids = useId()
  const choice = (key: string, icon: string, title: string, note: string, run: () => void, first = false) => (
    <button
      type="button"
      className={`lnr-choice__card${first ? ' lnr-choice__card--primary' : ''}`}
      aria-labelledby={`${ids}-${key}`}
      aria-describedby={`${ids}-${key}-note`}
      autoFocus={first}
      onClick={run}
      data-testid={`first-connect-${key}`}
    >
      <span className="lnr-choice__icon"><Icon name={icon} /></span>
      <span id={`${ids}-${key}`} className="lnr-choice__title">{title}</span>
      <span id={`${ids}-${key}-note`} className="lnr-choice__p">{note}</span>
    </button>
  )
  return (
    <div className="lnr-center lnr-center--firstchoose" data-screen="first-connect">
      <img className="lnr-hero" src={logo} alt="" />
      <h1 className="lnr-h">{S.firstConnect.title}</h1>
      <p className="lnr-sub lnr-sub--narrow">{S.firstConnect.sub}</p>
      <div className="lnr-choice lnr-choice--list" role="group" aria-label={S.firstConnect.title}>
        {choice('address', 'globe', S.firstConnect.addressTitle, S.firstConnect.addressNote, onAddress, true)}
        {p2p && choice('invite', 'link', S.firstConnect.inviteTitle, S.firstConnect.inviteNote, onInvite)}
        {onScan && choice('scan', 'camera', S.firstConnect.scanTitle, S.firstConnect.scanNote, onScan)}
      </div>
      <p className="lnr-first__foot dk-small dk-muted"><Icon name="lock" /> <span>{S.firstConnect.foot}</span></p>
    </div>
  )
}

/**
 * Screen 27: the remote default does not answer. Three equal paths; the
 * launcher retries by itself. Without a local server (`onChooseOther` set) the
 * second path opens the server switcher instead of switching to This computer.
 */
export function RemoteUnreachableScreen({
  server,
  status,
  now,
  onSwitchLocal,
  onChooseOther,
  onEdit,
  showTunnel = false,
}: {
  server?: ServerRowView
  status: RemoteStatusView
  now: number
  /** Show the P2P forwarder state and error of the server as text (a client-only app). */
  showTunnel?: boolean
  /** Switch to This computer; absent without a local server, where `onChooseOther` takes its place. */
  onSwitchLocal?: () => void
  onChooseOther?: () => void
  onEdit: () => void
}) {
  const b = useBridge()
  const [details, setDetails] = useState(false)
  const [busy, setBusy] = useState(false)
  const name = server?.name ?? S.chip.remote
  const host = server?.host ?? ''
  const at = clockTime(status.at)
  const secs = Math.max(0, Math.ceil((status.nextAt - now) / 1000))
  const why = status.reason
  return (
    <div className="lnr-center lnr-center--unreach" data-screen="remote-unreachable">
      <div className="lnr-banner lnr-banner--error" role="alert">
        <Icon name="alert-circle" />
        <div>
          <strong>{S.unreach.title(name)}</strong>
          <p className="lnr-banner__p">{why === 'timed out' ? S.unreach.body(host, at, why) : S.unreach.bodyOther(host, at, why)}</p>
          {showTunnel && server?.p2p && <p className="lnr-banner__p" data-testid="unreach-tunnel">{tunnelLine(server.p2p)}</p>}
        </div>
      </div>
      <h1 className="lnr-h lnr-h--mid">{S.unreach.heading}</h1>
      <p className="lnr-sub lnr-sub--narrow">{S.unreach.sub}</p>
      <section className="dk-card lnr-card lnr-paths" aria-label={S.unreach.heading}>
        <div className="lnr-path">
          <div className="lnr-grow"><strong>{S.unreach.retryTitle}</strong><p className="lnr-path__p dk-muted">{S.unreach.retryNote}</p></div>
          <Button
            variant="primary"
            autoFocus
            busy={busy}
            onClick={async () => {
              setBusy(true)
              try {
                await b.retryRemote?.()
              } finally {
                setBusy(false)
              }
            }}
            data-testid="retry-button"
          >
            {S.unreach.retry}
          </Button>
        </div>
        {onChooseOther ? (
          <div className="lnr-path">
            <div className="lnr-grow"><strong>{S.unreach.chooseOther}</strong><p className="lnr-path__p dk-muted">{S.unreach.chooseOtherNote(name)}</p></div>
            <Button onClick={onChooseOther} data-testid="choose-other-button">{S.unreach.chooseOther}</Button>
          </div>
        ) : onSwitchLocal && (
          <div className="lnr-path">
            <div className="lnr-grow"><strong>{S.unreach.computerTitle}</strong><p className="lnr-path__p dk-muted">{S.unreach.computerNote(name)}</p></div>
            <Button onClick={onSwitchLocal} data-testid="switch-local-button">{S.unreach.computer}</Button>
          </div>
        )}
        <div className="lnr-path">
          <div className="lnr-grow"><strong>{S.unreach.editTitle}</strong><p className="lnr-path__p dk-muted">{S.unreach.editNote}</p></div>
          <Button onClick={onEdit} data-testid="edit-server-button">{S.unreach.edit}</Button>
        </div>
      </section>
      <p className="dk-small dk-muted">
        <button type="button" className="lnr-link" aria-expanded={details} onClick={() => setDetails(!details)}>{details ? S.unreach.hideDetails : S.unreach.details}</button>
        {details && <> · {S.unreach.lastError(why)} · {S.unreach.nextTry(secs)}</>}
      </p>
    </div>
  )
}

/**
 * Screens 25 and 26: the calm card of a switch. The old page stays behind it.
 * After 10 seconds it says so. A failed try is shown here too, in plain words.
 */
export function SwitchingCard({ switching, now, onCancel }: { switching: SwitchingView; now: number; onCancel: () => void }) {
  const slow = now - switching.startedAt >= 10_000
  const reached = switching.step === 'loading'
  const cancel = useRef<HTMLButtonElement>(null)
  useEffect(() => cancel.current?.focus(), [])
  return (
    <div className="lnr-scrim lnr-scrim--soft" data-testid="switching-scrim">
      <div className="lnr-dialog lnr-dialog--card" role="status" aria-live="polite" aria-label={S.switching.opening(switching.name)} data-testid="switching-card">
        <div className="lnr-switching__head">
          <Icon name="spinner" spin className="lnr-switching__spin" />
          <div>
            <h2 className="lnr-dialog__title">{S.switching.opening(switching.name)}</h2>
            <p className="dk-mono dk-small lnr-dialog__sub">{switching.host}</p>
          </div>
        </div>
        <ol className="lnr-steps lnr-switching__steps">
          <li className="lnr-step"><div className="lnr-step__row">
            <span className={`lnr-step__mark ${reached ? 'lnr-step__mark--done' : 'lnr-step__mark--active'}`} aria-hidden="true">{reached ? <Icon name="check" /> : 1}</span>
            <span>{reached ? S.switching.reached : <strong>{S.switching.reaching}</strong>}</span>
          </div></li>
          <li className="lnr-step"><div className="lnr-step__row">
            <span className={`lnr-step__mark ${reached ? 'lnr-step__mark--active' : 'lnr-step__mark--todo'}`} aria-hidden="true">2</span>
            <span className={reached ? undefined : 'dk-muted'}>{reached ? <strong>{S.switching.loading}</strong> : S.switching.loading}</span>
          </div></li>
        </ol>
        <p className="dk-small dk-muted">{slow ? `${S.switching.slow} ` : ''}{S.switching.stays}</p>
        <div className="lnr-row lnr-dialog__actions">
          <span className="lnr-grow" />
          <Button ref={cancel} onClick={onCancel} data-testid="switch-cancel">{S.switching.cancel}</Button>
        </div>
      </div>
    </div>
  )
}

/** A switch that did not start: the server did not pass the test. Plain words, one way out. */
export function SwitchFailedCard({ name, onClose, onEdit }: { name: string; onClose: () => void; onEdit: () => void }) {
  return (
    <div className="lnr-scrim lnr-scrim--soft">
      <div className="lnr-dialog lnr-dialog--card" role="alertdialog" aria-labelledby="switch-failed-title" data-testid="switch-failed">
        <h2 id="switch-failed-title" className="lnr-dialog__title">{S.switching.failedTitle(name)}</h2>
        <p className="lnr-dialog__p">{S.switching.failedBody}</p>
        <div className="lnr-row lnr-dialog__actions">
          <span className="lnr-grow" />
          <Button onClick={onEdit}>{S.switching.edit}</Button>
          <Button variant="primary" autoFocus onClick={onClose}>{S.switching.failedClose}</Button>
        </div>
      </div>
    </div>
  )
}
