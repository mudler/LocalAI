import { RouteProvider } from 'react-router-dom'
import { registerLocales } from '../../../internal/host-compat/src/i18n.js'
import Telegram from './pages/Telegram'
import en from './i18n/telegram.en.json'

registerLocales({ telegram: { en } })

export function createTelegram() {
  function Component({ page, path, search }) {
    if (page !== 'bots') return null
    return (
      <RouteProvider value={{ page, path: path || '', search: search || '' }}>
        <Telegram />
      </RouteProvider>
    )
  }
  return { Component, components: {}, chatActions: [], dispose() {} }
}
