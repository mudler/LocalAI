// The words of the Talking Head page, one JSON bundle per language of the web
// UI. English is the source and the fallback of every key.
// addons/internal/i18ncheck checks that every bundle has the keys and
// placeholders of English.
import en from './head.en.json'
import it from './head.it.json'
import de from './head.de.json'
import es from './head.es.json'
import fr from './head.fr.json'
import id from './head.id.json'
import ko from './head.ko.json'
import bn from './head.bn.json'
import pt_BR from './head.pt-BR.json'
import zh_CN from './head.zh-CN.json'

export const locales = { en, it, de, es, fr, id, ko, bn, 'pt-BR': pt_BR, 'zh-CN': zh_CN }
