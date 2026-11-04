import { RouteProvider } from 'react-router-dom'
import { registerLocales } from '../../../internal/host-compat/src/i18n.js'
import Talk from './pages/Talk'
import en from './i18n/talk.en.json'
import it from './i18n/talk.it.json'
import es from './i18n/talk.es.json'
import de from './i18n/talk.de.json'
import fr from './i18n/talk.fr.json'
import zh from './i18n/talk.zh-CN.json'
import id from './i18n/talk.id.json'
import ko from './i18n/talk.ko.json'
import pt from './i18n/talk.pt-BR.json'

// The words of the page are the host's own `talk` locale file.
// The words of the page, in every language of the web UI; a key a language lacks reads in English.
registerLocales({ talk: { en, it, es, de, fr, 'zh-CN': zh, id, ko, 'pt-BR': pt } })

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
