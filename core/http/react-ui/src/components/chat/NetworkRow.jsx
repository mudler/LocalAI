/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Button from '../ui/Button'
import { useAuth } from '../../context/AuthContext'
import { AdminHint, CardAnswerButtons } from './NetworkConsentCard'
import { askedAboveLimit, cardAnswers, ceilingKind, cardStateKey, customDomains, hostPort, networkMode, presetChoices, presetLabel, sortedCards } from '../../utils/chatEgress'
import { addonSettingsAddress, NETWORK_SECTION } from '../../utils/addonSettings'
import './network.css'

// The Network row of the chat's Tools menu: how much of the network the
// chat's isolated commands may reach (off, the custom list of package
// registries and the administrator's domains, all public sites), up to the administrator's limit, and the panel with the chat's
// rules and the cards that asked. Commands that run on this machine have the
// server's network: the row is a line that says so.
export default function NetworkRow({ egress }) {
  const { t } = useTranslation('chats')
  const labelId = useId()
  const { isAdmin } = useAuth()
  const [open, setOpen] = useState(false)
  const mode = networkMode(egress)
  if (mode === 'hidden') return null
  if (mode === 'host' || mode === 'unverified') return (
    <div className="cmd-notice__line" data-testid="chat-network-unenforced">
      <p>{t(mode === 'host' ? 'egress.hostNetwork' : 'egress.unverified')}</p>
      <p>{t('egress.configuredPolicy', { preset: presetLabel(egress, egress.preset || 'off', t) })}</p>
    </div>
  )
  const choices = presetChoices(egress)
  const kind = ceilingKind(egress)
  const asked = askedAboveLimit(egress)
  const hosts = customDomains(egress)
  const items = egress.rules.length + egress.cards.length + hosts.length
  // The Custom list is part of the panel, so the panel is there whenever a
  // limit above Off lets a chat choose Custom, even with the list empty.
  const showPanel = items > 0 || kind !== 'off'
  return (
    <div className="chat-network" data-testid="chat-network">
      <div className="chat-network__head">
        <span id={labelId} className="chat-network__label">{t('egress.label')}</span>
        <div className="dk-segmented" role="radiogroup" aria-labelledby={labelId}>
          {choices.map(c => (
            <button
              key={c.id} type="button" role="radio" className="dk-seg" aria-checked={c.chosen} aria-pressed={c.chosen}
              disabled={c.limited} title={c.limited ? t('egress.limited') : t(`egress.presetHint.${c.id}`)}
              onClick={() => { if (c.id !== egress.preset) egress.setPreset(c.id) }} data-testid={`chat-network-${c.id}`}
            >
              {c.title || t(`egress.preset.${c.id}`)}
            </button>
          ))}
        </div>
      </div>
      <p className="chat-network__note">
        {kind === 'off'
          ? t('egress.blocked.rowOff')
          : <>
            {t(`egress.presetHint.${egress.effective || 'off'}`)}
            {kind === 'capped' && <> {t('egress.limitedBy', { limit: presetLabel(egress, egress.ceiling, t) })}</>}
          </>}
      </p>
      {asked && (
        <p className="chat-network__note" data-testid="chat-network-asks">
          {t('egress.asksAbove', { choice: presetLabel(egress, asked, t), limit: presetLabel(egress, egress.ceiling || 'off', t) })}
        </p>
      )}
      <p className="chat-network__note" data-testid={egress.draft ? 'chat-network-draft' : 'chat-network-limit-note'}>
        {t(egress.draft ? 'egress.draftNote' : 'egress.limitNote')}
      </p>
      {kind && <AdminHint isAdmin={isAdmin} egress={egress} testId="chat-network-limit-admin" />}
      {hosts.length > 0 && egress.effective && egress.effective !== 'off' && (
        <p className="chat-network__note" data-testid="chat-network-allowed-note">{t('egress.allowedHostsNote', { count: hosts.length })}</p>
      )}
      {showPanel && (
        <button type="button" className="chat-network__toggle" aria-expanded={open} onClick={() => setOpen(v => !v)} data-testid="chat-network-panel-toggle">
          {egress.rules.length + egress.cards.length > 0 ? t('egress.panel', { count: egress.rules.length + egress.cards.length }) : t('egress.panelCustom')}
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
  const hosts = customDomains(egress)
  const addon = egress.addon || ''
  return (
    <div className="chat-network__panel" data-testid="chat-network-panel">
      <div className="chat-network__allowed" data-testid="chat-network-allowed">
        <p className="chat-network__note">
          {t(hosts.length > 0 ? 'egress.customDomains' : 'egress.customEmpty')}
          {hosts.length > 0 && (!egress.effective || egress.effective === 'off') && <> {t('egress.allowedHostsOff')}</>}
        </p>
        {hosts.length > 0 && (
          <ul className="chat-network__list" aria-label={t('egress.customLabel')}>
            {hosts.map(h => (
              <li key={h} className="chat-network__item" data-testid="chat-network-allowed-host">
                <code className="chat-network__host">{h}</code>
              </li>
            ))}
          </ul>
        )}
        {isAdmin ? (
          addon && <p className="chat-network__note"><Link to={addonSettingsAddress(addon, NETWORK_SECTION)} data-testid="chat-network-edit-domains">{t('egress.customEdit')}</Link></p>
        ) : (
          <p className="chat-network__note" data-testid="chat-network-custom-admin">{t('egress.customAdmin')}</p>
        )}
      </div>
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
