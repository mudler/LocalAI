// The words of the face pages, one JSON bundle per language of the web UI.
// English is the source and the fallback of every key. Italian is a draft
// that wants a native review; a language whose bundle repeats the English
// text of a key has no translation for it yet. addons/internal/i18ncheck
// checks that every bundle has the keys and placeholders of English.
import en from './face.en.json'
import it from './face.it.json'
import de from './face.de.json'
import es from './face.es.json'
import fr from './face.fr.json'
import id from './face.id.json'
import ko from './face.ko.json'
import pt_BR from './face.pt-BR.json'
import zh_CN from './face.zh-CN.json'

export const locales = { en, it, de, es, fr, id, ko, 'pt-BR': pt_BR, 'zh-CN': zh_CN }
