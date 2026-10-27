import { RouteProvider } from 'react-router-dom'
import { registerLocales } from '../../../internal/host-compat/src/i18n.js'
import Talk from './pages/Talk'
import en from './i18n/talk.en.json'
import zh from './i18n/talk.zh-CN.json'

// The words of the page are the host's own `talk` locale file.
registerLocales({ talk: { en, 'zh-CN': zh } })

export function createTalk() {
  function Component({ page, path, search }) {
    if (page !== 'talk') return null
    return (
      <RouteProvider value={{ page, path: path || '', search: search || '' }}>
        <Talk />
      </RouteProvider>
    )
  }
  return { Component, components: {}, chatActions: [], dispose() {} }
}
