import { RouteProvider } from 'react-router-dom'
import { registerLocales } from '../../../internal/host-compat/src/i18n.js'
import TalkingHead from './pages/TalkingHead'
import { locales } from './i18n/index.js'

// The words of the page, in every language of the web UI; a key a language lacks reads in English.
registerLocales({ head: locales })

export function createTalkingHead() {
  function Component({ page, path, search }) {
    if (page !== 'talking-head') return null
    return (
      <RouteProvider value={{ page, path: path || '', search: search || '' }}>
        <TalkingHead />
      </RouteProvider>
    )
  }
  return { Component, components: {}, chatActions: [], dispose() {} }
}
