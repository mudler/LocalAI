import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import ChatMCPConnectors from './ChatMCPConnectors'
import './chatskills.css'

// The user's MCP connectors: MCP servers that chats and saved agents can use.
// It is its own sidebar item, apart from the messaging connectors.
export default function MCPConnectors() {
  const { t } = useTranslation('chats')
  return (
    <div className="cs-page" data-testid="mcp-connectors-page">
      <header className="cs-head">
        <h1 className="cs-title">{t('mcpPage.title')}</h1>
      </header>
      <ChatMCPConnectors />
    </div>
  )
}
