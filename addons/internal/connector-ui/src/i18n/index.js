// The shared words of the connector pages, in every language of the web UI.
// English is the source and the fallback of every key; a key that a language
// lacks reads in English. addons/internal/i18ncheck checks that no
// bundle misses a key, adds one, or breaks a placeholder.
import { registerLocales } from '../../../host-compat/src/i18n.js'
import en from './connector.en.json'
import it from './connector.it.json'
import fr from './connector.fr.json'
import es from './connector.es.json'
import de from './connector.de.json'
import zh from './connector.zh-CN.json'
import id from './connector.id.json'
import ko from './connector.ko.json'
import pt from './connector.pt-BR.json'

export const connectorLocales = { en, it, es, de, fr, 'zh-CN': zh, id, ko, 'pt-BR': pt }

export function registerConnectorLocales() {
  registerLocales({ connector: connectorLocales })
}
