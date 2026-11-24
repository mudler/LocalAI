/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../../context/AuthContext'
import { visibleConnectionTabs } from '../../utils/connectionsTabs'
import '../../pages/connectors.css'

// The tab strip at the top of the Connections pages: Accounts and Messaging
// (two views of /app/chats/connectors) and MCP servers (its own page). The tabs
// are links, so each one has an address and a back button works; the kit tab
// style is the one the other pages use.
//
//   current   the id of the open tab: accounts, messaging or mcp
export default function ConnectionsTabs({ current }) {
  const { t } = useTranslation('chats')
  const { hasFeature } = useAuth()
  const tabs = visibleConnectionTabs(hasFeature)
  // One tab is no choice: a viewer who may open one page sees no strip.
  if (tabs.length < 2) return null
  return (
    <nav className="dk-tabs cn-tabs" aria-label={t('connectionsTabs.label')} data-testid="connections-tabs">
      {tabs.map((tab) => (
        <Link key={tab.id} to={tab.to} className="dk-tab" data-testid={`connections-tab-${tab.id}`}
          aria-current={tab.id === current ? 'page' : undefined}>
          {t(tab.labelKey)}
        </Link>
      ))}
    </nav>
  )
}
