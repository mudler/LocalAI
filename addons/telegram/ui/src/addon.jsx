import { RouteProvider } from 'react-router-dom'
import { registerLocales } from '../../../internal/host-compat/src/i18n.js'
import ConnectorPage from '../../../internal/connector-ui/src/ConnectorPage'
import en from './i18n/telegram.en.json'
import it from './i18n/telegram.it.json'
import es from './i18n/telegram.es.json'
import de from './i18n/telegram.de.json'
import fr from './i18n/telegram.fr.json'
import zh from './i18n/telegram.zh-CN.json'
import id from './i18n/telegram.id.json'
import ko from './i18n/telegram.ko.json'
import pt from './i18n/telegram.pt-BR.json'

// A group or channel of Telegram has a negative ID; a person or a private chat a positive one.
const isGroupId = (x) => /^-[0-9]+$/.test(x)

// The words of the page, in every language of the web UI; a key a language lacks reads in English.
registerLocales({ telegram: { en, it, es, de, fr, 'zh-CN': zh, id, ko, 'pt-BR': pt } })

// The page of the Telegram add-on: the shared connector page with one bot
// token and one allowlist of chat IDs, user IDs and @usernames.
export function createTelegram() {
  function Component({ page, path, search }) {
    if (page !== 'bots') return null
    return (
      <RouteProvider value={{ page, path: path || '', search: search || '' }}>
        <ConnectorPage kind="telegram" ns="telegram" testId="telegram-page" notifyMode="numeric" isChannelEntry={isGroupId} />
      </RouteProvider>
    )
  }
  return { Component, components: {}, chatActions: [], dispose() {} }
}
