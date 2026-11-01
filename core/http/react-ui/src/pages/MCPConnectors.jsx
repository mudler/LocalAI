// eslint-disable-next-line no-unused-vars
import { Page } from '../components/ui'
// eslint-disable-next-line no-unused-vars
import ChatMCPConnectors from './ChatMCPConnectors'

// The user's MCP connectors: MCP servers that chats and saved agents can use.
// It is its own sidebar item, apart from the messaging connectors. The full
// width matches Add-ons, Files and Models, so the title does not move between
// them. The header lives in ChatMCPConnectors, because its primary action
// depends on the list.
export default function MCPConnectors() {
  return (
    <Page width="full" className="mcp-page" data-testid="mcp-connectors-page">
      <ChatMCPConnectors />
    </Page>
  )
}
