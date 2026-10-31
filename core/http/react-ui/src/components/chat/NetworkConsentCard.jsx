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
  const answers = cardAnswers(card, isAdmin)

  if (card.state !== 'pending') {
    const when = card.answered_at ? new Date(card.answered_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : ''
    const state = t(`egress.card.${cardStateKey(card)}`)
    const who = card.answered_by ? (card.answered_by === user?.id ? t('egress.consent.you') : card.answered_by) : ''
    return (
      <div data-testid="chat-network-card-answered" data-state={card.state}>
        <p className="chat-network-card__line" data-testid="chat-network-card-line" data-state={card.state}>
          <code className="chat-network__host">{target}</code>{' '}
          {who ? t('egress.consent.answered', { state, who, time: when }) : t('egress.consent.answeredNoWho', { state, time: when })}
        </p>
        {/* A card that expired with its turn can still be answered: the
            answer serves the next command. A card above the limit gets no
            buttons, since no answer can make it work. */}
        {answers.length > 0 && <CardAnswerButtons answers={answers} busy={busy} onAnswer={answer} testId="chat-network-late" />}
        {error && <p className="cmd-consent__error" role="alert">{error}</p>}
      </div>
    )
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
  return (
    <Callout
      tone="warn"
      icon="globe"
      className="cmd-consent"
      role="alertdialog"
      title={t('egress.consent.title')}
      testId="chat-network-consent"
      actions={<CardAnswerButtons answers={answers} busy={busy} onAnswer={answer} testId="chat-network-consent" primary />}
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

// The answer buttons of a network card: Allow once, Allow for this chat and
// Deny, as far as the person may give them. primary marks Allow once as the
// main action (the waiting card); a late answer to an expired card keeps
// every button quiet.
export function CardAnswerButtons({ answers, busy, onAnswer, testId, primary = false }) {
  const { t } = useTranslation('chats')
  return (
    <>
      {answers.includes('once') && <Button size="sm" variant={primary ? 'primary' : undefined} disabled={!!busy} onClick={() => onAnswer('once')} data-testid={`${testId}-once`}>{t('egress.consent.once')}</Button>}
      {answers.includes('chat') && <Button size="sm" disabled={!!busy} onClick={() => onAnswer('chat')} data-testid={`${testId}-chat`}>{t('egress.consent.chat')}</Button>}
      {answers.includes('deny') && <Button size="sm" variant="ghost" disabled={!!busy} onClick={() => onAnswer('deny')} data-testid={`${testId}-deny`}>{t('egress.consent.deny')}</Button>}
    </>
  )
}
