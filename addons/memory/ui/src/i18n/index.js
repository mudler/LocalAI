// The words of the memory page, one JSON bundle per language of the web UI.
// English is the source and the fallback of every key. Italian is a draft that
// wants a native review; the other bundles repeat English except for the few
// strings the host already translated. addons/internal/i18ncheck checks that
// every bundle has the keys and placeholders of English.
import en from './memory.en.json'
import it from './memory.it.json'
import de from './memory.de.json'
import es from './memory.es.json'
import fr from './memory.fr.json'
import id from './memory.id.json'
import ko from './memory.ko.json'
import pt_BR from './memory.pt-BR.json'
import zh_CN from './memory.zh-CN.json'

export const locales = { en, it, de, es, fr, id, ko, 'pt-BR': pt_BR, 'zh-CN': zh_CN }
