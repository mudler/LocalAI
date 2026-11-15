// The words of the contacts page, one JSON bundle per language of the web UI.
// English is the source and the fallback of every key. The other bundles are
// drafts that want a native review. addons/internal/i18ncheck checks that every
// bundle has the keys and placeholders of English.
import en from './contacts.en.json'
import it from './contacts.it.json'
import de from './contacts.de.json'
import es from './contacts.es.json'
import fr from './contacts.fr.json'
import id from './contacts.id.json'
import ko from './contacts.ko.json'
import pt_BR from './contacts.pt-BR.json'
import zh_CN from './contacts.zh-CN.json'

export const locales = { en, it, de, es, fr, id, ko, 'pt-BR': pt_BR, 'zh-CN': zh_CN }
