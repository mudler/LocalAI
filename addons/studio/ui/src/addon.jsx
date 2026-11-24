import { RouteProvider } from 'react-router-dom'
import { registerLocales } from '../../../internal/host-compat/src/i18n.js'
import Studio from './pages/Studio'
import en from './i18n/media.en.json'
import it from './i18n/media.it.json'
import es from './i18n/media.es.json'
import de from './i18n/media.de.json'
import zh from './i18n/media.zh-CN.json'
import id from './i18n/media.id.json'
import ko from './i18n/media.ko.json'
import pt from './i18n/media.pt-BR.json'

// The words of the pages are the host's own `media` locale files.
registerLocales({ media: { en, it, es, de, 'zh-CN': zh, id, ko, 'pt-BR': pt } })

export function createStudio() {
  // The shell passes the page, the rest of the path (the tab) and the query.
  function Component({ page, path, search }) {
    if (page !== 'studio') return null
    return (
      <RouteProvider value={{ page, path: path || '', search: search || '' }}>
        <Studio />
      </RouteProvider>
    )
  }
  return { Component, components: {}, chatActions: [], dispose() {} }
}
