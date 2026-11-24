/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useId, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import Button from '../ui/Button'
import Callout from '../ui/Callout'
import { copyToClipboard } from '../../utils/clipboard'
import { relativeTime, shortPeer } from '../../utils/p2p'
import '../p2p/p2p.css'

// "Connect a remote" with an invite link: the link, what it says before
// anything is sent (the server, the kind, the relay pass, the way the dial
// goes and the expiry), Connect, and the answer. The answer is a live region
// that takes focus when it arrives.
//
//   invite   the object useRemoteInvite returns
export default function RemoteInviteStep({ invite }) {
  const { t, i18n } = useTranslation('p2p')
  const uid = useId()
  const resultRef = useRef(null)
  const { parsed, problem, phase, failure, saved } = invite
  const link = parsed.ok ? parsed.link : null

  useEffect(() => {
    if (phase === 'ok' || phase === 'failed') resultRef.current?.focus()
  }, [phase])

  const submit = (e) => {
    e.preventDefault()
    invite.connect()
  }
  const relay = link?.relays[0]
  const relayName = relay?.host || shortPeer(relay?.addr)
  const path = relay
    ? t('invite.preview.pathRelay', { relay: relayName })
    : t('invite.preview.pathDirect')
  const done = Boolean(saved)
  const count = invite.models.length

  return (
    <form className="remotes-form" onSubmit={submit} noValidate data-testid="remote-invite-step">
      <fieldset className="remotes-fields" disabled={phase === 'connecting' || done}>
        <div className="dk-field">
          <label className="dk-label" htmlFor={`${uid}-link`}>{t('invite.link')}</label>
          <textarea
            id={`${uid}-link`}
            className="dk-textarea p2p-invite-input dk-mono"
            rows={3}
            autoComplete="off"
            spellCheck={false}
            placeholder="localai://pair?v=1&k=remote&p=…"
            value={invite.link}
            onChange={e => invite.setLink(e.target.value)}
            aria-describedby={`${uid}-link-hint`}
            aria-invalid={Boolean(problem) || undefined}
            data-testid="remote-invite-link"
          />
          <p className="dk-hint" id={`${uid}-link-hint`}>{t('invite.linkHint')}</p>
        </div>

        {problem && (
          <Callout tone="warn" testId="remote-invite-problem">{t(`invite.problem.${problem}`, { kind: t(`kinds.${link?.kind}.title`, link?.kind || '') })}</Callout>
        )}

        {link && !problem && (
          <dl className="p2p-facts p2p-invite-preview" data-testid="remote-invite-preview">
            <dt>{t('invite.preview.server')}</dt>
            <dd>{link.label ? `${link.label} · ` : ''}<span className="dk-mono" title={link.peer}>{shortPeer(link.peer)}</span></dd>
            <dt>{t('invite.preview.kind')}</dt>
            <dd>{t('kinds.remote.title')}</dd>
            <dt>{t('invite.preview.pass')}</dt>
            <dd>{link.pass ? t('invite.preview.passIncluded', { relay: relayName }) : t('invite.preview.passNone')}</dd>
            <dt>{t('invite.preview.path')}</dt>
            <dd>{path}</dd>
            <dt>{t('invite.preview.expires')}</dt>
            <dd>{t('invite.preview.expiresAt', { when: relativeTime(link.expiresAt, i18n.language) })}</dd>
          </dl>
        )}
        {link && !problem && link.relays.some(r => r.open) && (
          <Callout tone="warn" testId="remote-invite-open-relay">{t('invite.openRelay')}</Callout>
        )}

        <div className="remotes-actions">
          <Button type="submit" variant="primary" icon="plug" busy={phase === 'connecting'} disabled={!link || Boolean(problem) || done} data-testid="remote-invite-connect">
            {done ? t('invite.connected') : phase === 'connecting' ? t('invite.connecting') : t('invite.connect')}
          </Button>
        </div>
      </fieldset>

      <div className="remotes-result" ref={resultRef} tabIndex={-1} aria-live="polite" data-testid="remote-invite-result">
        {phase === 'ok' && (
          <Callout tone="ok" role="status" testId="remote-invite-ok">
            {t('invite.ok', { name: saved?.remote?.name || link?.label || '', count })}
          </Callout>
        )}
        {phase === 'failed' && failure && <InviteFailure invite={invite} failure={failure} relay={relayName} />}
        {invite.switched && (
          <Callout tone="info" title={t('invite.switched.title')} testId="remote-invite-switched">
            {phase === 'ok' ? null : t('invite.switched.body')}
          </Callout>
        )}
      </div>
    </form>
  )
}

// Why the connect failed and what helps. A server that was not reached gets
// the three ways around a network that blocks direct connections.
function InviteFailure({ invite, failure, relay }) {
  const { t } = useTranslation('p2p')
  const copy = () => copyToClipboard([failure.message, invite.parsed.ok ? `peer ${invite.parsed.link.peer}` : ''].filter(Boolean).join('\n'))
  if (failure.kind !== 'unreachable') {
    return (
      <Callout tone="error" role="alert" title={t('invite.fail.title')} testId="remote-invite-fail" data-kind={failure.kind}>
        {t(`invite.fail.${failure.kind}`)}
        {failure.message && <span className="remotes-detail"> {failure.message}</span>}
      </Callout>
    )
  }
  return (
    <Callout tone="error" role="alert" title={t('invite.blocked.title')} testId="remote-invite-fail" data-kind={failure.kind}>
      {relay ? t('invite.blocked.bodyRelay', { relay }) : t('invite.blocked.body')}
      <ul className="p2p-advice">
        <li><b>{t('invite.blocked.relayTitle')}</b> {t('invite.blocked.relayBody')}</li>
        <li><b>{t('invite.blocked.portTitle')}</b> {t('invite.blocked.portBody')}</li>
        <li><b>{t('invite.blocked.networkTitle')}</b> {t('invite.blocked.networkBody')}</li>
      </ul>
      {failure.message && <span className="remotes-detail dk-mono">{failure.message}</span>}
      <span className="p2p-fail-actions">
        <Button size="sm" onClick={invite.connect}>{t('invite.blocked.retry')}</Button>
        <Button size="sm" variant="ghost" onClick={copy}>{t('invite.blocked.copy')}</Button>
      </span>
    </Callout>
  )
}
