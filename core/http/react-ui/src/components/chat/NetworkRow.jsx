/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Button from '../ui/Button'
import { useAuth } from '../../context/AuthContext'
import { AdminHint, CardAnswerButtons } from './NetworkConsentCard'
import { cardAnswers, ceilingKind, cardStateKey, hostPort, networkMode, presetChoices, sortedCards } from '../../utils/chatEgress'
import './network.css'

// The Network row of the chat's Tools menu: how much of the network the
// chat's Sandbox commands may reach (off, the package registries, all public
// sites), up to the administrator's limit, and the panel with the chat's
// rules and the cards that asked. Commands that run on this machine have the
// server's network: the row is a line that says so.
export default function NetworkRow({ egress }) {
  const { t } = useTranslation('chats')
  const labelId = useId()
  const { isAdmin } = useAuth()
  const [open, setOpen] = useState(false)
  const mode = networkMode(egress)
  if (mode === 'hidden') return null
  if (mode === 'host') return <p className="cmd-notice__line" data-testid="chat-network-host">{t('egress.hostNetwork')}</p>
  const choices = presetChoices(egress)
  const kind = ceilingKind(egress)
  const hosts = egress.allowed_hosts || []
  const items = egress.rules.length + egress.cards.length + hosts.length
  return (
    <div className="chat-network" data-testid="chat-network">
      <div className="chat-network__head">
        <span id={labelId} className="chat-network__label">{t('egress.label')}</span>
        <div className="dk-segmented" role="radiogroup" aria-labelledby={labelId}>
          {choices.map(c => (
            <button
              key={c.id} type="button" role="radio" className="dk-seg" aria-checked={c.chosen} aria-pressed={c.chosen}
              disabled={c.limited} title={c.limited ? t('egress.limited') : t(`egress.presetHint.${c.id}`)}
              onClick={() => { if (!c.chosen) egress.setPreset(c.id) }} data-testid={`chat-network-${c.id}`}
            >
              {t(`egress.preset.${c.id}`)}
            </button>
          ))}
        </div>
      </div>
      <p className="chat-network__note">
        {kind === 'off'
          ? t('egress.blocked.rowOff')
          : <>
            {t(`egress.presetHint.${egress.effective || 'off'}`)}
            {kind === 'capped' && <> {t('egress.limitedBy', { limit: t(`egress.preset.${egress.ceiling}`) })}</>}
          </>}
      </p>
      {kind && <AdminHint isAdmin={isAdmin} egress={egress} testId="chat-network-limit-admin" />}
      {hosts.length > 0 && egress.effective && egress.effective !== 'off' && (
        <p className="chat-network__note" data-testid="chat-network-allowed-note">{t('egress.allowedHostsNote', { count: hosts.length })}</p>
      )}
      {items > 0 && (
        <button type="button" className="chat-network__toggle" aria-expanded={open} onClick={() => setOpen(v => !v)} data-testid="chat-network-panel-toggle">
          {t('egress.panel', { count: items })}
        </button>
      )}
      {open && <NetworkPanel egress={egress} />}
      {egress.error && <p className="chat-network__error" role="alert">{egress.error}</p>}
    </div>
  )
}

// The rules of the chat with a remove button each, and its cards.
function NetworkPanel({ egress }) {
  const { t } = useTranslation('chats')
  const { isAdmin } = useAuth()
  const hosts = egress.allowed_hosts || []
  return (
    <div className="chat-network__panel" data-testid="chat-network-panel">
      {hosts.length > 0 && (
        <div className="chat-network__allowed" data-testid="chat-network-allowed">
          <p className="chat-network__note">
            {t('egress.allowedHosts')}
            {(!egress.effective || egress.effective === 'off') && <> {t('egress.allowedHostsOff')}</>}
          </p>
          <ul className="chat-network__list" aria-label={t('egress.allowedHosts')}>
            {hosts.map(h => (
              <li key={h} className="chat-network__item" data-testid="chat-network-allowed-host">
                <code className="chat-network__host">{h}</code>
              </li>
            ))}
          </ul>
        </div>
      )}
      {egress.rules.length > 0 && (
        <ul className="chat-network__list" aria-label={t('egress.rules')}>
          {egress.rules.map(r => (
            <li key={hostPort(r)} className="chat-network__item" data-testid="chat-network-rule">
              <code className="chat-network__host">{hostPort(r)}</code>
              <span className="chat-network__state">
                {t(r.allow ? 'egress.ruleAllow' : 'egress.ruleDeny')}
                {r.above_limit && <> · {t('egress.aboveLimit')}</>}
              </span>
              <Button size="sm" variant="ghost" onClick={() => egress.removeRule(r.host, r.port)} aria-label={t('egress.removeLabel', { host: hostPort(r) })}>
                {t('egress.remove')}
              </Button>
            </li>
          ))}
        </ul>
      )}
      {egress.cards.length > 0 && (
        <ul className="chat-network__list" aria-label={t('egress.cards')}>
          {sortedCards(egress.cards).map(c => (
            <li key={c.id} className="chat-network__item" data-testid="chat-network-card" data-state={c.state}>
              <code className="chat-network__host">{hostPort(c)}</code>
              <span className="chat-network__state">{t(`egress.card.${cardStateKey(c)}`)}</span>
              {c.state === 'expired' && cardAnswers(c, isAdmin).length > 0 && (
                <CardAnswerButtons answers={cardAnswers(c, isAdmin)} busy="" onAnswer={(d) => egress.answer(c.id, d)} testId={`chat-network-card-${c.id}`} />
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
