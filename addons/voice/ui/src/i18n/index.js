// The words of the voice pages, one JSON bundle per language of the web UI.
// English is the source and the fallback of every key. Italian is a draft
// that wants a native review; a language whose bundle repeats the English
// text of a key has no translation for it yet. addons/internal/i18ncheck
// checks that every bundle has the keys and placeholders of English.
import en from './voice.en.json'
import it from './voice.it.json'
import de from './voice.de.json'
import es from './voice.es.json'
import fr from './voice.fr.json'
import id from './voice.id.json'
import ko from './voice.ko.json'
import pt_BR from './voice.pt-BR.json'
import zh_CN from './voice.zh-CN.json'

export const locales = { en, it, de, es, fr, id, ko, 'pt-BR': pt_BR, 'zh-CN': zh_CN }
