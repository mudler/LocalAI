// The words of the gmail pages, in every language of the web UI; a key a language
// lacks reads in English. Importing this file registers them once for the page
// and the approval card.
import { registerAddonLocales } from '../../../../internal/connector-ui/src/i18n'
import en from './gmail.en.json'
import it from './gmail.it.json'
import es from './gmail.es.json'
import de from './gmail.de.json'
import fr from './gmail.fr.json'
import zh from './gmail.zh-CN.json'
import id from './gmail.id.json'
import ko from './gmail.ko.json'
import pt from './gmail.pt-BR.json'

registerAddonLocales('gmail', { en, it, es, de, fr, 'zh-CN': zh, id, ko, 'pt-BR': pt })
