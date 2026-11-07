// The words of the calendar page, one JSON bundle per language of the web UI.
// English is the source and the fallback of every key. Italian is a draft that
// wants a native review; the other bundles repeat English until they are
// translated. addons/internal/i18ncheck checks that every bundle has the keys
// and placeholders of English.
import en from './calendar.en.json'
import it from './calendar.it.json'
import de from './calendar.de.json'
import es from './calendar.es.json'
import fr from './calendar.fr.json'
import id from './calendar.id.json'
import ko from './calendar.ko.json'
import pt_BR from './calendar.pt-BR.json'
import zh_CN from './calendar.zh-CN.json'

export const locales = { en, it, de, es, fr, id, ko, 'pt-BR': pt_BR, 'zh-CN': zh_CN }
