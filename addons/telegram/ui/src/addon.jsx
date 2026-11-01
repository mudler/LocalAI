import { RouteProvider } from 'react-router-dom'
import { registerLocales } from '../../../internal/host-compat/src/i18n.js'
import ConnectorPage from '../../../internal/connector-ui/src/ConnectorPage'
import en from './i18n/telegram.en.json'

registerLocales({ telegram: { en } })

// The page of the Telegram add-on: the shared connector page with one bot
// token and one allowlist of chat IDs, user IDs and @usernames.
export function createTelegram() {
  function Component({ page, path, search }) {
    if (page !== 'bots') return null
    return (
      <RouteProvider value={{ page, path: path || '', search: search || '' }}>
        <ConnectorPage kind="telegram" ns="telegram" testId="telegram-page" notifyMode="numeric" />
      </RouteProvider>
    )
  }
  return { Component, components: {}, chatActions: [], dispose() {} }
}
