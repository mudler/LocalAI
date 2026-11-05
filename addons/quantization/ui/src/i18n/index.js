// The words of the quantization pages, one JSON bundle per language of the web UI.
// English is the source and the fallback of every key. The texts repeat the
// host's own tools.json for each language; the few keys the add-on owns
// are drafts that want a native review. addons/internal/i18ncheck
// checks that every bundle has the keys and placeholders of English.
import en from './quantization.en.json'
import it from './quantization.it.json'
import de from './quantization.de.json'
import es from './quantization.es.json'
import fr from './quantization.fr.json'
import id from './quantization.id.json'
import ko from './quantization.ko.json'
import pt_BR from './quantization.pt-BR.json'
import zh_CN from './quantization.zh-CN.json'

export const locales = { en, it, de, es, fr, id, ko, 'pt-BR': pt_BR, 'zh-CN': zh_CN }
