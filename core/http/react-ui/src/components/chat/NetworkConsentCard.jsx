/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Button from '../ui/Button'
import Callout from '../ui/Callout'
import { useAuth } from '../../context/AuthContext'
import { chatEgressApi } from '../../utils/api'
import { cardAnswers, cardStateKey, hostPort, SANDBOX_NETWORK_SETTINGS } from '../../utils/chatEgress'
import './network.css'

// The card a command of the Sandbox waits on when it asks for a host the chat
// does not allow but the administrator's limit would. Allow once covers the
// rest of that command; Allow for this chat stores a rule; Deny is remembered
// so the host does not ask again. The host is shown as plain text, in
// punycode as the server sends it. An answered card collapses to one line.
export default function NetworkConsentCard({ card, chatId, egress }) {
  const { t } = useTranslation('chats')
  const { isAdmin, user } = useAuth()
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const sending = useRef(false)
  if (!card?.id) return null
  const target = hostPort(card)

  if (card.state !== 'pending') {
    const when = card.answered_at ? new Date(card.answered_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : ''
    const state = t(`egress.card.${cardStateKey(card)}`)
    const who = card.answered_by ? (card.answered_by === user?.id ? t('egress.consent.you') : card.answered_by) : ''
    return (
      <p className="chat-network-card__line" data-testid="chat-network-card-line" data-state={card.state}>
        <code className="chat-network__host">{target}</code>{' '}
        {who ? t('egress.consent.answered', { state, who, time: when }) : t('egress.consent.answeredNoWho', { state, time: when })}
      </p>
    )
  }

  // One request at a time: a second click while the first runs is ignored,
  // and a failure reads the card again, since the server may have refused it
  // or someone answered it from another tab.
  const answer = async (decision) => {
    if (sending.current) return
    sending.current = true
    setBusy(decision)
    setError('')
    try {
      await chatEgressApi.consent(chatId, card.id, decision)
    } catch (e) {
      setError(e.message || 'error')
      setBusy('')
      egress?.refresh?.()
    } finally {
      sending.current = false
    }
  }
  if (card.above_ceiling) {
    // Nothing to click: the server's limit decides, not the person.
    const off = (egress?.ceiling || 'off') === 'off'
    return (
      <Callout tone="warn" icon="globe" className="cmd-consent" role="status" title={t(off ? 'egress.blocked.titleOff' : 'egress.blocked.titleCapped')} testId="chat-network-blocked">
        <p>
          {t(off ? 'egress.blocked.bodyOff' : 'egress.blocked.bodyCapped', { limit: t(`egress.preset.${egress?.ceiling || 'off'}`) })}{' '}
          <code className="chat-network__host" data-testid="chat-network-consent-host">{target}</code>
        </p>
        <AdminHint isAdmin={isAdmin} egress={egress} testId="chat-network-blocked-admin" />
      </Callout>
    )
  }
  const answers = cardAnswers(card, isAdmin)
  return (
    <Callout
      tone="warn"
      icon="globe"
      className="cmd-consent"
      role="alertdialog"
      title={t('egress.consent.title')}
      testId="chat-network-consent"
      actions={(
        <>
          {answers.includes('once') && <Button size="sm" variant="primary" disabled={!!busy} onClick={() => answer('once')} data-testid="chat-network-consent-once">{t('egress.consent.once')}</Button>}
          {answers.includes('chat') && <Button size="sm" disabled={!!busy} onClick={() => answer('chat')} data-testid="chat-network-consent-chat">{t('egress.consent.chat')}</Button>}
          <Button size="sm" variant="ghost" disabled={!!busy} onClick={() => answer('deny')} data-testid="chat-network-consent-deny">{t('egress.consent.deny')}</Button>
        </>
      )}
    >
      <p>{t('egress.consent.body')} <code className="chat-network__host" data-testid="chat-network-consent-host">{target}</code></p>
      {egress && (
        <p className="cmd-consent__hint">
          {t('egress.consent.preset', { preset: t(`egress.preset.${egress.preset || 'off'}`), limit: t(`egress.preset.${egress.ceiling || 'off'}`) })}
        </p>
      )}
      {card.private && !isAdmin && <p className="cmd-consent__hint" data-testid="chat-network-consent-admin">{t('egress.consent.adminOnly')}</p>}
      {error && <p className="cmd-consent__error" role="alert">{error}</p>}
    </Callout>
  )
}

// Who can change the limit: an administrator gets the link, anyone else is
// told to ask one. Also shown in the Network row.
export function AdminHint({ isAdmin, egress, testId }) {
  const { t } = useTranslation('chats')
  const off = (egress?.ceiling || 'off') === 'off'
  return (
    <p className="cmd-consent__hint" data-testid={testId}>
      {isAdmin ? (
        <>
          {t(off ? 'egress.blocked.adminOff' : 'egress.blocked.adminCapped')}{' '}
          <Link to={SANDBOX_NETWORK_SETTINGS} data-testid={`${testId}-link`}>{t('egress.blocked.adminLink')}</Link>
        </>
      ) : t('egress.blocked.askAdmin')}
    </p>
  )
}
