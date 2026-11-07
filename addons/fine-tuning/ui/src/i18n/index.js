// The words of the fine-tuning pages, one JSON bundle per language of the web UI.
// English is the source and the fallback of every key. The texts repeat the
// host's own tools.json for each language; the few keys the add-on owns
// are drafts that want a native review. addons/internal/i18ncheck
// checks that every bundle has the keys and placeholders of English.
import en from './fine-tuning.en.json'
import it from './fine-tuning.it.json'
import de from './fine-tuning.de.json'
import es from './fine-tuning.es.json'
import fr from './fine-tuning.fr.json'
import id from './fine-tuning.id.json'
import ko from './fine-tuning.ko.json'
import pt_BR from './fine-tuning.pt-BR.json'
import zh_CN from './fine-tuning.zh-CN.json'

export const locales = { en, it, de, es, fr, id, ko, 'pt-BR': pt_BR, 'zh-CN': zh_CN }
