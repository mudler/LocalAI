/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import './agentparts.css'

// The Connections section of the agent form: which connections of the person
// the agent's add-ons may use, and where the agent is already connected. Both
// lists come from data the form already holds (the add-on list of the draft
// tools and the messaging connections). Nothing is stored here except the
// `connections` field the form always had.
export default function AgentConnections({ addons, bindings, onChange }) {
  const { t } = useTranslation('agents')
  const credentialed = addons.filter((a) => a.credentialed && !a.gone)
  return (
    <div className="aed-conn" data-testid="agent-connections">
      <div className="aed-conn__block">
        <h4 className="aed-conn__h">{t('create.connections.addonsTitle')}</h4>
        {credentialed.length === 0
          ? <p className="ag-note" data-testid="agent-connections-addons-empty">{t('create.connections.addonsNone')}</p>
          : (
            <ul className="aed-conn__list">
              {credentialed.map((a) => <AddonRow key={a.id} addon={a} onChange={onChange} />)}
            </ul>
          )}
      </div>
      <div className="aed-conn__block">
        <h4 className="aed-conn__h">{t('create.connections.messagingTitle')}</h4>
        {bindings.length === 0
          ? <p className="ag-note" data-testid="agent-connections-messaging-empty">{t('create.connections.messagingNone')}</p>
          : (
            <ul className="aed-conn__list" data-testid="agent-connections-messaging">
              {bindings.map((b) => (
                <li key={b.id} className="aed-conn__item">
                  <span className="aed-conn__name">{b.name}</span>
                  <span className="aed-conn__meta">{t('create.connections.allows', { count: b.caps.length })}</span>
                  <Link className="aed-conn__link" to={b.to}>{t('create.connections.open')}</Link>
                </li>
              ))}
            </ul>
          )}
        <p className="ag-note">{t('create.connections.messagingHelp')}</p>
      </div>
    </div>
  )
}

function AddonRow({ addon, onChange }) {
  const { t } = useTranslation('agents')
  const hintId = useId()
  const options = addon.connections || []
  const chosen = addon.enabled_connections || []
  const toggle = (id, on) => onChange(addon.id, options.map((o) => o.id).filter((x) => (x === id ? on : chosen.includes(x))))
  return (
    <li className="aed-conn__item aed-conn__item--col" data-testid="agent-connections-addon">
      <div className="aed-conn__top">
        <span className="aed-conn__name">{addon.title || addon.id}</span>
        {addon.off && <span className="aed-conn__tag">{t('create.connections.addonOff')}</span>}
      </div>
      {options.length === 0 ? (
        <p className="ag-note">
          {t('create.connections.noneYet')}{' '}
          <Link className="aed-conn__link" to="/app/chats/connectors">{t('create.connections.add')}</Link>
        </p>
      ) : (
        <div role="group" aria-label={t('create.connections.groupLabel', { name: addon.title || addon.id })} aria-describedby={hintId} className="aed-conn__checks">
          {options.map((o) => (
            <label key={o.id} className="aed-conn__check" data-testid="agent-connections-option">
              <input type="checkbox" checked={chosen.includes(o.id)} data-value={o.id} onChange={(e) => toggle(o.id, e.target.checked)} />
              <span>{o.name}</span>
            </label>
          ))}
        </div>
      )}
      <span className="ag-note" id={hintId} data-testid="agent-connections-hint">
        {chosen.length ? t('create.connections.chosenHelp') : t('create.connections.perChat')}
      </span>
    </li>
  )
}
