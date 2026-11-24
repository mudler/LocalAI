// The words of the gdrive pages, in every language of the web UI; a key a language
// lacks reads in English. Importing this file registers them once for the page
// and the approval card.
import { registerAddonLocales } from '../../../../internal/connector-ui/src/i18n'
import en from './gdrive.en.json'
import it from './gdrive.it.json'
import es from './gdrive.es.json'
import de from './gdrive.de.json'
import fr from './gdrive.fr.json'
import zh from './gdrive.zh-CN.json'
import id from './gdrive.id.json'
import ko from './gdrive.ko.json'
import pt from './gdrive.pt-BR.json'

registerAddonLocales('gdrive', { en, it, es, de, fr, 'zh-CN': zh, id, ko, 'pt-BR': pt })
