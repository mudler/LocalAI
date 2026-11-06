/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import './agentparts.css'

// Where the agent is already connected: the messaging connections that answer
// with it. Read only, from data the form already holds; the link of each goes
// to the page of that connection.
export default function AgentConnected({ bindings }) {
  const { t } = useTranslation('agents')
  return (
    <div className="aed-conn__block" data-testid="agent-connections">
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
  )
}
